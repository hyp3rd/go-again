package scheduler

import (
	"bytes"
	"context"
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

	"github.com/goccy/go-json"
	"github.com/hyp3rd/ewrap"
	"github.com/hyp3rd/sectools/pkg/validate"

	"github.com/hyp3rd/go-again"
)

const (
	defaultCallbackMethod      = http.MethodPost
	defaultCallbackMaxBodySize = 4096
	defaultHistoryLimit        = 20
	logFieldError              = "error"
)

//nolint:gochecknoglobals // test seam for simulating default validator initialization failures.
var newDefaultURLValidator = func() (*validate.URLValidator, error) {
	return validate.NewURLValidator()
}

type jobEntry struct {
	job    Job
	cancel context.CancelFunc
	runsWg sync.WaitGroup
}

// Scheduler coordinates scheduled jobs.
type Scheduler struct {
	mu           sync.Mutex
	jobs         map[string]*jobEntry
	jobsStorage  JobsStorage
	historyLimit int
	client       *http.Client
	logger       *slog.Logger
	sem          chan struct{}
	ctx          context.Context //nolint:containedctx // base context for scheduler lifecycle
	cancel       context.CancelFunc
	urlValidator *validate.URLValidator
	// urlValidatorConfigured is true when callers explicitly set WithURLValidator, including nil.
	urlValidatorConfigured bool
	wg                     sync.WaitGroup
	counter                uint64
	stopped                bool
}

// NewScheduler creates a scheduler with the provided options.
func NewScheduler(opts ...Option) *Scheduler {
	schedulerInstance, err := NewSchedulerWithError(opts...)
	if err != nil && schedulerInstance != nil && schedulerInstance.logger != nil {
		if errors.Is(err, ErrURLValidatorInitialization) {
			schedulerInstance.logger.Log(
				schedulerInstance.ctx,
				slog.LevelWarn,
				"default URL validator initialization failed; URL validation disabled unless overridden",
				slog.Any(logFieldError, err),
			)
		} else {
			schedulerInstance.logger.Log(
				schedulerInstance.ctx,
				slog.LevelWarn,
				"scheduler initialization failed; state reconciliation skipped",
				slog.Any(logFieldError, err),
			)
		}
	}

	return schedulerInstance
}

// NewSchedulerWithError creates a scheduler and returns constructor-time errors.
// It fails when recovered storage state reconciliation fails or when default URL validator initialization fails.
// If WithURLValidator is provided (including nil), default validator initialization is skipped.
func NewSchedulerWithError(opts ...Option) (*Scheduler, error) {
	ctx, cancel := context.WithCancel(context.Background())
	schedulerInstance := &Scheduler{
		jobs:         make(map[string]*jobEntry),
		jobsStorage:  NewInMemoryJobsStorage(),
		historyLimit: defaultHistoryLimit,
		client:       http.DefaultClient,
		logger:       slog.Default(),
		ctx:          ctx,
		cancel:       cancel,
	}

	for _, opt := range opts {
		opt(schedulerInstance)
	}

	err := schedulerInstance.reconcileRecoveredState()
	if err != nil {
		return schedulerInstance, err
	}

	if schedulerInstance.urlValidatorConfigured {
		return schedulerInstance, nil
	}

	validator, err := newDefaultURLValidator()
	if err != nil {
		return schedulerInstance, fmt.Errorf("%w: %w", ErrURLValidatorInitialization, err)
	}

	schedulerInstance.urlValidator = validator

	return schedulerInstance, nil
}

// Schedule registers a job and starts its schedule loop.
func (s *Scheduler) Schedule(job Job) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return "", ErrSchedulerStopped
	}

	normalized, err := s.normalizeJob(job)
	if err != nil {
		return "", err
	}

	if normalized.ID == "" {
		normalized.ID = s.nextID()
	}

	if _, exists := s.jobs[normalized.ID]; exists {
		return "", ewrap.Wrapf(ErrInvalidJob, "job id already exists: %s", normalized.ID)
	}

	err = s.jobsStorage.Save(normalized)
	if err != nil {
		if errors.Is(err, errJobAlreadyExists) {
			return "", ewrap.Wrapf(ErrInvalidJob, "job id already exists: %s", normalized.ID)
		}

		return "", ewrap.Wrapf(ErrStorageOperation, "job storage save failed: %v", err)
	}

	err = s.jobsStorage.UpsertStatus(normalized.ID, JobStateScheduled)
	if err != nil {
		_ = s.jobsStorage.Delete(normalized.ID)

		return "", ewrap.Wrapf(ErrStorageOperation, "job status upsert failed: %v", err)
	}

	ctx, cancelCause := context.WithCancelCause(s.ctx)
	cancel := func() {
		cancelCause(nil)
	}

	entry := &jobEntry{
		job:    normalized,
		cancel: cancel,
	}
	s.jobs[normalized.ID] = entry

	s.wg.Add(1)

	go func() {
		defer cancel()

		s.runJob(ctx, entry)
	}()

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
	_ = s.jobsStorage.Delete(id)

	err := s.jobsStorage.MarkRemoved(id)
	if err != nil {
		s.logStorageWriteFailure("mark removed", id, err)
	}

	return true
}

