package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Global map to store active multiplexed sessions by subdomain
var activeTunnels = make(map[string]*yamux.Session)
var tunnelMutex sync.RWMutex

func startControlServer() {
	listener, err := net.Listen("tcp", ":7000")
	if err != nil {
		log.Fatalf("Failed to start Control Server: %v", err)
	}
	log.Println("Control Server listening on port :7000...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleClientConnection(conn)
	}
}

func handleClientConnection(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	token, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	token = strings.TrimSpace(token)
	conn.SetReadDeadline(time.Time{})

	var subdomain, expiration string
	var tcpPort int // Add this variable
	currentTime := time.Now().Format("2006-01-02 15:04:05")

	err = db.QueryRow(`SELECT subdomain, expiration_timestamp, tcp_port FROM users WHERE token = ? AND is_active = 1 AND expiration_timestamp > ?`, token, currentTime).Scan(&subdomain, &expiration, &tcpPort)
	if err != nil {
		conn.Write([]byte("ERROR: Invalid or expired token.\n"))
		conn.Close()
		return
	}

	conn.Write([]byte("SUCCESS: Authenticated as " + subdomain + "\n"))

	// --- NEW: Wrap the connection in a Yamux Server ---
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("Yamux error: %v", err)
		return
	}

	// --- NEW: Raw TCP Port Forwarding ---
	if tcpPort > 0 {
		// Start listening on the user's assigned public port (e.g., 0.0.0.0:20000)
		publicListener, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			log.Printf("Warning: Could not bind public TCP port %d for %s: %v", tcpPort, subdomain, err)
		} else {
			log.Printf("TCP Proxy ONLINE: VPS Port %d -> %s's local machine", tcpPort, subdomain)
			
			// Close the public port automatically if the client disconnects
			go func() {
				<-session.CloseChan()
				publicListener.Close()
			}()

			// Accept incoming public connections and pipe them down the tunnel
			go func() {
				for {
					publicConn, err := publicListener.Accept()
					if err != nil {
						break // Exits loop when listener is closed
					}

					go func(pConn net.Conn) {
						defer pConn.Close()
						
						// Open a new Yamux stream to the desktop client
						stream, err := session.Open()
						if err != nil {
							return
						}
						defer stream.Close()

						// Pipe the raw bytes in both directions simultaneously
						go io.Copy(stream, pConn)
						io.Copy(pConn, stream)
					}(publicConn)
				}
			}()
		}
	}

	// Save the session to our global map so the HTTP proxy can find it
	tunnelMutex.Lock()
	activeTunnels[subdomain] = session
	tunnelMutex.Unlock()

	log.Printf("Tunnel ONLINE: %s.tun.robotservice.eu.org", subdomain)

	// Keep the function running until the client disconnects
	<-session.CloseChan()

	// Clean up when they disconnect
	tunnelMutex.Lock()
	delete(activeTunnels, subdomain)
	tunnelMutex.Unlock()
	log.Printf("Tunnel OFFLINE: %s", subdomain)
}