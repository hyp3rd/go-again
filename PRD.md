# PRD: go-again Current State Audit, Documentation Alignment, and Gap Backlog

## Audit Date

February 26, 2026

## Purpose

This document replaces the earlier change-focused PRD ("Retrier Hardening + Scheduler Extension") with a status-aligned PRD for the current repository state.

It captures:

- what is implemented today,
- how the implementation compares to the original PRD goals/acceptance criteria,
- what documentation was updated,
- what gaps, missing features, and flaws remain.

## Scope Reviewed

### Core packages

- `retrier.go`
- `registry.go`
- `timer.go`
- `options.go`
- `errors.go`
- `pkg/scheduler/*.go`

### Tests

- `tests/retrier_test.go`
- `tests/scheduler_test.go`
- `tests/registry_test.go`
- `tests/timer_test.go`

### Docs / repo UX

- `README.md`
- `Makefile`
- `.pre-commit-config.yaml`
- `.pre-commit-ci-config.yaml`

## Validation Snapshot

Executed during this audit:

- `go test ./...` -> pass
- `go test -race ./...` -> pass
- `staticcheck ./...` -> pass
- `golangci-lint run` -> pass (`0 issues`)

## Executive Summary

- The original retrier hardening goals are implemented and tested.
- The scheduler extension is implemented and tested.
- The README previously contained several repo/runtime mismatches (example runner commands, outdated wording, missing lifecycle limitations); those docs are now aligned.
- Remaining work is mostly operational polish and lifecycle/introspection gaps, not core retry correctness.

## Follow-Up Update (Implemented)

After this audit, a follow-up implementation pass completed the highest-priority cleanup and DX items:

- Scheduler now removes completed jobs from internal state and avoids deleting newly scheduled replacements by matching the stored `jobEntry` pointer.
- `Schedule()` now returns `ErrSchedulerStopped` after `Scheduler.Stop()` is called.
- `Makefile` `run` now executes `__examples/*` (`make run example=...`) and `bench` now uses a valid `go test` benchmark command.
- Added tests for retrier hooks, scheduler concurrency limiting, scheduler post-stop scheduling guard, and completed-job cleanup behavior.

This closes gap `#1`, closes gap `#5`, and partially addresses gap `#2` and gap `#7` below.

## Status vs Original PRD

### Retrier Hardening Goals (Original PRD)

- `Do` concurrent safety: implemented
      - Error state is per-call (`Errors` object from pool), not shared mutable retrier state.
      - Verified by `go test -race ./...`.
- Early return on non-temporary errors: implemented
      - Stops without appending `ErrMaxRetriesReached`.
      - Covered by `TestDo_NonTemporaryStopsEarly`.
- Manual `Retrier` initialization safety (no panic without `NewRetrier`): implemented
      - Internal lazy initialization via `ensureInitialized()`.
      - Covered by `TestDo_ManualRetrierInitialization`.
- Timeout detectability via `errors.Is(..., ErrTimeoutReached)`: implemented
      - Covered by `TestRetryTimeout`.
- README/API alignment: partially complete before this audit, now updated in this pass.
- Context-aware retry method for long-running operations: implemented (`DoWithContext`)
      - Covered by `TestDoWithContext_Cancel`.

### Scheduler Extension Goals (Original PRD)

- Interval scheduling with `StartAt`, `EndAt`, `MaxRuns`: implemented
      - Covered by `TestSchedulerEndAtInPastStopsImmediately`, `TestSchedulerMaxRunsStops`.
- Optional callback with bounded response body: implemented
      - Covered by `TestSchedulerRetryAndCallback`, `TestSchedulerCallbackBodyLimit`.
- Retrier integration for retryable statuses/errors: implemented
      - Covered by `TestSchedulerRetryAndCallback`, `TestSchedulerNonRetryableStatus`.
- URL validation on by default, override/disable support: implemented
      - Covered by `TestSchedulerURLValidationRejectsHTTP`, `TestSchedulerURLValidationDisabledAllowsHTTP`.
