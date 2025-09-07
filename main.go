package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

const (
	dbFile        = "croncraft.db"
	MaxLogsPerJob = 20
	serverPort    = ":8080"
)

func main() {
	if err := initializeDatabase(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	initializeCron()
	defer c.Stop()

	// Load existing jobs
	loadJobs()

	setupHTTPHandlers()

	// Graceful shutdown handling
	setupSignalHandling()

	log.Printf("CronCraft running at http://localhost%s", serverPort)
	if err := http.ListenAndServe(serverPort, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func setupSignalHandling() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("Shutting down CronCraft gracefully...")
		shutdown()
		os.Exit(0)
	}()
}

func shutdown() {
	if db != nil {
		if _, err := db.Exec("PRAGMA wal_checkpoint(FULL);"); err != nil {
			log.Printf("Failed to checkpoint WAL: %v", err)
		}
		db.Close()
	}

	if c != nil {
		c.Stop()
	}
}
