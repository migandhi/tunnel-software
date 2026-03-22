package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/hashicorp/yamux"
)

const ExpectedClientVersion = "1.0.0"

type AuthRequest struct {
	Token    string `json:"token"`
	Version  string `json:"version"`
	Protocol string `json:"protocol"` 
}

// NEW: Tracks open TCP ports so we don't crash if a user reconnects
var activeListeners = make(map[string]net.Listener)

func startControlServer() {
	listener, err := net.Listen("tcp", ":7000")
	if err != nil {
		fmt.Println("Error starting control server:", err)
		return
	}
	fmt.Println("Control server listening on port 7000...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Failed to accept connection:", err)
			continue
		}
		go handleClientConnection(conn)
	}
}

func handleClientConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)
	authLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}

	var req AuthRequest
	if err := json.Unmarshal([]byte(authLine), &req); err != nil {
		conn.Write([]byte("Server: ERROR: Invalid request format\n"))
		conn.Close()
		return
	}

	if req.Version != ExpectedClientVersion {
		conn.Write([]byte("Server: ERROR: Invalid client version. Please redownload the client.\n"))
		conn.Close()
		return
	}

	var subdomain string
	var tcpPort int
	
	// Strict check: Must be active and not expired
	query := `SELECT subdomain, tcp_port FROM users 
			  WHERE token = ? AND is_active = 1 AND expiration_timestamp > CURRENT_TIMESTAMP`
	err = db.QueryRow(query, req.Token).Scan(&subdomain, &tcpPort)
	if err != nil {
		conn.Write([]byte("Server: ERROR: Subscription inactive or expired. Please renew.\n"))
		conn.Close()
		return
	}

	conn.Write([]byte("Server: SUCCESS: Authenticated\n"))

	ymxConfig := yamux.DefaultConfig()
	ymxConfig.MaxStreamWindowSize = 8 * 1024 * 1024 
	ymxConfig.KeepAliveInterval = 30 * time.Second  

	session, err := yamux.Server(conn, ymxConfig)
	if err != nil {
		fmt.Println("Yamux initialization error:", err)
		conn.Close()
		return
	}

	tunnelMutex.Lock()
	defer tunnelMutex.Unlock()

	if req.Protocol == "tcp" && tcpPort > 0 {
		portStr := strconv.Itoa(tcpPort)
		fmt.Printf("[+] New TCP Tunnel Online: Port %s\n", portStr)
		activeTcpTunnels[portStr] = session

		// NEW: Spin up the public listener on the VPS for this specific port
		if _, exists := activeListeners[portStr]; !exists {
			ln, err := net.Listen("tcp", ":"+portStr)
			if err == nil {
				activeListeners[portStr] = ln
				go handlePublicTCP(ln, portStr)
				fmt.Printf("[*] Successfully opened public port %s on VPS\n", portStr)
			} else {
				fmt.Printf("[!] Failed to open public port %s: %v\n", portStr, err)
			}
		}
	} else {
		fmt.Printf("[+] New HTTP Tunnel Online: %s.tun.robotservice.eu.org\n", subdomain)
		activeHttpTunnels[subdomain] = session
	}
}

// NEW: Bridges public internet traffic directly into the Yamux tunnel
func handlePublicTCP(ln net.Listener, portStr string) {
	for {
		publicConn, err := ln.Accept()
		if err != nil {
			continue // Keep listening even if one connection fails
		}
		
		go func(pubConn net.Conn) {
			defer pubConn.Close()

			tunnelMutex.RLock()
			session, ok := activeTcpTunnels[portStr]
			tunnelMutex.RUnlock()

			if !ok || session.IsClosed() {
				return // Drop traffic if the user's desktop client is offline
			}

			// Open a virtual stream inside the Yamux tunnel
			stream, err := session.Open()
			if err != nil {
				return
			}
			defer stream.Close()

			// Pipe data back and forth
			errc := make(chan error, 2)
			go func() {
				_, err := io.Copy(stream, pubConn)
				errc <- err
			}()
			go func() {
				_, err := io.Copy(pubConn, stream)
				errc <- err
			}()
			<-errc
		}(publicConn)
	}
}