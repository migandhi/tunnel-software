package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
	"strings"
)

type User struct {
	ID             int
	Email          string
	Subdomain      string
	Token          string
	Expiration     string
	IsActive       bool
	TCPPort        int
	BandwidthUsed  string 
	BandwidthLimit string 
	StatusHTML     template.HTML // NEW FIELD
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func adminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_used, bandwidth_limit FROM users")
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var rawUsed, rawLimit int64
		
		rows.Scan(&u.ID, &u.Email, &u.Subdomain, &u.Token, &u.Expiration, &u.IsActive, &u.TCPPort, &rawUsed, &rawLimit)
		
		u.BandwidthUsed = formatBytes(rawUsed)
		if rawLimit == 0 {
			u.BandwidthLimit = "Unlimited"
		} else {
			u.BandwidthLimit = formatBytes(rawLimit)
		}

		// NEW: Calculate days left and set the color
		// NEW: Strip out T and Z so Go can read the date properly
		cleanExp := strings.Replace(u.Expiration, "T", " ", 1)
		cleanExp = strings.Replace(cleanExp, "Z", "", 1)

		expTime, _ := time.Parse("2006-01-02 15:04:05", cleanExp)
		daysLeft := int(time.Until(expTime).Hours() / 24)

		if !u.IsActive {
			u.StatusHTML = template.HTML(`<span style="color: #dc3545; font-weight: bold;">Expired/Kicked</span>`)
		} else if daysLeft <= 3 {
			u.StatusHTML = template.HTML(fmt.Sprintf(`<span style="color: #ff8c00; font-weight: bold;">Expiring Soon (%d days)</span>`, daysLeft))
		} else {
			u.StatusHTML = template.HTML(`<span style="color: #28a745; font-weight: bold;">Active</span>`)
		}
		
		users = append(users, u)
	}

	tmpl := template.Must(template.ParseFiles("templates/admin.html"))
	tmpl.Execute(w, struct{ Users []User }{users})
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}

	email := r.FormValue("email")
	subdomain := r.FormValue("subdomain")
	tcpPort := r.FormValue("tcp_port")
	if tcpPort == "" {
		tcpPort = "0"
	}
	
	bwLimitStr := r.FormValue("bandwidth_limit")
	var bandwidthLimit int64
	if bwLimitStr == "0" {
		bandwidthLimit = 0 
	} else {
		gb, _ := strconv.ParseInt(bwLimitStr, 10, 64)
		bandwidthLimit = gb * 1024 * 1024 * 1024 
	}

	token := generateToken()
	expiration := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")

	_, err := db.Exec(`INSERT INTO users (email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_used, bandwidth_limit) 
		VALUES (?, ?, ?, ?, 1, ?, 0, ?)`, email, subdomain, token, expiration, tcpPort, bandwidthLimit)
	
	if err != nil {
		log.Printf("Error inserting user: %v", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func generateToken() string {
	bytes := make([]byte, 8) 
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func renewUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	subdomain := r.FormValue("subdomain")
	newExpiration := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")
	db.Exec(`UPDATE users SET expiration_timestamp = ?, is_active = 1, bandwidth_used = 0 WHERE subdomain = ?`, newExpiration, subdomain)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func resetBandwidthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	subdomain := r.FormValue("subdomain")
	db.Exec(`UPDATE users SET bandwidth_used = 0, is_active = 1 WHERE subdomain = ?`, subdomain)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	subdomain := r.FormValue("subdomain")

	tunnelMutex.Lock()
	// Kill HTTP Session if it exists
	if session, exists := activeHttpTunnels[subdomain]; exists {
		session.Close()
		delete(activeHttpTunnels, subdomain)
	}
	// Kill TCP Session if it exists
	if session, exists := activeTcpTunnels[subdomain]; exists {
		session.Close()
		delete(activeTcpTunnels, subdomain)
	}
	tunnelMutex.Unlock()
	
	db.Exec(`DELETE FROM users WHERE subdomain = ?`, subdomain)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}