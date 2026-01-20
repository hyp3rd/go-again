package again

import (
	"context"
	"errors"
	"log/slog"
	"math"
	randv2 "math/rand/v2"
	"sync"
	"time"

	"github.com/hyp3rd/ewrap"
)

const (
	defaultMaxRetries    = 5
	defaultJitter        = 1 * time.Second
	defaultBackoffFactor = 2
	defaultInterval      = 500 * time.Millisecond
	defaultTimeout       = 20 * time.Second
)

var (
	// ErrInvalidRetrier is the error returned when the retrier is invalid.
	ErrInvalidRetrier = ewrap.New("invalid retrier")
	// ErrMaxRetriesReached is the error returned when the maximum number of retries is reached.
	ErrMaxRetriesReached = ewrap.New("maximum number of retries reached")
	// ErrTimeoutReached is the error returned when the timeout is reached.
	ErrTimeoutReached = ewrap.New("operation timeout reached")
	// ErrOperationStopped is the error returned when the retry is stopped.
	ErrOperationStopped = ewrap.New("operation stopped")
	// ErrNilRetryableFunc is the error returned when the retryable function is nil.
	ErrNilRetryableFunc = ewrap.New("failed to invoke the function. It appears to be nil")
)

// RetryableFunc signature of retryable function.
type RetryableFunc func() error

// RetryableFuncWithContext is a retryable function that observes context cancellation.
type RetryableFuncWithContext func(ctx context.Context) error

// Errors holds the error returned by the retry function along with the trace of each attempt.
type Errors struct {
	// Attempts holds the trace of each attempt in order.
	Attempts []error
	// Last holds the last error returned by the retry function.
	Last error
}

// Reset clears stored errors.
func (e *Errors) Reset() {
	e.Attempts = e.Attempts[:0]
	e.Last = nil
}

// Join aggregates all attempt errors into one.
func (e *Errors) Join() error {
	return errors.Join(e.Attempts...)
}

// Retrier is a type that retries a function until it returns a nil error or the maximum number of retries is reached.
//
//nolint:containedctx
type Retrier struct {
	// MaxRetries is the maximum number of retries after the first attempt.
	MaxRetries int
	// Jitter is the amount of jitter to apply to the retry interval.
	Jitter time.Duration
	// BackoffFactor is the factor to apply to the retry interval.
	BackoffFactor float64
	// Interval is the interval between retries.
	Interval time.Duration
	// Timeout is the timeout for the retry function.
	Timeout time.Duration
	// Registry is the registry for temporary errors.
	Registry *Registry
	// registryOnce ensures the registry is initialized only once.
	registryOnce sync.Once
	// cancelOnce ensures Cancel is called only once.
	cancelOnce sync.Once
	// stopOnce ensures Stop is called only once.
	stopOnce sync.Once
	// initOnce ensures internal state is initialized once per Retrier instance.
	initOnce sync.Once
	// ctxOnce ensures the internal context is initialized once per Retrier instance.
	ctxOnce sync.Once
	// timer is the timer pool.
	timer *TimerPool
	// errorsPool is a pool of Errors objects used to reduce memory allocations.
	errorsPool sync.Pool
	// ctx is used to cancel retries.
	ctx        context.Context
	cancelFunc context.CancelCauseFunc
	// Logger used for logging attempts.
	Logger *slog.Logger
	// Hooks executed during retries.
	Hooks Hooks
}

// NewRetrier returns a new Retrier configured with the given options.
// If no options are provided, the default options are used.
// The default options are:
//   - MaxRetries: 5 (retries after the first attempt)
//   - Jitter: 1 * time.Second
//   - Interval: 500 * time.Millisecond
//   - Timeout: 20 * time.Second
func NewRetrier(ctx context.Context, opts ...Option) (retrier *Retrier, err error) {
	// initiate a Retrier with the defaults.
	retrier = &Retrier{
		MaxRetries:    defaultMaxRetries,
		Jitter:        defaultJitter,
		BackoffFactor: defaultBackoffFactor,
		Interval:      defaultInterval,
		Timeout:       defaultTimeout,
		Registry:      NewRegistry(),
		Logger:        slog.Default(),
	}

	retrier.errorsPool = sync.Pool{
		New: func() any {
			return &Errors{Attempts: make([]error, 0)}
		},
	}

	// apply the options.
	applyOptions(retrier, opts...)

	// validate the Retrier.
	err = retrier.Validate()
	if err != nil {
		return retrier, err
	}

	// initialize the timer pool and internal context.
	retrier.timer = NewTimerPool(retrier.MaxRetries+1, retrier.Timeout)
	retrier.ctx, retrier.cancelFunc = context.WithCancelCause(ctx)

	return retrier, err
}

