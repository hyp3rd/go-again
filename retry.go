package again

import (
	"context"
	"errors"
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

	// If the maximum number of retries is 0, call the function once and return the result.
	if r.MaxRetries == 0 {
		errs.Last = retryableFunc()
		return
	}

	// Check for invalid inputs.
	if retryableFunc == nil {
		errs.Last = errors.New("failed to invoke the function. It appears to be is nil")
		return
	}

	// `rng` is the random number generator used to apply jitter to the retry interval.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for attempt := 0; attempt < r.MaxRetries; attempt++ {
		// Increment the attempt.

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

		// Check if the context is cancelled.
		select {
		case <-ctx.Done():
			errs.Last = ctx.Err()
			return
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
			errs.Last = errors.New("retries cancelled")
			return
		case <-r.stop:
			r.timer.put(timer)
			errs.Last = errors.New("retries stopped")
			return
		case <-timer.C:
			r.timer.put(timer)
		}
	}

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
