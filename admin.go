package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
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
	BandwidthUsed  string
	BandwidthLimit string
	StatusHTML     template.HTML
}

func generateToken() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
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
	// 1. Inject F12 Logging Header
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<script>console.log('--- Go Backend Diagnostics Started ---');</script>\n")

	rows, err := db.Query(`SELECT id, email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_used, bandwidth_limit FROM users`)
	if err != nil {
		safeErr := strings.ReplaceAll(err.Error(), `"`, `\"`)
		fmt.Fprintf(w, "<script>console.error(\"DB Query Error: %s\");</script>\n", safeErr)
		return
	}
	defer rows.Close()

	var users []User
	rowCount := 0

	for rows.Next() {
		rowCount++
		var u User
		
		var id, tcpPort, rawUsed, rawLimit sql.NullInt64
		var email, subdomain, token, expiration sql.NullString
		
		// THE SPONGE: This safely absorbs literally any data type SQLite throws at it without crashing
		var chaoticIsActive interface{} 

		err := rows.Scan(&id, &email, &subdomain, &token, &expiration, &chaoticIsActive, &tcpPort, &rawUsed, &rawLimit)
		if err != nil {
			safeErr := strings.ReplaceAll(err.Error(), `"`, `\"`)
			fmt.Fprintf(w, "<script>console.error(\"Row %d Scan Error: %s\");</script>\n", rowCount, safeErr)
			continue
		}

		u.ID = int(id.Int64)
		u.Email = email.String
		u.Subdomain = subdomain.String
		u.Token = token.String
		u.Expiration = expiration.String
		u.TCPPort = int(tcpPort.Int64)

		// Safely figure out what SQLite actually handed us for the Active status
		switch v := chaoticIsActive.(type) {
		case int64:
			u.IsActive = v == 1
		case bool:
			u.IsActive = v
		case string:
			u.IsActive = (v == "true" || v == "1")
		case []byte:
			u.IsActive = (string(v) == "true" || string(v) == "1")
		default:
			u.IsActive = false
		}

		u.BandwidthUsed = formatBytes(rawUsed.Int64)
		if rawLimit.Int64 == 0 {
			u.BandwidthLimit = "Unlimited"
		} else {
			u.BandwidthLimit = formatBytes(rawLimit.Int64)
		}

		cleanExp := strings.Replace(u.Expiration, "T", " ", 1)
		cleanExp = strings.Replace(cleanExp, "Z", "", 1)
		
		expTime, err := time.Parse("2006-01-02 15:04:05", cleanExp)
		if err == nil {
			daysLeft := int(time.Until(expTime).Hours() / 24)

			if !u.IsActive {
				u.StatusHTML = template.HTML(`<span style="color: #dc3545; font-weight: bold;">Expired/Kicked</span>`)
			} else if daysLeft <= 3 {
				u.StatusHTML = template.HTML(fmt.Sprintf(`<span style="color: #ff8c00; font-weight: bold;">Expiring Soon (%d days)</span>`, daysLeft))
			} else {
				u.StatusHTML = template.HTML(`<span style="color: #28a745; font-weight: bold;">Active</span>`)
			}
		} else {
			u.StatusHTML = template.HTML(`<span style="color: #6c757d; font-weight: bold;">Unknown</span>`)
		}

		users = append(users, u)
	}

	fmt.Fprintf(w, "<script>console.log(\"Successfully extracted %d users from database\");</script>\n", len(users))

	tmpl, err := template.ParseFiles("templates/admin.html")
	if err != nil {
		safeErr := strings.ReplaceAll(err.Error(), `"`, `\"`)
		fmt.Fprintf(w, "<script>console.error(\"Template Parse Error: %s\");</script>\n", safeErr)
		return
	}

	// Catch the silent HTML crash and print it to the screen in bold red
	err = tmpl.Execute(w, users)
	if err != nil {
		fmt.Fprintf(w, "<br><br><div style='background:red;color:white;padding:20px;font-size:24px;font-weight:bold;text-align:center;'>HTML TEMPLATE CRASH:<br>%v</div>", err)
	}
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	subdomain := r.FormValue("subdomain")
	tcpPortStr := r.FormValue("tcp_port")
	limitGbStr := r.FormValue("limit_gb")

	tcpPort := 0
	if tcpPortStr != "" {
		tcpPort, _ = strconv.Atoi(tcpPortStr)
	}

	var limitBytes int64 = 0
	if limitGbStr != "" && limitGbStr != "unlimited" {
		limitGb, _ := strconv.Atoi(limitGbStr)
		limitBytes = int64(limitGb) * 1024 * 1024 * 1024
	}

	token := generateToken()
	expiration := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04:05")

	// Back to using '1' to enforce integers moving forward
	_, err = db.Exec(`INSERT INTO users (email, subdomain, token, expiration_timestamp, is_active, tcp_port, bandwidth_limit) 
		VALUES (?, ?, ?, ?, 1, ?, ?)`, email, subdomain, token, expiration, tcpPort, limitBytes)
	
	if err != nil {
		http.Error(w, "Failed to create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	subdomain := r.FormValue("subdomain")
	if subdomain != "" {
		db.Exec(`DELETE FROM users WHERE subdomain = ?`, subdomain)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func renewUserHandler(w http.ResponseWriter, r *http.Request) {
	subdomain := r.FormValue("subdomain")
	if subdomain != "" {
		newExpiration := time.Now().AddDate(0, 1, 0).Format("2006-01-02 15:04:05")
		db.Exec(`UPDATE users SET expiration_timestamp = ?, is_active = 1, bandwidth_used = 0 WHERE subdomain = ?`, newExpiration, subdomain)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func resetBandwidthHandler(w http.ResponseWriter, r *http.Request) {
	subdomain := r.FormValue("subdomain")
	if subdomain != "" {
		db.Exec(`UPDATE users SET bandwidth_used = 0 WHERE subdomain = ?`, subdomain)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}