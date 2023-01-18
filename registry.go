package again

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"
)

// ITemporaryError is an interface for temporary errors.
type ITemporaryError interface {
	error
}

// type ITemporaryError error

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
	defaults := map[string]func() ITemporaryError{
		"os.SyscallError": func() ITemporaryError {
			return &os.SyscallError{}
		},
		"context.DeadlineExceededError": func() ITemporaryError {
			return context.DeadlineExceeded
		},
		"http.ErrHandlerTimeout": func() ITemporaryError {
			return http.ErrHandlerTimeout
		},
		"http.ErrServerClosed": func() ITemporaryError {
			return http.ErrServerClosed
		},
		"net.ErrClosed": func() ITemporaryError {
			return net.ErrClosed
		},
		"net.ErrWriteToConnected": func() ITemporaryError {
			return net.ErrWriteToConnected
		},
	}

	// Register default temporary errors.
	r.RegisterTemporaryErrors(defaults)
	return r
}

// RegisterTemporaryError registers a temporary error.
func (r *registry) RegisterTemporaryError(name string, fn func() ITemporaryError) {
	r.storage.Store(name, fn())
}

// RegisterTemporaryErrors registers multiple temporary errors.
func (r *registry) RegisterTemporaryErrors(temporaryErrors map[string]func() ITemporaryError) {
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
func (r *registry) UnRegisterTemporaryErrors(temporaryErrors map[string]func() ITemporaryError) {
	for name := range temporaryErrors {
		r.storage.Delete(name)
	}
}

// GetTemporaryError returns a temporary error by name.
func (r *registry) GetTemporaryError(name string) (ITemporaryError, bool) {
	tempErr, ok := r.storage.Load(name)

	return tempErr.(ITemporaryError), ok
}

// GetTemporaryErrors returns a list of temporary errors filtered by name.
func (r *registry) GetTemporaryErrors(names ...string) []ITemporaryError {
	var errors []ITemporaryError

	for _, name := range names {
		tempErr, ok := r.storage.Load(name)
		if !ok {
			continue
		}
		errors = append(errors, tempErr.(ITemporaryError))
	}

	return errors
}

// ListTemporaryErrors returns a list of temporary errors.
func (r *registry) ListTemporaryErrors() []ITemporaryError {
	var errors []ITemporaryError

	r.storage.Range(func(key, value any) bool {
		errors = append(errors, value.(ITemporaryError))
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
