package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyp3rd/ewrap"

	"github.com/hyp3rd/go-again"
)

const (
	defaultCallbackMethod      = http.MethodPost
	defaultCallbackMaxBodySize = 4096
)

type jobEntry struct {
	job    Job
	cancel context.CancelFunc
}

// Scheduler coordinates scheduled jobs.
type Scheduler struct {
	mu      sync.Mutex
	jobs    map[string]*jobEntry
	client  *http.Client
	logger  *slog.Logger
	sem     chan struct{}
	ctx     context.Context //nolint:containedctx // base context for scheduler lifecycle
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	counter uint64
}

// NewScheduler creates a scheduler with the provided options.
func NewScheduler(opts ...Option) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	schedulerInstance := &Scheduler{
		jobs:   make(map[string]*jobEntry),
		client: http.DefaultClient,
		logger: slog.Default(),
		ctx:    ctx,
		cancel: cancel,
	}

	for _, opt := range opts {
		opt(schedulerInstance)
	}

	return schedulerInstance
}

// Schedule registers a job and starts its schedule loop.
func (s *Scheduler) Schedule(job Job) (string, error) {
	normalized, err := s.normalizeJob(job)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if normalized.ID == "" {
		normalized.ID = s.nextID()
	}

	if _, exists := s.jobs[normalized.ID]; exists {
		return "", fmt.Errorf("%w: job id already exists: %s", ErrInvalidJob, normalized.ID)
	}

	ctx, cancel := context.WithCancel(s.ctx)
	entry := &jobEntry{
		job:    normalized,
		cancel: cancel,
	}
	s.jobs[normalized.ID] = entry

	s.wg.Add(1)

	go s.runJob(ctx, entry.job)

	return normalized.ID, nil
}

// Remove stops a scheduled job.
func (s *Scheduler) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.jobs[id]
	if !ok {
		return false
	}

	entry.cancel()
	delete(s.jobs, id)

	return true
}

// Stop cancels all jobs and waits for completion.
func (s *Scheduler) Stop() {
	s.cancel()

	s.mu.Lock()

	for _, entry := range s.jobs {
		entry.cancel()
	}

	s.jobs = make(map[string]*jobEntry)
	s.mu.Unlock()

	s.wg.Wait()
}

func (s *Scheduler) nextID() string {
	id := atomic.AddUint64(&s.counter, 1)

	return fmt.Sprintf("job-%d", id)
}

func (*Scheduler) normalizeJob(job Job) (Job, error) {
	if job.Schedule.Every <= 0 {
		return Job{}, fmt.Errorf("%w: schedule interval must be > 0", ErrInvalidJob)
	}

	if job.Schedule.MaxRuns < 0 {
		return Job{}, fmt.Errorf("%w: max runs must be >= 0", ErrInvalidJob)
	}

	if !job.Schedule.EndAt.IsZero() && !job.Schedule.StartAt.IsZero() && job.Schedule.EndAt.Before(job.Schedule.StartAt) {
		return Job{}, fmt.Errorf("%w: end time precedes start time", ErrInvalidJob)
	}

	if job.Request.URL == "" {
		return Job{}, fmt.Errorf("%w: request URL is required", ErrInvalidJob)
	}

	if job.Request.Method == "" {
		job.Request.Method = http.MethodGet
	}

	job.Request.Method = strings.ToUpper(job.Request.Method)
	if !isSupportedMethod(job.Request.Method) {
		return Job{}, fmt.Errorf("%w: %s", ErrUnsupportedMethod, job.Request.Method)
	}

	if job.Callback.URL != "" {
		if job.Callback.Method == "" {
			job.Callback.Method = defaultCallbackMethod
		}

		job.Callback.Method = strings.ToUpper(job.Callback.Method)
		if !isSupportedMethod(job.Callback.Method) {
			return Job{}, fmt.Errorf("%w: %s", ErrUnsupportedMethod, job.Callback.Method)
		}

		if job.Callback.MaxBodyBytes <= 0 {
			job.Callback.MaxBodyBytes = defaultCallbackMaxBodySize
		}
	}

	if job.RetryPolicy.Retrier == nil {
		retrier, err := again.NewRetrier(context.Background())
		if err != nil {
			return Job{}, fmt.Errorf("%w: %w", ErrInvalidJob, err)
		}

		retrier.Registry.LoadDefaults()
		job.RetryPolicy.Retrier = retrier
	}

	return job, nil
}

