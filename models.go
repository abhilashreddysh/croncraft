package main

import "time"

type Job struct {
	ID       int
	Name     string
	Schedule string
	Command  string
    LastRun  string
}

// type Run struct {
// 	ID     int
// 	RunAt  string
// 	Status string
// 	Output string
// }

type Run struct {
    ID         int
    RunAt      time.Time
    Status     string
    Duration   string // Formatted duration string
    OutputSize string // Formatted file size string
}