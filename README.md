# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]
[![Codacy Security Scan](https://github.com/hyp3rd/go-again/actions/workflows/codacy.yml/badge.svg)][codacy-security-scan-link]

`go-again` **thread safely** wraps a given function and executes it until it returns a nil error or exceeds the maximum number of retries.
The configuration consists of the maximum number of retries, the interval, a jitter to add a randomized backoff, the timeout, and a registry to store errors that you consider temporary, hence worth a retry.
The `Retry` method takes a context, a function, and an optional list of `temporary errors` as arguments. It supports cancellation from the context and a channel invoking the `Cancel()` function.
The registry only allows you to retry a function if it returns a registered error:

```go
    retrier := again.NewRetrier(3, time.Millisecond*10, time.Second, time.Second)
    retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() TemporaryError {
        return http.ErrAbortHandler
    })

    defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")
    var retryCount int
    err := retrier.Retry(context.TODO(), func() error {
        retryCount++
        if retryCount < 3 {
            return http.ErrAbortHandler
        }
        return nil
    }, "http.ErrAbortHandler")

    if err != nil {
        // handle error
    }
```

Should you retry regardless of the error returned, that's easy. It's enough calling the Retry function without passing a plausible set of registered error names:

```go
    var retryCount int
    retrier := again.NewRetrier(3, time.Millisecond*10, time.Second, time.Second)
    err := retrier.Retry(context.TODO(), func() error {
        retryCount++
        if retryCount < 3 {
            return http.ErrAbortHandler
        }
        return nil
    })
    if err != nil {
        // handle error
    }
```

It's also possible to create a Registry with the [**temporary default errors**](./registry.go?plain=1#L26):
`retrier.Registry.LoadDefaults()`.
You can extend the list with your errors by calling the `RegisterTemporaryError` method.

`go-again` is helpful in cases where you want to retry a function if it returns a temporary error, for example, when connecting to a database or a network service.

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
    retrier := again.NewRetrier(5, time.Millisecond*10, time.Second, time.Second)

    // Register a temporary error.
    retrier.Registry.RegisterTemporaryError("temporary error", func() again.TemporaryError {
        return fmt.Errorf("temporary error")
    })

    // Retry a function.
    err := retrier.Retry(context.TODO(), func() error {
        // Do something here.
        return fmt.Errorf("temporary error")
    }, "temporary error")
    if err != nil {
        fmt.Println(err)
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
