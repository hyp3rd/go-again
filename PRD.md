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

- Change retry semantics when `temporaryErrors` are omitted.
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
- README reflects actual `Errors` fields and registry usage.

### Non-Functional

- Preserve current public API and default behavior.
- Maintain performance characteristics (no per-attempt allocations beyond today).

## Proposed Changes

- Remove reliance on `Retrier.err`; use local variables per call.
- Add internal initialization to avoid nil timer/context/registry/pool.
- Wrap timeouts with `ErrTimeoutReached` and include attempt + last error in message.
- Return early on non-temporary errors when a list is provided.
- Update README snippets and API notes to match current code.

## Test Plan

- Add a test verifying non-temporary error returns early (no max-retries marker).
- Add a test ensuring manually constructed `Retrier` does not panic in `Do`.
- Assert `errors.Is(err, ErrTimeoutReached)` is true on timeouts.
- Run `go test ./...`.

## Rollout

arly on non-temporary errors when a list is provided.

arly on non-temporary errors when a list is provided.

- Update README snippets and API notes to match current code.
