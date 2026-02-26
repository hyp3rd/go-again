# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]

`go-again` provides:

- `again`: a thread-safe retry helper with exponential backoff, jitter, timeout, cancellation, hooks, and a temporary-error registry.
- `pkg/scheduler`: a lightweight in-memory HTTP scheduler that reuses the retrier for retryable requests and optional callbacks.

## Status

As of February 26, 2026, the core retrier hardening work and the scheduler extension described in `PRD.md` are implemented and covered by tests, including race checks.

## Features

### Retrier (`github.com/hyp3rd/go-again`)

- Configurable `MaxRetries`, `Interval`, `Jitter`, `BackoffFactor`, and `Timeout`
- `Do` and `DoWithContext` retry APIs
- Temporary error filtering via explicit error list and/or `Registry`
- Retry-all behavior when no temporary errors are supplied and the registry is empty
- Cancellation via caller context and `Retrier.Cancel()` / `Retrier.Stop()`
- `Errors` trace (`Attempts`, `Last`) plus `Errors.Join()`
- `DoWithResult[T]` helper
- Optional `slog` logger and retry hooks

### Scheduler (`github.com/hyp3rd/go-again/pkg/scheduler`)

- Interval scheduling with `StartAt`, `EndAt`, and `MaxRuns`
- HTTP request execution (`GET`, `POST`, `PUT`)
- Retry integration via `RetryPolicy`
- Optional callback with bounded response-body capture
- URL validation by default (via `sectools`) with override/disable support
- Custom HTTP client, logger, and concurrency limit

## Installation

```bash
go get github.com/hyp3rd/go-again
```

Requires Go `1.26+` (see `go.mod`).

## Retrier Quick Start

```go
package main

import (
 "context"
 "errors"
 "fmt"
 "net/http"
 "time"

 again "github.com/hyp3rd/go-again"
)

func main() {
 retrier, err := again.NewRetrier(
  context.Background(),
  again.WithMaxRetries(3),                 // retries after the first attempt
  again.WithInterval(100*time.Millisecond),
  again.WithJitter(50*time.Millisecond),
  again.WithTimeout(2*time.Second),
 )
 if err != nil {
  panic(err)
 }

 retrier.Registry.LoadDefaults()
 retrier.Registry.RegisterTemporaryError(http.ErrAbortHandler)

 var attempts int
 errs := retrier.Do(context.Background(), func() error {
  attempts++
  if attempts < 3 {
   return http.ErrAbortHandler
  }

  return nil
 })
 defer retrier.PutErrors(errs)

 if errs.Last != nil {
  fmt.Println("failed:", errs.Last)
  return
 }

 fmt.Println("success after attempts:", attempts)
 _ = errors.Join(errs.Attempts...) // equivalent to errs.Join()
}
```

### Retrier Notes

- `MaxRetries` counts retries after the first attempt (`total attempts = MaxRetries + 1`).
- If `temporaryErrors` is omitted and `Registry` has entries, the registry is used as the retry filter.
- If `temporaryErrors` is omitted and the registry is empty, all errors are retried until success/timeout/cancel/max-retries.
- `Do` checks cancellation between attempts. For long-running work, use `DoWithContext`.
- `Cancel()` and `Stop()` cancel the retrier's internal lifecycle context; they are terminal for that retrier instance.

## Context-Aware Retrying

Use `DoWithContext` when the operation itself accepts a context and should stop promptly on cancellation:

```go
// assuming `retrier` was created as in the previous example
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

errs := retrier.DoWithContext(ctx, func(ctx context.Context) error {
 select {
 case <-time.After(250 * time.Millisecond):
  return nil
 case <-ctx.Done():
  return ctx.Err()
 }
})
defer retrier.PutErrors(errs)
```

The retryable function should observe `ctx.Done()`; if it ignores context cancellation, the work may continue running after the retrier returns.

## Scheduler Quick Start

The scheduler runs jobs immediately when scheduled (or at `StartAt` if set), then continues every `Schedule.Every` until `MaxRuns`, `EndAt`, removal, or `Stop()`.

Request and callback URLs are validated by default using `sectools` (HTTPS only, no userinfo, and no private/localhost hosts unless configured otherwise).

