package scheduler

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidJob is returned when a job fails validation.
	ErrInvalidJob = errors.New("invalid job")
	// ErrSchedulerStopped is returned when scheduling is attempted after Stop.
	ErrSchedulerStopped = errors.New("scheduler stopped")
	// ErrURLValidatorInitialization is returned when the default URL validator cannot be created.
	ErrURLValidatorInitialization = errors.New("url validator initialization failed")
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
