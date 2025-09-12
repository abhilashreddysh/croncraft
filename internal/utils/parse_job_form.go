package utils

import (
	"errors"
	"net/http"
	"strings"

	"github.com/abhilashreddysh/croncraft/internal/models"
	"github.com/robfig/cron/v3"
)

// ParseJobForm reads form values from the request and returns a Job struct
func ParseJobForm(r *http.Request) (*models.Job, error) {
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("invalid form data")
	}

	name := strings.TrimSpace(r.FormValue("name"))
	schedule := strings.TrimSpace(r.FormValue("schedule"))
	command := strings.TrimSpace(r.FormValue("command"))
	status := r.FormValue("enabled") == "on"

	if name == "" || schedule == "" || command == "" {
		return nil, errors.New("all fields are required")
	}

	// Validate cron expression
	if _, err := cron.ParseStandard(schedule); err != nil {
		return nil, errors.New("invalid cron expression: " + err.Error())
	}

	job := &models.Job{
		Name:     name,
		Schedule: schedule,
		Command:  command,
		Status:   status,
	}

	return job, nil
}
