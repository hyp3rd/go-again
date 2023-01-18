package again

import (
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
	// Timeout is the timeout for the retry function.
	Timeout time.Duration
	// Registry is the registry for temporary errors.
	Registry *registry
	// once is used to ensure the registry is initialized only once.
	once sync.Once
	// timer is the timer used to timeout the retry function.
	timer *time.Timer
}

// NewRetrier returns a new Retrier.
func NewRetrier(maxRetries int, jitter time.Duration, timeout time.Duration) *Retrier {
	return &Retrier{
		MaxRetries: maxRetries,
		Jitter:     jitter,
		Timeout:    timeout,
		Registry:   NewRegistry(),
		timer:      time.NewTimer(timeout),
	}
}

// SetRegistry sets the registry for temporary errors.
func (r *Retrier) SetRegistry(reg *registry) {
	r.once.Do(func() {
		r.Registry = reg
	})
}

// Retry retries a function until it returns a nil error or the maximum number of retries is reached.
func (r *Retrier) Retry(fn func() error, temporaryErrors ...string) error {
	// If the maximum number of retries is 0, call the function once and return the result.
	if r.MaxRetries == 0 {
		return fn()
	}

	// Check for invalid inputs.
	if fn == nil {
		return errors.New("fn cannot be nil")
	}

	// Create a new random number generator.
	// rng is the random number generator used to apply jitter to the retry interval.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// defer stop the timer for the timeout.
	defer r.timer.Stop()

	// Initialize a variable to store the error returned by the function.
	var err error

	// Retry the function until it returns a nil error or the maximum number of retries is reached.
	for i := 0; i < r.MaxRetries; i++ {
		// Call the function.
		err = fn()

		// If the function returns a nil error, return nil.
		if err == nil {
			return nil
		}

		// Check if the error returned by the function is temporary when the list of temporary errors is not empty.
		if len(temporaryErrors) > 0 && !r.IsTemporaryError(err, temporaryErrors...) {
			break
		}

		// Sleep for a random duration between 0 and the jitter value.
		sleepDuration := time.Duration(rng.Int63n(int64(r.Jitter))) + 1
		select {
		case <-r.timer.C:
			// Return an error if the timeout is reached.
			return fmt.Errorf("timeout reached after %v: %w", r.Timeout, err)
		case <-time.After(sleepDuration):
			// Continue the loop if the sleep duration expires.
		}
	}

	// Return an error indicating that the maximum number of retries was reached.
	retryErr := retryErrorPool.Get().(*RetryError)
	retryErr.MaxRetries = r.MaxRetries
	retryErr.Err = err
	return retryErr
}

// IsTemporaryError checks if the error is in the list of temporary errors.
func (r *Retrier) IsTemporaryError(err error, names ...string) bool {
	tempErrors := r.Registry.GetTemporaryErrors(names...)
	for _, tempErr := range tempErrors {
		return errors.Is(tempErr, err) && err.Error() == tempErr.Error()
	}
	return false
}