// Stop cancels all jobs and waits for completion.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		s.wg.Wait()

		return
	}

	s.stopped = true
	s.cancel()

	for _, entry := range s.jobs {
		entry.cancel()
		_ = s.jobsStorage.Delete(entry.job.ID)

		err := s.jobsStorage.MarkTerminal(entry.job.ID, JobStateStopped)
		if err != nil {
			s.logStorageWriteFailure("mark stopped", entry.job.ID, err)
		}
	}

	s.jobs = make(map[string]*jobEntry)
	s.mu.Unlock()

	s.wg.Wait()
}

// JobCount returns the number of jobs currently registered in the scheduler.
func (s *Scheduler) JobCount() int {
	return s.jobsStorage.Count()
}

// JobIDs returns the currently registered job IDs in sorted order.
func (s *Scheduler) JobIDs() []string {
	return s.jobsStorage.IDs()
}

// JobStatus returns the latest known status for a job.
func (s *Scheduler) JobStatus(id string) (JobStatus, bool) {
	return s.jobsStorage.Status(id)
}

// JobStatuses returns all known job statuses sorted by job ID.
func (s *Scheduler) JobStatuses() []JobStatus {
	return s.jobsStorage.Statuses()
}

// JobHistory returns retained execution history for a job.
func (s *Scheduler) JobHistory(id string) ([]JobRun, bool) {
	return s.jobsStorage.History(id)
}

// QueryJobStatuses returns status snapshots filtered by job IDs and/or states.
// Results are always sorted by job ID and then paginated via Offset/Limit.
func (s *Scheduler) QueryJobStatuses(query JobStatusQuery) []JobStatus {
	statuses := s.jobsStorage.Statuses()
	if len(statuses) == 0 {
		return []JobStatus{}
	}

	idFilter := makeIDFilter(query.IDs)
	stateFilter := makeStateFilter(query.States)

	filtered := make([]JobStatus, 0, len(statuses))
	for _, status := range statuses {
		if len(idFilter) > 0 {
			if _, ok := idFilter[status.JobID]; !ok {
				continue
			}
		}

		if len(stateFilter) > 0 {
			if _, ok := stateFilter[status.State]; !ok {
				continue
			}
		}

		filtered = append(filtered, status)
	}

	offset := max(query.Offset, 0)

	if offset >= len(filtered) {
		return []JobStatus{}
	}

	end := len(filtered)
	if query.Limit > 0 && offset+query.Limit < end {
		end = offset + query.Limit
	}

	return append([]JobStatus(nil), filtered[offset:end]...)
}

// QueryJobHistory returns retained execution history for a job with optional sequence and limit filters.
// Returned runs preserve ascending sequence order.
func (s *Scheduler) QueryJobHistory(id string, query JobHistoryQuery) ([]JobRun, bool) {
	history, ok := s.jobsStorage.History(id)
	if !ok {
		return nil, false
	}

	filtered := history
	if query.FromSequence > 0 {
		filtered = make([]JobRun, 0, len(history))
		for _, run := range history {
			if run.Sequence >= query.FromSequence {
				filtered = append(filtered, run)
			}
		}
	}

	if query.Limit > 0 && len(filtered) > query.Limit {
		filtered = filtered[len(filtered)-query.Limit:]
	}

	return append([]JobRun(nil), filtered...), true
}

func makeIDFilter(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}

	filter := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		filter[id] = struct{}{}
	}

	return filter
}

func makeStateFilter(states []JobState) map[JobState]struct{} {
	if len(states) == 0 {
		return nil
	}

	filter := make(map[JobState]struct{}, len(states))
	for _, state := range states {
		filter[state] = struct{}{}
	}

	return filter
}

