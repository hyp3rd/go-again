package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyp3rd/go-again"
	"github.com/hyp3rd/go-again/pkg/scheduler"
)

const (
	schedulerTimeout = 200 * time.Millisecond
	schedulerRetries = 5
)

//nolint:funlen
func TestSchedulerRetryAndCallback(t *testing.T) {
	t.Parallel()

	var targetHits int32

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit := atomic.AddInt32(&targetHits, 1)
		if hit < 3 {
			w.WriteHeader(http.StatusInternalServerError)

			_, err := w.Write([]byte("retry"))
			if err != nil {
				t.Fatalf("failed to write response: %v", err)
			}

			return
		}

		w.WriteHeader(http.StatusOK)

		_, err := w.Write([]byte("ok"))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer target.Close()

	callbackCh := make(chan scheduler.CallbackPayload, 1)

	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			err := r.Body.Close()
			if err != nil {
				t.Logf("failed to close request body: %v", err)
			}
		}()

		var payload scheduler.CallbackPayload

		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			t.Fatalf("failed to decode callback payload: %v", err)
		}

		callbackCh <- payload

		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(schedulerRetries),
		again.WithInterval(1*time.Millisecond),
		again.WithJitter(1*time.Millisecond),
		again.WithTimeout(schedulerTimeout),
	)
	if err != nil {
		t.Fatalf("failed to create retrier: %v", err)
	}

	sched := scheduler.NewScheduler()
	defer sched.Stop()

	job := scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   10 * time.Millisecond,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    target.URL,
		},
		Callback: scheduler.Callback{
			URL: callback.URL,
		},
		RetryPolicy: scheduler.RetryPolicy{
			Retrier:          retrier,
			RetryStatusCodes: []int{http.StatusInternalServerError},
		},
	}

	jobID, err := sched.Schedule(job)
	if err != nil {
		t.Fatalf("failed to schedule job: %v", err)
	}

	select {
	case payload := <-callbackCh:
		if payload.JobID != jobID {
			t.Fatalf("expected job id %q, got %q", jobID, payload.JobID)
		}

		if !payload.Success {
			t.Fatalf("expected success, got error %q", payload.Error)
		}

		if payload.Attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", payload.Attempts)
		}

		if payload.StatusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, payload.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}
