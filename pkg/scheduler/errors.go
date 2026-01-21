package scheduler

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidJob is returned when a job fails validation.
	ErrInvalidJob = errors.New("invalid job")
	// ErrUnsupportedMethod is returned for unsupported HTTP methods.
	ErrUnsupportedMethod = errors.New("unsupported method")
	// ErrRetryableStatus marks responses that should be retried.
	ErrRetryableStatus = errors.New("retryable status")
)

// StatusError reports a non-successful response status.
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}
