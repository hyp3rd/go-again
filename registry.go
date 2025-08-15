package again

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

// Registry holds a set of temporary errors.
type Registry struct {
	mu      sync.RWMutex
	storage []error
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// LoadDefaults loads a set of default temporary errors.
func (r *Registry) LoadDefaults() *Registry {
	r.RegisterTemporaryErrors(
		context.DeadlineExceeded,
		http.ErrHandlerTimeout,
		http.ErrServerClosed,
		net.ErrClosed,
		net.ErrWriteToConnected,
	)

	return r
}

// RegisterTemporaryError registers a temporary error.
func (r *Registry) RegisterTemporaryError(err error) {
	r.RegisterTemporaryErrors(err)
}

// RegisterTemporaryErrors registers multiple temporary errors.
func (r *Registry) RegisterTemporaryErrors(errs ...error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.storage = append(r.storage, errs...)
}

// UnRegisterTemporaryError removes a temporary error.
func (r *Registry) UnRegisterTemporaryError(err error) {
	r.UnRegisterTemporaryErrors(err)
}

// UnRegisterTemporaryErrors removes multiple temporary errors.
func (r *Registry) UnRegisterTemporaryErrors(errs ...error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, target := range errs {
		for i, e := range r.storage {
			if errors.Is(e, target) {
				r.storage = append(r.storage[:i], r.storage[i+1:]...)
				break
			}
		}
	}
}

// ListTemporaryErrors returns all temporary errors in the registry.
func (r *Registry) ListTemporaryErrors() []error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]error, len(r.storage))
	copy(out, r.storage)

	return out
}

// Len returns the number of registered temporary errors.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.storage)
}

// Clean removes all temporary errors from the registry.
func (r *Registry) Clean() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.storage = nil
}

// IsTemporaryError reports whether err matches any of the temporary errors.
func (r *Registry) IsTemporaryError(err error, errs ...error) bool {
	var tempErrors []error

	if errs == nil {
		tempErrors = r.ListTemporaryErrors()
	} else {
		tempErrors = errs
	}

	for _, te := range tempErrors {
		if errors.Is(err, te) {
			return true
		}
	}

	return false
}
