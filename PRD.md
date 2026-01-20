# PRD: go-again Retrier Hardening

## Context

`go-again` retries a function with exponential backoff, jitter, and timeout. It provides a registry for temporary errors, cancellation support, and error aggregation. The goal is to fix correctness issues and align documentation with current APIs without changing the public surface.

## Goals

- Ensure `Retrier.Do` is safe for concurrent calls.
- Return early on non-temporary errors when a temporary-error list is supplied.
- Avoid panics when `Retrier` is constructed without `NewRetrier`.
- Make timeout errors detectable via `errors.Is`.
- Align README examples and API description with the code.

## Non-Goals

- Redesign backoff or introduce new retry strategies.
- Add new public APIs beyond documentation updates.

## Current Feature Inventory

- Configurable retry policy: max retries, backoff factor, interval, jitter, timeout.
- Temporary error registry with defaults and custom registration.
- Cancellation via caller context and `Retrier.Cancel`/`Retrier.Stop`.
- Error aggregation via `Errors` (attempt trace + `Join`).
- `DoWithResult` helper, hooks, and optional logging.

## Gaps / Bugs

- Shared mutable error field introduces data races under concurrent `Do` calls.
- Non-temporary errors can still append `ErrMaxRetriesReached`.
- `Retrier` built without `NewRetrier` can panic due to nil timer/context/registry.
- Timeout errors are not wrapped with `ErrTimeoutReached`, so `errors.Is` fails.
- README examples and `Errors` struct definition are out of date.

## Requirements

### Functional

- `Do` uses per-call error state only (no shared fields).
- Non-temporary errors return immediately when a temporary list is provided.
- `Do` initializes missing internals (timer pool, registry, context, pool) safely.
- Timeout path wraps `ErrTimeoutReached` as the cause.
- When `temporaryErrors` is empty, use the registry if it has entries; if the registry is empty, retry all errors.
- `MaxRetries` counts retries after the first attempt (total attempts = `MaxRetries + 1`).
- README reflects actual `Errors` fields and registry usage.

### Non-Functional

- Preserve current public API and default behavior.
- Maintain performance characteristics (no per-attempt allocations beyond today).

## Proposed Changes

- Remove reliance on `Retrier.err`; use local variables per call.
- Add internal initialization to avoid nil timer/context/registry/pool.
- Wrap timeouts with `ErrTimeoutReached` and include attempt + last error in message.
- Return early on non-temporary errors when a list is provided.
- Default to registry-based filtering when `temporaryErrors` is omitted (fallback to retry-all if registry is empty).
- Update `MaxRetries` semantics to mean retries after the first attempt and document it.
- Update README snippets and API notes to match current code.

## Acceptance Criteria

- `Do` is safe for concurrent calls (race-free under `go test -race`).
- Non-temporary errors stop retries when a list is provided (no max-retries marker).
- Manually constructed `Retrier` does not panic in `Do`.
- Timeout errors satisfy `errors.Is(err, ErrTimeoutReached)`.
- Registry defaults are applied when no temporary list is provided (while retry-all still works when the registry is empty).
- `MaxRetries` yields `MaxRetries + 1` total attempts.
- README examples compile against the current API.

## Test Plan

- Add a test verifying non-temporary error returns early (no max-retries marker).
- Add a test ensuring manually constructed `Retrier` does not panic in `Do`.
- Assert `errors.Is(err, ErrTimeoutReached)` is true on timeouts.
- Add a test verifying registry defaults are used when the temporary list is empty.
- Add a test verifying `MaxRetries` yields `MaxRetries + 1` total attempts.
- Run `go test ./...`.

## Risks

- Consumers matching error strings may need updates due to wrapping changes.
- Retrier validation rules can cause tests to fail if timeout/interval/max-retry combinations are invalid.
- Services relying on retry-all behavior may need to keep the registry empty when omitting the temporary list.

## Decisions

- `temporaryErrors` defaults to the registry when omitted; retry-all behavior remains when the registry is empty.
- `MaxRetries` counts retries after the first attempt.
- Long-running calls should be differentiated by documenting/encouraging context-aware retryable functions.

## Dependencies

- Go toolchain (module `go.mod`), `github.com/hyp3rd/ewrap`.

## Rollout

- Cut a patch release after tests pass.
- Add brief release notes covering retry correctness and documentation fixes.
