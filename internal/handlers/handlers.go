package handlers

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
	"time"

	"github.com/abhilashreddysh/croncraft/internal/db"
	"github.com/abhilashreddysh/croncraft/internal/jobs"
	"github.com/abhilashreddysh/croncraft/internal/models"
	"github.com/abhilashreddysh/croncraft/internal/utils"
)

//go:embed templates/*
var templatesFS embed.FS

func SetupHTTPHandlers() {
	// Static files	
	http.HandleFunc("/style.css", serveStaticFile("text/css", "templates/static/style.css"))

	// Application routes
	http.HandleFunc("/", overviewHandler)
	http.HandleFunc("/add", addJobHandler)
	http.HandleFunc("/run/", runHandler)
	http.HandleFunc("/delete/", deleteJobHandler)
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

func overviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobs, err := db.GetJobsFromDB()
	if err != nil {
		http.Error(w, fmt.Sprintf("DB error: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse base and index together
	tmpl, err := template.ParseFS(templatesFS,
		"templates/base.html",
		"templates/overview.html",
        "templates/modals/delete_confirm.html",
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template parse error: %v", err), http.StatusInternalServerError)
		return
	}

	// Execute base template (which includes index.html)
	if err := tmpl.ExecuteTemplate(w, "base", map[string]interface{}{"Jobs": jobs,"ActivePage": "overview"}); err != nil {
		http.Error(w, fmt.Sprintf("Template execute error: %v", err), http.StatusInternalServerError)
		return
	}

}

func addJobHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tmpl := template.Must(template.ParseFS(
			templatesFS,
			"templates/base.html",
			"templates/add.html",
			"templates/modals/schedule_helper.html",
		))
		_ = tmpl.ExecuteTemplate(w, "base", map[string]interface{}{"ActivePage": "add"})

	case http.MethodPost:
		job, err := utils.ParseJobForm(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := UpsertJob(job); err != nil {
			http.Error(w, "Failed to add job: "+err.Error(), http.StatusInternalServerError)
			return
		}

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

	var j models.Job
	err = db.DB.QueryRow("SELECT name, command FROM jobs WHERE id = ?", id).
		Scan(&j.Name, &j.Command)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	go jobs.RunJob(id, j.Name, j.Command)
	w.Write([]byte("Job started in background"))
}

// deleteJobHandler deletes a job from DB and cron scheduler
func deleteJobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/delete/")
	jobID, _ := strconv.Atoi(idStr)

	if err := DeleteJob(jobID, true); err != nil {
		http.Error(w, "Failed to delete job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
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

    // Stream SQLite preview first
    var preview string
    err = db.DB.QueryRow("SELECT output FROM job_runs WHERE id = ?", runID).Scan(&preview)
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
	utils.CleanupEmptyLogs("./logs")
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

	var j models.Job
	var lastRun,Created,Updated sql.NullString
	err = db.DB.QueryRow(`SELECT j.id, j.name, j.schedule, j.command, j.status, j.created_at, j.updated_at,
						COALESCE((SELECT MAX(r.run_at) FROM job_runs r WHERE r.job_id = j.id), '') AS last_run 
						FROM jobs j WHERE id = ?`, id).Scan(&j.ID, &j.Name, &j.Schedule, &j.Command, &j.Status, &Created, &Updated, &lastRun)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	j.CreatedAt = utils.NullTimeAgo(Created)
	j.UpdatedAt = utils.NullTimeAgo(Updated)
	j.LastRun   = utils.NullTimeAgo(lastRun)

	tmpl := template.Must(template.ParseFS(
	templatesFS,
	"templates/base.html",
	"templates/edit.html",
    "templates/modals/schedule_helper.html",
    "templates/modals/delete_confirm.html",
	))
	if err := tmpl.ExecuteTemplate(w, "base", map[string]interface{}{"Job": j}); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
// POST /edit/{id}
func editJobSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/edit/")
	jobID, _ := strconv.Atoi(idStr)

	job, err := utils.ParseJobForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job.ID = jobID

	if err := UpsertJob(job); err != nil {
		http.Error(w, "Failed to update job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}



func createTemplate() *template.Template {
    return template.New("").Funcs(template.FuncMap{
        "formatDate": utils.FormatDate,
        "formatTime": utils.FormatTime,
    })
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
    var j models.Job
    err = db.DB.QueryRow("SELECT id, name FROM jobs WHERE id = ?", id).
        Scan(&j.ID, &j.Name)
    if errors.Is(err, sql.ErrNoRows) {
        http.Error(w, "Job not found", http.StatusNotFound)
        return
    } else if err != nil {
        http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
        return
    }

    // Fetch runs for this job with additional metadata
    rows, err := db.DB.Query(`
        SELECT 
            id, 
            run_at, 
            status, 
            duration_ms,
            LENGTH(output) as output_size
        FROM job_runs 
        WHERE job_id = ? 
        ORDER BY run_at DESC
    `, id)
    if err != nil {
        http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var logs []models.Run
    for rows.Next() {
        var logEntry models.Run
        var runAtStr string
        var durationMs sql.NullInt64
        var outputSize sql.NullInt64
        
        if err := rows.Scan(
            &logEntry.ID,
            &runAtStr,
            &logEntry.Status, 
            &durationMs,
            &outputSize,
        ); err != nil {
            log.Printf("Failed to scan log row: %v", err)
            continue
        }

        t, err := time.Parse(time.RFC3339, runAtStr)
        if err != nil {
            log.Printf("Failed to parse run_at: %v", err)
            continue
        }
        logEntry.RunAt = t

        
        // Convert duration to human-readable format
        if durationMs.Valid {
            logEntry.Duration = utils.FormatDuration(durationMs.Int64)
        }
        
        // Convert output size to human-readable format
        if outputSize.Valid {
            logEntry.OutputSize = utils.FormatFileSize(outputSize.Int64)
        }
        
        logs = append(logs, logEntry)
    }
    if err := rows.Err(); err != nil {
        http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
        return
    }

    // Render template
	tmpl := createTemplate()
	tmpl, err = tmpl.ParseFS(templatesFS,
		"templates/base.html",
		"templates/logs.html",
	)

    if err != nil {
        http.Error(w, fmt.Sprintf("Template parse error: %v", err), http.StatusInternalServerError)
        return
    }

    if err := tmpl.ExecuteTemplate(w, "base", map[string]interface{}{
        "Job":  j,
        "Logs": logs,
    }); err != nil {
        log.Printf("Template execution error: %v", err)
        http.Error(w, "Template execution failed", http.StatusInternalServerError)
    }
}