func (s *Scheduler) reconcileRecoveredState() error {
	ids := s.jobsStorage.IDs()

	for _, id := range ids {
		// Scheduler runtime registrations are not resumed automatically after process restart.
		// Persisted scheduled/running jobs are reconciled to canceled status and then removed from active storage.
		state, ok := s.jobsStorage.State(id)
		if ok && (state == JobStateScheduled || state == JobStateRunning) {
			err := s.jobsStorage.MarkTerminal(id, JobStateCanceled)
			if err != nil {
				return ewrap.Wrapf(ErrStorageOperation, "mark recovered job canceled failed: id=%s err=%v", id, err)
			}
		}

		_ = s.jobsStorage.Delete(id)
	}

	return nil
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
		return ewrap.Wrap(ErrInvalidJob, "schedule interval must be > 0")
	}

	if schedule.MaxRuns < 0 {
		return ewrap.Wrap(ErrInvalidJob, "max runs must be >= 0")
	}

	if !schedule.EndAt.IsZero() && !schedule.StartAt.IsZero() && schedule.EndAt.Before(schedule.StartAt) {
		return ewrap.Wrap(ErrInvalidJob, "end time precedes start time")
	}

	return nil
}

func (s *Scheduler) normalizeRequest(request Request) (Request, error) {
	if request.URL == "" {
		return Request{}, ewrap.Wrap(ErrInvalidJob, "request URL is required")
	}

	if request.Method == "" {
		request.Method = http.MethodGet
	}

	request.Method = strings.ToUpper(request.Method)
	if !isSupportedMethod(request.Method) {
		return Request{}, ewrap.Wrap(ErrUnsupportedMethod, request.Method)
	}

	if s.urlValidator != nil {
		result, err := s.urlValidator.Validate(s.ctx, request.URL)
		if err != nil {
			return Request{}, ewrap.Wrap(ErrInvalidJob, fmt.Sprintf("request URL invalid: %v", err))
		}

		request.URL = result.NormalizedURL
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
		return Callback{}, ewrap.Wrap(ErrUnsupportedMethod, callback.Method)
	}

	if s.urlValidator != nil {
		result, err := s.urlValidator.Validate(s.ctx, callback.URL)
		if err != nil {
			return Callback{}, ewrap.Wrap(ErrInvalidJob, fmt.Sprintf("callback URL invalid: %v", err))
		}

		callback.URL = result.NormalizedURL
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
		return ewrap.Wrap(ErrInvalidJob, fmt.Sprintf("failed to create retrier: %v", err))
	}

	retrier.Registry.LoadDefaults()
	job.RetryPolicy.Retrier = retrier

	return nil
}

