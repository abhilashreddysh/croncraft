# CronCraft

CronCraft is a lightweight web-based job scheduler written in Go.
It allows scheduling, running, and monitoring shell commands with cron-style schedules.

## Key Features

- Web interface to manage jobs
- Add, edit, run, and delete jobs
- Cron-style scheduling with `robfig/cron`
- Job run logs stored in SQLite with disk-backed output
- Streaming logs via `/logs/{jobID}/output`
- Auto-pruning of old job logs

---

# Installation

## Requirements

- Go 1.20+
- SQLite3
- Linux / macOS / Windows

## Build

```bash
git clone https://github.com/yourusername/croncraft.git
cd croncraft
go build -o croncraft
```

## Run

```bash
./croncraft
```

The web interface is available at `http://localhost:8080/`.

# Usage

## Web Interface

- **Dashboard (`/`)**: View all scheduled jobs and next run times.
- **Add Job (`/add`)**: Create a new job:
  - Name
  - Cron schedule (e.g., `0 2 * * *`)
  - Command to execute
- **Edit Job (`/edit/{id}`)**: Update job details and schedule.
- **Run Job (`/run/{id}`)**: Trigger a job immediately.
- **Delete Job (`/delete/{id}`)**: Remove a job and its logs.
- **View Logs (`/logs/{jobID}`)**: See past runs.
- **View Run Output (`/logs/{runID}/output`)**: Stream or download log output with `?download=1`.

# Database Schema

## Tables

### jobs

| Column   | Type    | Description   |
| -------- | ------- | ------------- |
| id       | INTEGER | Primary key   |
| name     | TEXT    | Job name      |
| schedule | TEXT    | Cron schedule |
| command  | TEXT    | Shell command |

### job_runs

| Column | Type     | Description                       |
| ------ | -------- | --------------------------------- |
| id     | INTEGER  | Primary key                       |
| job_id | INTEGER  | Foreign key to `jobs.id`          |
| run_at | DATETIME | Timestamp of job run              |
| status | TEXT     | `running`, `success`, or `failed` |
| output | TEXT     | Preview of job output             |

# Logging

- Each job run stores up to 500 KB preview in SQLite (`job_runs.output`).
- Full logs saved in `./logs/{runID}.log`.
- Logs are streamed via `/logs/{runID}/output` with real-time updates.
- Supports downloading logs using `?download=1`.

# Architecture Notes

## Cron Scheduling

- Uses `robfig/cron/v3` for cron-style scheduling.

## Concurrency

- `cronMap` protected by `sync.RWMutex`.
- Database writes serialized with `dbMu`.

## Job Execution

- Runs shell commands via `sh -c`.
- Stdout and stderr combined and streamed.
- Logs written to both disk and DB preview.
- Supports long-running jobs with real-time streaming.

# Contributing

- Fork the repository.
- Create a feature/fix branch.
- Submit a pull request.
- Ensure logs and cron schedules are correctly handled.

# License

This project is licensed under **GPLv3**.

---

# To-Do

- [ ] Run job immediately
- [ ] Option to enable and disable the job
