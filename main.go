package main

import (
	"context"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
)

func main() {
	// 1. Initialize the SQLite Database
	initDB()

	// 2. Start the Background Enforcer
	go startSubscriptionEnforcer()

	// 3. Start the Admin UI Server (Port 3050)

	go func() {
		mux := http.NewServeMux()
		
		// Wrap the sensitive routes in our security middleware
		mux.HandleFunc("/", basicAuth(adminDashboardHandler))
		mux.HandleFunc("/create-user", basicAuth(createUserHandler))
		
		// Leave the downloads folder public so users can actually get the client
		mux.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("./downloads"))))
		
		log.Println("Starting Admin UI on port :3050...")
		if err := http.ListenAndServe(":3050", mux); err != nil {
			log.Fatalf("Admin UI failed: %v", err)
		}
	}()

	// ---------------------------------------------------------
	// 2. Start the HTTP Tunnel Proxy (Port 8080)
	// ---------------------------------------------------------
	// 3. Start the HTTP Tunnel Proxy (Port 8080)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			
			// --- NEW: Serve Landing Page on the Root Domain ---
			if r.Host == "tun.robotservice.eu.org" {
				tmpl, err := template.ParseFiles("templates/index.html")
				if err != nil {
					http.Error(w, "Landing page under construction.", http.StatusInternalServerError)
					return
				}
				tmpl.Execute(w, nil)
				return
			}

			// --- EXISTING: Proxy Subdomains down the Tunnel ---
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
						return session.Open()
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



// ---------------------------------------------------------
	// 3. Start the Control Server (Port 7000)
	// ---------------------------------------------------------
	// This function contains an infinite loop, so it will keep the program running.
	startControlServer()
}

	// Security Middleware: Protects routes with a Username and Password
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

		// If credentials match, proceed to the requested page
		next(w, r)
	}
}