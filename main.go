package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite"
)

//go:embed templates/*
var templatesFS embed.FS

type Job struct {
	ID       int
	Name     string
	Schedule string
	Command  string
}

type Run struct {
	RunAt  string
	Status string
	Output string
}

var db *sql.DB
var c *cron.Cron
var cronMap map[int]cron.EntryID
var dbMu sync.Mutex

const dbFile = "croncraft.db"
const MaxLogsPerJob = 20

func main() {
	// Ensure DB exists
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		f, err := os.Create(dbFile)
		if err != nil {
			log.Fatalf("Failed to create DB file: %v", err)
		}
		f.Close()
	}

	var err error
	db, err = sql.Open("sqlite", dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Enable WAL and busy timeout for better concurrency
	_, _ = db.Exec(`PRAGMA journal_mode = WAL;`)
	_, _ = db.Exec(`PRAGMA busy_timeout = 5000;`)

	// Create tables if not exist
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		schedule TEXT,
		command TEXT
	)`)
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS job_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER,
		run_at TEXT,
		status TEXT,
		output TEXT
	)`)
	if err != nil {
		log.Fatal(err)
	}

	// Initialize cron
	c = cron.New()
	cronMap = make(map[int]cron.EntryID)
	c.Start()
	defer c.Stop()

	// Load existing jobs
	loadJobs()

	// Add daily prune job
	c.AddFunc("@daily", pruneAllLogs)

	// Serve CSS
	http.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")

		// --- Dev mode: external file ---
		// data, err := os.ReadFile("templates/style.css")
		// if err != nil {
		// 	http.Error(w, "CSS not found on disk", http.StatusInternalServerError)
		// 	return
		// }
		// w.Write(data)

		// --- Production: embedded ---
		data, err := templatesFS.ReadFile("templates/style.css")
		if err != nil {
			http.Error(w, "Embedded CSS not found", http.StatusInternalServerError)
			return
		}
		w.Write(data)
	})

	// HTTP Handlers
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addJobHandler)
	http.HandleFunc("/run/", runHandler)
	http.HandleFunc("/delete/", deleteHandler)
	http.HandleFunc("/logs/", logsHandler)

	fmt.Println("CronCraft running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func loadJobs() {
	rows, err := safeQuery("SELECT id, name, schedule, command FROM jobs")
	if err != nil {
		log.Printf("Failed to query jobs: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command); err != nil {
			log.Printf("Failed to scan job row: %v", err)
			continue
		}
		registerCron(j)
	}
	if err := rows.Err(); err != nil {
		log.Printf("Error during rows iteration: %v", err)
	}
}

func registerCron(j Job) {
	id, err := c.AddFunc(j.Schedule, func() { runJob(j.ID, j.Name, j.Command) })
	if err != nil {
		log.Printf("Invalid cron for job %s: %s", j.Name, j.Schedule)
		return
	}
	cronMap[j.ID] = id
}

func runJob(id int, name, command string) {
	runAt := time.Now().Format(time.RFC3339)
	log.Printf("[%s] Running job: %s", runAt, name)

	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.CombinedOutput()
	status := "success"
	if err != nil {
		status = "failed"
	}
	output := string(out)

	err = safeExec("INSERT INTO job_runs(job_id, run_at, status, output) VALUES(?, ?, ?, ?)", id, runAt, status, output)
	if err != nil {
		log.Printf("Failed to insert job run: %v", err)
	}
}

// pruneAllLogs runs once a day for all jobs
func pruneAllLogs() {
	log.Println("Running daily log pruning...")
	rows, err := safeQuery("SELECT id FROM jobs")
	if err != nil {
		log.Printf("Failed to list jobs for pruning: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var jobID int
		if err := rows.Scan(&jobID); err == nil {
			pruneLogs(jobID)
		}
	}
}

func pruneLogs(jobID int) {
	err := safeExec(`
	DELETE FROM job_runs 
	WHERE id NOT IN (
		SELECT id FROM job_runs 
		WHERE job_id=? 
		ORDER BY run_at DESC 
		LIMIT ?
	)`, jobID, MaxLogsPerJob)
	if err != nil {
		log.Printf("Failed to prune logs for job %d: %v", jobID, err)
	}
}

// ===================== DB Wrappers =====================

func safeExec(query string, args ...any) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	_, err := db.Exec(query, args...)
	return err
}

func safeQuery(query string, args ...any) (*sql.Rows, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	return db.Query(query, args...)
}

func safeQueryRow(query string, args ...any) *sql.Row {
	dbMu.Lock()
	defer dbMu.Unlock()
	return db.QueryRow(query, args...)
}

// ===================== HTTP Handlers =====================

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFS(templatesFS, "templates/index.html"))
	rows, err := safeQuery("SELECT id, name, schedule, command FROM jobs")
	if err != nil {
		http.Error(w, fmt.Sprintf("DB error: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command)
		jobs = append(jobs, j)
	}

	tmpl.Execute(w, map[string]interface{}{"Jobs": jobs})
}

func addJobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	name := r.FormValue("name")
	schedule := r.FormValue("schedule")
	command := r.FormValue("command")

	_, err := cron.ParseStandard(schedule)
	if err != nil {
		http.Error(w, "Invalid cron expression: "+err.Error(), http.StatusBadRequest)
		return
	}

	res, err := db.Exec("INSERT INTO jobs(name, schedule, command) VALUES(?, ?, ?)", name, schedule, command)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to add job: %v", err), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	j := Job{ID: int(id), Name: name, Schedule: schedule, Command: command}
	registerCron(j)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func runHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/run/"):]
	id, _ := strconv.Atoi(idStr)
	row := safeQueryRow("SELECT name, command FROM jobs WHERE id=?", id)
	var j Job
	row.Scan(&j.Name, &j.Command)
	go runJob(id, j.Name, j.Command)
	w.Write([]byte("Job running..."))
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/delete/"):]
	id, _ := strconv.Atoi(idStr)

	if entryID, ok := cronMap[id]; ok {
		c.Remove(entryID)
		delete(cronMap, id)
	}

	safeExec("DELETE FROM jobs WHERE id=?", id)
	safeExec("DELETE FROM job_runs WHERE job_id=?", id)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/logs/"):]
	id, _ := strconv.Atoi(idStr)
	row := safeQueryRow("SELECT id, name FROM jobs WHERE id=?", id)
	var j Job
	row.Scan(&j.ID, &j.Name)

	rows, _ := safeQuery("SELECT run_at, status, output FROM job_runs WHERE job_id=? ORDER BY run_at DESC", id)
	defer rows.Close()

	var logs []Run
	for rows.Next() {
		var logEntry Run
		rows.Scan(&logEntry.RunAt, &logEntry.Status, &logEntry.Output)
		logs = append(logs, logEntry)
	}

	tmpl := template.Must(template.ParseFS(templatesFS, "templates/logs.html"))
	tmpl.Execute(w, map[string]interface{}{"Job": j, "Logs": logs})
}
