package main

import (
	"context"
	"database/sql"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	_ "github.com/mattn/go-sqlite3"
)

var (
	db            *sql.DB
	activeTunnels = make(map[string]*yamux.Session)
	tunnelMutex   sync.RWMutex
)

// --- BANDWIDTH TRACKER ---
var (
	bwMutex sync.Mutex
	bwUsage = make(map[string]int64) // Maps subdomain -> bytes used
)

func addBytes(subdomain string, n int64) {
	bwMutex.Lock()
	bwUsage[subdomain] += n
	bwMutex.Unlock()
}

type TrackingConn struct {
	net.Conn
	subdomain string
}

func (t *TrackingConn) Read(b []byte) (int, error) {
	n, err := t.Conn.Read(b)
	if n > 0 {
		addBytes(t.subdomain, int64(n))
	}
	return n, err
}

func (t *TrackingConn) Write(b []byte) (int, error) {
	n, err := t.Conn.Write(b)
	if n > 0 {
		addBytes(t.subdomain, int64(n))
	}
	return n, err
}
// -------------------------

// Security Middleware for Admin UI
func basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		// CHANGE THESE CREDENTIALS!
		expectedUsername := "admin"
		expectedPassword := "yahusein5253"

		if !ok || username != expectedUsername || password != expectedPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted Admin Area"`)
			http.Error(w, "Unauthorized Access. Nice try!", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "./tunnel.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Ensure tables exist with bandwidth columns
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT,
		subdomain TEXT UNIQUE,
		token TEXT UNIQUE,
		expiration_timestamp TEXT,
		is_active BOOLEAN,
		tcp_port INTEGER DEFAULT 0,
		bandwidth_used INTEGER DEFAULT 0,
		bandwidth_limit INTEGER DEFAULT 53687091200
	)`)
	if err != nil {
		log.Fatalf("Failed to initialize database tables: %v", err)
	}

	// 1. Start the Control Server (Port 7000)
	go startControlServer()

	// 2. Start the Admin UI Server (Port 3050)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", basicAuth(adminDashboardHandler))
		mux.HandleFunc("/create-user", basicAuth(createUserHandler))
		mux.HandleFunc("/renew-user", basicAuth(renewUserHandler)) // <-- ADD THIS LINE
		mux.HandleFunc("/delete-user", basicAuth(deleteUserHandler)) // <-- ADD THIS LINE
		mux.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("./downloads"))))
		
		log.Println("Starting Admin UI on port :3050...")
		if err := http.ListenAndServe(":3050", mux); err != nil {
			log.Fatalf("Admin UI failed: %v", err)
		}
	}()

	// 3. Start the HTTP Tunnel Proxy (Port 8080)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Serve Landing Page on the Root Domain
			if r.Host == "tun.robotservice.eu.org" {
				tmpl, err := template.ParseFiles("templates/index.html")
				if err != nil {
					http.Error(w, "Landing page under construction.", http.StatusInternalServerError)
					return
				}
				tmpl.Execute(w, nil)
				return
			}

			// Proxy Subdomains down the Tunnel
			hostParts := strings.Split(r.Host, ".")
			subdomain := hostParts[0]

			tunnelMutex.RLock()
			session, exists := activeTunnels[subdomain]
			tunnelMutex.RUnlock()

			if !exists {
				http.Error(w, "Tunnel is offline or does not exist.", http.StatusBadGateway)
				return
			}

			proxy := &httputil.ReverseProxy{
				Director: func(req *http.Request) {
					req.URL.Scheme = "http"
					req.URL.Host = "localhost" 
				},
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						stream, err := session.Open()
						if err != nil {
							return nil, err
						}
						// Wrap the stream to count bytes
						return &TrackingConn{Conn: stream, subdomain: subdomain}, nil
					},
				},
			}
			proxy.ServeHTTP(w, r)
		})
		
		log.Println("Starting HTTP Tunnel Proxy on port :8080...")
		if err := http.ListenAndServe(":8080", mux); err != nil {
			log.Fatalf("Tunnel Proxy failed: %v", err)
		}
	}()

	// 4. Background Enforcer & Bandwidth Sync (Runs every 10 seconds)
	for {
		time.Sleep(10 * time.Second)
		currentTime := time.Now().Format("2006-01-02 15:04:05")

		// A. Sync Bandwidth to Database
		bwMutex.Lock()
		for sub, bytes := range bwUsage {
			if bytes > 0 {
				db.Exec("UPDATE users SET bandwidth_used = bandwidth_used + ? WHERE subdomain = ?", bytes, sub)
				bwUsage[sub] = 0
			}
		}
		bwMutex.Unlock()

		// B. Enforce Limits (Kick expired OR over-limit users)
		rows, err := db.Query(`SELECT subdomain FROM users 
			WHERE is_active = 1 AND 
			(expiration_timestamp <= ? OR (bandwidth_limit > 0 AND bandwidth_used >= bandwidth_limit))`, currentTime)
		
		if err == nil {
			for rows.Next() {
				var sub string
				rows.Scan(&sub)
				
				tunnelMutex.Lock()
				if session, exists := activeTunnels[sub]; exists {
					log.Printf("ENFORCER: Disconnecting %s (Expired or Out of Bandwidth)", sub)
					session.Close()
					delete(activeTunnels, sub)
				}
				tunnelMutex.Unlock()
				
				db.Exec("UPDATE users SET is_active = 0 WHERE subdomain = ?", sub)
			}
			rows.Close()
		}
	}
}