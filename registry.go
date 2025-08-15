package again

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
)

// TemporaryError implements the error interface.
type TemporaryError error

// Registry for temporary errors.
type Registry struct {
	storage sync.Map // store for temporary errors
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{
		storage: sync.Map{},
	}
}

// LoadDefaults loads the default temporary errors into the registry.
func (r *Registry) LoadDefaults() *Registry {
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

// RegisterTemporaryError registers a single temporary error.
func (r *Registry) RegisterTemporaryError(name string, fn func() TemporaryError) {
	r.RegisterTemporaryErrors(map[string]func() TemporaryError{name: fn})
}

// RegisterTemporaryErrors registers multiple temporary errors.
func (r *Registry) RegisterTemporaryErrors(temporaryErrors map[string]func() TemporaryError) {
	for name, fn := range temporaryErrors {
		r.storage.Store(name, fn())
	}
}

// UnRegisterTemporaryError unregisters one or more temporary errors by name.
func (r *Registry) UnRegisterTemporaryError(names ...string) {
	for _, name := range names {
		r.storage.Delete(name)
	}
}

// UnRegisterTemporaryErrors unregisters multiple temporary errors.
func (r *Registry) UnRegisterTemporaryErrors(temporaryErrors map[string]func() TemporaryError) {
	names := make([]string, 0, len(temporaryErrors))
	for name := range temporaryErrors {
		names = append(names, name)
	}

	r.UnRegisterTemporaryError(names...)
}

// GetTemporaryError returns a temporary error by name.
func (r *Registry) GetTemporaryError(name string) (TemporaryError, bool) {
	tempErr, ok := r.storage.Load(name)
	if !ok {
		return nil, false
	}

	err, ok := tempErr.(TemporaryError)
	if !ok {
		return nil, false
	}

	return err, true
}

// GetTemporaryErrors returns a list of temporary errors filtered by name.
func (r *Registry) GetTemporaryErrors(names ...string) []TemporaryError {
	errors := make([]TemporaryError, 0, len(names))

	for _, name := range names {
		tempErr, ok := r.storage.Load(name)
		if !ok {
			continue
		}

		err, ok := tempErr.(TemporaryError)
		if !ok {
			continue
		}

		errors = append(errors, err)
	}

	return errors
}

// ListTemporaryErrors returns a list of temporary errors.
func (r *Registry) ListTemporaryErrors() []TemporaryError {
	errors := make([]TemporaryError, 0, r.Len())

	r.storage.Range(func(key, value any) bool {
		err, ok := value.(TemporaryError)
		if ok {
			errors = append(errors, err)
		}

		return true
	})

	return errors
}

// Len returns the number of temporary errors in the registry.
func (r *Registry) Len() int {
	count := 0

	r.storage.Range(func(_, _ any) bool {
		count++

		return true
	})

	return count
}

// Clean cleans the Registry.
func (r *Registry) Clean() {
	r.storage.Clear()
}

// IsTemporaryError checks if the error is in the list of temporary errors.
func (r *Registry) IsTemporaryError(err error, errorsList ...string) bool {
	var tempErrors []TemporaryError

	if errorsList == nil {
		tempErrors = r.ListTemporaryErrors()
	} else {
		tempErrors = r.GetTemporaryErrors(errorsList...)
	}

	for _, tempErr := range tempErrors {
		if errors.Is(tempErr, err) && err.Error() == tempErr.Error() {
			return true
		}
	}

	return false
}
