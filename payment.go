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

// Data structures for JSON communication with the frontend
type OrderRequest struct {
	Plan          string `json:"plan"` // "basic" (99) or "pro" (199)
	Email         string `json:"email"`
	RequestedName string `json:"requested_subdomain"`
}

type PaymentVerification struct {
	OrderID           string `json:"razorpay_order_id"`
	PaymentID         string `json:"razorpay_payment_id"`
	Signature         string `json:"razorpay_signature"`
	Plan              string `json:"plan"`
	Email             string `json:"email"`
	RequestedName     string `json:"requested_subdomain"`
}

// 1. Auto-Assigner: Finds the next available TCP Port
func getNextTCPPort() int {
	var maxPort sql.NullInt64
	err := db.QueryRow("SELECT MAX(tcp_port) FROM users WHERE tcp_port > 0").Scan(&maxPort)
	if err != nil || !maxPort.Valid || maxPort.Int64 < 20000 {
		return 20000 // Start assigning from 20000
	}
	return int(maxPort.Int64) + 1
}

// 2. Auto-Assigner: Ensures subdomains are unique
func getUniqueSubdomain(requested string) string {
	if requested == "" {
		bytes := make([]byte, 4)
		rand.Read(bytes)
		return "tun-" + hex.EncodeToString(bytes)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users WHERE subdomain = ?", requested).Scan(&count)
	if count > 0 {
		// If taken, append a random 2-byte hex to make it unique
		bytes := make([]byte, 2)
		rand.Read(bytes)
		return requested + "-" + hex.EncodeToString(bytes)
	}
	return requested
}

// 3. API: Creates the Razorpay Order when the user clicks "Buy"
func createOrderHandler(w http.ResponseWriter, r *http.Request) {
	var req OrderRequest
	json.NewDecoder(r.Body).Decode(&req)

	price := 9900 // Default to basic (Amount is in paisa, so 9900 = ₹99.00)
	if req.Plan == "pro" {
		price = 19900 // ₹199.00
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

// 4. API: Verifies the payment and creates the user in the database
func verifyPaymentHandler(w http.ResponseWriter, r *http.Request) {
	var req PaymentVerification
	json.NewDecoder(r.Body).Decode(&req)

	// Cryptographic Signature Verification (To prevent hackers from faking payments)
	data := req.OrderID + "|" + req.PaymentID
	h := hmac.New(sha256.New, []byte(RazorpaySecret))
	h.Write([]byte(data))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	if hmac.Equal([]byte(expectedSignature), []byte(req.Signature)) {
		// PAYMENT IS REAL! Create the user.
		subdomain := getUniqueSubdomain(req.RequestedName)
		token := generateToken() // Uses your existing generator from admin.go
		expiration := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04:05")

		tcpPort := 0
		var limitBytes int64 = 50 * 1024 * 1024 * 1024 // 50 GB default
		
		if req.Plan == "pro" {
			tcpPort = getNextTCPPort()
			limitBytes = 0 // Unlimited for Pro
		}

		_, err := db.Exec(`INSERT INTO users (email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_limit) 
			VALUES (?, ?, ?, ?, 1, ?, ?)`, req.Email, subdomain, token, expiration, tcpPort, limitBytes)

		if err != nil {
			http.Error(w, "Failed to save user to DB", http.StatusInternalServerError)
			return
		}

		// Send the credentials straight to their browser screen
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