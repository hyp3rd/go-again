package tests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hyp3rd/go-again"
)

func TestNewRetrier(t *testing.T) {
	r, err := again.NewRetrier()
	if err != nil {
		t.Errorf("failed to create new Retrier: %v", err)
	}

	// Test default values.
	if r.MaxRetries != 5 {
		t.Errorf("unexpected value for MaxRetries: got %d, want %d", r.MaxRetries, 5)
	}
	if r.Jitter != 1*time.Second {
		t.Errorf("unexpected value for Jitter: got %v, want %v", r.Jitter, 1*time.Second)
	}
	if r.BackoffFactor != 2 {
		t.Errorf("unexpected value for BackoffFactor: got %v, want %v", r.BackoffFactor, 2)
	}
	if r.Interval != 500*time.Millisecond {
		t.Errorf("unexpected value for Interval: got %v, want %v", r.Interval, 500*time.Millisecond)
	}
	if r.Timeout != 20*time.Second {
		t.Errorf("unexpected value for Timeout: got %v, want %v", r.Timeout, 20*time.Second)
	}
	if r.Registry == nil {
		t.Errorf("unexpected value for Registry: got nil, want not nil")
	}

	// Test setting options.
	r, err = again.NewRetrier(
		again.WithMaxRetries(10),
		again.WithJitter(2*time.Second),
		again.WithBackoffFactor(3),
		again.WithInterval(1*time.Second),
		again.WithTimeout(30*time.Second),
	)
	if err != nil {
		t.Errorf("failed to create new Retrier: %v", err)
	}
	if r.MaxRetries != 10 {
		t.Errorf("unexpected value for MaxRetries: got %d, want %d", r.MaxRetries, 10)
	}
	if r.Jitter != 2*time.Second {
		t.Errorf("unexpected value for Jitter: got %v, want %v", r.Jitter, 2*time.Second)
	}
	if r.BackoffFactor != 3 {
		t.Errorf("unexpected value for BackoffFactor: got %v, want %v", r.BackoffFactor, 3)
	}
	if r.Interval != 1*time.Second {
		t.Errorf("unexpected value for Interval: got %v, want %v", r.Interval, 1*time.Second)
	}
	if r.Timeout != 30*time.Second {
		t.Errorf("unexpected value for Timeout: got %v, want %v", r.Timeout, 30*time.Second)
	}

	// Test invalid options.
	_, err = again.NewRetrier(
		again.WithMaxRetries(-1),
	)
	if err == nil {
		t.Errorf("expected error for invalid MaxRetries option, got nil")
	}
}

func TestRetrier_Validate(t *testing.T) {
	// Test valid Retrier.
	r := &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      500 * time.Millisecond,
		Timeout:       20 * time.Second,
	}
	if err := r.Validate(); err != nil {
		t.Errorf("failed to validate Retrier: %v", err)
	}

	// Test invalid MaxRetries.
	r = &again.Retrier{
		MaxRetries:    -1,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      1 * time.Second,
		Timeout:       10 * time.Second,
	}
	err := r.Validate()
	if err == nil || err.Error() != fmt.Sprintf("%v: invalid max retries: %d, the value should be greater than zero", again.ErrInvalidRetrier, r.MaxRetries) {
		t.Errorf("expected error %q, but got %v", fmt.Sprintf("%v: invalid max retries: %d, the value should be greater than zero", again.ErrInvalidRetrier, r.MaxRetries), err)
	}

	// Test invalid BackoffFactor.
	r = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 1,
		Interval:      1 * time.Second,
		Timeout:       10 * time.Second,
	}
	err = r.Validate()
	if err == nil || err.Error() != fmt.Sprintf("%v: invalid backoff factor: %f, the value should be greater than 1", again.ErrInvalidRetrier, r.BackoffFactor) {
		t.Errorf("expected error %q, but got %v", fmt.Sprintf("%v: invalid backoff factor: %f, the value should be greater than 1", again.ErrInvalidRetrier, r.BackoffFactor), err)
	}

	// Test invalid Interval and Timeout.
	r = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      10 * time.Second,
		Timeout:       1 * time.Second,
	}
	err = r.Validate()
	if err == nil || err.Error() != fmt.Sprintf("%v: the interval %s should be less than timeout %s", again.ErrInvalidRetrier, r.Interval, r.Timeout) {
		t.Errorf("expected error %q, but got %v", fmt.Sprintf("%v: the interval %s should be less than timeout %s", again.ErrInvalidRetrier, r.Interval, r.Timeout), err)
	}

	// Test invalid Interval * MaxRetries.
	r = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      1 * time.Second,
		Timeout:       5 * time.Second,
	}
	err = r.Validate()
	if err == nil || err.Error() != fmt.Sprintf("%v: the interval %s multiplied by max retries %d should be less than timeout %s", again.ErrInvalidRetrier, r.Interval, r.MaxRetries, r.Timeout) {
		t.Errorf("expected error %q, but got %v", fmt.Sprintf("%v: the interval %s multiplied by max retries %d should be less than timeout %s", again.ErrInvalidRetrier, r.Interval, r.MaxRetries, r.Timeout), err)
	}
}

