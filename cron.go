package main

import (
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	c       *cron.Cron
	cronMap map[int]cron.EntryID
	mu      sync.RWMutex // For thread-safe access to cronMap
	dbMu    sync.Mutex   // For serializing database write operations
)

func initializeCron() {
	c = cron.New()
	cronMap = make(map[int]cron.EntryID)
	c.Start()
}

func loadJobs() {
	jobs, err := getJobsFromDB()
	if err != nil {
		log.Printf("Failed to load jobs: %v", err)
		return
	}

	for _, job := range jobs {
		registerCron(job)
	}
}

func registerCron(j Job) {
	id, err := c.AddFunc(j.Schedule, func() {
		runJob(j.ID, j.Name, j.Command)
	})

	if err != nil {
		log.Printf("Invalid cron for job %s: %s", j.Name, j.Schedule)
		return
	}

	mu.Lock()
	cronMap[j.ID] = id
	mu.Unlock()
}

func runJob(id int, name, command string) {
	runAt := time.Now().Format(time.RFC3339)
	log.Printf("[%s] Running job: %s", runAt, name)

	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.CombinedOutput()

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("[%s] Job %s failed: %v", runAt, name, err)
	}

	output := string(out)
	if len(output) > 10000 { // Limit output size
		output = output[:10000] + "... (truncated)"
	}

	// Use retry logic for saving job run
	if err := retryDBOperation(func() error {
		return saveJobRun(id, runAt, status, output)
	}); err != nil {
		log.Printf("Failed to save job run after retries: %v", err)
	}

	// Use retry logic for pruning logs
	if err := retryDBOperation(func() error {
		return pruneLogs(id)
	}); err != nil {
		log.Printf("Failed to prune logs for job %d after retries: %v", id, err)
	}
}
