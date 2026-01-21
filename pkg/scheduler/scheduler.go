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
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyp3rd/ewrap"
	"github.com/hyp3rd/sectools/pkg/validate"

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
	mu           sync.Mutex
	jobs         map[string]*jobEntry
	client       *http.Client
	logger       *slog.Logger
	sem          chan struct{}
	ctx          context.Context //nolint:containedctx // base context for scheduler lifecycle
	cancel       context.CancelFunc
	urlValidator *validate.URLValidator
	wg           sync.WaitGroup
	counter      uint64
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

	validator, err := validate.NewURLValidator()
	if err == nil {
		schedulerInstance.urlValidator = validator
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

func (s *Scheduler) normalizeJob(job Job) (Job, error) {
	err := validateSchedule(job.Schedule)
	if err != nil {
		return Job{}, err
	}

	request, err := s.normalizeRequest(job.Request)
	if err != nil {
		return Job{}, err
	}

	job.Request = request

	callback, err := s.normalizeCallback(job.Callback)
	if err != nil {
		return Job{}, err
	}

	job.Callback = callback

	err = ensureRetrier(&job)
	if err != nil {
		return Job{}, err
	}

	return job, nil
}

func validateSchedule(schedule Schedule) error {
	if schedule.Every <= 0 {
		return fmt.Errorf("%w: schedule interval must be > 0", ErrInvalidJob)
	}

	if schedule.MaxRuns < 0 {
		return fmt.Errorf("%w: max runs must be >= 0", ErrInvalidJob)
	}

	if !schedule.EndAt.IsZero() && !schedule.StartAt.IsZero() && schedule.EndAt.Before(schedule.StartAt) {
		return fmt.Errorf("%w: end time precedes start time", ErrInvalidJob)
	}

	return nil
}

func (s *Scheduler) normalizeRequest(request Request) (Request, error) {
	if request.URL == "" {
		return Request{}, fmt.Errorf("%w: request URL is required", ErrInvalidJob)
	}

	if request.Method == "" {
		request.Method = http.MethodGet
	}

	request.Method = strings.ToUpper(request.Method)
	if !isSupportedMethod(request.Method) {
		return Request{}, fmt.Errorf("%w: %s", ErrUnsupportedMethod, request.Method)
	}

	if s.urlValidator != nil {
		_, err := s.urlValidator.Validate(s.ctx, request.URL)
		if err != nil {
			return Request{}, fmt.Errorf("%w: request URL invalid: %w", ErrInvalidJob, err)
		}
	}

	return request, nil
}

func (s *Scheduler) normalizeCallback(callback Callback) (Callback, error) {
	if callback.URL == "" {
		return callback, nil
	}

	if callback.Method == "" {
		callback.Method = defaultCallbackMethod
	}

	callback.Method = strings.ToUpper(callback.Method)
	if !isSupportedMethod(callback.Method) {
		return Callback{}, fmt.Errorf("%w: %s", ErrUnsupportedMethod, callback.Method)
	}

	if s.urlValidator != nil {
		_, err := s.urlValidator.Validate(s.ctx, callback.URL)
		if err != nil {
			return Callback{}, fmt.Errorf("%w: callback URL invalid: %w", ErrInvalidJob, err)
		}
	}

	if callback.MaxBodyBytes <= 0 {
		callback.MaxBodyBytes = defaultCallbackMaxBodySize
	}

	return callback, nil
}

func ensureRetrier(job *Job) error {
	if job.RetryPolicy.Retrier != nil {
		return nil
	}

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJob, err)
	}

	retrier.Registry.LoadDefaults()
	job.RetryPolicy.Retrier = retrier

	return nil
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
		if err != nil {
			return nil, ewrap.Wrap(err, "draining body failed")
		}

		return nil, nil
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

	if isNilError(err) {
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

func isNilError(err error) bool {
	if err == nil {
		return true
	}

	value := reflect.ValueOf(err)
	//nolint:exhaustive // only need to check for nil-able kinds
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
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
