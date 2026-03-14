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
)

type User struct {
	ID             int
	Email          string
	Subdomain      string
	Token          string
	Expiration     string
	IsActive       bool
	TCPPort        int
	BandwidthUsed  string // Formatted for UI (e.g., "1.50 GB")
	BandwidthLimit string // Formatted for UI (e.g., "50.00 GB" or "Unlimited")
}

// formatBytes converts raw bytes into a readable string
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
	
	// Handle bandwidth limit selection
	bwLimitStr := r.FormValue("bandwidth_limit")
	var bandwidthLimit int64
	if bwLimitStr == "0" {
		bandwidthLimit = 0 // Unlimited
	} else {
		gb, _ := strconv.ParseInt(bwLimitStr, 10, 64)
		bandwidthLimit = gb * 1024 * 1024 * 1024 // Convert GB to Bytes
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
	bytes := make([]byte, 8) // 16 hex characters
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func renewUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}

	subdomain := r.FormValue("subdomain")
	
	// Calculate new expiration date (30 days from right now)
	newExpiration := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")

	// Update the database: Extend time, set active, and reset bandwidth used
	_, err := db.Exec(`UPDATE users SET expiration_timestamp = ?, is_active = 1, bandwidth_used = 0 WHERE subdomain = ?`, newExpiration, subdomain)
	
	if err != nil {
		log.Printf("Error renewing user %s: %v", subdomain, err)
	}
	
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}

	subdomain := r.FormValue("subdomain")

	// 1. Instantly kill their active connection if they are online
	tunnelMutex.Lock()
	if session, exists := activeTunnels[subdomain]; exists {
		log.Printf("ADMIN: Force disconnecting and deleting user %s", subdomain)
		session.Close()
		delete(activeTunnels, subdomain)
	}
	tunnelMutex.Unlock()
	
	// 2. Permanently delete their record from the database
	_, err := db.Exec(`DELETE FROM users WHERE subdomain = ?`, subdomain)
	
	if err != nil {
		log.Printf("Error deleting user %s: %v", subdomain, err)
	}
	
	http.Redirect(w, r, "/", http.StatusSeeOther)
}