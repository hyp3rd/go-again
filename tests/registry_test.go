package tests

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hyp3rd/go-again"
)

func TestRegistry(t *testing.T) {
	// Create a new registry and load the default temporary errors.
	registry := again.NewRegistry().LoadDefaults()

	// Test registering a custom temporary error.
	customError := errors.New("custom temporary error")
	registry.RegisterTemporaryError(customError)
	assert.True(t, registry.IsTemporaryError(customError))

	// Test unregistering a temporary error.
	registry.UnRegisterTemporaryError(customError)
	assert.False(t, registry.IsTemporaryError(customError))

	// Test listing all temporary errors.
	tempErrors := registry.ListTemporaryErrors()
	assert.GreaterOrEqual(t, len(tempErrors), 5)

	// Test cleaning the registry.
	registry.Clean()
	assert.Equal(t, 0, registry.Len())
}

// TestRegistryIsTemporaryError tests the registry IsTemporaryError function.
func TestRegistryIsTemporaryError(t *testing.T) {
	r := again.NewRegistry()
	r.RegisterTemporaryError(http.ErrAbortHandler)

	defer r.UnRegisterTemporaryError(http.ErrAbortHandler)

	retrier, _ := again.NewRetrier(context.Background())
	retrier.Registry = r

	if retrier.Registry.IsTemporaryError(http.ErrAbortHandler, http.ErrAbortHandler) != true {
		t.Errorf("registry failed to register a temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrSkipAltProtocol, http.ErrHandlerTimeout) != false {
		t.Errorf("registry failed to validate temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrAbortHandler, http.ErrHandlerTimeout) != false {
		t.Errorf("registry failed to validate temporary error")
	}
}
