package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/hyp3rd/go-again/pkg/scheduler"
)

const (
	pollEvery   = 20 * time.Millisecond
	pollTimeout = 3 * time.Second
)

func main() {
	dbPath := filepath.Join(os.TempDir(), "go-again-scheduler-example.db")
	_ = os.Remove(dbPath)

	storage, err := scheduler.NewSQLiteJobsStorage(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create sqlite storage failed: %v\n", err)

		return
	}
	defer func() {
		_ = storage.Close()
		_ = os.Remove(dbPath)
	}()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	s := scheduler.NewScheduler(
		scheduler.WithJobsStorage(storage),
		scheduler.WithURLValidator(nil), // allow local endpoint for example usage
	)
	defer s.Stop()

	jobID, err := s.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   pollEvery,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    target.URL,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule failed: %v\n", err)

		return
	}

	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		status, ok := s.JobStatus(jobID)
		if ok && status.State == scheduler.JobStateCompleted {
			fmt.Printf("completed: job=%s runs=%d\n", jobID, status.Runs)

			return
		}

		time.Sleep(pollEvery)
	}

	fmt.Fprintln(os.Stderr, "timed out waiting for completed status")
}
