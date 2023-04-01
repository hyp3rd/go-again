package main

import (
	"fmt"
	"time"

	"github.com/hyp3rd/go-again"
)

func main() {
	_, err := again.NewRetrier(
		again.WithMaxRetries(3),
		again.WithTimeout(1*time.Second), // change this to 5*time.Second to see the difference
	)

	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("success")
	}

	_, err = again.NewRetrier(
		again.WithMaxRetries(3),
		again.WithJitter(4*time.Second),
		again.WithTimeout(5*time.Second), // change this to 5*time.Second to see the difference
	)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("success")
	}
}
