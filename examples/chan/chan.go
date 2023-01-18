package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hyp3rd/go-again"
)

func main() {
	// Create a context with a timeout of 5 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a retrier with a maximum of 3 retries, jitter of 0.5, and timeout of 1 second.
	retrier := again.NewRetrier(again.WithTimeout(1*time.Second),
		again.WithJitter(500*time.Millisecond),
		again.WithMaxRetries(3))

	// Define the function to retry.
	fn := func() error {
		fmt.Println("Trying...")
		go func() {
			time.Sleep(1 * time.Second)
			retrier.Cancel()
		}()
		// Simulate a failure.
		return fmt.Errorf("failed")
	}

	// Retry the function.
	err := retrier.Retry(ctx, fn)
	if err != nil {
		fmt.Println(err)
	}
}
