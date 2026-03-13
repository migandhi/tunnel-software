package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

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
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		log.Fatalf("Connection failed: %v", err)
	}

	// 1. Authenticate
	conn.Write([]byte(*token + "\n"))
	response, _ := bufio.NewReader(conn).ReadString('\n')
	fmt.Print("Server: ", response)

	if len(response) > 7 && response[:7] == "SUCCESS" {
		fmt.Printf("Tunneling public traffic to localhost:%s...\n", *localPort)
		
		// 2. Wrap the connection in a Yamux Client
		session, err := yamux.Client(conn, nil)
		if err != nil {
			log.Fatalf("Yamux error: %v", err)
		}

		// 3. Listen for incoming streams from the VPS
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				log.Println("Server disconnected.")
				break
			}

			// 4. Handle each web request in the background
			go func(remote net.Conn) {
				defer remote.Close()
				
				// Dial the user's local application
				local, err := net.Dial("tcp", "127.0.0.1:"+*localPort)
				if err != nil {
					log.Printf("Failed to connect to local port %s. Is your app running?", *localPort)
					return
				}
				defer local.Close()

				// Pipe data back and forth simultaneously
				go io.Copy(remote, local)
				io.Copy(local, remote)
			}(stream)
		}
	}
}