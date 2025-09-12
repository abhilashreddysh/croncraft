package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/abhilashreddysh/croncraft/internal/db"
	"github.com/abhilashreddysh/croncraft/internal/handlers"
	"github.com/abhilashreddysh/croncraft/internal/jobs"
)

const (
	DBFile        = "croncraft.db"
	
	serverPort    = ":8080"
)

func main() {
	if err := db.InitializeDatabase(DBFile); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	jobs.InitializeCron()
	defer jobs.C.Stop()

	// Load existing jobs
	jobs.LoadJobs()

	handlers.SetupHTTPHandlers()

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
	if db.DB != nil {
		// Flush all pending WAL changes into the main DB
		if _, err := db.DB.Exec("PRAGMA wal_checkpoint(FULL);"); err != nil {
			log.Printf("Failed to checkpoint WAL: %v", err)
		}

		if err := db.DB.Close(); err != nil {
			log.Printf("Failed to close DB: %v", err)
		}
	}

	if jobs.C != nil {
		jobs.C.Stop()
	}
}