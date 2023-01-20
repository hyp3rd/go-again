package again

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

var (
	// errorsPool is a pool of Errors objects.
	errorsPool = sync.Pool{
		New: func() interface{} {
			return &Errors{
				Retries: make(map[int]error),
			}
		},
	}

	// ErrInvalidRetrier is the error returned when the retrier is invalid.
	ErrInvalidRetrier = errors.New("invalid retrier")
	// ErrMaxRetriesReached is the error returned when the maximum number of retries is reached.
	ErrMaxRetriesReached = errors.New("maximum number of retries reached")
	// ErrTimeoutReached is the error returned when the timeout is reached.
	ErrTimeoutReached = errors.New("operation timeout reached")
	// ErrOperationCanceled is the error returned when the retry is canceled.
	ErrOperationCanceled = errors.New("retry canceled invoking the `Cancel` function")
	// ErrOperationStopped is the error returned when the retry is stopped.
	ErrOperationStopped = errors.New("operation stopped")
	// ErrNilRetryableFunc is the error returned when the retryable function is nil.
	ErrNilRetryableFunc = errors.New("failed to invoke the function. It appears to be is nil")
)

// RetryableFunc signature of retryable function
type RetryableFunc func() error

// Errors holds the error returned by the retry function along with the trace of each attempt.
type Errors struct {
	// Retries holds the trace of each attempt.
	Retries map[int]error
	// Last holds the last error returned by the retry function.
	Last error
}

// Retrier is a type that retries a function until it returns a nil error or the maximum number of retries is reached.
type Retrier struct {
	// MaxRetries is the maximum number of retries.
	MaxRetries int
	// Jitter is the amount of jitter to apply to the retry interval.
	Jitter time.Duration
	// Interval is the interval between retries.
	Interval time.Duration
	// Timeout is the timeout for the retry function.
	Timeout time.Duration
	// Registry is the registry for temporary errors.
	Registry *registry
	// once is used to ensure the registry is initialized only once.
	once sync.Once
	// mutex is the mutex used to synchronize access to the timer.
	mutex sync.RWMutex
	// timer is the timer pool.
	timer *timerPool
	// err is the error returned by the retry function.
	err error
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
func NewRetrier(opts ...Option) (r *Retrier, err error) {
	// initiate a Retrier with the defaults.
	r = &Retrier{
		MaxRetries: 5,
		Jitter:     1 * time.Second,
		Interval:   500 * time.Millisecond,
		Timeout:    20 * time.Second,
		Registry:   NewRegistry(),
	}

	// apply the options.
	applyOptions(r, opts...)

	// validate the Retrier.
	if err = r.Validate(); err != nil {
		return
	}

	// initialize the timer pool.
	r.timer = newTimerPool(r.MaxRetries, r.Timeout)
	// initialize the stop and cancel channels.
	r.cancel = make(chan struct{}, r.MaxRetries)
	r.stop = make(chan struct{}, r.MaxRetries)

	return
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
	if r.Interval*time.Duration(r.MaxRetries) > r.Timeout {
		return fmt.Errorf("%w: the interval %s multiplied by max retries %d should be less than timeout %s", ErrInvalidRetrier, r.Interval, r.MaxRetries, r.Timeout)
	}
	return nil
}

// SetRegistry sets the registry for temporary errors.
// Use this function to set a custom registry if:
// - you want to add custom temporary errors.
// - you want to remove the default temporary errors.
// - you want to replace the default temporary errors with your own.
// - you have initialized the Retrier without using the constructor `NewRetrier`.
func (r *Retrier) SetRegistry(reg *registry) error {
	// set the registry if not nil.
	if reg == nil {
		return errors.New("registry cannot be nil")
	}
	// set the registry if not already set.
	r.once.Do(func() {
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
func (r *Retrier) Do(ctx context.Context, retryableFunc RetryableFunc, temporaryErrors ...string) (errs *Errors) {
	// lock the mutex to synchronize access to the timer.
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// get a new Errors object from the pool.
	errs = errorsPool.Get().(*Errors)
	defer errorsPool.Put(errs)

	// validate the Retrier.
	err := r.Validate()
	if err != nil {
		errs.Last = err
		return
	}

	// Check for invalid inputs.
	if retryableFunc == nil {
		errs.Last = ErrNilRetryableFunc
		return
	}

	// `rng` is the random number generator used to apply jitter to the retry interval.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Start the timeout timer.
	timeoutTimer := r.timer.get()
	defer r.timer.put(timeoutTimer)
	timeoutTimer.Reset(r.Timeout)

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for attempt := 0; attempt < r.MaxRetries; attempt++ {
		// Call the function to retry.
		r.err = retryableFunc()

		// If the function returns a nil error, return nil.
		if r.err == nil {
			errs.Last = nil
			return
		}

		// Set the error returned by the function.
		errs.Retries[attempt] = r.err

		// Check if the error returned by the function is temporary when the list of temporary errors is not empty.
		if len(temporaryErrors) > 0 && !r.IsTemporaryError(r.err, temporaryErrors...) {
			break
		}

		// Set a retry random interval adding to the `Interval` a random duration between 0 and the `Jitter` value.
		retryInterval := time.Duration(rng.Int63n(int64(r.Jitter))) + r.Interval

		// Wait for the retry interval.
		timer := r.timer.get()
		timer.Reset(retryInterval)
		select {
		case <-r.cancel: // Check if the retries are cancelled.
			r.timer.put(timer)
			errs.Last = ErrOperationCanceled
			return
		case <-r.stop:
			r.timer.put(timer)
			errs.Last = ErrOperationStopped
			return
		case <-ctx.Done(): // Check if the context is cancelled.
			errs.Last = ctx.Err()
			return
		case <-timer.C: // Wait for the retry interval.
			r.timer.put(timer)
		case <-timeoutTimer.C: // Check if the timeout is reached.
			errs.Last = fmt.Errorf("%v at attempt %v with error %w", ErrTimeoutReached, attempt, r.err)
			return
		}
	}

	// If the maximum number of retries is reached, return the errors.
	errs.Last = fmt.Errorf("%v with error %w", ErrMaxRetriesReached, r.err)
	return
}

// Cancel cancels the retries notifying the `Do` function to return.
func (r *Retrier) Cancel() {
	r.once.Do(func() {
		close(r.cancel)
	})
}

// IsTemporaryError checks if the error is in the list of temporary errors.
func (r *Retrier) IsTemporaryError(err error, names ...string) bool {
	tempErrors := r.Registry.GetTemporaryErrors(names...)
	for _, tempErr := range tempErrors {
		return errors.Is(tempErr, err) && err.Error() == tempErr.Error()
	}
	return false
}
