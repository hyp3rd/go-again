package again

import "time"

// Option is a function type that can be used to configure the `Retrier` struct.
type Option func(*Retrier)

// applyOptions applies the options to the retrier.
func applyOptions(retrier *Retrier, options ...Option) {
	for _, option := range options {
		option(retrier)
	}
}

// WithMaxRetries returns an option that sets the maximum number of retries.
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
