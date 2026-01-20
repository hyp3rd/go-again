# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]

`go-again` **thread safely** wraps a given function and executes it until it returns a nil error or exceeds the maximum number of retries.
The configuration consists of the maximum number of retries (after the first attempt), the interval, a jitter to add a randomized backoff, the timeout, and a registry to store errors that you consider temporary, hence worth a retry.

The `Do` method takes a context, a function, and an optional list of `temporary errors` as arguments. If the list is omitted and the registry has entries, the registry is used as the default filter; if the registry is empty, all errors are retried. It supports cancellation from the context and a channel invoking the `Cancel()` function; long-running operations should observe the context inside the retryable function.
The returned type is `Errors` which contains the list of errors returned at each attempt and the last error returned by the function.

```golang
// Errors holds the error returned by the retry function along with the trace of each attempt.
type Errors struct {
    // Attempts holds the trace of each attempt in order.
    Attempts []error
    // Last holds the last error returned by the retry function.
    Last error
}
```

When you pass a list of temporary errors to `Do`, retries only happen when the error matches that list. The registry is a convenience store for temporary errors you want to pass to `Do`, or to use as the default filter when the list is omitted:

```go
    // Init with defaults.
    retrier, err := again.NewRetrier(context.Background())
    if err != nil {
        // handle error
    }

    retrier.Registry.RegisterTemporaryError(http.ErrAbortHandler)

    defer retrier.Registry.UnRegisterTemporaryError(http.ErrAbortHandler)

    var retryCount int

    errs := retrier.Do(context.TODO(), func() error {
        retryCount++
        if retryCount < 3 {
            return http.ErrAbortHandler
        }

        return nil
    }, http.ErrAbortHandler)

    if errs.Last != nil {
        // handle error
    }
```

Should you retry regardless of the error returned, call `Do` without passing any temporary errors and keep the registry empty:

```go
    var retryCount int

    retrier, err := again.NewRetrier(context.Background(), again.WithTimeout(1*time.Second),
        again.WithJitter(500*time.Millisecond),
        again.WithMaxRetries(3))

    if err != nil {
        // handle error
    }

    errs := retrier.Do(context.TODO(), func() error {
        retryCount++
        if retryCount < 3 {
            return http.ErrAbortHandler
        }

        return nil
    })
    if errs.Last != nil {
        // handle error
    }
```

It's also possible to create a Registry with the [**temporary default errors**](./registry.go?plain=1#L26):
`retrier.Registry.LoadDefaults()`.
You can extend the list with your errors by calling the `RegisterTemporaryError` method.

**Walk through the [documentation](https://pkg.go.dev/github.com/hyp3rd/go-again@v1.0.8#section-documentation) for further details about the settings, the programmability, the implementation.**

## Performance

A retrier certainly adds overhead to the execution of a function. `go-again` is designed to produce a minimal impact on the performance of your code, keeping thread safety and flexibility. The following benchmark shows the overhead of a retrier with 5 retries, 1s interval, 10ms jitter, and 1s timeout:

```bash
go test -bench=. -benchmem -benchtime=4s . -timeout 30m
goos: darwin
goarch: arm64
pkg: github.com/hyp3rd/go-again/tests
cpu: Apple M2 Pro
BenchmarkRetry-12       337406      13686 ns/op     5378 B/op       1 allocs/op
PASS
ok   github.com/hyp3rd/go-again/tests 40.587s
```

## Installation

```bash
go get github.com/hyp3rd/go-again
```

## Usage

For examples with cancellation, see [**examples**](./examples). To run the examples you can leverage the `Makefile`:

```bash
make run example=chan
make run example=context
```

### Retrier

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/hyp3rd/go-again"
)

func main() {
    // Create a new retrier.
    retrier, err := again.NewRetrier(context.Background(), again.WithTimeout(1*time.Second),
        again.WithJitter(500*time.Millisecond),
        again.WithMaxRetries(3))

    if err != nil {
        // handle error
    }

    // Register a temporary error.
    tempErr := errors.New("temporary error")
    retrier.Registry.RegisterTemporaryError(tempErr)

    // Retry a function.
    errs := retrier.Do(context.TODO(), func() error {
        // Do something here.
        return tempErr
    }, tempErr)
    if errs.Last != nil {
        // handle error
    }
}
```

## License

The code and documentation in this project are released under Mozilla Public License 2.0.

## Author

I'm a surfer, a crypto trader, and a software architect with 15 years of experience designing highly available distributed production environments and developing cloud-native apps in public and private clouds. Feel free to hook me up on [LinkedIn](https://www.linkedin.com/in/francesco-cosentino/).

[build-link]: https://github.com/hyp3rd/go-again/actions/workflows/go.yml
[codeql-link]: https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml
[codacy-security-scan-link]:
