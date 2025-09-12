package handlers

import (
	"os"

	"github.com/abhilashreddysh/croncraft/internal/db"
	"github.com/abhilashreddysh/croncraft/internal/jobs"
	"github.com/abhilashreddysh/croncraft/internal/models"
)

// UpsertJob inserts or updates a job in DB and updates the cron schedule
func UpsertJob(job *models.Job) error {
	// Determine DB status int
	statusInt := 0
	if job.Status {
		statusInt = 1
	}

	if job.ID == 0 {
		// Insert new job
		res, err := db.DB.Exec(
			"INSERT INTO jobs(name, schedule, command, status) VALUES(?, ?, ?, ?)",
			job.Name, job.Schedule, job.Command, statusInt,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		job.ID = int(id)
	} else {
		// Update existing job
		_, err := db.DB.Exec(
			"UPDATE jobs SET name = ?, schedule = ?, command = ?, status = ? WHERE id = ?",
			job.Name, job.Schedule, job.Command, statusInt, job.ID,
		)
		if err != nil {
			return err
		}
	}

	// Update cron
	if entryID, ok := jobs.CronMap[job.ID]; ok {
		jobs.C.Remove(entryID)
		delete(jobs.CronMap, job.ID)
	}

	if job.Status {
		jobs.RegisterCron(*job)
	}

	return nil
}

// DeleteJob removes a job from DB, cron, and optionally logs
func DeleteJob(jobID int, removeLogs bool) error {
	// Remove from cron
	jobs.Mu.Lock()
	if entryID, ok := jobs.CronMap[jobID]; ok {
		jobs.C.Remove(entryID)
		delete(jobs.CronMap, jobID)
	}
	jobs.Mu.Unlock()

	// DB transaction
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM job_runs WHERE job_id = ?", jobID)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM jobs WHERE id = ?", jobID)
	if err != nil {
		return err
	}

	if removeLogs {
		_ = os.RemoveAll("./logs") // optional: cleanup all logs or per-job
	}

	return tx.Commit()
}
