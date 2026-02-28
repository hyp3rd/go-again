# go-again

[![Go](https://github.com/hyp3rd/go-again/actions/workflows/go.yml/badge.svg)][build-link] [![CodeQL](https://github.com/hyp3rd/go-again/actions/workflows/codeql.yml/badge.svg)][codeql-link]

`go-again` provides:

- `again`: a thread-safe retry helper with exponential backoff, jitter, timeout, cancellation, hooks, and a temporary-error registry.
- `pkg/scheduler`: a lightweight HTTP scheduler with pluggable state storage (in-memory by default, SQLite built-in) that reuses the retrier for retryable requests and optional callbacks.

## Status

As of February 27, 2026, the core retrier hardening work and the scheduler extension described in `PRD.md` are implemented and covered by tests, including race checks.

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
- Custom HTTP client, logger, concurrency limit, and scheduler-state storage backend

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

## Scheduler Examples

### Example: Schedule Once + Callback

Runnable version:

```bash
go run ./__examples/scheduler
```

Source:
[`__examples/scheduler/scheduler.go`](__examples/scheduler/scheduler.go)

```go
s := scheduler.NewScheduler(
 scheduler.WithHTTPClient(server.Client()),
 scheduler.WithURLValidator(nil), // allow local endpoints for example usage
)
defer s.Stop()

jobID, err := s.Schedule(scheduler.Job{
 Schedule: scheduler.Schedule{Every: 10 * time.Millisecond, MaxRuns: 1},
 Request: scheduler.Request{Method: http.MethodGet, URL: server.URL + "/target"},
 Callback: scheduler.Callback{URL: server.URL + "/callback"},
})
if err != nil {
 panic(err)
}

payload := <-callbackCh
fmt.Println("job:", jobID, "success:", payload.Success, "status:", payload.StatusCode)
```

### Example: Query Status and History

```go
// after Schedule(...)
status, ok := s.JobStatus(jobID)
if ok {
 fmt.Println("state:", status.State, "runs:", status.Runs, "active:", status.ActiveRuns)
}

history, ok := s.JobHistory(jobID)
if ok {
 for _, run := range history {
  fmt.Println("run#", run.Sequence, "status:", run.Payload.StatusCode, "success:", run.Payload.Success)
 }
}

filtered := s.QueryJobStatuses(scheduler.JobStatusQuery{
 States: []scheduler.JobState{scheduler.JobStateRunning, scheduler.JobStateScheduled},
 Offset: 0,
 Limit:  50,
})
fmt.Println("filtered statuses:", len(filtered))

recentRuns, ok := s.QueryJobHistory(jobID, scheduler.JobHistoryQuery{
 FromSequence: 10,
 Limit:        5,
})
if ok {
 fmt.Println("recent retained runs:", len(recentRuns))
}
```

### Example: Durable Scheduler State with SQLite

Runnable version:

```bash
go run ./__examples/scheduler_sqlite
```

Source:
[`__examples/scheduler_sqlite/scheduler_sqlite.go`](__examples/scheduler_sqlite/scheduler_sqlite.go)

```go
dbPath := filepath.Join(os.TempDir(), "go-again-scheduler-example.db")
storage, err := scheduler.NewSQLiteJobsStorage(dbPath)
if err != nil {
 panic(err)
}
defer storage.Close()

s := scheduler.NewScheduler(
 scheduler.WithJobsStorage(storage),
 scheduler.WithURLValidator(nil),
)
defer s.Stop()

jobID, err := s.Schedule(scheduler.Job{
 Schedule: scheduler.Schedule{Every: 20 * time.Millisecond, MaxRuns: 1},
 Request: scheduler.Request{Method: http.MethodGet, URL: target.URL},
})
if err != nil {
 panic(err)
}
fmt.Println("scheduled job:", jobID)
```

### Example: Fail-Closed Scheduler Construction

Use `NewSchedulerWithError(...)` when constructor-time URL validator initialization errors must fail startup.

```go
s, err := scheduler.NewSchedulerWithError(
 scheduler.WithConcurrency(8),
)
if err != nil {
 // fail startup instead of warning + degraded mode
 return err
}
defer s.Stop()
```

### Scheduler Options

- `WithHTTPClient(client)` sets the HTTP client used for requests and callbacks.
- `WithLogger(logger)` sets the scheduler logger.
- `WithConcurrency(n)` limits concurrent executions when `n > 0`.
- `WithJobsStorage(storage)` sets pluggable scheduler state storage (active jobs plus status/history; default: in-memory).
- `WithHistoryLimit(limit)` sets retained per-job history length (default `20`).
- `WithURLValidator(validator)` overrides URL validation. Pass `nil` to disable validation.
- `NewSchedulerWithError(...)` returns constructor errors (including startup state reconciliation failures and default URL validator initialization failure).

### Scheduler Behavior Notes

- Supported methods for requests and callbacks: `GET`, `POST`, `PUT`.
- Callbacks are skipped when `Callback.URL` is empty.
- Callback method defaults to `POST`.
- `Callback.MaxBodyBytes` defaults to `4096`.
- `Request.Timeout` and `Callback.Timeout` apply per HTTP request/callback (not the schedule lifetime).
- If `RetryPolicy.Retrier` is nil, the scheduler creates a default retrier and loads registry defaults.
- Calling `Schedule` after `Stop()` returns `scheduler.ErrSchedulerStopped`.
- `Schedule` returns `scheduler.ErrStorageOperation` when required scheduler-state persistence fails.
- `NewSchedulerWithError(...)` should be preferred for fail-closed startup behavior in security-sensitive paths.
- `JobCount()` and `JobIDs()` provide lightweight read-only scheduler introspection.
- `JobStatus(id)`, `JobStatuses()`, and `JobHistory(id)` provide status and retained run history snapshots.
- `QueryJobStatuses(JobStatusQuery)` adds ID/state filters with pagination (`Offset`, `Limit`) over status snapshots.
- `QueryJobHistory(id, JobHistoryQuery)` adds history filtering (`FromSequence`) and tail limiting (`Limit`) while preserving ascending sequence order.
- Default `InMemoryJobsStorage` is process-local; use `WithJobsStorage(...)` for custom durable/backed storage.
- `NewSQLiteJobsStorage(path)` provides a built-in durable storage implementation for `WithJobsStorage(...)`; call `Close()` when finished.
- On scheduler startup, recovered active-job registrations from storage are reconciled: `scheduled`/`running` states are marked `canceled`, then active-job IDs are cleared. Jobs are not auto-resumed.
- Non-fatal storage write failures during runtime transitions are logged (warn) and execution continues.
- Non-fatal request/callback response body read/close failures are logged (warn) and execution continues.
- `NewSchedulerWithError(...)` fails constructor-time reconciliation errors; `NewScheduler()` logs a warning and continues.
- `NewScheduler()` logs a warning and continues if default URL validator initialization fails; use `NewSchedulerWithError()` to fail closed.

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
go run ./__examples/scheduler
go run ./__examples/scheduler_sqlite
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
- `NewScheduler()` (non-error constructor) intentionally degrades to warning-only behavior if default URL validator initialization fails; use `NewSchedulerWithError()` when you need constructor-time failure.

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
