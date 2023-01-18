package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hyp3rd/go-again"
)

func main() {
	// Create a context with a timeout of 5 seconds. Adjust this to see the difference.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a retrier with a maximum of 3 retries, jitter of 0.5, and timeout of 1 second.
	retrier := again.NewRetrier(3, time.Second, 500*time.Millisecond, 1*time.Second)

	// Define the function to retry.
	fn := func() error {
		fmt.Println("Trying...")
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
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
