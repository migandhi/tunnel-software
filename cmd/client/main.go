package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	token := flag.String("token", "", "Your 16-character auth token")
	localPort := flag.String("local", "8000", "The local port to expose (e.g., 8000)")
	flag.Parse()

	if *token == "" {
		fmt.Println("Usage: client --token <your_token> --local <port>")
		os.Exit(1)
	}

	serverAddr := "tun.robotservice.eu.org:7000"

	// THE RESILIENCE LOOP
	// This keeps the program running forever, retrying on failure.
	for {
		fmt.Printf("Connecting to %s...\n", serverAddr)
		
		err := runTunnel(serverAddr, *token, *localPort)
		if err != nil {
			log.Printf("Connection lost or failed: %v", err)
		}
		
		fmt.Println("Reconnecting in 5 seconds...")
		time.Sleep(5 * time.Second)
	}
}

// runTunnel handles a single connection lifecycle
func runTunnel(serverAddr, token, localPort string) error {
	// Added a 10-second timeout so it doesn't hang if the internet is completely down
	conn, err := net.DialTimeout("tcp", serverAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 1. Authenticate
	conn.Write([]byte(token + "\n"))
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read server response: %v", err)
	}
	fmt.Print("Server: ", response)

	if len(response) > 7 && response[:7] == "SUCCESS" {
		fmt.Printf("Tunneling public traffic to localhost:%s...\n", localPort)
		
		// 2. Wrap the connection in a Yamux Client
		session, err := yamux.Client(conn, nil)
		if err != nil {
			return fmt.Errorf("yamux setup failed: %v", err)
		}
		defer session.Close()

		// 3. Listen for incoming streams from the VPS
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return fmt.Errorf("session ended: %v", err)
			}

			// 4. Handle each web/tcp request in the background
			go func(remote net.Conn) {
				defer remote.Close()
				
				local, err := net.Dial("tcp", "127.0.0.1:"+localPort)
				if err != nil {
					log.Printf("Failed to connect to local port %s. Is your app running?", localPort)
					return
				}
				defer local.Close()

				go io.Copy(remote, local)
				io.Copy(local, remote)
			}(stream)
		}
	}
	
	// If the server responded with an ERROR (like an expired token)
	return fmt.Errorf("authentication rejected")
}