- Custom HTTP client / concurrency / logging support: implemented in API
      - `WithHTTPClient`, `WithConcurrency`, `WithLogger`, `WithURLValidator`.
      - Logging/concurrency behavior exists but is not deeply covered by tests.

## Current Feature Inventory

### Retrier

- Configurable retry policy (`MaxRetries`, `Interval`, `Jitter`, `BackoffFactor`, `Timeout`)
- Temporary error registry (`Registry`) with defaults via `LoadDefaults()`
- Retry filtering from explicit temporary error list or registry defaults
- Retry-all fallback when no temporary list is supplied and the registry is empty
- `Do(ctx, func() error, temporaryErrors...)`
- `DoWithContext(ctx, func(context.Context) error, temporaryErrors...)`
- `DoWithResult[T]`
- Cancellation via caller context and retrier lifecycle (`Cancel`, `Stop`)
- Error trace aggregation via `Errors{Attempts, Last}` and `Errors.Join()`
- Optional logging (`slog`) and retry hooks (`Hooks.OnRetry`)

### Scheduler

- In-memory scheduler with per-job goroutine lifecycle
- Request execution with supported methods: `GET`, `POST`, `PUT`
- Request retrying through `again.Retrier`
- Retry-by-status (`RetryStatusCodes`) and retry-by-error (`TemporaryErrors`)
- Optional callback payload with execution metadata and bounded response body
- URL validation by default via `sectools` (customizable/disable-able)
- Custom HTTP client (`WithHTTPClient`)
- Optional concurrency limit (`WithConcurrency`)
- Logging (`WithLogger`)
- Job removal (`Remove`) and scheduler shutdown (`Stop`)

## Gaps, Missing Features, and Flaws (Prioritized)

### 1. Completed scheduler jobs are not automatically removed from the internal job map

- Impact:
      - Long-running processes that schedule many one-shot jobs can accumulate stale entries.
      - `Remove(id)` can return `true` for jobs that already finished, which is surprising.
- Evidence:
      - `Schedule()` stores jobs in `s.jobs`.
      - `runJob()` exits on completion but does not delete the entry.
      - `Stop()` clears the map, but normal completion does not.
- Recommendation:
      - Add completion cleanup (`defer` removal) with care around concurrent `Remove()` calls.
      - Optionally expose job status and retain completed jobs only when explicitly configured.

### 2. Lifecycle terminal semantics are easy to misuse and were undocumented

- `Retrier.Cancel()` / `Retrier.Stop()` cancel the retrier's internal lifecycle context permanently.
- `Scheduler.Stop()` cancels the scheduler lifecycle; reusing the same instance after `Stop()` is not a supported pattern.
- Impact:
        - Reusing instances after cancel/stop can produce confusing behavior (immediate cancellation, jobs never running).
      - Recommendation:
        - Keep docs explicit (done in README).
        - Consider defensive guards (for example, returning an error from `Schedule()` after `Stop()`).

### 3. `DoWithContext` depends on cooperative cancellation and can leave work running if the callback ignores context

- Implementation detail:
      - `DoWithContext` runs the retryable function in a goroutine and returns on timeout/cancel.
      - If user code ignores `ctx.Done()`, the goroutine may continue running until the function exits.
- Impact:
      - Potential goroutine/work leakage in user applications.
- Recommendation:
      - Keep this clearly documented (done in README).
      - Optionally add stronger docs/examples/tests around cooperative cancellation patterns.

### 4. Scheduler default URL validator initialization failure silently disables validation

- In `NewScheduler()`, if `validate.NewURLValidator()` returns an error, the scheduler proceeds with `urlValidator == nil`.
- Impact:
      - Security expectations ("validation on by default") can be violated silently in rare initialization-failure scenarios.
- Recommendation:
      - Prefer fail-fast or explicit logging when default validator creation fails.
      - Alternative: add `NewSchedulerWithError(...) (*Scheduler, error)` constructor.

