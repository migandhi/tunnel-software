package main

import (
	"context"
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
		mux.HandleFunc("/", adminDashboardHandler)
		mux.HandleFunc("/create-user", createUserHandler)
		
		// Serve the downloads folder publicly
		mux.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("./downloads"))))
		
		log.Println("Starting Admin UI on port :3050...")
		if err := http.ListenAndServe(":3050", mux); err != nil {
			log.Fatalf("Admin UI failed: %v", err)
		}
	}()

	// ---------------------------------------------------------
	// 2. Start the HTTP Tunnel Proxy (Port 8080)
	// ---------------------------------------------------------
	go func() {
		mux := http.NewServeMux()
		
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Extract the subdomain (e.g., "myapp" from "myapp.tun.robotservice.eu.org")
			hostParts := strings.Split(r.Host, ".")
			subdomain := hostParts[0]

			// Look up the active tunnel session
			tunnelMutex.RLock()
			session, exists := activeTunnels[subdomain]
			tunnelMutex.RUnlock()

			if !exists {
				http.Error(w, "Tunnel is offline or does not exist.", http.StatusBadGateway)
				return
			}

			// Create a reverse proxy that dials over our Yamux stream instead of the internet
			proxy := &httputil.ReverseProxy{
				Director: func(req *http.Request) {
					req.URL.Scheme = "http"
					req.URL.Host = "localhost" // This gets ignored by the custom dialer
				},
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						// Open a new stream down the tunnel to the user's desktop!
						return session.Open()
					},
				},
			}

			// Serve the traffic
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