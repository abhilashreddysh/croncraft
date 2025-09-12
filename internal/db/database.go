package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"github.com/abhilashreddysh/croncraft/internal/models"
	"github.com/abhilashreddysh/croncraft/internal/utils"
)

const (MaxLogsPerJob = 10)

var DB *sql.DB

func InitializeDatabase(dbFile string) error {
	// Create DB file if it doesn't exist
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		file, err := os.Create(dbFile)
		if err != nil {
			return fmt.Errorf("failed to create DB file: %w", err)
		}
		file.Close()
	}

	var err error
	DB, err = sql.Open("sqlite", dbFile)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool parameters
	DB.SetMaxOpenConns(1) // Critical for SQLite to avoid locking issues
	DB.SetMaxIdleConns(1)
	DB.SetConnMaxLifetime(0) // Connections don't timeout

	// Verify connection
	if err = DB.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := DB.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("Failed to enable WAL mode: %v", err)
	}

	// Enable foreign keys
	if _, err := DB.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		log.Printf("Failed to enable foreign keys: %v", err)
	}

	// Create tables if they don't exist
	queries := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			schedule TEXT NOT NULL,
			command TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TRIGGER IF NOT EXISTS trg_jobs_updated_at
		AFTER UPDATE ON jobs
		FOR EACH ROW
		BEGIN
			UPDATE jobs
			SET updated_at = CURRENT_TIMESTAMP
			WHERE id = OLD.id;
		END;`,
		`CREATE TABLE IF NOT EXISTS job_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id INTEGER NOT NULL,
			run_at TEXT NOT NULL,
			status TEXT NOT NULL,
			output TEXT,
			duration_ms INT,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		"CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id)",
		"CREATE INDEX IF NOT EXISTS idx_job_runs_run_at ON job_runs(run_at DESC)",
	}

	for _, query := range queries {
		if _, err := DB.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query %s: %w", query, err)
		}
	}

	return nil
}

func GetJobsFromDB() ([]models.Job, error) {
	var jobs []models.Job

	err := utils.RetryDBOperation(func() error {
		rows, err := DB.Query(`
    SELECT j.id, j.name, j.schedule, j.command, j.status,
           COALESCE((
               SELECT MAX(r.run_at)
               FROM job_runs r
               WHERE r.job_id = j.id
           ), '') AS last_run
			FROM jobs j
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var j models.Job
			var lastRun sql.NullString // or sql.NullTime if it's a DATETIME
			if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command, &j.Status, &lastRun); err != nil {
				log.Printf("Failed to scan job row: %v", err)
				continue
			}
			j.LastRun = utils.NullTimeAgo(lastRun)
			jobs = append(jobs, j)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}

	return jobs, nil
}


