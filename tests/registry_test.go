package tests

import (
	"context"
	"net/http"
	"testing"

	"github.com/hyp3rd/ewrap"
	"github.com/stretchr/testify/assert"

	"github.com/hyp3rd/go-again"
)

const errFailedToCreateRetrier = "failed to create retrier: %v"

func TestRegistry(t *testing.T) {
	t.Parallel()
	// Create a new registry and load the default temporary errors.
	registry := again.NewRegistry().LoadDefaults()

	// Test registering a custom temporary error.
	customError := ewrap.New("custom temporary error")
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
	t.Parallel()

	registry := again.NewRegistry()
	registry.RegisterTemporaryError(http.ErrAbortHandler)

	defer registry.UnRegisterTemporaryError(http.ErrAbortHandler)

	retrier, err := again.NewRetrier(context.Background())
	if err != nil {
		t.Fatalf(errFailedToCreateRetrier, err)
	}

	retrier.Registry = registry

	if !retrier.Registry.IsTemporaryError(http.ErrAbortHandler, http.ErrAbortHandler) {
		t.Error("registry failed to register a temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrSkipAltProtocol, http.ErrHandlerTimeout) {
		t.Error("registry failed to validate temporary error")
	}

	if retrier.Registry.IsTemporaryError(http.ErrAbortHandler, http.ErrHandlerTimeout) {
		t.Error("registry failed to validate temporary error")
	}
}
