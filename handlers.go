package main

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/robfig/cron/v3"
)

//go:embed templates/*
var templatesFS embed.FS

func setupHTTPHandlers() {
	// Static files
	http.HandleFunc("/style.css", serveStaticFile("text/css", "templates/style.css"))
	// http.HandleFunc("/style.css", serveStaticFile("text/css", "templates/pico.min.css"))

	// Application routes
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addJobHandler)
	http.HandleFunc("/run/", runHandler)
	http.HandleFunc("/delete/", deleteHandler)
	http.HandleFunc("/logs/", logsHandler)
}

func serveStaticFile(contentType, filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := templatesFS.ReadFile(filePath)
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobs, err := getJobsFromDB()
	if err != nil {
		http.Error(w, fmt.Sprintf("DB error: %v", err), http.StatusInternalServerError)
		return
	}

	tmpl := template.Must(template.ParseFS(templatesFS, "templates/index.html"))
	if err := tmpl.Execute(w, map[string]interface{}{"Jobs": jobs}); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func addJobHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Serve the add job form
		tmpl := template.Must(template.ParseFS(templatesFS, "templates/add.html"))
		if err := tmpl.Execute(w, nil); err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
		}

	case http.MethodPost:
		// Process form submission
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}

		name := r.FormValue("name")
		schedule := r.FormValue("schedule")
		command := r.FormValue("command")

		if name == "" || schedule == "" || command == "" {
			http.Error(w, "All fields are required", http.StatusBadRequest)
			return
		}

		// Validate cron expression
		if _, err := cron.ParseStandard(schedule); err != nil {
			http.Error(w, "Invalid cron expression: "+err.Error(), http.StatusBadRequest)
			return
		}

		res, err := db.Exec(
			"INSERT INTO jobs(name, schedule, command) VALUES(?, ?, ?)",
			name, schedule, command,
		)

		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to add job: %v", err), http.StatusInternalServerError)
			return
		}

		id, _ := res.LastInsertId()
		j := Job{ID: int(id), Name: name, Schedule: schedule, Command: command}
		registerCron(j)

		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func runHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := strconv.Atoi(r.URL.Path[len("/run/"):])
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	var j Job
	err = db.QueryRow("SELECT name, command FROM jobs WHERE id = ?", id).
		Scan(&j.Name, &j.Command)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	go runJob(id, j.Name, j.Command)
	w.Write([]byte("Job started in background"))
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := strconv.Atoi(r.URL.Path[len("/delete/"):])
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	mu.Lock()
	if entryID, ok := cronMap[id]; ok {
		c.Remove(entryID)
		delete(cronMap, id)
	}
	mu.Unlock()

	// Use transaction for atomic deletion
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback() // Will be ignored if tx is committed

	_, err = tx.Exec("DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec("DELETE FROM job_runs WHERE job_id = ?", id)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if err = tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := strconv.Atoi(r.URL.Path[len("/logs/"):])
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	var j Job
	err = db.QueryRow("SELECT id, name FROM jobs WHERE id = ?", id).
		Scan(&j.ID, &j.Name)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	rows, err := db.Query(
		"SELECT run_at, status, output FROM job_runs WHERE job_id = ? ORDER BY run_at DESC",
		id,
	)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var logs []Run
	for rows.Next() {
		var logEntry Run
		if err := rows.Scan(&logEntry.RunAt, &logEntry.Status, &logEntry.Output); err != nil {
			log.Printf("Failed to scan log row: %v", err)
			continue
		}
		logs = append(logs, logEntry)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	tmpl := template.Must(template.ParseFS(templatesFS, "templates/logs.html"))
	if err := tmpl.Execute(w, map[string]interface{}{"Job": j, "Logs": logs}); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}
