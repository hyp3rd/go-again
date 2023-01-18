package again

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"
)

var (
	// retryErrorPool is a pool of RetryError objects.
	retryErrorPool = sync.Pool{
		New: func() interface{} {
			return &RetryError{}
		},
	}
)

// Function signature of retryable function
type RetryableFunc func() error

// RetryError is an error returned by the Retry function when the maximum number of retries is reached.
type RetryError struct {
	MaxRetries int
	Err        error
}

// Error returns the error message.
func (e *RetryError) Error() string {
	return "maximum number of retries (" + strconv.Itoa(e.MaxRetries) + ") reached: " + e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *RetryError) Unwrap() error {
	return e.Err
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
func NewRetrier(opts ...Option) *Retrier {
	// initiate a Retrier with the defaults.
	r := &Retrier{
		MaxRetries: 5,
		Jitter:     1 * time.Second,
		Interval:   500 * time.Millisecond,
		Timeout:    20 * time.Second,
		Registry:   NewRegistry(),
	}

	// apply the options.
	applyOptions(r, opts...)

	// initialize the timer pool.
	r.timer = newTimerPool(r.MaxRetries, r.Timeout)
	// initialize the stop and cancel channels.
	r.cancel = make(chan struct{}, r.MaxRetries)
	r.stop = make(chan struct{}, r.MaxRetries)

	return r
}

// SetRegistry sets the registry for temporary errors.
func (r *Retrier) SetRegistry(reg *registry) {
	r.once.Do(func() {
		r.Registry = reg
	})
}

// Retry retries a `retryableFunc` until it returns a nil error or the maximum number of retries is reached.
//   - If the maximum number of retries is reached, the function returns a `RetryError` object.
//   - If the `retryableFunc` returns a nil error, the function returns nil.
//   - If the `retryableFunc` returns a temporary error, the function retries the function.
//   - If the `retryableFunc` returns a non-temporary error, the function returns the error.
//   - If the `temporaryErrors` list is empty, the function retries the function until the maximum number of retries is reached.
//   - The context is used to cancel the retries, or set a deadline if the `retryableFunc` hangs.
func (r *Retrier) Retry(ctx context.Context, retryableFunc RetryableFunc, temporaryErrors ...string) error {
	// lock the mutex to synchronize access to the timer.
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// If the maximum number of retries is 0, call the function once and return the result.
	if r.MaxRetries == 0 {
		return retryableFunc()
	}

	// Check for invalid inputs.
	if retryableFunc == nil {
		return errors.New("failed to invoke the function. It appears to be is nil")
	}

	// `rng` is the random number generator used to apply jitter to the retry interval.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for i := 0; i < r.MaxRetries; i++ {
		// Call the function to retry.
		r.err = retryableFunc()

		// If the function returns a nil error, return nil.
		if r.err == nil {
			return nil
		}

		// Check if the error returned by the function is temporary when the list of temporary errors is not empty.
		if len(temporaryErrors) > 0 && !r.IsTemporaryError(r.err, temporaryErrors...) {
			break
		}

		// Check if the context is cancelled.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Set a retry random interval adding to the `Interval` a random duration between 0 and the `Jitter` value.
		retryInterval := time.Duration(rng.Int63n(int64(r.Jitter))) + r.Interval

		// Wait for the retry interval.
		timer := r.timer.get()
		timer.Reset(retryInterval)
		select {
		case <-r.cancel:
			r.timer.put(timer)
			return fmt.Errorf("retries cancelled")
		case <-r.stop:
			r.timer.put(timer)
			return r.err
		case <-timer.C:
			r.timer.put(timer)
		}
	}

	// Return an error indicating that the maximum number of retries was reached.
	retryErr := retryErrorPool.Get().(*RetryError)
	retryErr.MaxRetries = r.MaxRetries
	retryErr.Err = r.err
	return retryErr
}

// Cancel cancels the retries notifying the `Retry` function to return.
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
