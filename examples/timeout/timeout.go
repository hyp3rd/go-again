package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hyp3rd/go-again"
)

func main() {
	var retryCount int
	retrier, _ := again.NewRetrier(
		again.WithMaxRetries(3),
		again.WithTimeout(2*time.Second), // change this to 5*time.Second to see the difference
	)
	retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

	errs := retrier.Do(context.TODO(), func() error {
		retryCount++
		if retryCount < 3 {
			time.Sleep(1 * time.Second)
			return http.ErrAbortHandler
		}
		return nil
	}, "http.ErrAbortHandler")

	if errs.Last != nil {
		fmt.Printf("retry returned an unexpected error: %v\n", errs.Last)
	} else {
		fmt.Println("success")
	}
}
