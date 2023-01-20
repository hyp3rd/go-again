package again

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestRetry tests the retry function.
func TestRetry(t *testing.T) {
	var retryCount int
	retrier, _ := NewRetrier()
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if errs.Last != nil {
		t.Errorf("retry returned an unexpected error: %v", errs.Last)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 3)
	}
}

// TestWithoutRegistry tests the retry function without a registry.
func TestWithoutRegistry(t *testing.T) {
	var retryCount int
	retrier, _ := NewRetrier()

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
	retrier, _ := NewRetrier()
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
	retrier, _ := NewRetrier(
		WithTimeout(1 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
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
	retrier, _ := NewRetrier(
		WithTimeout(10 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
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
	retrier, _ := NewRetrier(
		WithTimeout(10 * time.Second),
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
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

	if errs.Last == nil || !errors.Is(errs.Last, ErrOperationCanceled) {
		t.Errorf("was expecting a %v error", ErrOperationCanceled)
	}
	if retryCount != 1 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 1)
	}
}

// TestRegistry tests the registry.
func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
		return http.ErrAbortHandler
	})

	defer r.UnRegisterTemporaryError("http.ErrAbortHandler")

	retrier, _ := NewRetrier()
	retrier.Registry = r

	if retrier.IsTemporaryError(http.ErrAbortHandler, "http.ErrAbortHandler") != true {
		t.Errorf("registry failed to register a temporary error")
	}

	if retrier.IsTemporaryError(http.ErrSkipAltProtocol, "http.ErrHandlerTimeout") != false {
		t.Errorf("registry failed to validate temporary error")
	}

	if retrier.IsTemporaryError(http.ErrAbortHandler, "http.ErrHandlerTimeout") != false {
		t.Errorf("registry failed to validate temporary error")
	}

	r.Clean()
	errs := r.ListTemporaryErrors()
	if len(errs) != 0 {
		t.Errorf("registry failed to clean temporary errors")
	}
}

func BenchmarkRetry(b *testing.B) {
	r, _ := NewRetrier()
	r.Registry.RegisterTemporaryError("temporary error", func() TemporaryError {
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
