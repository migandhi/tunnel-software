package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"time"
	"strings" // Add this!

	"github.com/hashicorp/yamux"
)

func main() {
	token := flag.String("token", "", "Your unique tunnel token")
	server := flag.String("server", "tun.robotservice.eu.org:7000", "Control server address")
	localHTTP := flag.String("local-http", "", "Local port for HTTP web traffic")
	localTCP := flag.String("local-tcp", "", "Local port for raw TCP traffic")
	flag.Parse()

	if *token == "" {
		log.Fatal("Error: --token is required")
	}

	if *localHTTP == "" && *localTCP == "" {
		log.Fatal("Error: You must provide either --local-http or --local-tcp")
	}
	if *localHTTP != "" && *localTCP != "" {
		log.Fatal("Error: Please run HTTP and TCP in two separate terminal windows.")
	}

	mode := "HTTP"
	targetPort := *localHTTP
	if *localTCP != "" {
		mode = "TCP"
		targetPort = *localTCP
	}

	for {
		err := connectAndServe(*token, *server, targetPort, mode)
		log.Printf("Disconnected: %v. Reconnecting in 5 seconds...", err)
		time.Sleep(5 * time.Second)
	}
}

func connectAndServe(token, server, targetPort, mode string) error {
	conn, err := net.Dial("tcp", server)
	if err != nil {
		return fmt.Errorf("could not connect to server: %v", err)
	}
	defer conn.Close()

	// Send Token AND Mode to the server
	fmt.Fprintf(conn, "%s|%s\n", token, mode)

	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read auth response: %v", err)
	}

	// Clean up any stray newlines
	response = strings.TrimSpace(response)

	// We use HasPrefix now instead of an exact match, in case a warning is attached
	if !strings.HasPrefix(response, "SUCCESS: Authenticated") {
		return fmt.Errorf("auth failed: %s", response)
	}

	log.Println("Server: SUCCESS: Authenticated")
	
	// Check for a server warning attached to the response
	parts := strings.Split(response, "|")
	if len(parts) > 1 {
		log.Printf("⚠️ SERVER MESSAGE: %s", strings.TrimSpace(parts[1]))
	}

	log.Printf("Routing %s traffic to localhost:%s", mode, targetPort)

	session, err := yamux.Client(conn, nil)
	if err != nil {
		return fmt.Errorf("yamux error: %v", err)
	}

	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		go handleStream(stream, targetPort)
	}
}

func handleStream(stream net.Conn, targetPort string) {
	defer stream.Close()

	localConn, err := net.Dial("tcp", "localhost:"+targetPort)
	if err != nil {
		log.Printf("Could not connect to localhost:%s", targetPort)
		return
	}
	defer localConn.Close()

	go io.Copy(localConn, stream)
	io.Copy(stream, localConn)
}