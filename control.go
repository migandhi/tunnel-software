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
			continue
		}
		go handleControlConnection(conn)
	}
}

func handleControlConnection(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	authLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	// Parse "TOKEN|MODE" (e.g., "XYZ123|HTTP" or "XYZ123|TCP")
	authLine = strings.TrimSpace(authLine)
	parts := strings.Split(authLine, "|")
	if len(parts) != 2 {
		conn.Write([]byte("ERROR: Invalid client version. Please redownload the client.\n"))
		conn.Close()
		return
	}
	token := parts[0]
	mode := parts[1] // Will be "HTTP" or "TCP"

	var subdomain, expiration string
	var tcpPort int
	currentTime := time.Now().Format("2006-01-02 15:04:05")
	
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

	if mode == "TCP" && tcpPort == 0 {
		conn.Write([]byte("ERROR: Your plan does not include a TCP port.\n"))
		conn.Close()
		return
	}

	// Calculate days left for terminal warning
	// NEW: Fix date parsing for the terminal warning
	cleanExp := strings.Replace(expiration, "T", " ", 1)
	cleanExp = strings.Replace(cleanExp, "Z", "", 1)
	
	expTime, _ := time.Parse("2006-01-02 15:04:05", cleanExp)
	daysLeft := int(time.Until(expTime).Hours() / 24)

	if daysLeft <= 3 {
		conn.Write([]byte(fmt.Sprintf("SUCCESS: Authenticated|WARNING: Your token expires in %d days! Message support on WhatsApp to renew.\n", daysLeft)))
	} else {
		conn.Write([]byte("SUCCESS: Authenticated\n"))
	}
	
	log.Printf("Client connected: %s (Mode: %s)", subdomain, mode)
	

	session, err := yamux.Server(conn, nil)
	if err != nil {
		conn.Close()
		return
	}

	// Sort the connection into the correct map
	tunnelMutex.Lock()
	if mode == "HTTP" {
		if oldSession, exists := activeHttpTunnels[subdomain]; exists {
			oldSession.Close()
		}
		activeHttpTunnels[subdomain] = session
	} else if mode == "TCP" {
		if oldSession, exists := activeTcpTunnels[subdomain]; exists {
			oldSession.Close()
		}
		activeTcpTunnels[subdomain] = session
	}
	tunnelMutex.Unlock()

	// If this is a TCP connection, start the raw port forwarder
	if mode == "TCP" && tcpPort > 0 {
		publicListener, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			log.Printf("Warning: Could not bind TCP port %d for %s: %v", tcpPort, subdomain, err)
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
	if mode == "HTTP" && activeHttpTunnels[subdomain] == session {
		delete(activeHttpTunnels, subdomain)
	} else if mode == "TCP" && activeTcpTunnels[subdomain] == session {
		delete(activeTcpTunnels, subdomain)
	}
	tunnelMutex.Unlock()
	log.Printf("Client disconnected: %s (Mode: %s)", subdomain, mode)
}