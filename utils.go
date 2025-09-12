package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"
)

const (
	maxDBRetries = 5                      // Maximum retries for database operations
	retryDelay   = 100 * time.Millisecond // Delay between retries
)

// retryDBOperation retries a database operation if it fails due to locking
func retryDBOperation(operation func() error) error {
	var err error
	for i := 0; i < maxDBRetries; i++ {
		err = operation()
		if err == nil {
			return nil
		}

		// Check if it's a locking error
		if isDBLockedError(err) {
			time.Sleep(retryDelay * time.Duration(i+1))
			continue
		}

		// For other errors, return immediately
		return err
	}
	return fmt.Errorf("operation failed after %d retries: %w", maxDBRetries, err)
}

// isDBLockedError checks if the error is a database locked error
func isDBLockedError(err error) bool {
	return err != nil && (err.Error() == "database is locked" ||
		err.Error() == "database is locked (5) (SQLITE_BUSY)")
}

func cleanupEmptyLogs(logDir string) {
    files, err := os.ReadDir(logDir)
    if err != nil {
        log.Printf("Failed to read log dir for cleanup: %v", err)
        return
    }

    for _, f := range files {
        if f.Type().IsRegular() {
            path := fmt.Sprintf("%s/%s", logDir, f.Name())
            info, err := f.Info()
            if err != nil {
                continue
            }
            if info.Size() == 0 {
                _ = os.Remove(path)
                log.Printf("Removed empty log file: %s", path)
            }
        }
    }
}


// Helper function to format date for display
func formatDate(t time.Time) string {
    return t.Format("Jan 2, 2006")
}

// Helper function to format time for display
func formatTime(t time.Time) string {
    return t.Format("3:04 PM")
}

// Helper function to format file size in bytes to human-readable format
func formatFileSize(bytes int64) string {
    const unit = 1024
    if bytes < unit {
        return fmt.Sprintf("%d B", bytes)
    }
    
    div, exp := int64(unit), 0
    for n := bytes / unit; n >= unit; n /= unit {
        div *= unit
        exp++
    }
    
    units := []string{"KB", "MB", "GB", "TB"}
    return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

// Helper function to format duration in milliseconds to human-readable format
func formatDuration(ms int64) string {
    if ms < 1000 {
        return fmt.Sprintf("%dms", ms)
    }
    
    seconds := float64(ms) / 1000
    if seconds < 60 {
        return fmt.Sprintf("%.1fs", seconds)
    }
    
    minutes := seconds / 60
    if minutes < 60 {
        return fmt.Sprintf("%.1fm", minutes)
    }
    
    hours := minutes / 60
    return fmt.Sprintf("%.1fh", hours)
}

func timeAgo(t time.Time) string {
	now := time.Now()
	if t.After(now) {
		return "in the future"
	}

	diff := now.Sub(t)
	seconds := int(diff.Seconds())

	switch {
	case seconds < 60:
		return fmt.Sprintf("%d seconds ago", seconds)
	case seconds < 3600:
		mins := seconds / 60
		return fmt.Sprintf("%d minutes ago", mins)
	case seconds < 86400:
		hours := seconds / 3600
		return fmt.Sprintf("%d hours ago", hours)
	case seconds < 2592000: // ~30 days
		days := seconds / 86400
		return fmt.Sprintf("%d days ago", days)
	case seconds < 31536000: // < 12 months
		months := seconds / 2592000
		days := (seconds % 2592000) / 86400
		if days > 0 {
			return fmt.Sprintf("%d months %d days ago", months, days)
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := seconds / 31536000
		months := (seconds % 31536000) / 2592000
		if months > 0 {
			return fmt.Sprintf("%d years %d months ago", years, months)
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

func nullTimeAgo(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}

	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return ""
	}

	return timeAgo(t)
}
