package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/hyp3rd/ewrap"
	"github.com/hyp3rd/sectools/pkg/validate"
)

const expectedSchedulerInstanceMsg = "expected scheduler instance, got nil"

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewSchedulerWithError_DefaultURLValidatorFailure(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ErrInvalidJob
	})
	defer restore()

	sched, err := NewSchedulerWithError(t.Context())
	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	if err == nil || !errors.Is(err, ErrURLValidatorInitialization) {
		t.Fatalf("expected URL validator initialization error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewSchedulerWithError_ExplicitNilValidatorSkipsDefaultInitialization(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ErrInvalidJob
	})
	defer restore()

	sched, err := NewSchedulerWithError(t.Context(), WithURLValidator(nil))
	if err != nil {
		t.Fatalf("expected nil error when validator is explicitly disabled, got %v", err)
	}

	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	if sched.urlValidator != nil {
		t.Fatal("expected URL validator to be nil when explicitly disabled")
	}
}

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewScheduler_WarnsWhenDefaultURLValidatorFails(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ewrap.Wrap(ErrInvalidJob, "default URL validator initialization failed")
	})
	defer restore()

	logBuffer := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(t.Context(), WithLogger(logger))
	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	if sched.urlValidator != nil {
		t.Fatal("expected URL validator to be nil when default initialization fails")
	}

	logs := logBuffer.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "default URL validator initialization failed") {
		t.Fatalf("expected warning log for default validator initialization failure, got: %q", logs)
	}
}

func TestSchedulerLogErrorWarnLevel(t *testing.T) {
	t.Parallel()

	logBuffer := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(
		t.Context(),
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop(t.Context())

	sched.logError("warn-path", ErrInvalidJob)

	logs := logBuffer.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "warn-path") {
		t.Fatalf("expected warn-level scheduler log, got: %q", logs)
	}
}

