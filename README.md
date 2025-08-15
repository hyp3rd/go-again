# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]
[![Codacy Security Scan](https://github.com/hyp3rd/go-again/actions/workflows/codacy.yml/badge.svg)][codacy-security-scan-link]

`go-again` **thread safely** wraps a given function and executes it until it returns a nil error or exceeds the maximum number of retries.
The configuration consists of the maximum number of retries, the interval, a jitter to add a randomized backoff, the timeout, and a registry to store errors that you consider temporary, hence worth a retry.

The `Do` method takes a context, a function, and an optional list of `temporary errors` as arguments. It supports cancellation from the context and a channel invoking the `Cancel()` function.
The returned type is `Errors` which contains the list of errors returned at each attempt and the last error returned by the function.

```golang
// Errors holds the error returned by the retry function along with the trace of each attempt.
type Errors struct {
    // Retries hold the trace of each attempt.
    Retries map[int]error
    // Last holds the last error returned by the retry function.
    Last error
}
```

The registry only allows you to retry a function if it returns a registered error:

```go
    // Init with defaults.
    retrier, err := again.NewRetrier()
    if err != nil {
        // handle error
    }
    retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
        return http.ErrAbortHandler
    })

    defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")

    var retryCount int

    errs := retrier.Do(context.TODO(), func() error {
        retryCount++
        if retryCount < 3 {
            return http.ErrAbortHandler
        }

        return nil
    }, "http.ErrAbortHandler")

    if errs.Last != nil {
        // handle error
    }
```

Should you retry regardless of the error returned, that's easy. It's enough calling the Do function without passing a plausible set of registered error names:

```go
    var retryCount int

    retrier, err := again.NewRetrier(again.WithTimeout(1*time.Second),
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
goarch: amd64
pkg: github.com/hyp3rd/go-again
cpu: Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz
BenchmarkRetry-16         490851          8926 ns/op        5376 B/op          1 allocs/op
PASS
ok      github.com/hyp3rd/go-again  40.390s
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
    "fmt"
    "time"

    "github.com/hyp3rd/go-again"
)

func main() {
    // Create a new retrier.
    retrier, err := again.NewRetrier(again.WithTimeout(1*time.Second),
        again.WithJitter(500*time.Millisecond),
        again.WithMaxRetries(3))

    if err != nil {
        // handle error
    }

    // Register a temporary error.
    retrier.Registry.RegisterTemporaryError("temporary error", func() again.TemporaryError {
        return fmt.Errorf("temporary error")
    })

    // Retry a function.
    errs := retrier.Do(context.TODO(), func() error {
        // Do something here.
        return fmt.Errorf("temporary error")
    }, "temporary error")
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
[codeql-link]:https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml
[codacy-security-scan-link]:https://github.com/hyp3rd/go-again/actions/workflows/codacy.yml
