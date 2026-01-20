package again

import (
	"log/slog"
	"time"
)

// Option is a function type that can be used to configure the `Retrier` struct.
type Option func(*Retrier)

// applyOptions applies the options to the retrier.
func applyOptions(retrier *Retrier, options ...Option) {
	for _, option := range options {
		option(retrier)
	}
}

// WithMaxRetries returns an option that sets the maximum number of retries after the first attempt.
func WithMaxRetries(num int) Option {
	return func(retrier *Retrier) {
		retrier.MaxRetries = num
	}
}

// WithJitter returns an option that sets the jitter.
func WithJitter(jitter time.Duration) Option {
	return func(retrier *Retrier) {
		retrier.Jitter = jitter
	}
}

// WithBackoffFactor returns an option that sets the backoff factor.
func WithBackoffFactor(factor float64) Option {
	return func(retrier *Retrier) {
		retrier.BackoffFactor = factor
	}
}

// WithInterval returns an option that sets the interval.
func WithInterval(interval time.Duration) Option {
	return func(retrier *Retrier) {
		retrier.Interval = interval
	}
}

// WithTimeout returns an option that sets the timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(retrier *Retrier) {
		retrier.Timeout = timeout
	}
}

// WithLogger sets a slog logger.
func WithLogger(logger *slog.Logger) Option {
	return func(retrier *Retrier) {
		retrier.Logger = logger
	}
}

// Hooks defines callback functions invoked by the retrier.
type Hooks struct {
	// OnRetry is called before waiting for the next retry interval.
	OnRetry func(attempt int, err error)
}

// WithHooks sets hooks executed during retries.
func WithHooks(h Hooks) Option {
	return func(retrier *Retrier) {
		retrier.Hooks = h
	}
}
