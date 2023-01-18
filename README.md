# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]

`go-again` thrad-safely wraps a given function and executes it until it returns a nil error or exceeds the maximum number of retries.
The configuration consists of the maximum number of retries, a jitter to add a randomized backoff, the timeout, and a registry to store errors that you consider temporary, hence worth a retry.
The `Retry` method takes a function and an optional list of `temporary errors` as arguments.
The registry only allows you to retry a function if it returns a registered error:

```go
    retrier := NewRetrier(3, time.Second*5, 15*time.Second)
    retrier.Registry.RegisterTemporaryError("http.ErrAbortHandler", func() ITemporaryError {
        return http.ErrAbortHandler
    })

    defer retrier.Registry.UnRegisterTemporaryError("http.ErrAbortHandler")
    var retryCount int
    err := retrier.Retry(func() error {
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

You should retry regardless of the error returned, and that's easy. It's enough calling the Retry function without passing a plausible set of registered error names:

```go
    var retryCount int
    retrier := NewRetrier(3, time.Second*5, 15*time.Second)
    err := retrier.Retry(func() error {
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
Bear in mind that the list can be extended with your own errors calling the `RegisterTemporaryError` method.

`go-again` is helpful in cases where you want to retry a function if it returns a temporary error, for example, when connecting to a database or a network service.

## Installation

```bash
go get github.com/hyp3rd/go-again
```

## Usage

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
    retrier := again.NewRetrier(5, time.Millisecond*10, time.Second)

    // Register a temporary error.
    retrier.Registry.RegisterTemporaryError("temporary error", func() again.ITemporaryError {
        return fmt.Errorf("temporary error")
    })

    // Retry a function.
    err := retrier.Retry(func() error {
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
