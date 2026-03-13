package main

import (
	"log"
	"time"
)

// startSubscriptionEnforcer runs continuously in the background
func startSubscriptionEnforcer() {
	// Check the database every 1 minute. 
	// (In production, you might change this to 5 or 10 minutes)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	log.Println("Subscription Enforcer active. Monitoring for expired accounts...")

	for range ticker.C {
		currentTime := time.Now().Format("2006-01-02 15:04:05")

		// 1. Find users who are marked active but their time has run out
		rows, err := db.Query(`SELECT subdomain FROM users WHERE is_active = 1 AND expiration_timestamp <= ?`, currentTime)
		if err != nil {
			log.Printf("Enforcer DB error: %v", err)
			continue
		}

		var expiredSubdomains []string
		for rows.Next() {
			var sub string
			if err := rows.Scan(&sub); err == nil {
				expiredSubdomains = append(expiredSubdomains, sub)
			}
		}
		rows.Close()

		// 2. Process each expired user
		for _, sub := range expiredSubdomains {
			log.Printf("ENFORCER: Account for '%s' has expired. Revoking access.", sub)

			// Update their database status to inactive (0)
			_, err := db.Exec(`UPDATE users SET is_active = 0 WHERE subdomain = ?`, sub)
			if err != nil {
				log.Printf("Failed to update status for %s: %v", sub, err)
			}

			// Forcefully sever their active connection if they are currently tunneling
			tunnelMutex.Lock()
			if session, exists := activeTunnels[sub]; exists {
				session.Close() // This kills the Yamux stream instantly
				delete(activeTunnels, sub)
				log.Printf("ENFORCER: Dropped active network connection for '%s'.", sub)
			}
			tunnelMutex.Unlock()
		}
	}
}