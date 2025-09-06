package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite"
)

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

func main() {
	var err error
	db, err = sql.Open("sqlite", "croncraft.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// tables
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		schedule TEXT,
		command TEXT
	)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS job_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id INTEGER,
		run_at TEXT,
		status TEXT,
		output TEXT
	)`)

	c = cron.New()
	cronMap = make(map[int]cron.EntryID)
	c.Start()
	defer c.Stop()

	// load existing jobs
	rows, _ := db.Query("SELECT id, name, schedule, command FROM jobs")
	for rows.Next() {
		var j Job
		rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command)
		registerCron(j)
	}
	rows.Close()

	// HTTP Handlers
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/add", addJobHandler)
	http.HandleFunc("/run/", runHandler)
	http.HandleFunc("/delete/", deleteHandler)
	http.HandleFunc("/logs/", logsHandler)

	fmt.Println("CronCraft running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func registerCron(j Job) {
	id, err := c.AddFunc(j.Schedule, func() { runJob(j.ID, j.Name, j.Command) })
	if err != nil {
		fmt.Println("Invalid cron for job:", j.Name, j.Schedule)
		return
	}
	cronMap[j.ID] = id
}

const MaxLogsPerJob = 20

func runJob(id int, name, command string) {
	runAt := time.Now().Format(time.RFC3339)
	fmt.Printf("[%s] Running job: %s\n", runAt, name)

	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.CombinedOutput()
	status := "success"
	if err != nil {
		status = "failed"
	}
	output := string(out)

	db.Exec("INSERT INTO job_runs(job_id, run_at, status, output) VALUES(?, ?, ?, ?)", id, runAt, status, output)
	pruneLogs(id)
	// fmt.Printf("[%s] Job output:\n%s\n", runAt, output)
}

func pruneLogs(jobID int) {
	db.Exec(`
	DELETE FROM job_runs 
	WHERE id NOT IN (
		SELECT id FROM job_runs 
		WHERE job_id=? 
		ORDER BY run_at DESC 
		LIMIT ?
	)`, jobID, MaxLogsPerJob)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	rows, _ := db.Query("SELECT id, name, schedule, command FROM jobs")
	var jobs []Job
	for rows.Next() {
		var j Job
		rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command)
		jobs = append(jobs, j)
	}
	rows.Close()
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

	// validate cron
	_, err := cron.ParseStandard(schedule)
	if err != nil {
		http.Error(w, "Invalid cron expression: "+err.Error(), http.StatusBadRequest)
		return
	}

	res, _ := db.Exec("INSERT INTO jobs(name, schedule, command) VALUES(?, ?, ?)", name, schedule, command)
	id, _ := res.LastInsertId()
	j := Job{ID: int(id), Name: name, Schedule: schedule, Command: command}
	registerCron(j)

	// For HTMX: return updated table only
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	rows, _ := db.Query("SELECT id, name, schedule, command FROM jobs")
	var jobs []Job
	for rows.Next() {
		var job Job
		rows.Scan(&job.ID, &job.Name, &job.Schedule, &job.Command)
		jobs = append(jobs, job)
	}
	rows.Close()
	tmpl.ExecuteTemplate(w, "jobTable", jobs)
}

func runHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/run/"):]
	id, _ := strconv.Atoi(idStr)
	row := db.QueryRow("SELECT name, command FROM jobs WHERE id=?", id)
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

	db.Exec("DELETE FROM jobs WHERE id=?", id)
	db.Exec("DELETE FROM job_runs WHERE job_id=?", id)

	// For HTMX: return updated table only
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	rows, _ := db.Query("SELECT id, name, schedule, command FROM jobs")
	var jobs []Job
	for rows.Next() {
		var job Job
		rows.Scan(&job.ID, &job.Name, &job.Schedule, &job.Command)
		jobs = append(jobs, job)
	}
	rows.Close()
	tmpl.ExecuteTemplate(w, "jobTable", jobs)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/logs/"):]
	id, _ := strconv.Atoi(idStr)
	row := db.QueryRow("SELECT id, name FROM jobs WHERE id=?", id)
	var j Job
	row.Scan(&j.ID, &j.Name)

	rows, _ := db.Query("SELECT run_at, status, output FROM job_runs WHERE job_id=? ORDER BY run_at DESC", id)
	var logs []Run
	for rows.Next() {
		var logEntry Run
		rows.Scan(&logEntry.RunAt, &logEntry.Status, &logEntry.Output)
		logs = append(logs, logEntry)
	}
	rows.Close()

	tmpl := template.Must(template.ParseFiles("templates/logs.html"))
	tmpl.Execute(w, map[string]interface{}{"Job": j, "Logs": logs})
}
