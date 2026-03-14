package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

func startControlServer() {
	listener, err := net.Listen("tcp", ":7000")
	if err != nil {
		log.Fatalf("Control server failed to start: %v", err)
	}
	log.Println("Starting Control Server on port :7000...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		go handleControlConnection(conn)
	}
}

func handleControlConnection(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	token, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	token = strings.TrimSpace(token)
	conn.SetReadDeadline(time.Time{})

	var subdomain, expiration string
	var tcpPort int

	currentTime := time.Now().Format("2006-01-02 15:04:05")
	
	// Ensure token is valid, active, not expired, AND bandwidth is under the limit (or unlimited)
	err = db.QueryRow(`SELECT subdomain, expiration_timestamp, tcp_port FROM users 
		WHERE token = ? AND is_active = 1 
		AND expiration_timestamp > ? 
		AND (bandwidth_limit = 0 OR bandwidth_used < bandwidth_limit)`, 
		token, currentTime).Scan(&subdomain, &expiration, &tcpPort)

	if err != nil {
		conn.Write([]byte("ERROR: Invalid token, expired, or out of bandwidth\n"))
		conn.Close()
		return
	}

	conn.Write([]byte("SUCCESS: Authenticated\n"))
	log.Printf("Client connected: %s (Expires: %s)", subdomain, expiration)

	session, err := yamux.Server(conn, nil)
	if err != nil {
		conn.Close()
		return
	}

	tunnelMutex.Lock()
	if oldSession, exists := activeTunnels[subdomain]; exists {
		oldSession.Close()
	}
	activeTunnels[subdomain] = session
	tunnelMutex.Unlock()

	// --- Raw TCP Port Forwarding ---
	if tcpPort > 0 {
		publicListener, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			log.Printf("Warning: Could not bind public TCP port %d for %s: %v", tcpPort, subdomain, err)
		} else {
			log.Printf("TCP Proxy ONLINE: VPS Port %d -> %s's local machine", tcpPort, subdomain)
			
			go func() {
				<-session.CloseChan()
				publicListener.Close()
			}()

			go func() {
				for {
					publicConn, err := publicListener.Accept()
					if err != nil {
						break 
					}

					go func(pConn net.Conn) {
						defer pConn.Close()
						
						stream, err := session.Open()
						if err != nil {
							return
						}
						defer stream.Close()

						// Wrap the stream to count raw TCP bytes
						trackedStream := &TrackingConn{Conn: stream, subdomain: subdomain}

						go io.Copy(trackedStream, pConn)
						io.Copy(pConn, trackedStream)
					}(publicConn)
				}
			}()
		}
	}

	<-session.CloseChan()

	tunnelMutex.Lock()
	if activeTunnels[subdomain] == session {
		delete(activeTunnels, subdomain)
	}
	tunnelMutex.Unlock()
	log.Printf("Client disconnected: %s", subdomain)
}