func (s *Scheduler) runJob(ctx context.Context, entry *jobEntry) {
	defer s.wg.Done()
	defer s.cleanupJob(entry)

	job := entry.job

	terminalState := JobStateCompleted

	defer func() {
		s.finalizeJobState(job.ID, terminalState)
	}()
	defer entry.runsWg.Wait()

	if !job.Schedule.StartAt.IsZero() {
		if !s.waitUntil(ctx, job.Schedule.StartAt) {
			terminalState = s.contextTerminalState(job.ID)

			return
		}
	}

	ticker := time.NewTicker(job.Schedule.Every)
	defer ticker.Stop()

	var runs int

	for {
		if ctx.Err() != nil {
			terminalState = s.contextTerminalState(job.ID)

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
		s.enqueue(ctx, entry, scheduledAt)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) cleanupJob(entry *jobEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.jobs[entry.job.ID]
	if !ok || current != entry {
		return
	}

	delete(s.jobs, entry.job.ID)
	_ = s.jobsStorage.Delete(entry.job.ID)
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

func (s *Scheduler) enqueue(ctx context.Context, entry *jobEntry, scheduledAt time.Time) {
	if s.sem != nil {
		s.sem <- struct{}{}
	}

	entry.runsWg.Add(1)

	s.wg.Go(func() {
		defer entry.runsWg.Done()

		if s.sem != nil {
			defer func() { <-s.sem }()
		}

		s.execute(ctx, entry.job, scheduledAt)
	})
}

func (s *Scheduler) execute(ctx context.Context, job Job, scheduledAt time.Time) {
	s.markExecutionStart(job.ID)

	startedAt := time.Now()
	retrier, temporaryErrors := job.RetryPolicy.Retrier,
		s.buildTemporaryErrors(job.RetryPolicy.Retrier, job.RetryPolicy)
	state := &executionState{}

	errs := retrier.DoWithContext(ctx, func(attemptCtx context.Context) error {
		state.incrementAttempts()

		reqCtx := attemptCtx

		if job.Request.Timeout > 0 {
			var cancel context.CancelFunc

			reqCtx, cancel = context.WithTimeout(attemptCtx, job.Request.Timeout)
			defer cancel()
		}

		req, err := http.NewRequestWithContext(reqCtx, job.Request.Method, job.Request.URL, bytes.NewReader(job.Request.Body))
		if err != nil {
			return ewrap.Wrapf(err, "failed to create request for %s %s", job.Request.Method, job.Request.URL)
		}

		for key, value := range job.Request.Headers {
			req.Header.Set(key, value)
		}

		// G704 false positive: job request URL is validated/normalized in normalizeRequest before scheduling.
		resp, err := s.client.Do(req) // #nosec G704
		if err != nil {
			return ewrap.Wrap(err, "request failed")
		}

		defer func() {
			closeErr := resp.Body.Close()
			if closeErr != nil {
				s.logError("response body close failed", closeErr)
			}
		}()

		body, readErr := s.readBody(resp.Body, job.Callback.URL != "", job.Callback.MaxBodyBytes)
		if readErr != nil {
			s.logError("response body read failed", readErr)
		}

		state.setLastResponse(resp.StatusCode, body)

		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return nil
		}

		if isRetryableStatus(resp.StatusCode, job.RetryPolicy.RetryStatusCodes) {
			return ewrap.Wrapf(ErrRetryableStatus, "status %d", resp.StatusCode)
		}

		return &StatusError{StatusCode: resp.StatusCode}
	}, temporaryErrors...)

	snapshot := state.snapshot()
	finishedAt := time.Now()
	payload := CallbackPayload{
		JobID:        job.ID,
		ScheduledAt:  scheduledAt,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Attempts:     snapshot.attempts,
		Success:      errs.Last == nil,
		StatusCode:   snapshot.status,
		Error:        errorString(errs.Last),
		ResponseBody: string(snapshot.body),
	}

	s.recordExecutionResult(job.ID, payload)
	s.sendCallback(ctx, job, payload)
}

type executionState struct {
	mu         sync.Mutex
	attempts   int
	lastStatus int
	lastBody   []byte
}

type executionSnapshot struct {
	attempts int
	status   int
	body     []byte
}

func (s *executionState) incrementAttempts() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.attempts++
}

func (s *executionState) setLastResponse(status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastStatus = status
	s.lastBody = body
}

func (s *executionState) snapshot() executionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return executionSnapshot{
		attempts: s.attempts,
		status:   s.lastStatus,
		body:     append([]byte(nil), s.lastBody...),
	}
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

	cbCtx := ctx

	if job.Callback.Timeout > 0 {
		var cancel context.CancelFunc

		cbCtx, cancel = context.WithTimeout(ctx, job.Callback.Timeout)
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

	// G704 false positive: callback URL is validated/normalized in normalizeCallback before scheduling.
	resp, err := s.client.Do(req) // #nosec G704
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
		s.logger.Log(s.ctx, slog.LevelDebug, msg, slog.Any(logFieldError, err))

		return
	}

	s.logger.Log(s.ctx, slog.LevelWarn, msg, slog.Any(logFieldError, err))
}

func (s *Scheduler) finalizeJobState(id string, state JobState) {
	err := s.jobsStorage.MarkTerminal(id, state)
	if err != nil {
		s.logStorageWriteFailure("mark terminal", id, err)
	}
}

func (s *Scheduler) contextTerminalState(id string) JobState {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()

	state, ok := s.jobsStorage.State(id)
	if ok && state == JobStateRemoved {
		return JobStateRemoved
	}

	if stopped {
		return JobStateStopped
	}

	return JobStateCanceled
}

func (s *Scheduler) markExecutionStart(id string) {
	err := s.jobsStorage.MarkExecutionStart(id)
	if err != nil {
		s.logStorageWriteFailure("mark execution start", id, err)
	}
}

func (s *Scheduler) recordExecutionResult(id string, payload CallbackPayload) {
	err := s.jobsStorage.RecordExecutionResult(id, payload, s.historyLimit)
	if err != nil {
		s.logStorageWriteFailure("record execution result", id, err)
	}
}

func (s *Scheduler) logStorageWriteFailure(operation, id string, err error) {
	s.logError(
		"scheduler storage write failed",
		ewrap.Wrapf(err, "operation=%s job_id=%s", operation, id),
	)
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
