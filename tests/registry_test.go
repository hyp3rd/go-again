package tests

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/hyp3rd/go-again"
	"github.com/stretchr/testify/assert"
)

func TestRegistry(t *testing.T) {
	// Create a new registry and load the default temporary errors.
	registry := again.NewRegistry().LoadDefaults()

	// Test registering a custom temporary error.
	customError := errors.New("custom temporary error")
	registry.RegisterTemporaryError("customError", func() again.TemporaryError { return customError })

	// Test getting the custom temporary error.
	tempErr, ok := registry.GetTemporaryError("customError")
	assert.True(t, ok)
	assert.Equal(t, customError, tempErr)

	// Test unregistering a temporary error.
	registry.UnRegisterTemporaryError("customError")
	tempErr, ok = registry.GetTemporaryError("customError")
	assert.False(t, ok)
	assert.Nil(t, tempErr)

	// Test getting multiple temporary errors by name.
	tempErrors := registry.GetTemporaryErrors("os.SyscallError", "context.DeadlineExceededError")
	assert.Equal(t, 2, len(tempErrors))
	assert.IsType(t, &os.SyscallError{}, tempErrors[0])
	assert.Equal(t, context.DeadlineExceeded, tempErrors[1])

	// Test listing all temporary errors.
	tempErrors = registry.ListTemporaryErrors()
	assert.Equal(t, 6, len(tempErrors))

	// Test cleaning the registry.
	registry.Clean()
	tempErrors = registry.ListTemporaryErrors()
	assert.Equal(t, 0, len(tempErrors))
}

// TestRegistryIsTemporaryError tests the registry IsTemporaryError function.
func TestRegistryIsTemporaryError(t *testing.T) {
	r := again.NewRegistry()
	r.RegisterTemporaryError("http.ErrAbortHandler", func() again.TemporaryError {
		return http.ErrAbortHandler
	})

	defer r.UnRegisterTemporaryError("http.ErrAbortHandler")

	retrier, _ := again.NewRetrier()
	retrier.Registry = r

	if retrier.Registry.IsTemporaryError(http.ErrAbortHandler, "http.ErrAbortHandler") != true {
		t.Errorf("registry failed to register a temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrSkipAltProtocol, "http.ErrHandlerTimeout") != false {
		t.Errorf("registry failed to validate temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrAbortHandler, "http.ErrHandlerTimeout") != false {
		t.Errorf("registry failed to validate temporary error")
	}
}
