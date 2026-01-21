package scheduler

import (
	"log/slog"
	"net/http"
)

// Option configures the scheduler.
type Option func(*Scheduler)

// WithHTTPClient sets the HTTP client used for requests and callbacks.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Scheduler) {
		if client != nil {
			s.client = client
		}
	}
}

// WithLogger sets the scheduler logger.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Scheduler) {
		s.logger = logger
	}
}

// WithConcurrency limits the number of concurrent executions.
func WithConcurrency(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.sem = make(chan struct{}, n)
		}
	}
}
