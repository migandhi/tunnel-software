package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	// Your Contabo VPS Address
	ServerAddr = "tun.robotservice.eu.org:7000"
	
	// IMPORTANT: This MUST exactly match the version string in your Server code!
	ClientVersion = "1.0.0" 
)

// AuthRequest matches the expected JSON payload on the server
type AuthRequest struct {
	Token    string `json:"token"`
	Version  string `json:"version"`
	Protocol string `json:"protocol"` // "http" or "tcp"
}

func main() {
	// 1. Define the exact flags from your website UI
	token := flag.String("token", "", "Your 16-character auth token")
	localHTTP := flag.String("local-http", "", "The local HTTP port to expose (e.g., 8000)")
	localTCP := flag.String("local-tcp", "", "The local TCP port to expose (e.g., 25565)")

	flag.Parse()

	// 2. Strict Validation: Ensure they don't mess up the commands
	if *token == "" {
		fmt.Println("Error: --token is required.")
		fmt.Println("Usage: tunnel-client --token <token> --local-http <port>")
		os.Exit(1)
	}
	if *localHTTP == "" && *localTCP == "" {
		fmt.Println("Error: You must specify either --local-http OR --local-tcp.")
		os.Exit(1)
	}
	if *localHTTP != "" && *localTCP != "" {
		fmt.Println("Error: You cannot run both at the same time. Choose one.")
		os.Exit(1)
	}

	// 3. Determine the Mode (HTTP vs TCP)
	var localPort string
	var protocol string

	if *localHTTP != "" {
		localPort = *localHTTP
		protocol = "http"
		fmt.Printf("Mode: HTTP Web Tunnel -> Routing to localhost:%s\n", localPort)
	} else {
		localPort = *localTCP
		protocol = "tcp"
		fmt.Printf("Mode: TCP Game/App Tunnel -> Routing to localhost:%s\n", localPort)
	}

	// 4. Start the Auto-Reconnect Loop
	for {
		err := startSession(*token, localPort, protocol)
		if err != nil {
			fmt.Printf("Disconnected: %v. Reconnecting in 5 seconds...\n", err)
		} else {
			fmt.Println("Disconnected normally. Reconnecting in 5 seconds...")
		}
		time.Sleep(5 * time.Second)
	}
}

func startSession(token, localPort, protocol string) error {
	fmt.Printf("Connecting to %s...\n", ServerAddr)
	
	// Connect to the Contabo GoTunnel Server
	conn, err := net.Dial("tcp", ServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Create Auth Payload
	authReq := AuthRequest{
		Token:    token,
		Version:  ClientVersion,
		Protocol: protocol,
	}

	// Send Auth JSON to Server
	reqBytes, _ := json.Marshal(authReq)
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return fmt.Errorf("failed to send auth: %v", err)
	}

	// Read Server Response
	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read auth response: %v", err)
	}

	// Print the server's reply (e.g., "Server: SUCCESS: Authenticated")
	fmt.Print(resp)

	// If the server explicitly rejected us (like the Version Error), stop trying to reconnect
	if strings.Contains(resp, "ERROR") {
		fmt.Println("Fatal authentication error. Exiting.")
		os.Exit(1)
	}

	// -----------------------------------------------------------------
	// NEW: Optimized Yamux Config for High Latency (India -> Germany)
	// -----------------------------------------------------------------
	ymxConfig := yamux.DefaultConfig()
	ymxConfig.MaxStreamWindowSize = 8 * 1024 * 1024 // 8MB window size for fast video streaming
	ymxConfig.KeepAliveInterval = 30 * time.Second  // 30-second ping to prevent idle timeouts

	session, err := yamux.Client(conn, ymxConfig)
	if err != nil {
		return fmt.Errorf("yamux initialization failed: %v", err)
	}
	defer session.Close()

	// Listen for incoming tunnel requests from the public internet
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return fmt.Errorf("session closed by server: %v", err)
		}
		// Proxy the traffic to their local app
		go proxyTraffic(stream, localPort)
	}
}

// proxyTraffic routes the incoming Yamux stream directly to localhost
func proxyTraffic(stream net.Conn, localPort string) {
	defer stream.Close()

	// Connect to the user's local application (e.g., localhost:8000)
	localApp, err := net.Dial("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		fmt.Printf("Error: Cannot reach local app on port %s. Is it running?\n", localPort)
		return
	}
	defer localApp.Close()

	// Pipe data back and forth concurrently
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(localApp, stream)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(stream, localApp)
		errc <- err
	}()
	
	<-errc
}