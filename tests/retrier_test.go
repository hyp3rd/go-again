package tests

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyp3rd/ewrap"

	"github.com/hyp3rd/go-again"
)

var errTemporary = ewrap.New("temporary error")

const (
	errRetryCountMismatch = "retry did not retry the function the expected number of times. Got: %d, Expecting: %d"
	errNonTemporary       = "non-temporary error"
	defaultBackoffFactor  = 3
	defaultRetryCount     = 3
	defaultMaxRetries     = 5
	defaultInterval       = 500 * time.Millisecond
)

func TestNewRetrier(t *testing.T) {
	t.Parallel()

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Errorf("failed to create new Retrier: %v", err)
	}

	// Test default values.
	if retrier.MaxRetries != defaultMaxRetries {
		t.Errorf("unexpected value for MaxRetries: got %d, want %d", retrier.MaxRetries, defaultMaxRetries)
	}

	if retrier.Jitter != 1*time.Second {
		t.Errorf("unexpected value for Jitter: got %v, want %v", retrier.Jitter, 1*time.Second)
	}

	if retrier.BackoffFactor != 2 {
		t.Errorf("unexpected value for BackoffFactor: got %v, want %v", retrier.BackoffFactor, 2)
	}

	if retrier.Interval != defaultInterval {
		t.Errorf("unexpected value for Interval: got %v, want %v", retrier.Interval, defaultInterval)
	}

	if retrier.Timeout != 20*time.Second {
		t.Errorf("unexpected value for Timeout: got %v, want %v", retrier.Timeout, 20*time.Second)
	}

	if retrier.Registry == nil {
		t.Error("unexpected value for Registry: got nil, want not nil")
	}

	// Test setting options.
	retrier, err = again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(10),
		again.WithJitter(2*time.Second),
		again.WithBackoffFactor(defaultBackoffFactor),
		again.WithInterval(1*time.Second),
		again.WithTimeout(30*time.Second),
	)
	if err != nil {
		t.Errorf("failed to create new Retrier: %v", err)
	}

	if retrier.MaxRetries != 10 {
		t.Errorf("unexpected value for MaxRetries: got %d, want %d", retrier.MaxRetries, 10)
	}

	if retrier.Jitter != 2*time.Second {
		t.Errorf("unexpected value for Jitter: got %v, want %v", retrier.Jitter, 2*time.Second)
	}

	if retrier.BackoffFactor != defaultBackoffFactor {
		t.Errorf("unexpected value for BackoffFactor: got %v, want %v", retrier.BackoffFactor, defaultBackoffFactor)
	}

	if retrier.Interval != 1*time.Second {
		t.Errorf("unexpected value for Interval: got %v, want %v", retrier.Interval, 1*time.Second)
	}

	if retrier.Timeout != 30*time.Second {
		t.Errorf("unexpected value for Timeout: got %v, want %v", retrier.Timeout, 30*time.Second)
	}

	// Test invalid options.
	_, err = again.NewRetrier(context.Background(),
		again.WithMaxRetries(-1),
	)
	if err == nil {
		t.Error("expected error for invalid MaxRetries option, got nil")
	}
}

func TestRetrier_Validate(t *testing.T) {
	t.Parallel()

	// Test valid Retrier.
	retrier := &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      defaultInterval,
		Timeout:       20 * time.Second,
	}

	err := retrier.Validate()
	if err != nil {
		t.Errorf("failed to validate Retrier: %v", err)
	}

	// Test zero MaxRetries is valid.
	retrier = &again.Retrier{
		MaxRetries:    0,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      defaultInterval,
		Timeout:       20 * time.Second,
	}

	err = retrier.Validate()
	if err != nil {
		t.Errorf("failed to validate Retrier with zero MaxRetries: %v", err)
	}

	// Test invalid MaxRetries.
	retrier = &again.Retrier{
		MaxRetries:    -1,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      1 * time.Second,
		Timeout:       10 * time.Second,
	}

	err = retrier.Validate()
	if err == nil || !errors.Is(err, again.ErrInvalidRetrier) || !strings.Contains(err.Error(), "invalid max retries") {
		t.Errorf("expected invalid max retries error, got %v", err)
	}

	// Test invalid BackoffFactor.
	retrier = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 1,
		Interval:      1 * time.Second,
		Timeout:       10 * time.Second,
	}

	err = retrier.Validate()
	if err == nil || !errors.Is(err, again.ErrInvalidRetrier) || !strings.Contains(err.Error(), "backoff factor") {
		t.Errorf("expected invalid backoff factor error, got %v", err)
	}

	// Test invalid Interval and Timeout.
	retrier = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      10 * time.Second,
		Timeout:       1 * time.Second,
	}

	err = retrier.Validate()
	if err == nil || !errors.Is(err, again.ErrInvalidRetrier) || !strings.Contains(err.Error(), "interval") {
		t.Errorf("expected invalid interval error, got %v", err)
	}

	// Test invalid Interval * MaxRetries.
	retrier = &again.Retrier{
		MaxRetries:    5,
		Jitter:        1 * time.Second,
		BackoffFactor: 2,
		Interval:      1 * time.Second,
		Timeout:       5 * time.Second,
	}

	err = retrier.Validate()
	if err == nil || !errors.Is(err, again.ErrInvalidRetrier) || !strings.Contains(err.Error(), "multiplied by max retries") {
		t.Errorf("expected invalid interval*max retries error, got %v", err)
	}
}