// Validate validates the Retrier.
// This method will check if:
//   - `MaxRetries` is less than zero
//   - `Interval` is greater than or equal to `Timeout`
//   - The total time consumed by all retries (`Interval` multiplied by `MaxRetries`) should be less than `Timeout`.
func (r *Retrier) Validate() error {
	if r.MaxRetries < 0 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid max retries: %d, the value should be greater than or equal to zero", r.MaxRetries)
	}

	if r.Interval <= 0 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid interval: %s, the value should be greater than zero", r.Interval)
	}

	if r.Jitter <= 0 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid jitter: %s, the value should be greater than zero", r.Jitter)
	}

	if r.Timeout <= 0 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid timeout: %s, the value should be greater than zero", r.Timeout)
	}

	if r.Interval >= r.Timeout {
		return ewrap.Wrapf(ErrInvalidRetrier, "the interval %s should be less than timeout %s", r.Interval, r.Timeout)
	}

	if r.Interval*time.Duration(r.MaxRetries) >= r.Timeout {
		return ewrap.Wrapf(
			ErrInvalidRetrier,
			"the interval %s multiplied by max retries %d should be less than timeout %s",
			r.Interval,
			r.MaxRetries,
			r.Timeout,
		)
	}

	if r.BackoffFactor <= 1 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid backoff factor: %f, the value should be greater than 1", r.BackoffFactor)
	}

	return nil
}

// SetRegistry sets the registry for temporary errors.
// Use this function to set a custom registry if:
// - you want to add custom temporary errors.
// - you want to remove the default temporary errors.
// - you want to replace the default temporary errors with your own.
// - you have initialized the Retrier without using the constructor `NewRetrier`.
func (r *Retrier) SetRegistry(reg *Registry) error {
	// set the registry if not nil.
	if reg == nil {
		return ewrap.New("registry cannot be nil")
	}
	// set the registry if not already set.
	r.registryOnce.Do(func() {
		r.Registry = reg
	})

	return nil
}

// Do retries a `retryableFunc` until it returns a nil error or the maximum number of retries is reached.
//   - If the maximum number of retries is reached, the function returns an `Errors` object.
//   - If the `retryableFunc` returns a nil error, the function assigns an `Errors.Last` before returning.
//   - If the `retryableFunc` returns a temporary error, the function retries the function.
//   - If the `retryableFunc` returns a non-temporary error, the function assigns the error to `Errors.Last` and returns.
//   - If the `temporaryErrors` list is empty and the registry has entries, only those errors are retried.
//   - If the `temporaryErrors` list is empty and the registry is empty, all errors are retried.
//   - The context is checked between attempts; long-running functions should handle cancellation themselves.
//
//nolint:revive
func (r *Retrier) Do(ctx context.Context, retryableFunc RetryableFunc, temporaryErrors ...error) (errs *Errors) {
	// get a new Errors object from the pool.
	if obj, ok := r.errorsPool.Get().(*Errors); ok {
		errs = obj
		errs.Reset()
	} else {
		errs = &Errors{Attempts: make([]error, 0, r.MaxRetries+1)}
	}

	// validate the Retrier.
	err := r.Validate()
	if err != nil {
		errs.Last = err

		return errs
	}

	// Check for invalid inputs.
	if retryableFunc == nil {
		errs.Last = ErrNilRetryableFunc

		return errs
	}

	r.ensureInitialized()

	// Start the timeout timer.
	timeoutTimer := r.timer.Get()
	defer r.timer.Put(timeoutTimer)

	timeoutTimer.Reset(r.Timeout)

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		// Call the function to retry.
		err := retryableFunc()

		// If the function returns a nil error, return nil.
		if err == nil {
			errs.Last = nil

			return errs
		}

		// Record the error.
		errs.Attempts = append(errs.Attempts, err)

		if !r.shouldRetry(err, temporaryErrors) {
			errs.Last = err

			return errs
		}

		if attempt == r.MaxRetries {
			errs.Last = err

			break
		}

		if r.Hooks.OnRetry != nil {
			r.Hooks.OnRetry(attempt, err)
		}

		if r.Logger != nil {
			r.Logger.Log(ctx, slog.LevelDebug, "retry", slog.Int("attempt", attempt), slog.Any("error", err))
		}

		waitErr := r.waitRetry(ctx, timeoutTimer, attempt, err)
		if waitErr != nil {
			errs.Last = waitErr

			return errs
		}

		errs.Last = err
	}

	// If the maximum number of retries is reached register the last attempt error.
	errs.Attempts = append(errs.Attempts, ewrap.Wrapf(errs.Last, "%v", ErrMaxRetriesReached))

	// Register the last error returned by the function as the last error and return.
	return errs
}

