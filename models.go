package main

type Job struct {
	ID       int
	Name     string
	Schedule string
	Command  string
}

type Run struct {
	ID     int
	RunAt  string
	Status string
	Output string
}
