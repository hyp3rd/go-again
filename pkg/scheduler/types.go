package scheduler

import (
	"time"

	"github.com/hyp3rd/go-again"
)

// Schedule defines when a job should run.
type Schedule struct {
	Every   time.Duration
	StartAt time.Time
	EndAt   time.Time
	MaxRuns int
}

// Request describes the target HTTP request.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	Timeout time.Duration
}

// Callback describes where to send execution results.
type Callback struct {
	URL          string
	Method       string
	Headers      map[string]string
	Timeout      time.Duration
	MaxBodyBytes int
}

// RetryPolicy defines how retries should be performed.
type RetryPolicy struct {
	Retrier          *again.Retrier
	TemporaryErrors  []error
	RetryStatusCodes []int
}

// Job defines the schedule and behavior for a task.
type Job struct {
	ID          string
	Schedule    Schedule
	Request     Request
	Callback    Callback
	RetryPolicy RetryPolicy
}

// CallbackPayload is posted to the callback endpoint after each execution.
type CallbackPayload struct {
	JobID        string    `json:"job_id"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Attempts     int       `json:"attempts"`
	Success      bool      `json:"success"`
	StatusCode   int       `json:"status_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
}

// JobState represents the scheduler lifecycle state of a job.
type JobState string

const (
	// JobStateScheduled indicates a job is registered and waiting for its next run.
	JobStateScheduled JobState = "scheduled"
	// JobStateRunning indicates a job currently has one or more active executions.
	JobStateRunning JobState = "running"
	// JobStateCompleted indicates a job completed naturally (for example MaxRuns reached).
	JobStateCompleted JobState = "completed"
	// JobStateCanceled indicates a job was canceled before natural completion.
	JobStateCanceled JobState = "canceled"
	// JobStateRemoved indicates a job was explicitly removed via Remove.
	JobStateRemoved JobState = "removed"
	// JobStateStopped indicates a job was stopped during scheduler shutdown.
	JobStateStopped JobState = "stopped"
)

// JobStatus describes the current scheduler status for a job.
type JobStatus struct {
	JobID      string
	State      JobState
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Runs       int
	ActiveRuns int
	LastRun    *CallbackPayload
}

// JobRun captures one completed execution result for a job.
type JobRun struct {
	Sequence int
	Payload  CallbackPayload
}
