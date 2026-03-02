package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/goccy/go-json"

	"github.com/hyp3rd/go-again/pkg/scheduler"
)

const (
	pollTimeout   = 5 * time.Second
	scheduleEvery = 10 * time.Millisecond
	waitTimeout   = 2 * time.Second
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	callbackCh := make(chan scheduler.CallbackPayload, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			_ = r.Body.Close()
		}()

		var payload scheduler.CallbackPayload
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode callback payload failed: %v\n", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		callbackCh <- payload
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	s := scheduler.NewScheduler(
		ctx,
		scheduler.WithHTTPClient(server.Client()),
		scheduler.WithURLValidator(nil), // allow local endpoints for example usage
	)
	defer s.Stop(ctx)

	jobID, err := s.Schedule(ctx,
		scheduler.Job{
			Schedule: scheduler.Schedule{
				Every:   scheduleEvery,
				MaxRuns: 1,
			},
			Request: scheduler.Request{
				Method: http.MethodGet,
				URL:    server.URL + "/target",
			},
			Callback: scheduler.Callback{
				URL: server.URL + "/callback",
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule failed: %v\n", err)

		return
	}

	select {
	case payload := <-callbackCh:
		fmt.Printf("callback: job=%s success=%t status=%d\n", jobID, payload.Success, payload.StatusCode)
	case <-time.After(waitTimeout):
		fmt.Fprintln(os.Stderr, "timed out waiting for callback")

		return
	}

	status, ok := s.JobStatus(ctx, jobID)
	if ok {
		fmt.Printf("status: state=%s runs=%d active=%d\n", status.State, status.Runs, status.ActiveRuns)
	}

	history, ok := s.JobHistory(ctx, jobID)
	if ok {
		fmt.Printf("history entries: %d\n", len(history))
	}
}
