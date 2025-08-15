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
	ErrNilRetryableFunc = ewrap.New("failed to invoke the function. It appears to be is nil")
)

// RetryableFunc signature of retryable function.
type RetryableFunc func() error

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
type Retrier struct {
	// MaxRetries is the maximum number of retries.
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
	// mutex is the mutex used to synchronize access to the timer.
	mutex sync.RWMutex
	// timer is the timer pool.
	timer *TimerPool
	// err is the error returned by the retry function.
	err error
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
//   - MaxRetries: 5
//   - Jitter: 1 * time.Second
//   - Interval: 500 * time.Millisecond
//   - Timeout: 20 * time.Second
func NewRetrier(opts ...Option) (retrier *Retrier, err error) {
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
	retrier.ctx, retrier.cancelFunc = context.WithCancelCause(context.Background())

	return retrier, err
}

// Validate validates the Retrier.
// This method will check if:
//   - `MaxRetries` is less than or equal to zero
//   - `Interval` is greater than or equal to `Timeout`
//   - The total time consumed by all retries (`Interval` multiplied by `MaxRetries`) should be less than `Timeout`.
func (r *Retrier) Validate() error {
	if r.MaxRetries <= 0 {
		return ewrap.Wrapf(ErrInvalidRetrier, "invalid max retries: %d, the value should be greater than zero", r.MaxRetries)
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
		return ewrap.Wrapf(ErrInvalidRetrier, "the interval %s multiplied by max retries %d should be less than timeout %s", r.Interval, r.MaxRetries, r.Timeout)
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
//   - If the `temporaryErrors` list is empty, the function retries the function until the maximum number of retries is reached.
//   - The context is used to cancel the retries, or set a deadline if the `retryableFunc` hangs.
//
//nolint:cyclop // 13 out of 12 is acceptable for this method.
func (r *Retrier) Do(ctx context.Context, retryableFunc RetryableFunc, temporaryErrors ...error) (errs *Errors) {
	// lock the mutex to synchronize access to the timer.
	r.mutex.RLock()
	defer r.mutex.RUnlock()

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

	// Start the timeout timer.
	timeoutTimer := r.timer.Get()
	defer r.timer.Put(timeoutTimer)

	timeoutTimer.Reset(r.Timeout)

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for attempt := range r.MaxRetries {
		// Call the function to retry.
		r.err = retryableFunc()

		// If the function returns a nil error, return nil.
		if r.err == nil {
			errs.Last = nil

			return errs
		}

		// Record the error.
		errs.Attempts = append(errs.Attempts, r.err)

		// Check if the error is temporary when the list is not empty.
		if len(temporaryErrors) > 0 && !r.Registry.IsTemporaryError(r.err, temporaryErrors...) {
			break
		}

		if r.Hooks.OnRetry != nil {
			r.Hooks.OnRetry(attempt, r.err)
		}
		if r.Logger != nil {
			r.Logger.Log(ctx, slog.LevelDebug, "retry", slog.Int("attempt", attempt), slog.Any("error", r.err))
		}

		// Calculate the exponential backoff interval with jitter.
		backoffInterval := float64(r.Interval) * math.Pow(r.BackoffFactor, float64(attempt))
		backoffDuration := time.Duration(backoffInterval)
		jitterDuration := time.Duration(randv2.Int64N(int64(r.Jitter)))
		retryInterval := backoffDuration + jitterDuration

		// Wait for the retry interval.
		timer := r.timer.Get()
		timer.Reset(retryInterval)

		select {
		case <-ctx.Done():
			r.timer.Put(timer)
			errs.Last = ctx.Err()
			return errs
		case <-r.ctx.Done():
			r.timer.Put(timer)
			errs.Last = context.Cause(r.ctx)
			return errs
		case <-timeoutTimer.C:
			r.timer.Put(timer)
			errs.Last = ewrap.Wrapf(r.err, "attempt %v: %v", attempt, ErrTimeoutReached)
			return errs
		case <-timer.C:
			r.timer.Put(timer)
		}
	}

	// If the maximum number of retries is reached register the last attempt error.
	errs.Attempts = append(errs.Attempts, ewrap.Wrapf(r.err, "%v", ErrMaxRetriesReached))

	// Register the last error returned by the function as the last error and return.
	errs.Last = r.err

	return errs
}

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
	r.cancelOnce.Do(func() {
		r.cancelFunc(ErrOperationStopped)
	})
}

// Stop stops the retries.
func (r *Retrier) Stop() {
	r.stopOnce.Do(func() {
		r.cancelFunc(ErrOperationStopped)
	})
}

// PutErrors returns an Errors object to the pool after resetting it.
func (r *Retrier) PutErrors(errs *Errors) {
	errs.Reset()
	r.errorsPool.Put(errs)
}
