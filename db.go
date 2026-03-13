package main

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

// Global database variable so other files can access it
var db *sql.DB

func initDB() {
	var err error
	// This creates a file named 'tunnel.db' in your project folder
	db, err = sql.Open("sqlite3", "./tunnel.db")
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}

	// Create the Users table if it doesn't exist
	createTableSQL := `CREATE TABLE IF NOT EXISTS users (
		id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		email TEXT UNIQUE NOT NULL,
		subdomain TEXT UNIQUE,      -- e.g., 'myapp' for myapp.tun.robotservice.eu.org
		token TEXT UNIQUE NOT NULL, -- The secret key the desktop client uses to connect
		expiration_timestamp DATETIME, -- The core of your manual subscription logic
		is_active BOOLEAN DEFAULT 0 -- 0 = Pending/Expired, 1 = Paid & Active
	);`

	_, err = db.Exec(createTableSQL)
	if err != nil {
		log.Fatalf("Error creating users table: %v", err)
	}

	log.Println("SQLite Database initialized and Users table verified.")
}