func (s *Scheduler) runJob(ctx context.Context, job Job) {
	defer s.wg.Done()

	if !job.Schedule.StartAt.IsZero() {
		if !s.waitUntil(ctx, job.Schedule.StartAt) {
			return
		}
	}

	ticker := time.NewTicker(job.Schedule.Every)
	defer ticker.Stop()

	var runs int

	for {
		if ctx.Err() != nil {
			return
		}

		if job.Schedule.MaxRuns > 0 && runs >= job.Schedule.MaxRuns {
			return
		}

		if !job.Schedule.EndAt.IsZero() && time.Now().After(job.Schedule.EndAt) {
			return
		}

		runs++
		scheduledAt := time.Now()
		s.enqueue(ctx, job, scheduledAt)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (*Scheduler) waitUntil(ctx context.Context, when time.Time) bool {
	delay := time.Until(when)
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Scheduler) enqueue(ctx context.Context, job Job, scheduledAt time.Time) {
	if s.sem != nil {
		s.sem <- struct{}{}
	}

	s.wg.Go(func() {
		if s.sem != nil {
			defer func() { <-s.sem }()
		}

		s.execute(ctx, job, scheduledAt)
	})
}

func (s *Scheduler) execute(ctx context.Context, job Job, scheduledAt time.Time) {
	startedAt := time.Now()
	retrier := job.RetryPolicy.Retrier
	temporaryErrors := s.buildTemporaryErrors(retrier, job.RetryPolicy)

	var (
		attempts   int
		lastStatus int
		lastBody   []byte
	)

	errs := retrier.DoWithContext(ctx, func(attemptCtx context.Context) error {
		attempts++

		reqCtx, cancel := s.requestContext(attemptCtx, job.Request.Timeout)
		if cancel != nil {
			defer cancel()
		}

		req, err := http.NewRequestWithContext(reqCtx, job.Request.Method, job.Request.URL, bytes.NewReader(job.Request.Body))
		if err != nil {
			return ewrap.Wrapf(err, "failed to create request for %s %s", job.Request.Method, job.Request.URL)
		}

		for key, value := range job.Request.Headers {
			req.Header.Set(key, value)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			return ewrap.Wrap(err, "request failed")
		}

		defer func() {
			closeErr := resp.Body.Close()
			if closeErr != nil {
				s.logError("response body close failed", closeErr)
			}
		}()

		lastStatus = resp.StatusCode

		body, readErr := s.readBody(resp.Body, job.Callback.URL != "", job.Callback.MaxBodyBytes)
		if readErr != nil {
			s.logError("response body read failed", readErr)
		}

		lastBody = body

		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return nil
		}

		if isRetryableStatus(resp.StatusCode, job.RetryPolicy.RetryStatusCodes) {
			return fmt.Errorf("%w: status %d", ErrRetryableStatus, resp.StatusCode)
		}

		return &StatusError{StatusCode: resp.StatusCode}
	}, temporaryErrors...)

	finishedAt := time.Now()
	s.sendCallback(ctx, job, CallbackPayload{
		JobID:        job.ID,
		ScheduledAt:  scheduledAt,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Attempts:     attempts,
		Success:      errs.Last == nil,
		StatusCode:   lastStatus,
		Error:        errorString(errs.Last),
		ResponseBody: string(lastBody),
	})
}

func (*Scheduler) requestContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, nil
	}

	return context.WithTimeout(ctx, timeout)
}

func (s *Scheduler) sendCallback(ctx context.Context, job Job, payload CallbackPayload) {
	if job.Callback.URL == "" {
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.logError("callback marshal failed", err)

		return
	}

	cbCtx, cancel := s.requestContext(ctx, job.Callback.Timeout)
	if cancel != nil {
		defer cancel()
	}

	req, err := http.NewRequestWithContext(cbCtx, job.Callback.Method, job.Callback.URL, bytes.NewReader(body))
	if err != nil {
		s.logError("callback request failed", err)

		return
	}

	req.Header.Set("Content-Type", "application/json")

	for key, value := range job.Callback.Headers {
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.logError("callback send failed", err)

		return
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			s.logError("callback response close failed", closeErr)
		}
	}()

	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		s.logError("callback response drain failed", err)
	}
}

func (*Scheduler) buildTemporaryErrors(retrier *again.Retrier, policy RetryPolicy) []error {
	var temp []error

	if len(policy.RetryStatusCodes) > 0 {
		temp = append(temp, ErrRetryableStatus)
	}

	if len(policy.TemporaryErrors) > 0 {
		temp = append(temp, policy.TemporaryErrors...)
	}

	if len(temp) > 0 && retrier.Registry != nil && retrier.Registry.Len() > 0 {
		temp = append(temp, retrier.Registry.ListTemporaryErrors()...)
	}

	return temp
}

func (*Scheduler) readBody(body io.ReadCloser, include bool, limit int) ([]byte, error) {
	if !include {
		_, err := io.Copy(io.Discard, body)

		return nil, ewrap.Wrap(err, "draining body failed")
	}

	if limit <= 0 {
		limit = defaultCallbackMaxBodySize
	}

	limited := io.LimitReader(body, int64(limit)+1)

	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, ewrap.Wrap(err, "reading body failed")
	}

	if len(data) > limit {
		data = data[:limit]
	}

	_, err = io.Copy(io.Discard, body)
	if err != nil {
		return data, ewrap.Wrap(err, "draining body failed")
	}

	return data, nil
}

func (s *Scheduler) logError(msg string, err error) {
	if s.logger == nil {
		return
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.logger.Log(s.ctx, slog.LevelDebug, msg, slog.Any("error", err))

		return
	}

	s.logger.Log(s.ctx, slog.LevelWarn, msg, slog.Any("error", err))
}

func isRetryableStatus(code int, statuses []int) bool {
	return slices.Contains(statuses, code)
}

func isSupportedMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut:
		return true
	default:
		return false
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
