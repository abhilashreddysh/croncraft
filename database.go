package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func initializeDatabase() error {
	// Create DB file if it doesn't exist
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		file, err := os.Create(dbFile)
		if err != nil {
			return fmt.Errorf("failed to create DB file: %w", err)
		}
		file.Close()
	}

	var err error
	db, err = sql.Open("sqlite", dbFile)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool parameters
	db.SetMaxOpenConns(1) // Critical for SQLite to avoid locking issues
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // Connections don't timeout

	// Verify connection
	if err = db.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("Failed to enable WAL mode: %v", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		log.Printf("Failed to enable foreign keys: %v", err)
	}

	// Create tables if they don't exist
	queries := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			schedule TEXT NOT NULL,
			command TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS job_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id INTEGER NOT NULL,
			run_at TEXT NOT NULL,
			status TEXT NOT NULL,
			output TEXT,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		"CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id)",
		"CREATE INDEX IF NOT EXISTS idx_job_runs_run_at ON job_runs(run_at DESC)",
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query %s: %w", query, err)
		}
	}

	return nil
}

func getJobsFromDB() ([]Job, error) {
	var jobs []Job

	err := retryDBOperation(func() error {
		rows, err := db.Query("SELECT id, name, schedule, command FROM jobs")
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var j Job
			if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command); err != nil {
				log.Printf("Failed to scan job row: %v", err)
				continue
			}
			jobs = append(jobs, j)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}

	return jobs, nil
}

func saveJobRun(jobID int, runAt, status, output string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	_, err := db.Exec(
		"INSERT INTO job_runs(job_id, run_at, status, output) VALUES(?, ?, ?, ?)",
		jobID, runAt, status, output,
	)
	return err
}

func pruneLogs(jobID int) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	_, err := db.Exec(`
		DELETE FROM job_runs 
		WHERE id NOT IN (
			SELECT id FROM job_runs 
			WHERE job_id = ? 
			ORDER BY run_at DESC 
			LIMIT ?
		) AND job_id = ?`,
		jobID, MaxLogsPerJob, jobID,
	)

	return err
}