// DoWithContext retries a context-aware function that should observe cancellation.
func (r *Retrier) DoWithContext(ctx context.Context, retryableFunc RetryableFuncWithContext, temporaryErrors ...error) (errs *Errors) {
	// get a new Errors object from the pool.
	if obj, ok := r.errorsPool.Get().(*Errors); ok {
		errs = obj
		errs.Reset()
	} else {
		errs = &Errors{Attempts: make([]error, 0, r.MaxRetries+1)}
	}

	// validate the Retrier.
	err := r.Validate()
	if err != nil {
		errs.Last = err

		return errs
	}

	// Check for invalid inputs.
	if retryableFunc == nil {
		errs.Last = ErrNilRetryableFunc

		return errs
	}

	r.ensureInitialized()

	// Start the timeout timer.
	timeoutTimer := r.timer.Get()
	defer r.timer.Put(timeoutTimer)

	timeoutTimer.Reset(r.Timeout)

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		attemptErr, terminalErr := r.runAttemptWithContext(ctx, timeoutTimer, retryableFunc)
		if terminalErr != nil {
			if errors.Is(terminalErr, ErrTimeoutReached) {
				errs.Last = ewrap.Wrapf(ErrTimeoutReached, "attempt %v", attempt)
			} else {
				errs.Last = terminalErr
			}

			return errs
		}

		if attemptErr == nil {
			errs.Last = nil

			return errs
		}

		outcome := r.handleAttemptError(ctx, timeoutTimer, attempt, attemptErr, errs, temporaryErrors)
		if outcome == attemptStop {
			return errs
		}

		if outcome == attemptMaxRetries {
			break
		}
	}

	// If the maximum number of retries is reached register the last attempt error.
	errs.Attempts = append(errs.Attempts, ewrap.Wrapf(errs.Last, "%v", ErrMaxRetriesReached))

	// Register the last error returned by the function as the last error and return.
	return errs
}

type attemptOutcome int

const (
	attemptContinue attemptOutcome = iota
	attemptStop
	attemptMaxRetries
)

// DoWithResult retries a function that returns a result and an error.
func DoWithResult[T any](ctx context.Context, r *Retrier, fn func() (T, error), temporaryErrors ...error) (T, *Errors) {
	var result T

	errs := r.Do(ctx, func() error {
		var err error

		result, err = fn()

		return err
	}, temporaryErrors...)

	return result, errs
}

// Cancel cancels the retries notifying the `Do` function to return.
func (r *Retrier) Cancel() {
	r.ensureContext()

	r.cancelOnce.Do(func() {
		r.cancelFunc(ErrOperationStopped)
	})
}

// Stop stops the retries.
func (r *Retrier) Stop() {
	r.ensureContext()

	r.stopOnce.Do(func() {
		r.cancelFunc(ErrOperationStopped)
	})
}

// PutErrors returns an Errors object to the pool after resetting it.
func (r *Retrier) PutErrors(errs *Errors) {
	errs.Reset()
	r.errorsPool.Put(errs)
}

