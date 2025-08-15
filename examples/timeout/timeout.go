package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hyp3rd/go-again"
)

const (
	maxRetires     = 3
	retrierTimeout = 2 * time.Second
	fallAsleepFor  = time.Second
)

func main() {
	var retryCount int

	retrier, err := again.NewRetrier(
		again.WithMaxRetries(maxRetires),
		again.WithTimeout(retrierTimeout), // change this to 5*time.Second to see the difference
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create retrier: %v\n", err)

		return
	}

	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < maxRetires {
			time.Sleep(fallAsleepFor)

			return http.ErrAbortHandler
		}

		return nil
	}, "http.ErrAbortHandler")

	if errs.Last != nil {
		fmt.Fprintf(os.Stderr, "retry returned an unexpected error: %v\n", errs.Last)

		return
	}

	fmt.Fprintln(os.Stdout, "success")
}
