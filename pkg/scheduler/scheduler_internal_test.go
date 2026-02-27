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

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewSchedulerWithError_DefaultURLValidatorFailure(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ErrInvalidJob
	})
	defer restore()

	sched, err := NewSchedulerWithError()
	if sched == nil {
		t.Fatal("expected scheduler instance, got nil")
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

	sched, err := NewSchedulerWithError(WithURLValidator(nil))
	if err != nil {
		t.Fatalf("expected nil error when validator is explicitly disabled, got %v", err)
	}

	if sched == nil {
		t.Fatal("expected scheduler instance, got nil")
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

	sched := NewScheduler(WithLogger(logger))
	if sched == nil {
		t.Fatal("expected scheduler instance, got nil")
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
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop()

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
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop()

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
		WithLogger(logger),
		WithURLValidator(nil),
	)
	defer sched.Stop()

	sched.logError("ignored", nil)

	if got := logBuffer.String(); got != "" {
		t.Fatalf("expected no logs when error is nil, got: %q", got)
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
