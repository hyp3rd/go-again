package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hyp3rd/go-again"
)

const (
	timeout        = 5 * time.Second
	retrierTimeout = 3 * time.Second
	jitter         = 500 * time.Millisecond
	maxRetries     = 3
)

func main() {
	// Create a context with a timeout of 5 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create a retrier with a maximum of 3 retries, jitter of 0.5, and timeout of 3 second.
	retrier, err := again.NewRetrier(context.Background(), again.WithTimeout(retrierTimeout),
		again.WithJitter(jitter),
		again.WithMaxRetries(maxRetries))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating retrier:", err)

		return
	}

	// Define the function to retry.
	fn := func() error {
		fmt.Fprintln(os.Stdout, "Trying...")

		go func() {
			time.Sleep(1 * time.Second)
			retrier.Cancel()
		}()
		// Simulate a failure.
		return again.ErrOperationFailed
	}

	// Retry the function.
	errs := retrier.Do(ctx, fn)
	if errs.Last != nil {
		fmt.Fprintln(os.Stderr, errs)
	} else {
		fmt.Fprintln(os.Stdout, "success")
	}
}