### 5. Repo developer UX drift: Makefile targets do not match current repository contents/docs

- `README.md` previously instructed `make run example=...`, but `Makefile` does not implement that behavior.
- `Makefile` `run` target references `./cmd/app`, which is not present in this repo.
- `Makefile` `bench` target uses a malformed `-run` flag sequence (`-run=^-memprofile=...`).
- Impact:
      - New contributors lose time on broken commands.
- Recommendation:
      - Add a real example runner target, or remove/rename scaffold `run`.
      - Fix `bench` target command to `-run=^$ -memprofile=mem.out`.

### 6. Missing scheduler introspection/status APIs

- Current API supports `Schedule`, `Remove`, `Stop`, but not:
      - list jobs,
      - query job state,
      - inspect last run result,
      - metrics hooks.
- Impact:
      - Usable for embedded/simple scheduling, but limited for production observability.
- Recommendation:
      - Add optional read-only introspection APIs without breaking the current simple API.

### 7. Test coverage gaps (non-blocking, but worth addressing)

- No explicit tests for:
      - scheduler concurrency limiting/backpressure behavior (`WithConcurrency`)
      - scheduler logger behavior (`WithLogger`)
      - retrier hooks (`Hooks.OnRetry`)
      - behavior of `Schedule()` after `Scheduler.Stop()`
      - automatic cleanup of completed jobs (feature currently missing)
- Recommendation:
      - Add focused tests before changing scheduler lifecycle semantics.

## Missing Features (Deliberately Out of Scope Today)

These remain non-goals unless product scope changes:

- Persistent schedules / durable storage
- Distributed scheduler coordination
- Cron expressions
- Non-HTTP protocols (including gRPC scheduling targets)

## Documentation Alignment Completed in This Audit

### README updates

- Corrected feature descriptions and API wording
- Added explicit retrier and scheduler behavior notes
- Documented lifecycle limitations and cooperative cancellation requirements
- Replaced broken example commands with working `go run ./__examples/...` commands
- Added current dev/test/lint command references
- Added known limitations section

### PRD updates

- Converted from change-request format to current-state audit + backlog
- Added implementation status vs original PRD
- Added prioritized gap list and follow-up recommendations

## Follow-Up PRD (Next Increment)

### Goals

- Fix scheduler completed-job cleanup and lifecycle clarity
- Improve developer command UX in `Makefile`
- Harden URL validator initialization behavior
- Add missing tests for concurrency/hooks/lifecycle edge cases

### Functional Requirements

- Scheduler automatically removes completed jobs from `s.jobs` (or exposes retention mode explicitly)
- `Schedule()` returns a clear error if called after `Stop()`
- Makefile provides a working example runner target (or removes invalid `run`)
- `bench` target runs with a correct `go test` benchmark command
- Scheduler constructor surfaces URL validator initialization failures (error or warning)

### Non-Functional Requirements

- Preserve existing public retry/scheduler behavior where possible
- Avoid introducing data races or significant allocations
- Keep default scheduler usage simple for current users

### Acceptance Criteria

- No stale job map growth after one-shot jobs complete (verified by test)
- `Schedule()` after `Stop()` has deterministic documented behavior (verified by test)
- `make` example/bench commands documented in README and validated locally
- `go test ./...` and `go test -race ./...` remain green
- `golangci-lint run` and `staticcheck ./...` remain green

## Risks

- Changing scheduler cleanup semantics may affect callers implicitly relying on `Remove()` succeeding after completion.
- Exposing constructor errors or adding stop-state guards can be behavior changes for existing consumers.
- Makefile fixes may affect users depending on the current scaffold-style targets.

## Rollout

- Land doc alignment first (this change).
- Implement follow-up behavior changes in a separate PR with targeted tests.
- Include release notes for scheduler lifecycle/cleanup changes if behavior changes are introduced.
