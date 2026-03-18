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
	db                *sql.DB
	activeHttpTunnels = make(map[string]*yamux.Session)
	activeTcpTunnels  = make(map[string]*yamux.Session)
	tunnelMutex       sync.RWMutex
)

// --- BANDWIDTH TRACKER ---
var (
	bwMutex sync.Mutex
	bwUsage = make(map[string]int64)
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
		expectedUsername := "admin"
		expectedPassword := "yahusein5253" // Your custom password

		if !ok || username != expectedUsername || password != expectedPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted Admin Area"`)
			http.Error(w, "Unauthorized Access", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "tunnel.db?_busy_timeout=5000")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Ensure tables exist
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

	go startControlServer()

	// Admin UI Server (Port 3050)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", basicAuth(adminDashboardHandler))
		mux.HandleFunc("/create-user", basicAuth(createUserHandler))
		mux.HandleFunc("/renew-user", basicAuth(renewUserHandler))
		mux.HandleFunc("/reset-bandwidth", basicAuth(resetBandwidthHandler))
		mux.HandleFunc("/delete-user", basicAuth(deleteUserHandler))
		mux.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("./downloads"))))
		
		log.Println("Starting Admin UI on port :3050...")
		if err := http.ListenAndServe(":3050", mux); err != nil {
			log.Fatalf("Admin UI failed: %v", err)
		}
	}()

	// HTTP Tunnel Proxy & Landing Page (Port 8080)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			
			// 1. Handle Public Root Domain Traffic (Landing Page & APIs)
			if r.Host == "tun.robotservice.eu.org" {
				
				// --- Razorpay Automated Checkout APIs ---
				if r.URL.Path == "/api/create-order" {
					createOrderHandler(w, r)
					return
				}
				if r.URL.Path == "/api/verify-payment" {
					verifyPaymentHandler(w, r)
					return
				}

				// --- Standard Landing Page ---
				tmpl, err := template.ParseFiles("templates/index.html")
				if err != nil {
					http.Error(w, "Landing page error.", http.StatusInternalServerError)
					return
				}
				tmpl.Execute(w, nil)
				return
			}

			// 2. Handle Subdomain Traffic (The actual HTTP Tunnels)
			hostParts := strings.Split(r.Host, ".")
			subdomain := hostParts[0]

			tunnelMutex.RLock()
			session, exists := activeHttpTunnels[subdomain] // Route only to HTTP sessions
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
						return &TrackingConn{Conn: stream, subdomain: subdomain}, nil
					},
				},
			}
			proxy.ServeHTTP(w, r)
		})
		
		log.Println("Starting HTTP Tunnel Proxy & Web Server on port :8080...")
		if err := http.ListenAndServe(":8080", mux); err != nil {
			log.Fatalf("Tunnel Proxy failed: %v", err)
		}
	}()

	// Background Enforcer
	for {
		time.Sleep(10 * time.Second)
		currentTime := time.Now().Format("2006-01-02 15:04:05")

		bwMutex.Lock()
		for sub, bytes := range bwUsage {
			if bytes > 0 {
				db.Exec("UPDATE users SET bandwidth_used = bandwidth_used + ? WHERE subdomain = ?", bytes, sub)
				bwUsage[sub] = 0
			}
		}
		bwMutex.Unlock()

		rows, err := db.Query(`SELECT subdomain FROM users 
			WHERE is_active = 1 AND 
			(expiration_timestamp <= ? OR (bandwidth_limit > 0 AND bandwidth_used >= bandwidth_limit))`, currentTime)
		
		if err == nil {
			for rows.Next() {
				var sub string
				rows.Scan(&sub)
				
				tunnelMutex.Lock()
				// Kill HTTP Session if active
				if session, exists := activeHttpTunnels[sub]; exists {
					session.Close()
					delete(activeHttpTunnels, sub)
				}
				// Kill TCP Session if active
				if session, exists := activeTcpTunnels[sub]; exists {
					session.Close()
					delete(activeTcpTunnels, sub)
				}
				tunnelMutex.Unlock()
				
				db.Exec("UPDATE users SET is_active = 0 WHERE subdomain = ?", sub)
			}
			rows.Close()
		}
	}
}