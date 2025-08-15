package again

import (
	"time"
)

// TimerPool is a pool of timers.
type TimerPool struct {
	ch       chan *time.Timer // Channel of timers.
	duration time.Duration    // Duration of the timers.
}

// NewTimerPool creates a new timer pool.
func NewTimerPool(size int, timeout time.Duration) *TimerPool {
	// Create the pool.
	pool := &TimerPool{
		ch:       make(chan *time.Timer, size),
		duration: timeout,
	}
	// Create timers and put them into the pool.
	for range size {
		t := time.NewTimer(timeout)
		t.Stop() // ensure the timer isn't running while in the pool
		select {
		case <-t.C:
		default:
		}
		// Put the timer into the pool.
		pool.ch <- t
	}

	return pool
}

// Get retrieves a timer from the pool.
func (p *TimerPool) Get() *time.Timer {
	// Get a timer from the pool and reset it to the configured duration.
	t, ok := <-p.ch
	if !ok || t == nil {
		return nil
	}
	t.Reset(p.duration)

	return t
}

// Put returns a timer back into the pool.
func (p *TimerPool) Put(t *time.Timer) {
	// Stop the timer and drain its channel to avoid spurious wake-ups.
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	} else {
		select {
		case <-t.C:
		default:
		}
	}

	select {
	case p.ch <- t:
		// Timer was successfully put back into the pool.
	default:
		// Timer pool is full, discard the timer.
	}
}

// Close closes the pool.
func (p *TimerPool) Close() {
	// Close the channel.
	close(p.ch)
}

// Drain drains the pool.
func (p *TimerPool) Drain() {
	// Drain the channel.
	for range p.ch {
	}
}

// Len returns the number of timers in the pool.
func (p *TimerPool) Len() int {
	return len(p.ch)
}
