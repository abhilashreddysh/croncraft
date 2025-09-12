package jobs

import (
	"fmt"
	"log"
	"os"

	"github.com/abhilashreddysh/croncraft/internal/db"
)

func pruneLogs(jobID int) error {
	DbMu.Lock()
	defer DbMu.Unlock()

	rows, err := db.DB.Query(`
		SELECT id FROM job_runs 
		WHERE id NOT IN (
			SELECT id FROM job_runs 
			WHERE job_id = ? 
			ORDER BY run_at DESC 
			LIMIT ?
		) AND job_id = ?`,
		jobID, db.MaxLogsPerJob, jobID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var oldIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		oldIDs = append(oldIDs, id)
	}

	for _, id := range oldIDs {
		logFilePath := fmt.Sprintf("./logs/%d.log", id)
		if err := os.Remove(logFilePath); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to delete log file %s: %v", logFilePath, err)
		}
	}

	_, err = db.DB.Exec(`
		DELETE FROM job_runs 
		WHERE id NOT IN (
			SELECT id FROM job_runs 
			WHERE job_id = ? 
			ORDER BY run_at DESC 
			LIMIT ?
		) AND job_id = ?`,
		jobID, db.MaxLogsPerJob, jobID,
	)

	return err
}