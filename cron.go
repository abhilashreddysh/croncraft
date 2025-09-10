package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
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

func runJob(jobID int, name, command string) {
    runAt := time.Now().Format(time.RFC3339)
    const maxDBOutput = 500 * 1024       // 500 KB preview in DB
    const batchInterval = 2 * time.Second

    startTime := time.Now() // track duration

    var runRowID int64
    err := retryDBOperation(func() error {
        res, err := db.Exec(
            "INSERT INTO job_runs (job_id, run_at, status, output) VALUES (?, ?, ?, ?)",
            jobID, runAt, "running", "",
        )
        if err != nil {
            return err
        }
        runRowID, err = res.LastInsertId()
        return err
    })
    if err != nil {
        log.Printf("[%s] Failed to insert running job %s: %v", runAt, name, err)
        return
    }

    log.Printf("[%s] Running job: %s", runAt, name)

    // Create log directory & file
    logDir := "./logs"
    if err := os.MkdirAll(logDir, 0755); err != nil {
        log.Printf("[%s] Failed to create log directory for job %s: %v", runAt, name, err)
        return
    }
    logFilePath := fmt.Sprintf("%s/%d.log", logDir, runRowID)
    f, err := os.Create(logFilePath)
    if err != nil {
        log.Printf("[%s] Failed to create log file for job %s: %v", runAt, name, err)
        return
    }
    defer f.Close()

    // Start command
    cmd := exec.Command("sh", "-c", command)
    stdoutPipe, _ := cmd.StdoutPipe()
    stderrPipe, _ := cmd.StderrPipe()
    reader := io.MultiReader(stdoutPipe, stderrPipe)

    if err := cmd.Start(); err != nil {
        log.Printf("[%s] Failed to start job %s: %v", runAt, name, err)
        return
    }

    // Capture output for DB and file
    scanner := bufio.NewScanner(reader)
    buf := make([]byte, 0, 1024*1024)
    scanner.Buffer(buf, 10*1024*1024)

    var outputDB string
    truncated := false
    lastUpdate := time.Now()

    for scanner.Scan() {
        line := scanner.Text() + "\n"
        f.WriteString(line) // always write to file

        // Keep preview for DB
        if len(outputDB) < maxDBOutput {
            remaining := maxDBOutput - len(outputDB)
            if len(line) > remaining {
                outputDB += line[:remaining]
                truncated = true
            } else {
                outputDB += line
            }
        } else {
            truncated = true
        }

        // Batch DB update every batchInterval
        if time.Since(lastUpdate) > batchInterval {
            tempOutput := outputDB
            if truncated {
                tempOutput += "... (truncated)\n"
            }
            _ = retryDBOperation(func() error {
                _, err := db.Exec("UPDATE job_runs SET output = ? WHERE id = ?", tempOutput, runRowID)
                return err
            })
            lastUpdate = time.Now()
        }
    }

    err = cmd.Wait()
    status := "success"
    if err != nil {
        status = "failed"
        log.Printf("[%s] Job %s failed: %v", runAt, name, err)
    }

    // Final duration and output update
    duration := time.Since(startTime)
    finalOutput := outputDB
    if truncated {
        finalOutput += "... (truncated)\n"
    }
    _ = retryDBOperation(func() error {
        _, err := db.Exec(
            "UPDATE job_runs SET status = ?, duration_ms = ?, output = ? WHERE id = ?",
            status, duration.Milliseconds(), finalOutput, runRowID,
        )
        return err
    })

    // Optional: prune old logs
    _ = retryDBOperation(func() error {
        return pruneLogs(jobID)
    })
}
