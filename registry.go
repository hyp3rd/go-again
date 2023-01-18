package again

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"
)

// TemporaryError implements the error interface.
type TemporaryError error

// registry for temporary errors.
type registry struct {
	storage sync.Map // store for temporary errors
}

// NewRegistry creates a new registry.
func NewRegistry() *registry {
	return &registry{
		storage: sync.Map{},
	}
}

// LoadDefaults loads the default temporary errors.
func (r *registry) LoadDefaults() *registry {
	// Register default temporary errors.
	defaults := map[string]func() TemporaryError{
		"os.SyscallError": func() TemporaryError {
			return &os.SyscallError{}
		},
		"context.DeadlineExceededError": func() TemporaryError {
			return context.DeadlineExceeded
		},
		"http.ErrHandlerTimeout": func() TemporaryError {
			return http.ErrHandlerTimeout
		},
		"http.ErrServerClosed": func() TemporaryError {
			return http.ErrServerClosed
		},
		"net.ErrClosed": func() TemporaryError {
			return net.ErrClosed
		},
		"net.ErrWriteToConnected": func() TemporaryError {
			return net.ErrWriteToConnected
		},
	}

	// Register default temporary errors.
	r.RegisterTemporaryErrors(defaults)
	return r
}

// RegisterTemporaryError registers a temporary error.
func (r *registry) RegisterTemporaryError(name string, fn func() TemporaryError) {
	r.storage.Store(name, fn())
}

// RegisterTemporaryErrors registers multiple temporary errors.
func (r *registry) RegisterTemporaryErrors(temporaryErrors map[string]func() TemporaryError) {
	for name, fn := range temporaryErrors {
		r.storage.Store(name, fn())
	}
}

// UnRegisterTemporaryError unregisters a temporary error(s).
func (r *registry) UnRegisterTemporaryError(names ...string) {
	for _, name := range names {
		r.storage.Delete(name)
	}
}

// UnRegisterTemporaryErrors unregisters multiple temporary errors.
func (r *registry) UnRegisterTemporaryErrors(temporaryErrors map[string]func() TemporaryError) {
	for name := range temporaryErrors {
		r.storage.Delete(name)
	}
}

// GetTemporaryError returns a temporary error by name.
func (r *registry) GetTemporaryError(name string) (TemporaryError, bool) {
	tempErr, ok := r.storage.Load(name)

	return tempErr.(TemporaryError), ok
}

// GetTemporaryErrors returns a list of temporary errors filtered by name.
func (r *registry) GetTemporaryErrors(names ...string) []TemporaryError {
	var errors []TemporaryError

	for _, name := range names {
		tempErr, ok := r.storage.Load(name)
		if !ok {
			continue
		}
		errors = append(errors, tempErr.(TemporaryError))
	}

	return errors
}

// ListTemporaryErrors returns a list of temporary errors.
func (r *registry) ListTemporaryErrors() []TemporaryError {
	var errors []TemporaryError

	r.storage.Range(func(key, value any) bool {
		errors = append(errors, value.(TemporaryError))
		return true
	})

	return errors
}

// Clean cleans the registry.
func (r *registry) Clean() {
	r.storage.Range(func(key, value any) bool {
		r.storage.Delete(key)
		return true
	})
}
