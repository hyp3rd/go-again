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
	// timer is the timer used to timeout the retry function.
	// timer *time.Timer
	timer *timerPool
	// err is the error returned by the retry function.
	err error
	// cancel is a channel used to cancel the retries.
	cancel chan struct{}
	// stop is a channel used to stop the retries.
	stop chan struct{}
}

// NewRetrier returns a new Retrier.
func NewRetrier(maxRetries int, jitter time.Duration, interval time.Duration, timeout time.Duration) *Retrier {
	return &Retrier{
		MaxRetries: maxRetries,
		Jitter:     jitter,
		Interval:   interval,
		Timeout:    timeout,
		Registry:   NewRegistry(),
		// timer:      time.NewTimer(timeout),
		timer:  NewTimerPool(maxRetries, timeout),
		cancel: make(chan struct{}),
		stop:   make(chan struct{}),
	}
}

// SetRegistry sets the registry for temporary errors.
func (r *Retrier) SetRegistry(reg *registry) {
	r.once.Do(func() {
		r.Registry = reg
	})
}

// Retry retries a function until it returns a nil error or the maximum number of retries is reached.
func (r *Retrier) Retry(ctx context.Context, fn func() error, temporaryErrors ...string) error {
	// lock the mutex to synchronize access to the timer.
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// If the maximum number of retries is 0, call the function once and return the result.
	if r.MaxRetries == 0 {
		return fn()
	}

	// Check for invalid inputs.
	if fn == nil {
		return errors.New("failed to invoke the function. It appears to be is nil")
	}

	// Create a new random number generator.
	// rng is the random number generator used to apply jitter to the retry interval.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// defer stop the timer for the timeout.
	// defer r.timer.Stop()

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for i := 0; i < r.MaxRetries; i++ {
		// Call the function.
		r.err = fn()

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

		// Sleep for a random duration between interval and the jitter value.
		// sleepDuration := time.Duration(float64(r.Interval) * (1 + rng.Float64()*float64(r.Jitter))
		sleepDuration := time.Duration(rng.Int63n(int64(r.Jitter))) + r.Interval

		// Wait for the retry interval.
		// r.timer.Reset(time.Duration(float64(r.Interval) * (1 + rng.Float64()*float64(r.Jitter))))

		// Wait for the retry interval.
		timer := r.timer.Get()
		timer.Reset(sleepDuration)
		select {
		case <-r.cancel:
			r.timer.Put(timer)
			return fmt.Errorf("retries cancelled")
		case <-r.stop:
			r.timer.Put(timer)
			return r.err
		case <-timer.C:
			r.timer.Put(timer)
		}
	}

	// Return an error indicating that the maximum number of retries was reached.
	retryErr := retryErrorPool.Get().(*RetryError)
	retryErr.MaxRetries = r.MaxRetries
	retryErr.Err = r.err
	return retryErr
}

// Cancel cancels the retries.
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
