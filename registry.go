package again

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/go-ldap/ldap/v3"
)

// ITemporaryError is an interface for temporary errors.
type ITemporaryError interface {
	error
}

// registry is a registry for temporary errors.
type registry struct {
	mutex sync.RWMutex               // mutex for the store
	store map[string]ITemporaryError // store for temporary errors
}

// newRegistry returns a new registry.
func newRegistry() *registry {
	return &registry{
		store: make(map[string]ITemporaryError),
	}
}

// LoadDefaults loads the default temporary errors.
func (r *registry) LoadDefaults() *registry {
	// Register default temporary errors.
	defaults := map[string]func() ITemporaryError{
		"ldap.Error": func() ITemporaryError {
			return &ldap.Error{}
		},
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

// RegisterTemporaryError registers a temporary error by name.
func (r *registry) RegisterTemporaryError(name string, fn func() ITemporaryError) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.store[name] = fn()
}

// RegisterTemporaryErrors registers multiple temporary errors by name.
func (r *registry) RegisterTemporaryErrors(temporaryErrors map[string]func() ITemporaryError) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for name, fn := range temporaryErrors {
		r.store[name] = fn()
	}
}

// UnRegisterTemporaryError unregisters a temporary error by name.
func (r *registry) UnRegisterTemporaryError(names ...string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for _, name := range names {
		delete(r.store, name)
	}
}

// RegisterTemporaryErrors registers multiple temporary errors by name.
func (r *registry) UnRegisterTemporaryErrors(temporaryErrors map[string]func() ITemporaryError) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for name := range temporaryErrors {
		delete(r.store, name)
	}
}

// GetTemporaryError returns a temporary error by name.
func (r *registry) GetTemporaryError(name string) (ITemporaryError, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	tempErr, ok := r.store[name]

	return tempErr, ok
}

// GetTemporaryErrorsByName returns a list of temporary errors by name.
func (r *registry) GetTemporaryErrors(names ...string) []ITemporaryError {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	var errors []ITemporaryError
	for _, name := range names {
		if tempErr, ok := r.store[name]; ok {
			errors = append(errors, tempErr)
		}
	}
	return errors
}

// List returns a list of temporary errors.
func (r *registry) ListTemporaryErrors() []ITemporaryError {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	var errors []ITemporaryError
	for _, err := range r.store {
		errors = append(errors, err)
	}
	return errors
}

// Len returns the number of temporary errors.
func (r *registry) Len() int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return len(r.store)
}

// Clean cleans the registry.
func (r *registry) Clean() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.store = make(map[string]ITemporaryError)
}
