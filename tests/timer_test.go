package tests

import (
	"testing"
	"time"

	"github.com/hyp3rd/go-again"
)

func TestTimerPool(t *testing.T) {
	poolSize := 5
	timeout := time.Second

	// Create a new timer pool.
	pool := again.NewTimerPool(poolSize, timeout)

	// Verify that the pool is of the correct size.
	if pool.Len() != poolSize {
		t.Errorf("pool size is %d, expected %d", pool.Len(), poolSize)
	}

	// Get all of the timers from the pool and ensure that they are stopped.
	for i := 0; i < poolSize; i++ {
		timer := pool.Get()
		if len(timer.C) == 0 && !timer.Stop() {
			t.Error("timer was not stopped")
		}
	}

	// Ensure that getting another timer from the pool blocks until a timer is returned.
	doneCh := make(chan struct{})
	go func() {
		pool.Get()
		doneCh <- struct{}{}
	}()

	select {
	case <-doneCh:
		t.Error("Get() returned immediately, expected to block")
	case <-time.After(timeout / 2):
		// Expected case.
	}

	// Put a timer back into the pool and ensure that it is no longer blocked.
	timer := time.NewTimer(timeout)
	pool.Put(timer)

	select {
	case <-doneCh:
		// Expected case.
	case <-time.After(timeout / 2):
		t.Error("Get() did not return, expected to unblock")
	}

	// Ensure that the pool can be closed and that subsequent calls to Get() return nil.
	pool.Close()
	for i := 0; i < poolSize; i++ {
		timer := pool.Get()
		if timer != nil {
			t.Error("timer is not nil, expected nil")
		}
	}
}

func TestTimerPool_PutClosed(t *testing.T) {
	poolSize := 5
	timeout := time.Second

	// Create a new timer pool.
	pool := again.NewTimerPool(poolSize, timeout)

	// Ensure that putting a timer into a closed pool panics.
	timer := time.NewTimer(timeout)
	pool.Close()
	defer func() {
		if r := recover(); r == nil {
			t.Error("Put() did not panic, expected to panic")
		}
	}()
	pool.Put(timer)
}

func TestTimerPool_Reuse(t *testing.T) {
	poolSize := 1
	timeout := time.Second

	// Create a new timer pool.
	pool := again.NewTimerPool(poolSize, timeout)

	// Ensure that timers can be reused.
	timer := pool.Get()
	timer.Reset(timeout)
	pool.Put(timer)

	timer = pool.Get()
	if !timer.Stop() {
		t.Error("timer was not stopped")
	}
}
