package scheduler

import (
	"errors"
	"testing"

	"github.com/hyp3rd/sectools/pkg/validate"
)

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewSchedulerWithError_DefaultURLValidatorFailure(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ErrInvalidJob
	})
	defer restore()

	sched, err := NewSchedulerWithError()
	if sched == nil {
		t.Fatal("expected scheduler instance, got nil")
	}

	if err == nil || !errors.Is(err, ErrURLValidatorInitialization) {
		t.Fatalf("expected URL validator initialization error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level validator factory test seam.
func TestNewSchedulerWithError_ExplicitNilValidatorSkipsDefaultInitialization(t *testing.T) {
	restore := swapDefaultURLValidatorFactoryForTest(func() (*validate.URLValidator, error) {
		return nil, ErrInvalidJob
	})
	defer restore()

	sched, err := NewSchedulerWithError(WithURLValidator(nil))
	if err != nil {
		t.Fatalf("expected nil error when validator is explicitly disabled, got %v", err)
	}

	if sched == nil {
		t.Fatal("expected scheduler instance, got nil")
	}

	if sched.urlValidator != nil {
		t.Fatal("expected URL validator to be nil when explicitly disabled")
	}
}

func swapDefaultURLValidatorFactoryForTest(factory func() (*validate.URLValidator, error)) func() {
	previous := newDefaultURLValidator
	newDefaultURLValidator = factory

	return func() {
		newDefaultURLValidator = previous
	}
}
