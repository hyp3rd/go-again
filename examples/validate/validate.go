package main

import (
	"fmt"
	"os"
	"time"

	"github.com/hyp3rd/go-again"
)

const (
	maxRetries = 3
	jitter     = 4 * time.Second
	timeout    = 5 * time.Second
)

func main() {
	_, err := again.NewRetrier(
		again.WithMaxRetries(maxRetries),
		again.WithTimeout(time.Second), // change this to 5*time.Second to see the difference
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		fmt.Fprintln(os.Stdout, "success")
	}

	_, err = again.NewRetrier(
		again.WithMaxRetries(maxRetries),
		again.WithJitter(jitter),
		again.WithTimeout(timeout), // change this to 5*time.Second to see the difference
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return
	}

	fmt.Fprintln(os.Stdout, "success")
}