func TestSchedulerLogErrorDebugLevelOnContextCancellation(t *testing.T) {
	t.Parallel()

	logBuffer := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(
		t.Context(),
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop(t.Context())

	sched.logError("debug-path", context.Canceled)

	logs := logBuffer.String()
	if !strings.Contains(logs, "level=DEBUG") || !strings.Contains(logs, "debug-path") {
		t.Fatalf("expected debug-level scheduler log for context cancellation, got: %q", logs)
	}
}

func TestSchedulerLogErrorIgnoresNilError(t *testing.T) {
	t.Parallel()

	logBuffer := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(
		t.Context(),
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop(t.Context())

	sched.logError("ignored", nil)

	if got := logBuffer.String(); got != "" {
		t.Fatalf("expected no logs when error is nil, got: %q", got)
	}
}

func TestNewSchedulerWithError_ReconcilesRecoveredState(t *testing.T) {
	t.Parallel()

	const (
		scheduledJobID = "scheduled-job"
		runningJobID   = "running-job"
		completedJobID = "completed-job"
	)

	storage := NewInMemoryJobsStorage()

	err := seedRecoveredJobForTest(t.Context(), storage, scheduledJobID, JobStateScheduled)
	if err != nil {
		t.Fatalf("failed to seed scheduled recovered job: %v", err)
	}

	err = seedRecoveredJobForTest(t.Context(), storage, runningJobID, JobStateRunning)
	if err != nil {
		t.Fatalf("failed to seed running recovered job: %v", err)
	}

	err = seedRecoveredJobForTest(t.Context(), storage, completedJobID, JobStateCompleted)
	if err != nil {
		t.Fatalf("failed to seed completed recovered job: %v", err)
	}

	sched, err := NewSchedulerWithError(
		t.Context(),
		WithJobsStorage(storage),
		WithURLValidator(nil),
	)
	if err != nil {
		t.Fatalf("expected nil error while reconciling recovered state, got %v", err)
	}

	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	defer sched.Stop(t.Context())

	if got := sched.JobCount(t.Context()); got != 0 {
		t.Fatalf("expected zero active jobs after reconciliation, got %d", got)
	}

	if gotIDs := sched.JobIDs(t.Context()); len(gotIDs) != 0 {
		t.Fatalf("expected no active job IDs after reconciliation, got %v", gotIDs)
	}

	scheduledStatus, ok := sched.JobStatus(t.Context(), scheduledJobID)
	if !ok {
		t.Fatalf("expected status for %q to exist", scheduledJobID)
	}

	if scheduledStatus.State != JobStateCanceled {
		t.Fatalf("expected %q to be canceled after reconciliation, got %q", scheduledJobID, scheduledStatus.State)
	}

	runningStatus, ok := sched.JobStatus(t.Context(), runningJobID)
	if !ok {
		t.Fatalf("expected status for %q to exist", runningJobID)
	}

	if runningStatus.State != JobStateCanceled {
		t.Fatalf("expected %q to be canceled after reconciliation, got %q", runningJobID, runningStatus.State)
	}

	completedStatus, ok := sched.JobStatus(t.Context(), completedJobID)
	if !ok {
		t.Fatalf("expected status for %q to exist", completedJobID)
	}

	if completedStatus.State != JobStateCompleted {
		t.Fatalf("expected %q to remain completed after reconciliation, got %q", completedJobID, completedStatus.State)
	}
}

func TestNewSchedulerWithError_ReconcileFailureReturnsStorageError(t *testing.T) {
	t.Parallel()

	storage := NewInMemoryJobsStorage()

	err := seedRecoveredJobForTest(t.Context(), storage, "scheduled-job", JobStateScheduled)
	if err != nil {
		t.Fatalf("failed to seed recovered job: %v", err)
	}

	failingStorage := &failingReconcileJobsStorage{
		JobsStorage:      storage,
		failMarkTerminal: ErrInvalidJob,
	}

	sched, err := NewSchedulerWithError(
		t.Context(),
		WithJobsStorage(failingStorage),
		WithURLValidator(nil),
	)
	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	if err == nil || !errors.Is(err, ErrStorageOperation) {
		t.Fatalf("expected storage operation error from reconciliation failure, got %v", err)
	}
}

func TestNewScheduler_WarnsWhenStateReconciliationFails(t *testing.T) {
	t.Parallel()

	storage := NewInMemoryJobsStorage()

	err := seedRecoveredJobForTest(t.Context(), storage, "scheduled-job", JobStateScheduled)
	if err != nil {
		t.Fatalf("failed to seed recovered job: %v", err)
	}

	failingStorage := &failingReconcileJobsStorage{
		JobsStorage:      storage,
		failMarkTerminal: ErrInvalidJob,
	}

	logBuffer := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(
		t.Context(),
		WithLogger(logger),
		WithJobsStorage(failingStorage),
		WithURLValidator(nil),
	)
	if sched == nil {
		t.Fatal(expectedSchedulerInstanceMsg)
	}

	defer sched.Stop(t.Context())

	logs := logBuffer.String()
	if !strings.Contains(logs, "level=WARN") ||
		!strings.Contains(logs, "scheduler initialization failed; state reconciliation skipped") {
		t.Fatalf("expected warning log for state reconciliation initialization failure, got: %q", logs)
	}
}

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return 0, ewrap.Wrap(err, "locked log buffer write failed")
	}

	return n, nil
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func swapDefaultURLValidatorFactoryForTest(factory func() (*validate.URLValidator, error)) func() {
	previous := newDefaultURLValidator
	newDefaultURLValidator = factory

	return func() {
		newDefaultURLValidator = previous
	}
}

type failingReconcileJobsStorage struct {
	JobsStorage
	failMarkTerminal error
}

func (s *failingReconcileJobsStorage) MarkTerminal(ctx context.Context, id string, state JobState) error {
	if s.failMarkTerminal != nil {
		return s.failMarkTerminal
	}

	return s.JobsStorage.MarkTerminal(ctx, id, state)
}

func seedRecoveredJobForTest(ctx context.Context, storage JobsStorage, id string, state JobState) error {
	err := storage.Save(ctx, Job{
		ID: id,
	})
	if err != nil {
		return err
	}

	return storage.UpsertStatus(ctx, id, state)
}
