package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSchedulerValidation(t *testing.T) {
	t.Parallel()

	sched := scheduler.NewScheduler()
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every: time.Second,
		},
		Request: scheduler.Request{
			Method: http.MethodDelete,
			URL:    "http://example.com",
		},
	})
	if err == nil || !errors.Is(err, scheduler.ErrUnsupportedMethod) {
		t.Fatalf("expected unsupported method error, got %v", err)
	}

	_, err = sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every: time.Second,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
		},
	})
	if err == nil || !errors.Is(err, scheduler.ErrInvalidJob) {
		t.Fatalf("expected invalid job error, got %v", err)
	}
}

func TestSchedulerMaxRunsStops(t *testing.T) {
	t.Parallel()

	hitCh := make(chan struct{}, schedulerRetries)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	sched := scheduler.NewScheduler()
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   5 * time.Millisecond,
			MaxRuns: 2,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    target.URL,
		},
	})
	if err != nil {
		t.Fatalf("failed to schedule job: %v", err)
	}

	for i := range 2 {
		select {
		case <-hitCh:
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for hit %d", i+1)
		}
	}

	select {
	case <-hitCh:
		t.Fatal("expected scheduler to stop after max runs")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSchedulerCallbackBodyLimit(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", 10)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		_, err := w.Write([]byte(body))
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

	sched := scheduler.NewScheduler()
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   5 * time.Millisecond,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    target.URL,
		},
		Callback: scheduler.Callback{
			URL:          callback.URL,
			MaxBodyBytes: 4,
		},
	})
	if err != nil {
		t.Fatalf("failed to schedule job: %v", err)
	}

	select {
	case payload := <-callbackCh:
		if !payload.Success {
			t.Fatalf("expected success, got error %q", payload.Error)
		}

		if payload.Attempts != 1 {
			t.Fatalf("expected 1 attempt, got %d", payload.Attempts)
		}

		if payload.StatusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, payload.StatusCode)
		}

		if payload.ResponseBody != body[:4] {
			t.Fatalf("expected response body %q, got %q", body[:4], payload.ResponseBody)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}
