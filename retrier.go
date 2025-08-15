package again

import (
	"context"
	"fmt"
	"math"
	"math/rand"
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
	// Registry holds the trace of each attempt.
	Registry map[int]error
	// Last holds the last error returned by the retry function.
	Last error
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
	// cancelOnce ensures the cancel channel is closed only once.
	cancelOnce sync.Once
	// mutex is the mutex used to synchronize access to the timer.
	mutex sync.RWMutex
	// timer is the timer pool.
	timer *TimerPool
	// err is the error returned by the retry function.
	err error
	// errorsPool is a pool of Errors objects used to reduce memory allocations.
	errorsPool sync.Pool
	// cancel is a channel used to cancel the retries.
	cancel chan struct{}
	// stop is a channel used to stop the retries.
	stop chan struct{}
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
	}

	retrier.errorsPool = sync.Pool{
		New: func() any {
			return &Errors{
				Registry: make(map[int]error),
			}
		},
	}

	// apply the options.
	applyOptions(retrier, opts...)

	// validate the Retrier.
	err = retrier.Validate()
	if err != nil {
		return retrier, err
	}

	// initialize the timer pool.
	retrier.timer = NewTimerPool(retrier.MaxRetries, retrier.Timeout)
	// initialize the stop and cancel channels.
	retrier.cancel = make(chan struct{}, retrier.MaxRetries)
	retrier.stop = make(chan struct{}, retrier.MaxRetries)

	return retrier, err
}

// Validate validates the Retrier.
// This method will check if:
//   - `MaxRetries` is less than or equal to zero
//   - `Interval` is greater than or equal to `Timeout`
//   - The total time consumed by all retries (`Interval` multiplied by `MaxRetries`) should be less than `Timeout`.
func (r *Retrier) Validate() error {
	if r.MaxRetries <= 0 {
		return fmt.Errorf("%w: invalid max retries: %d, the value should be greater than zero", ErrInvalidRetrier, r.MaxRetries)
	}

	if r.Interval >= r.Timeout {
		return fmt.Errorf("%w: the interval %s should be less than timeout %s", ErrInvalidRetrier, r.Interval, r.Timeout)
	}

	if r.Interval*time.Duration(r.MaxRetries) >= r.Timeout {
		return fmt.Errorf("%w: the interval %s multiplied by max retries %d should be less than timeout %s", ErrInvalidRetrier, r.Interval, r.MaxRetries, r.Timeout)
	}

	if r.BackoffFactor <= 1 {
		return fmt.Errorf("%w: invalid backoff factor: %f, the value should be greater than 1", ErrInvalidRetrier, r.BackoffFactor)
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
func (r *Retrier) Do(ctx context.Context, retryableFunc RetryableFunc, temporaryErrors ...string) (errs *Errors) {
	// lock the mutex to synchronize access to the timer.
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// get a new Errors object from the pool.
	errs, ok := r.errorsPool.Get().(*Errors)
	if !ok {
		errs = &Errors{
			Registry: make(map[int]error),
		}
	}

	defer r.errorsPool.Put(errs)

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

	// `rng` is the random number generator used to apply jitter to the retry interval.
	//nolint:gosec // this has no security implications.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

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

		// Set the error returned by the function.
		errs.Registry[attempt] = r.err

		// Check if the error returned by the function is temporary when the list of temporary errors is not empty.
		if len(temporaryErrors) > 0 && !r.Registry.IsTemporaryError(r.err, temporaryErrors...) {
			break
		}

		// Set a retry random interval adding to the `Interval` a random duration between 0 and the `Jitter` value.
		// Calculate the exponential backoff interval
		backoffInterval := float64(r.Interval) * math.Pow(r.BackoffFactor, float64(attempt))
		backoffDuration := time.Duration(backoffInterval)

		// Add jitter to the backoff duration
		jitterDuration := time.Duration(rng.Int63n(int64(r.Jitter)))
		retryInterval := backoffDuration + jitterDuration

		// Wait for the retry interval.
		timer := r.timer.Get()
		// Adjust the timer to the retry interval including the jitter and backoff.
		timer.Reset(retryInterval)

		select {
		case <-r.cancel: // Check if the retries are cancelled.
			r.timer.Put(timer)

			errs.Last = ErrOperationStopped

			return errs
		case <-r.stop:
			r.timer.Put(timer)

			errs.Last = ErrOperationStopped

			return errs
		case <-ctx.Done(): // Check if the context is cancelled.
			r.timer.Put(timer)

			errs.Last = ctx.Err()

			return errs
		case <-timer.C: // Wait for the retry interval.
			r.timer.Put(timer)
		case <-timeoutTimer.C: // Check if the timeout is reached.
			r.timer.Put(timer)
			errs.Last = ewrap.Wrapf(r.err, "attempt %v: %v", attempt, ErrTimeoutReached)

			return errs
		}
	}

	// If the maximum number of retries is reached register the last attempt error.
	errs.Registry[r.MaxRetries] = ewrap.Wrapf(r.err, "%v: with error", ErrMaxRetriesReached)

	// Register the last error returned by the function as the last error and return.
	errs.Last = r.err

	return errs
}

// Cancel cancels the retries notifying the `Do` function to return.
func (r *Retrier) Cancel() {
	r.cancelOnce.Do(func() {
		close(r.cancel)
	})
}