func (r *Retrier) ensureContext() {
	r.ctxOnce.Do(func() {
		if r.ctx == nil || r.cancelFunc == nil {
			r.ctx, r.cancelFunc = context.WithCancelCause(context.Background())
		}
	})
}

func (r *Retrier) shouldRetry(err error, temporaryErrors []error) bool {
	if len(temporaryErrors) == 0 {
		if r.Registry.Len() == 0 {
			return true
		}

		return r.Registry.IsTemporaryError(err)
	}

	return r.Registry.IsTemporaryError(err, temporaryErrors...)
}

func (r *Retrier) waitRetry(ctx context.Context, timeoutTimer *time.Timer, attempt int, err error) error {
	// Calculate the exponential backoff interval with jitter.
	backoffInterval := float64(r.Interval) * math.Pow(r.BackoffFactor, float64(attempt))
	backoffDuration := time.Duration(backoffInterval)

	jitterDuration := time.Duration(randv2.Int64N(int64(r.Jitter))) // #nosec G404
	retryInterval := backoffDuration + jitterDuration

	// Wait for the retry interval.
	timer := r.timer.Get()

	timer.Reset(retryInterval)
	defer r.timer.Put(timer)

	select {
	case <-ctx.Done():
		return ewrap.Wrap(ctx.Err(), "context done")
	case <-r.ctx.Done():
		//nolint:contextcheck
		return ewrap.Wrap(context.Cause(r.ctx), "retrier context done")
	case <-timeoutTimer.C:
		return ewrap.Wrapf(ErrTimeoutReached, "attempt %v: %v", attempt, err)
	case <-timer.C:
		return nil
	}
}

func (r *Retrier) handleAttemptError(
	ctx context.Context,
	timeoutTimer *time.Timer,
	attempt int,
	err error,
	errs *Errors,
	temporaryErrors []error,
) attemptOutcome {
	// Record the error.
	errs.Attempts = append(errs.Attempts, err)

	if !r.shouldRetry(err, temporaryErrors) {
		errs.Last = err

		return attemptStop
	}

	if attempt == r.MaxRetries {
		errs.Last = err

		return attemptMaxRetries
	}

	if r.Hooks.OnRetry != nil {
		r.Hooks.OnRetry(attempt, err)
	}

	if r.Logger != nil {
		r.Logger.Log(ctx, slog.LevelDebug, "retry", slog.Int("attempt", attempt), slog.Any("error", err))
	}

	waitErr := r.waitRetry(ctx, timeoutTimer, attempt, err)
	if waitErr != nil {
		errs.Last = waitErr

		return attemptStop
	}

	errs.Last = err

	return attemptContinue
}

func (r *Retrier) runAttemptWithContext(
	ctx context.Context,
	timeoutTimer *time.Timer,
	retryableFunc RetryableFuncWithContext,
) (attemptErr, terminalErr error) {
	attemptCtx, attemptCancel := context.WithCancelCause(ctx)
	resultCh := make(chan error, 1)

	go func() {
		resultCh <- retryableFunc(attemptCtx)
	}()

	select {
	case err := <-resultCh:
		attemptCancel(nil)

		return err, nil
	case <-ctx.Done():
		attemptCancel(context.Cause(ctx))

		return nil, ewrap.Wrap(ctx.Err(), "context done")
	case <-r.ctx.Done():
		attemptCancel(ErrOperationStopped)
		//nolint:contextcheck
		return nil, ewrap.Wrap(context.Cause(r.ctx), "retrier context done")
	case <-timeoutTimer.C:
		attemptCancel(ErrTimeoutReached)

		return nil, ErrTimeoutReached
	}
}

func (r *Retrier) ensureInitialized() {
	r.initOnce.Do(func() {
		if r.Registry == nil {
			r.Registry = NewRegistry()
		}

		if r.Logger == nil {
			r.Logger = slog.Default()
		}

		if r.errorsPool.New == nil {
			r.errorsPool.New = func() any {
				return &Errors{Attempts: make([]error, 0, r.MaxRetries+1)}
			}
		}

		if r.timer == nil {
			r.timer = NewTimerPool(r.MaxRetries+1, r.Timeout)
		}

		r.ensureContext()
	})
}