// TestDo tests the Do function.
func TestDo(t *testing.T) {
	t.Parallel()

	// Test successful execution.
	retryableFunc := func() error {
		return nil
	}

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	errs := retrier.Do(context.Background(), retryableFunc)
	if errs.Last != nil {
		t.Errorf("unexpected error: %v", errs.Last)
	}

	// Test non-temporary error.
	retryableFunc = func() error {
		return ewrap.New(errNonTemporary)
	}

	rNonTemp, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(0),
		again.WithInterval(10*time.Millisecond),
		again.WithTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	errs = rNonTemp.Do(context.Background(), retryableFunc)
	if errs.Last == nil || errs.Last.Error() != errNonTemporary {
		t.Errorf("expected error %q, but got %v", errNonTemporary, errs.Last)
	}

	// Test temporary error.
	err = retrier.SetRegistry(again.NewRegistry())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	customError := errTemporary
	retrier.Registry.RegisterTemporaryError(customError)

	retryableFunc = func() error {
		return customError
	}

	errs = retrier.Do(context.Background(), retryableFunc, customError)
	if errs.Last == nil || errs.Last.Error() != "temporary error" {
		t.Errorf("unexpected error: %v", errs.Last)
	}

	// Test max retries.
	tempErr := errTemporary
	retryableFunc = func() error {
		return tempErr
	}

	retrier, err = again.NewRetrier(context.Background(), again.WithMaxRetries(1))
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	errs = retrier.Do(context.Background(), retryableFunc, tempErr)
	failure := errs.Attempts[len(errs.Attempts)-1]

	expected := again.ErrMaxRetriesReached.Error()

	if errs.Last == nil || !strings.Contains(failure.Error(), expected) {
		t.Errorf("expected error %q, but got %v", expected, failure)
	}
}

func TestDo_NonTemporaryStopsEarly(t *testing.T) {
	t.Parallel()

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	var attempts int

	nonTempErr := ewrap.New(errNonTemporary)
	tempErr := errTemporary

	errs := retrier.Do(context.Background(), func() error {
		attempts++

		return nonTempErr
	}, tempErr)

	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}

	if !errors.Is(errs.Last, nonTempErr) {
		t.Errorf("expected last error to be %v, got %v", nonTempErr, errs.Last)
	}

	if len(errs.Attempts) != 1 {
		t.Errorf("expected 1 attempt error, got %d", len(errs.Attempts))
	}

	if strings.Contains(errs.Attempts[0].Error(), again.ErrMaxRetriesReached.Error()) {
		t.Error("did not expect max retries error to be recorded")
	}
}

func TestDo_MaxRetriesCountsRetries(t *testing.T) {
	t.Parallel()

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(0),
		again.WithInterval(10*time.Millisecond),
		again.WithTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	var attempts int

	tempErr := errTemporary

	errs := retrier.Do(context.Background(), func() error {
		attempts++

		return tempErr
	}, tempErr)

	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}

	if !errors.Is(errs.Last, tempErr) {
		t.Errorf("expected last error to match %v, got %v", tempErr, errs.Last)
	}
}

func TestDoWithContext_Cancel(t *testing.T) {
	t.Parallel()

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(1),
		again.WithInterval(10*time.Millisecond),
		again.WithTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errs := retrier.DoWithContext(ctx, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()

		return ctx.Err()
	})

	if errs.Last == nil || !errors.Is(errs.Last, context.Canceled) {
		t.Errorf("expected last error to match %v, got %v", context.Canceled, errs.Last)
	}
}

// Test stop retries.
func TestStopRetries(t *testing.T) {
	t.Parallel()

	// Create a new Retrier with a small interval to speed up the test.
	retrier, err := again.NewRetrier(context.Background(), again.WithInterval(10*time.Millisecond), again.WithTimeout(5*time.Second))
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
		retrier.Cancel()

		return ewrap.New("error")
	}

	// Call the retry function.
	errs := retrier.Do(ctx, retryableFunc)

	defer cancel()

	// Check if the retry was stopped.
	if !errors.Is(errs.Last, again.ErrOperationStopped) {
		t.Errorf("expected error %q, but got %v", again.ErrOperationStopped, errs.Last)
	}

	// Check if the retryable function was called only once.
	if count != 1 {
		t.Errorf("expected retryable function to be called once, but got %d calls", count)
	}
}

// TestWithoutRegistry tests the retry function without a registry.
func TestWithoutRegistry(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			return http.ErrAbortHandler
		}

		return nil
	})

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}

	if retryCount != defaultRetryCount {
		t.Errorf(errRetryCountMismatch, retryCount, defaultRetryCount)
	}
}

