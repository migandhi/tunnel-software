package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/razorpay/razorpay-go"
)

// --- PASTE YOUR TEST KEYS HERE ---
const RazorpayKeyID = "rzp_test_SSdvB4Ueqi8GYZ"
const RazorpaySecret = "51gblw9D9m13gA9NIhldGWCQ"

// --- DATA STRUCTURES ---

type OrderRequest struct {
	Plan          string `json:"plan"`
	Email         string `json:"email"`
	RequestedName string `json:"requested_subdomain"`
	IsRenewal     bool   `json:"is_renewal"` // Add this flag
}

type PaymentVerification struct {
	OrderID           string `json:"razorpay_order_id"`
	PaymentID         string `json:"razorpay_payment_id"`
	Signature         string `json:"razorpay_signature"`
	Plan              string `json:"plan"`
	Email             string `json:"email"`
	RequestedName     string `json:"requested_subdomain"`
}

type RenewalVerification struct {
	OrderID           string `json:"razorpay_order_id"`
	PaymentID         string `json:"razorpay_payment_id"`
	Signature         string `json:"razorpay_signature"`
	Plan              string `json:"plan"`
	Subdomain         string `json:"subdomain"`
}

// --- HELPER FUNCTIONS ---
func getNextTCPPort() int {
	var maxPort sql.NullInt64
	err := db.QueryRow("SELECT MAX(tcp_port) FROM users WHERE tcp_port > 0").Scan(&maxPort)
	if err != nil || !maxPort.Valid || maxPort.Int64 < 20000 {
		return 20000
	}
	return int(maxPort.Int64) + 1
}

func getUniqueSubdomain(requested string) string {
	if requested == "" {
		bytes := make([]byte, 4)
		rand.Read(bytes)
		return "tun-" + hex.EncodeToString(bytes)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE subdomain = ?", requested).Scan(&count)
	if count > 0 {
		bytes := make([]byte, 2)
		rand.Read(bytes)
		return requested + "-" + hex.EncodeToString(bytes)
	}
	return requested
}

// --- API ROUTES ---

// 1. Creates the Razorpay Order
// 1. Creates the Razorpay Order (WITH STRICT SUBDOMAIN CHECK)
// 1. Creates the Razorpay Order (Smart Check for New vs Renewal)
func createOrderHandler(w http.ResponseWriter, r *http.Request) {
	var req OrderRequest
	json.NewDecoder(r.Body).Decode(&req)

	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE subdomain = ?", req.RequestedName).Scan(&count)

	if req.IsRenewal {
		// If it's a renewal, the subdomain MUST exist.
		if count == 0 {
			http.Error(w, "Error: Subdomain not found. Please check your spelling.", http.StatusNotFound)
			return
		}
	} else {
		// If it's a new user, the subdomain MUST NOT exist.
		if count > 0 {
			http.Error(w, "Error: The subdomain '"+req.RequestedName+"' is already taken. Please choose another one.", http.StatusConflict)
			return
		}
	}

	price := 9900
	if req.Plan == "pro" {
		price = 19900
	}

	client := razorpay.NewClient(RazorpayKeyID, RazorpaySecret)
	data := map[string]interface{}{
		"amount":   price,
		"currency": "INR",
		"receipt":  "receipt_" + time.Now().Format("20060102150405"),
	}

	body, err := client.Order.Create(data, nil)
	if err != nil {
		http.Error(w, "Failed to create order", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}
// 2. Verifies NEW user payment and creates them
func verifyPaymentHandler(w http.ResponseWriter, r *http.Request) {
	var req PaymentVerification
	json.NewDecoder(r.Body).Decode(&req)

	data := req.OrderID + "|" + req.PaymentID
	h := hmac.New(sha256.New, []byte(RazorpaySecret))
	h.Write([]byte(data))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	if hmac.Equal([]byte(expectedSignature), []byte(req.Signature)) {
		subdomain := getUniqueSubdomain(req.RequestedName)
		token := generateToken()
		expiration := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04:05")

		tcpPort := 0
		var limitBytes int64 = 50 * 1024 * 1024 * 1024
		
		if req.Plan == "pro" {
			tcpPort = getNextTCPPort()
			limitBytes = 0
		}

		_, err := db.Exec(`INSERT INTO users (email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_limit) 
			VALUES (?, ?, ?, ?, 1, ?, ?)`, req.Email, subdomain, token, expiration, tcpPort, limitBytes)

		if err != nil {
			http.Error(w, "Failed to save user to DB", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "success",
			"token":     token,
			"subdomain": subdomain,
			"tcp_port":  tcpPort,
		})
	} else {
		http.Error(w, "Invalid payment signature", http.StatusUnauthorized)
	}
}

// 3. Verifies RENEWAL payment, resets bandwidth, adds 30 days
func verifyRenewalHandler(w http.ResponseWriter, r *http.Request) {
	var req RenewalVerification
	json.NewDecoder(r.Body).Decode(&req)

	data := req.OrderID + "|" + req.PaymentID
	h := hmac.New(sha256.New, []byte(RazorpaySecret))
	h.Write([]byte(data))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	if hmac.Equal([]byte(expectedSignature), []byte(req.Signature)) {
		
		var currentPort int
		var currentToken string
		err := db.QueryRow("SELECT tcp_port, token FROM users WHERE subdomain = ?", req.Subdomain).Scan(&currentPort, &currentToken)
		if err != nil {
			http.Error(w, "Subdomain not found in our system.", http.StatusNotFound)
			return
		}

		tcpPort := currentPort
		var limitBytes int64 = 50 * 1024 * 1024 * 1024
		
		if req.Plan == "pro" {
			limitBytes = 0
			if tcpPort == 0 {
				tcpPort = getNextTCPPort() // Upgraded to Pro!
			}
		} else {
			tcpPort = 0 // Downgraded to Basic
		}

		newExpiration := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04:05")

		_, err = db.Exec(`UPDATE users SET 
			expiration_timestamp = ?, is_active = 1, bandwidth_used = 0, tcp_port = ?, bandwidth_limit = ? 
			WHERE subdomain = ?`, newExpiration, tcpPort, limitBytes, req.Subdomain)

		if err != nil {
			http.Error(w, "Failed to update user in DB", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "success",
			"token":     currentToken,
			"subdomain": req.Subdomain,
			"tcp_port":  tcpPort,
		})
	} else {
		http.Error(w, "Invalid payment signature", http.StatusUnauthorized)
	}
}