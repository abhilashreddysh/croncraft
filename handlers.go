package main

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
)

//go:embed templates/*
var templatesFS embed.FS

func setupHTTPHandlers() {
	// Static files
	http.HandleFunc("/style.css", serveStaticFile("text/css", "templates/style.css"))

	// Application routes
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addJobHandler)
	http.HandleFunc("/run/", runHandler)
	http.HandleFunc("/delete/", deleteHandler)
	http.HandleFunc("/edit/", func(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		editJobFormHandler(w, r)
	case http.MethodPost:
		editJobSubmitHandler(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
})

	// http.HandleFunc("/logs/", logsHandler)
	http.HandleFunc("/logs/", func(w http.ResponseWriter, r *http.Request) {
    if strings.HasSuffix(r.URL.Path, "/output") {
        outputHandler(w, r)
    } else {
        logsHandler(w, r)
    }})
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

    // Parse job ID from /logs/{jobID}
    id, err := strconv.Atoi(r.URL.Path[len("/logs/"):])
    if err != nil {
        http.Error(w, "Invalid job ID", http.StatusBadRequest)
        return
    }

    // Fetch job info
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

    // Fetch runs for this job (NO output here, just ID + metadata)
    rows, err := db.Query(
        "SELECT id, run_at, status FROM job_runs WHERE job_id = ? ORDER BY run_at DESC",
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
        if err := rows.Scan(&logEntry.ID, &logEntry.RunAt, &logEntry.Status); err != nil {
            log.Printf("Failed to scan log row: %v", err)
            continue
        }
        logs = append(logs, logEntry)
    }
    if err := rows.Err(); err != nil {
        http.Error(w, "Database error", http.StatusInternalServerError)
        return
    }

    // Render template
    tmpl := template.Must(template.ParseFS(templatesFS, "templates/logs.html"))
    if err := tmpl.Execute(w, map[string]interface{}{
        "Job":  j,
        "Logs": logs,
    }); err != nil {
        log.Printf("Template execution error: %v", err)
    }
}

func outputHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/logs/"), "/")
    if len(parts) != 2 || parts[1] != "output" {
        http.Error(w, "Invalid path", http.StatusBadRequest)
        return
    }

    runID, err := strconv.Atoi(parts[0])
    if err != nil {
        http.Error(w, "Invalid run ID", http.StatusBadRequest)
        return
    }

    if r.URL.Query().Get("download") == "1" {
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"run_%d.log\"", runID))
    } else {
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    }

    // 1️⃣ Stream SQLite preview first
    var preview string
    err = db.QueryRow("SELECT output FROM job_runs WHERE id = ?", runID).Scan(&preview)
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        http.Error(w, "Database error", http.StatusInternalServerError)
        return
    }

    contentWritten := false

    if preview != "" {
        _, _ = io.WriteString(w, preview)
        contentWritten = true
    }

    // 2️⃣ Stream remaining disk log, skipping preview
    logFilePath := fmt.Sprintf("/logs/%d.log", runID)
    f, err := os.Open(logFilePath)
    if err == nil { // only try to read if file exists
        defer f.Close()
        if _, err := f.Seek(int64(len(preview)), io.SeekStart); err != nil {
            log.Printf("Failed to seek disk log: %v", err)
        }

        buf := make([]byte, 64*1024)
        for {
            n, err := f.Read(buf)
            if n > 0 {
                _, _ = w.Write(buf[:n])
                w.(http.Flusher).Flush()
                contentWritten = true
            }
            if err != nil {
                if err != io.EOF {
                    log.Printf("Error reading log file: %v", err)
                }
                break
            }
        }
    }

    // 3️⃣ If nothing was written, show placeholder
    if !contentWritten {
        _, _ = io.WriteString(w, "⚠️ No log output available for this run.\n")
    }
	cleanupEmptyLogs("./logs")
}


// GET /edit/{id}
func editJobFormHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/edit/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	var j Job
	err = db.QueryRow("SELECT id, name, schedule, command FROM jobs WHERE id = ?", id).
		Scan(&j.ID, &j.Name, &j.Schedule, &j.Command)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	tmpl := template.Must(template.ParseFS(templatesFS, "templates/edit.html"))
	if err := tmpl.Execute(w, map[string]interface{}{"Job": j}); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}
// POST /edit/{id}
func editJobSubmitHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    idStr := strings.TrimPrefix(r.URL.Path, "/edit/")
    id, err := strconv.Atoi(idStr)
    if err != nil {
        http.Error(w, "Invalid job ID", http.StatusBadRequest)
        return
    }

    if err := r.ParseForm(); err != nil {
        http.Error(w, "Invalid form data", http.StatusBadRequest)
        return
    }

    name := r.FormValue("name")
    schedule := r.FormValue("schedule")
    command := r.FormValue("command")

    _, err = db.Exec("UPDATE jobs SET name = ?, schedule = ?, command = ? WHERE id = ?",
        name, schedule, command, id)
    if err != nil {
        http.Error(w, "Database update failed", http.StatusInternalServerError)
        return
    }
    if entryID, ok := cronMap[id]; ok {
        c.Remove(entryID)
    }

    newEntryID, err := c.AddFunc(schedule, func() {
        runJob(id, name, command)
    })
    if err != nil {
        log.Printf("Failed to update cron for job %d: %v", id, err)
        http.Error(w, "Failed to update cron schedule", http.StatusInternalServerError)
        return
    }

    cronMap[id] = newEntryID

    http.Redirect(w, r, "/", http.StatusSeeOther)
}