```go
package main

import (
 "context"
 "net/http"
 "time"

 again "github.com/hyp3rd/go-again"
 "github.com/hyp3rd/go-again/pkg/scheduler"
)

func main() {
 retrier, _ := again.NewRetrier(
  context.Background(),
  again.WithMaxRetries(5),
  again.WithInterval(10*time.Millisecond),
  again.WithJitter(10*time.Millisecond),
  again.WithTimeout(5*time.Second),
 )

 s := scheduler.NewScheduler(
  scheduler.WithConcurrency(8),
 )
 defer s.Stop()

 _, _ = s.Schedule(scheduler.Job{
  Schedule: scheduler.Schedule{
   Every:   1 * time.Minute,
   MaxRuns: 1,
  },
  Request: scheduler.Request{
   Method: http.MethodPost,
   URL:    "https://example.com/endpoint",
   Body:   []byte(`{"ping":"pong"}`),
  },
  Callback: scheduler.Callback{
   URL: "https://example.com/callback",
  },
  RetryPolicy: scheduler.RetryPolicy{
   Retrier:          retrier,
   RetryStatusCodes: []int{http.StatusTooManyRequests, http.StatusInternalServerError},
  },
 })
}
```

### Scheduler Options

- `WithHTTPClient(client)` sets the HTTP client used for requests and callbacks.
- `WithLogger(logger)` sets the scheduler logger.
- `WithConcurrency(n)` limits concurrent executions when `n > 0`.
- `WithURLValidator(validator)` overrides URL validation. Pass `nil` to disable validation.

### Scheduler Behavior Notes

- Supported methods for requests and callbacks: `GET`, `POST`, `PUT`.
- Callbacks are skipped when `Callback.URL` is empty.
- Callback method defaults to `POST`.
- `Callback.MaxBodyBytes` defaults to `4096`.
- `Request.Timeout` and `Callback.Timeout` apply per HTTP request/callback (not the schedule lifetime).
- If `RetryPolicy.Retrier` is nil, the scheduler creates a default retrier and loads registry defaults.
- Calling `Schedule` after `Stop()` returns `scheduler.ErrSchedulerStopped`.

### Custom URL Validation (Allow Local HTTPS)

```go
validator, _ := validate.NewURLValidator(
 validate.WithURLAllowPrivateIP(true),
 validate.WithURLAllowLocalhost(true),
 validate.WithURLAllowIPLiteral(true),
)

s := scheduler.NewScheduler(
 scheduler.WithURLValidator(validator),
)
```

### Disable URL Validation (Allow Non-HTTPS)

```go
s := scheduler.NewScheduler(
 scheduler.WithURLValidator(nil),
)
```

## Examples

Run the example programs directly:

```bash
go run ./__examples/chan
go run ./__examples/context
go run ./__examples/timeout
go run ./__examples/validate
```

## Development Commands

```bash
make test
make test-race
make lint
make sec
```

Benchmark (direct Go command):

```bash
go test -bench=. -benchtime=3s -benchmem -run=^$ -memprofile=mem.out ./...
```

## Known Limitations / Gaps

- `Scheduler.Stop()` cancels the scheduler lifecycle; the same instance is not intended to be reused afterward.
- `Retrier.Cancel()` / `Retrier.Stop()` are terminal for the retrier instance.
- `DoWithContext` can only stop work promptly if the retryable function respects the provided context.

## Performance

`go-again` adds retry orchestration overhead but is designed to keep allocations low. See the benchmark in `tests/retrier_test.go` and run the benchmark command above in your environment for current numbers.

## Documentation

- API docs: <https://pkg.go.dev/github.com/hyp3rd/go-again>
- Product/status notes: `PRD.md`

## License

The code and documentation in this project are released under Mozilla Public License 2.0.

## Author

I'm a surfer, a crypto trader, and a software architect with 15 years of experience designing highly available distributed production environments and developing cloud-native apps in public and private clouds. Feel free to hook me up on [LinkedIn](https://www.linkedin.com/in/francesco-cosentino/).

[build-link]: https://github.com/hyp3rd/go-again/actions/workflows/go.yml
[codeql-link]: https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml
