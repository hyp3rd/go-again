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

	"github.com/hyp3rd/sectools/pkg/validate"

	"github.com/hyp3rd/go-again"
	"github.com/hyp3rd/go-again/pkg/scheduler"
)

const (
	schedulerTimeout             = 200 * time.Millisecond
	schedulerRetries             = 5
	schedulerCallbackWaitTimeout = 2 * time.Second
	retryAndCallbackAttempts     = 3
	nonRetryableStatusMaxRetries = 3
	callbackBodyLimitMaxBytes    = 4
	targetPath                   = "/target"
	callbackPath                 = "/callback"
	scheduleJobError             = "failed to schedule job: %v"
)

func TestSchedulerRetryAndCallback(t *testing.T) {
	t.Parallel()

	var targetHits int32

	callbackCh := make(chan scheduler.CallbackPayload, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, newRetryThenSuccessHandler(t, &targetHits, retryAndCallbackAttempts))
	mux.HandleFunc(callbackPath, newCallbackPayloadHandler(t, callbackCh))

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	retrier := newSchedulerTestRetrier(t, schedulerRetries)
	sched := newTLSTestScheduler(t, server)

	job := scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   10 * time.Millisecond,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
		Callback: scheduler.Callback{
			URL: server.URL + callbackPath,
		},
		RetryPolicy: scheduler.RetryPolicy{
			Retrier:          retrier,
			RetryStatusCodes: []int{http.StatusInternalServerError},
		},
	}

	jobID, err := sched.Schedule(job)
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	payload := waitForCallbackPayload(t, callbackCh)
	assertSuccessfulRetryCallbackPayload(t, payload, jobID)
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
			URL:    "https://example.com",
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

	_, err = sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   time.Second,
			StartAt: time.Now().Add(1 * time.Minute),
			EndAt:   time.Now(),
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    "https://example.com",
		},
	})
	if err == nil || !errors.Is(err, scheduler.ErrInvalidJob) {
		t.Fatalf("expected invalid job error, got %v", err)
	}
}

func newTestURLValidator(t *testing.T) *validate.URLValidator {
	t.Helper()

	validator, err := validate.NewURLValidator(
		validate.WithURLAllowIPLiteral(true),
		validate.WithURLAllowPrivateIP(true),
		validate.WithURLAllowLocalhost(true),
	)
	if err != nil {
		t.Fatalf("failed to create url validator: %v", err)
	}

	return validator
}

func TestSchedulerURLValidationRejectsHTTP(t *testing.T) {
	t.Parallel()

	sched := scheduler.NewScheduler()
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every: time.Second,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    "http://example.com",
		},
	})
	if err == nil || !errors.Is(err, scheduler.ErrInvalidJob) {
		t.Fatalf("expected invalid job error, got %v", err)
	}
}

func TestSchedulerURLValidationDisabledAllowsHTTP(t *testing.T) {
	t.Parallel()

	hitCh := make(chan struct{}, 1)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	sched := scheduler.NewScheduler(
		scheduler.WithURLValidator(nil),
	)
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
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	select {
	case <-hitCh:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for target hit")
	}
}

func TestSchedulerEndAtInPastStopsImmediately(t *testing.T) {
	t.Parallel()

	hitCh := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}

		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	sched := scheduler.NewScheduler(
		scheduler.WithHTTPClient(server.Client()),
		scheduler.WithURLValidator(newTestURLValidator(t)),
	)
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every: 5 * time.Millisecond,
			EndAt: time.Now().Add(-100 * time.Millisecond),
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	select {
	case <-hitCh:
		t.Fatal("expected scheduler to skip runs after end time")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSchedulerMaxRunsStops(t *testing.T) {
	t.Parallel()

	hitCh := make(chan struct{}, schedulerRetries)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}

		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	sched := scheduler.NewScheduler(
		scheduler.WithHTTPClient(server.Client()),
		scheduler.WithURLValidator(newTestURLValidator(t)),
	)
	defer sched.Stop()

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   5 * time.Millisecond,
			MaxRuns: 2,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
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

func TestSchedulerRemoveCancels(t *testing.T) {
	t.Parallel()

	hitCh := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hitCh <- struct{}{}:
		default:
		}

		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	sched := scheduler.NewScheduler(
		scheduler.WithHTTPClient(server.Client()),
		scheduler.WithURLValidator(newTestURLValidator(t)),
	)
	defer sched.Stop()

	jobID, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   50 * time.Millisecond,
			StartAt: time.Now().Add(200 * time.Millisecond),
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	if !sched.Remove(jobID) {
		t.Fatalf("expected job %q to be removed", jobID)
	}

	select {
	case <-hitCh:
		t.Fatal("expected no hits after remove")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSchedulerNonRetryableStatus(t *testing.T) {
	t.Parallel()

	callbackCh := make(chan scheduler.CallbackPayload, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	mux.HandleFunc(callbackPath, newCallbackPayloadHandler(t, callbackCh))

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	retrier := newSchedulerTestRetrier(t, nonRetryableStatusMaxRetries)
	sched := newTLSTestScheduler(t, server)

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   10 * time.Millisecond,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
		Callback: scheduler.Callback{
			URL: server.URL + callbackPath,
		},
		RetryPolicy: scheduler.RetryPolicy{
			Retrier:          retrier,
			RetryStatusCodes: []int{http.StatusInternalServerError},
		},
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	payload := waitForCallbackPayload(t, callbackCh)
	assertNonRetryableCallbackPayload(t, payload)
}

func TestSchedulerCallbackBodyLimit(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", 10)

	callbackCh := make(chan scheduler.CallbackPayload, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(targetPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		mustWriteResponseBody(t, w, body)
	})

	mux.HandleFunc(callbackPath, newCallbackPayloadHandler(t, callbackCh))

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	sched := newTLSTestScheduler(t, server)

	_, err := sched.Schedule(scheduler.Job{
		Schedule: scheduler.Schedule{
			Every:   5 * time.Millisecond,
			MaxRuns: 1,
		},
		Request: scheduler.Request{
			Method: http.MethodGet,
			URL:    server.URL + targetPath,
		},
		Callback: scheduler.Callback{
			URL:          server.URL + callbackPath,
			MaxBodyBytes: callbackBodyLimitMaxBytes,
		},
	})
	if err != nil {
		t.Fatalf(scheduleJobError, err)
	}

	payload := waitForCallbackPayload(t, callbackCh)
	assertCallbackBodyLimitedPayload(t, payload, body[:callbackBodyLimitMaxBytes])
}

func newSchedulerTestRetrier(t *testing.T, maxRetries int) *again.Retrier {
	t.Helper()

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(maxRetries),
		again.WithInterval(1*time.Millisecond),
		again.WithJitter(1*time.Millisecond),
		again.WithTimeout(schedulerTimeout),
	)
	if err != nil {
		t.Fatalf("failed to create retrier: %v", err)
	}

	return retrier
}

func newTLSTestScheduler(t *testing.T, server *httptest.Server) *scheduler.Scheduler {
	t.Helper()

	sched := scheduler.NewScheduler(
		scheduler.WithHTTPClient(server.Client()),
		scheduler.WithURLValidator(newTestURLValidator(t)),
	)
	t.Cleanup(sched.Stop)

	return sched
}

func newRetryThenSuccessHandler(t *testing.T, targetHits *int32, successOnAttempt int32) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, _ *http.Request) {
		hit := atomic.AddInt32(targetHits, 1)
		if hit < successOnAttempt {
			w.WriteHeader(http.StatusInternalServerError)
			mustWriteResponseBody(t, w, "retry")

			return
		}

		w.WriteHeader(http.StatusOK)
		mustWriteResponseBody(t, w, "ok")
	}
}

func newCallbackPayloadHandler(t *testing.T, callbackCh chan<- scheduler.CallbackPayload) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

func mustWriteResponseBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	_, err := w.Write([]byte(body))
	if err != nil {
		t.Fatalf("failed to write response: %v", err)
	}
}

func waitForCallbackPayload(t *testing.T, callbackCh <-chan scheduler.CallbackPayload) scheduler.CallbackPayload {
	t.Helper()

	select {
	case payload := <-callbackCh:
		return payload
	case <-time.After(schedulerCallbackWaitTimeout):
		t.Fatal("timed out waiting for callback")

		return scheduler.CallbackPayload{}
	}
}

func assertSuccessfulRetryCallbackPayload(t *testing.T, payload scheduler.CallbackPayload, jobID string) {
	t.Helper()

	if payload.JobID != jobID {
		t.Fatalf("expected job id %q, got %q", jobID, payload.JobID)
	}

	if !payload.Success {
		t.Fatalf("expected success, got error %q", payload.Error)
	}

	if payload.Attempts != retryAndCallbackAttempts {
		t.Fatalf("expected %d attempts, got %d", retryAndCallbackAttempts, payload.Attempts)
	}

	if payload.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, payload.StatusCode)
	}
}

func assertNonRetryableCallbackPayload(t *testing.T, payload scheduler.CallbackPayload) {
	t.Helper()

	if payload.Success {
		t.Fatal("expected failure for non-retryable status")
	}

	if payload.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", payload.Attempts)
	}

	if payload.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, payload.StatusCode)
	}
}

func assertCallbackBodyLimitedPayload(t *testing.T, payload scheduler.CallbackPayload, wantBody string) {
	t.Helper()

	if !payload.Success {
		t.Fatalf("expected success, got error %q", payload.Error)
	}

	if payload.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", payload.Attempts)
	}

	if payload.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, payload.StatusCode)
	}

	if payload.ResponseBody != wantBody {
		t.Fatalf("expected response body %q, got %q", wantBody, payload.ResponseBody)
	}
}
