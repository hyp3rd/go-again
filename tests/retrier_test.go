package tests

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hyp3rd/go-again"
)

// TestRetry tests the retry function.
func TestRetry(t *testing.T) {
	var retryCount int
	retrier, _ := again.NewRetrier()
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
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

	if errs.Last == nil || !errors.Is(errs.Last, again.ErrOperationCanceled) {
		t.Errorf("was expecting a %v error", again.ErrOperationCanceled)
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