// TestRetryWithDefaults tests the retry function with the default registry.
func TestRetryWithDefaults(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry.LoadDefaults()

	defer retrier.Registry.Clean()

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			return http.ErrHandlerTimeout
		}

		return nil
	}, http.ErrHandlerTimeout)

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}

	if retryCount != 3 {
		t.Errorf(errRetryCountMismatch, retryCount, 3)
	}
}

func TestRetryWithDefaultsWithoutList(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry.LoadDefaults()

	defer retrier.Registry.Clean()

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			return http.ErrHandlerTimeout
		}

		return nil
	})

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}

	if retryCount != defaultRetryCount {
		t.Errorf(errRetryCountMismatch, retryCount, defaultRetryCount)
	}
}

func TestRetryTimeout(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithTimeout(1*time.Second),
		again.WithInterval(100*time.Millisecond),
		again.WithMaxRetries(3),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry.RegisterTemporaryError(http.ErrAbortHandler)

	defer retrier.Registry.UnRegisterTemporaryError(http.ErrAbortHandler)

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			time.Sleep(2 * time.Second)

			return http.ErrAbortHandler
		}

		return nil
	}, http.ErrAbortHandler)

	if errs.Last == nil {
		t.Error("was expecting a timeout error")
	}

	if !errors.Is(errs.Last, again.ErrTimeoutReached) {
		t.Errorf("expected error to match %v, got %v", again.ErrTimeoutReached, errs.Last)
	}
}

func TestDo_ManualRetrierInitialization(t *testing.T) {
	t.Parallel()

	retrier := &again.Retrier{
		MaxRetries:    2,
		Jitter:        1 * time.Millisecond,
		BackoffFactor: 2,
		Interval:      1 * time.Millisecond,
		Timeout:       10 * time.Millisecond,
	}

	tempErr := errTemporary
	errs := retrier.Do(context.Background(), func() error {
		return again.ErrOperationFailed
	}, tempErr)

	if errs.Last == nil {
		t.Fatal("expected last error, got nil")
	}

	if !errors.Is(errs.Last, again.ErrOperationFailed) {
		t.Errorf("expected last error to match %v, got %v", again.ErrOperationFailed, errs.Last)
	}

	if retrier.Registry == nil {
		t.Error("expected registry to be initialized")
	}
}

func TestRetryWithContextCancel(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithTimeout(10*time.Second),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry.RegisterTemporaryError(http.ErrAbortHandler)

	defer retrier.Registry.UnRegisterTemporaryError(http.ErrAbortHandler)

	ctx, cancel := context.WithCancel(context.Background())
	errs := retrier.Do(ctx, func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			time.Sleep(2 * time.Second)
			cancel()

			return http.ErrAbortHandler
		}

		return nil
	}, http.ErrAbortHandler)

	if errs.Last == nil || !errors.Is(errs.Last, context.Canceled) {
		t.Errorf("was expecting a %v error", context.Canceled)
	}

	if retryCount != 1 {
		t.Errorf(errRetryCountMismatch, retryCount, 1)
	}
}

func TestRetryWithChannelCancel(t *testing.T) {
	t.Parallel()

	var retryCount int

	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithTimeout(10*time.Second),
	)
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry.RegisterTemporaryError(http.ErrAbortHandler)

	defer retrier.Registry.UnRegisterTemporaryError(http.ErrAbortHandler)

	errs := retrier.Do(context.Background(), func() error {
		retryCount++
		if retryCount < defaultRetryCount {
			time.Sleep(2 * time.Second)
			retrier.Cancel()

			return http.ErrAbortHandler
		}

		return nil
	}, http.ErrAbortHandler)

	if errs.Last == nil || !errors.Is(errs.Last, again.ErrOperationStopped) {
		t.Errorf("was expecting a %v error", again.ErrOperationStopped)
	}

	if retryCount != 1 {
		t.Errorf(errRetryCountMismatch, retryCount, 1)
	}
}

func BenchmarkRetry(b *testing.B) {
	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		b.Fatalf(errFailedToCreateRetrier, err)
	}

	tempErr := errTemporary
	retrier.Registry.RegisterTemporaryError(tempErr)

	var retryCount int

	fn := func() error {
		retryCount++
		if retryCount < 5 {
			return tempErr
		}

		return nil
	}

	retryCount = 5

	for b.Loop() {
		retrier.Do(context.TODO(), fn, tempErr)
	}
}

func BenchmarkRetryWithRetries(b *testing.B) {
	retrier, err := again.NewRetrier(
		context.Background(),
		again.WithMaxRetries(5),
		again.WithInterval(1*time.Microsecond),
		again.WithJitter(1*time.Microsecond),
		again.WithTimeout(50*time.Millisecond),
	)
	if err != nil {
		b.Fatalf(errFailedToCreateRetrier, err)
	}

	tempErr := errTemporary
	retrier.Registry.RegisterTemporaryError(tempErr)

	var retryCount int

	fn := func() error {
		retryCount++
		if retryCount < 5 {
			return tempErr
		}

		return nil
	}

	for b.Loop() {
		retryCount = 0

		retrier.Do(context.TODO(), fn, tempErr)
	}
}
