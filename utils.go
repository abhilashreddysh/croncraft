package main

import (
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

// To convert to relative time
func timeAgo(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(diff.Hours()/24))
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(diff.Hours()/(24*7)))
	case diff < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(diff.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%d years ago", int(diff.Hours()/(24*365)))
	}
}