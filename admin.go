package main

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"time"
)

// User struct matches our SQLite database columns
type User struct {
	ID         int
	Email      string
	Subdomain  string
	Token      string
	Expiration string
	IsActive   bool
	TCPPort    int // New field
}

// Generate a secure random token for the desktop client
func generateToken() string {
	bytes := make([]byte, 8) // 8 bytes = 16 hex characters
	if _, err := rand.Read(bytes); err != nil {
		log.Fatalf("Failed to generate token: %v", err)
	}
	return hex.EncodeToString(bytes)
}

// Handler: Show the Admin Dashboard
func adminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, email, subdomain, token, expiration_timestamp, is_active, tcp_port FROM users")
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Email, &u.Subdomain, &u.Token, &u.Expiration, &u.IsActive, &u.TCPPort)
		users = append(users, u)
	}

	tmpl := template.Must(template.ParseFiles("templates/admin.html"))
	tmpl.Execute(w, struct{ Users []User }{users})
}

// Handler: Process the "Activate New User" form
func createUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}

	email := r.FormValue("email")
	subdomain := r.FormValue("subdomain")
	tcpPort := r.FormValue("tcp_port") // Retrieve the port from the form
	if tcpPort == "" {
		tcpPort = "0"
	}
	token := generateToken()
	expiration := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")

	_, err := db.Exec(`INSERT INTO users (email, subdomain, token, expiration_timestamp, is_active, tcp_port) 
		VALUES (?, ?, ?, ?, 1, ?)`, email, subdomain, token, expiration, tcpPort)
	
	if err != nil {
		log.Printf("Error inserting user: %v", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}