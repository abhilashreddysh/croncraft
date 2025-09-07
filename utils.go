package main

import (
	"fmt"
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
