package again

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestRetry tests the retry function.
func TestRetry(t *testing.T) {
	var retryCount int
	retrier := NewRetrier(3, time.Second*5, 15*time.Second)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() ITemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	err := retrier.Retry(func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if err != nil {
		t.Errorf("retry returned an unexpected error: %v", err)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 3)
	}
}

// TestWithoutRegistry tests the retry function without a registry.
func TestWithoutRegistry(t *testing.T) {
	var retryCount int
	retrier := NewRetrier(3, time.Second*5, 15*time.Second)

	err := retrier.Retry(func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrAbortHandler
		}
		return nil
	})

	if err != nil {
		t.Errorf("retry returned an unexpected error: %v", err)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 1)
	}
}

// TestRetryWithDefaults tests the retry function with the default registry.
func TestRetryWithDefaults(t *testing.T) {
	var retryCount int
	retrier := NewRetrier(3, time.Second*5, 15*time.Second)
	retrier.Registry.LoadDefaults()

	defer retrier.Registry.Clean()

	err := retrier.Retry(func() error {
		retryCount++
		if retryCount < 3 {
			return http.ErrHandlerTimeout
		}
		return nil
	}, "http.ErrHandlerTimeout")

	if err != nil {
		t.Errorf("retry returned an unexpected error: %v", err)
	}
	if retryCount != 3 {
		t.Errorf("retry did not retry the function the expected number of times. Got: %d, Expecting: %d", retryCount, 3)
	}
}

// TestRegistry tests the registry.
func TestRegistry(t *testing.T) {
	r := &registry{}
	r.RegisterTemporaryError("http.ErrAbortHandler", func() ITemporaryError {
		return http.ErrAbortHandler
	})

	defer r.UnRegisterTemporaryError("http.ErrAbortHandler")

	retrier := NewRetrier(3, time.Second*5, 15*time.Second)
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

// func BenchmarkRetry(b *testing.B) {
// 	r := NewRetrier(50, time.Millisecond*10, time.Second)
// 	r.Registry.RegisterTemporaryError("temporary error", func() ITemporaryError {
// 		return errors.New("temporary error")
// 	})

//		b.ResetTimer()
//		for i := 0; i < b.N; i++ {
//			retryCount := i
//			fn := func() error {
//				retryCount++
//				if retryCount < 50 {
//					time.Sleep(time.Millisecond * 10)
//					return errors.New("temporary error")
//				}
//				return nil
//			}
//			err := r.Retry(fn, "temporary error").(*RetryError)
//			if err != nil || err.MaxRetries != 50 {
//				b.Errorf("retry returned an unexpected error: %v", err)
//			}
//		}
//	}
func BenchmarkRetry(b *testing.B) {
	r := NewRetrier(50, time.Millisecond*10, time.Second)
	r.Registry.RegisterTemporaryError("temporary error", func() ITemporaryError {
		return errors.New("temporary error")
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var retryCount int
		fn := func() error {
			retryCount++
			if retryCount < 3 {
				return errors.New("temporary error")
			}
			return nil
		}
		b.StartTimer()
		r.Retry(fn, "temporary error")
		b.StopTimer()
	}
}
