package scheduler

import (
	"fmt"

	"github.com/hyp3rd/ewrap"
)

var (
	// ErrInvalidJob is returned when a job fails validation.
	ErrInvalidJob = ewrap.New("invalid job")
	// ErrStorageOperation is returned when scheduler state persistence fails.
	ErrStorageOperation = ewrap.New("storage operation failed")
	// ErrSchedulerStopped is returned when scheduling is attempted after Stop.
	ErrSchedulerStopped = ewrap.New("scheduler stopped")
	// ErrURLValidatorInitialization is returned when the default URL validator cannot be created.
	ErrURLValidatorInitialization = ewrap.New("url validator initialization failed")
	// ErrUnsupportedMethod is returned for unsupported HTTP methods.
	ErrUnsupportedMethod = ewrap.New("unsupported method")
	// ErrRetryableStatus marks responses that should be retried.
	ErrRetryableStatus = ewrap.New("retryable status")
)

// StatusError reports a non-successful response status.
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}