// TestDo tests the Do function.
func TestDo(t *testing.T) {
	// Test successful execution.
	retryableFunc := func() error {
		return nil
	}
	r, _ := again.NewRetrier()
	errs := r.Do(context.Background(), retryableFunc)
	if errs.Last != nil {
		t.Errorf("unexpected error: %v", errs.Last)
	}

	// Test non-temporary error.
	retryableFunc = func() error {
		return errors.New("non-temporary error")
	}
	errs = r.Do(context.Background(), retryableFunc)
	if errs.Last == nil || errs.Last.Error() != "non-temporary error" {
		t.Errorf("expected error %q, but got %v", "non-temporary error", errs.Last)
	}

	// Test temporary error.
	r.SetRegistry(again.NewRegistry())
	customError := errors.New("temporary error")
	r.Registry.RegisterTemporaryError("temporary error", func() again.TemporaryError { return customError })

	retryableFunc = func() error {
		return errors.New("temporary error")
	}
	errs = r.Do(context.Background(), retryableFunc, "temporary error")
	if errs.Last == nil || errs.Last.Error() != "temporary error" {
		t.Errorf("unexpected error: %v", errs.Last)
	}

	// Test max retries.
	retryableFunc = func() error {
		return errors.New("temporary error")
	}
	r, _ = again.NewRetrier(again.WithMaxRetries(1))
	errs = r.Do(context.Background(), retryableFunc, "temporary error")
	failure := errs.Registry[r.MaxRetries]

	expected := fmt.Sprintf("%v: with error: %v", again.ErrMaxRetriesReached, "temporary error")

	if errs.Last == nil || failure.Error() != expected {
		t.Errorf("expected error %q, but got %v", expected, failure)
	}
}

// func TestTimeout(t *testing.T) {
// 	// Test timeout.
// 	retryableFunc := func() error {
// 		time.Sleep(3 * time.Second)
// 		return errors.New("temporary error")
// 	}
// 	r, _ := again.NewRetrier(again.WithTimeout(1*time.Second), again.WithMaxRetries(3))
// 	errs := r.Do(context.Background(), retryableFunc, "temporary error")
// 	failure := errs.Retries[r.MaxRetries]

// 	expected := fmt.Sprintf("%v with error %v", again.ErrTimeoutReached, "temporary error")

// 	if errs.Last == nil || failure.Error() != expected {
// 		t.Errorf("expected error %q, but got %v", expected, failure)
// 	}
// }

// Test stop retries.
func TestStopRetries(t *testing.T) {
	// Create a new Retrier with a small interval to speed up the test.
	r, err := again.NewRetrier(again.WithInterval(10*time.Millisecond), again.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Use a context with a timeout to cancel the retries after 1500 milliseconds.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)

	// Retryable function that sleeps for 1 second and then returns an error.
	var count int
	retryableFunc := func() error {
		count++
		time.Sleep(100 * time.Millisecond)
		r.Cancel()
		return fmt.Errorf("error")
	}

	// Call the retry function.
	errs := r.Do(ctx, retryableFunc)
	defer cancel()

	// Check if the retry was stopped.
	if errs.Last != again.ErrOperationStopped {
		t.Errorf("expected error %q, but got %v", again.ErrOperationStopped, errs.Last)
	}

	// Check if the retryable function was called only once.
	if count != 1 {
		t.Errorf("expected retryable function to be called once, but got %d calls", count)
	}
}

// TestWithoutRegistry tests the retry function without a registry.
func TestWithoutRegistry(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier()

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrAbortHandler
		}
		return nil
	})

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 1)
	}
}

// TestRetryWithDefaults tests the retry function with the default registry.
func TestRetryWithDefaults(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier()
	retrier.Registry.LoadDefaults()

	defer retrier.Registry.Clean()

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrHandlerTimeout
		}
		return nil
	}, "http.ErrHandlerTimeout")

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 3)
	}
}

func TestRetryTimeout(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier(
		again.WithTimeout(1 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < 3 {
			time.Sleep(2 * time.Second)
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if errs.Last == nil {
		t.Errorf("was expecting a timeout error")
	}
}

func TestRetryWithContextCancel(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier(
		again.WithTimeout(10 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	ctx, cancel := context.WithCancel(context.Background())
	errs := retrier.Do(ctx, func() error {
		retryCount++
		if retryCount < 3 {
			time.Sleep(2 * time.Second)
			cancel()
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if errs.Last == nil || !errors.Is(errs.Last, context.Canceled) {
		t.Errorf("was expecting a %v error", context.Canceled)
	}
	if retryCount != 1 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 1)
	}
}

func TestRetryWithChannelCancel(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier(
		again.WithTimeout(10 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	errs := retrier.Do(context.Background(), func() error {
		retryCount++
		if retryCount < 3 {
			time.Sleep(2 * time.Second)
			retrier.Cancel()
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if errs.Last == nil || !errors.Is(errs.Last, again.ErrOperationStopped) {
		t.Errorf("was expecting a %v error", again.ErrOperationStopped)
	}
	if retryCount != 1 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 1)
	}
}

func BenchmarkRetry(b *testing.B) {
	r, _ := again.NewRetrier()
	r.Registry.RegisterTemporaryError("temporary error", func() again.TemporaryError {
		return errors.New("temporary error")
	})

	var retryCount int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn := func() error {
			retryCount++
			if retryCount < 5 {
				return errors.New("temporary error")
			}
			return nil
		}
		b.StartTimer()
		r.Do(context.TODO(), fn, "temporary error")
		b.StopTimer()
	}
}
