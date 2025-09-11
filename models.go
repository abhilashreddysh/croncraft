package main

import "time"

type Job struct {
	ID       int
	Name     string
	Schedule string
	Command  string
	Status  bool
    LastRun  string
    CreatedAt string
    UpdatedAt string
}

type Run struct {
    ID         int
    RunAt      time.Time
    Status     string
    Duration   string
    OutputSize string
}