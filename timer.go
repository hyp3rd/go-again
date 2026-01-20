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
		timer := time.NewTimer(timeout)
		timer.Stop() // ensure the timer isn't running while in the pool

		select {
		case <-timer.C:
		default:
		}
		// Put the timer into the pool.
		pool.ch <- timer
	}

	return pool
}

// Get retrieves a timer from the pool.
func (p *TimerPool) Get() *time.Timer {
	// Get a timer from the pool and reset it to the configured duration.
	timer, ok := <-p.ch
	if !ok || timer == nil {
		return nil
	}

	timer.Reset(p.duration)

	return timer
}

// Put returns a timer back into the pool.
func (p *TimerPool) Put(timer *time.Timer) {
	// Stop the timer and drain its channel to avoid spurious wake-ups.
	timer.Stop()

	select {
	case <-timer.C:
	default:
	}

	select {
	case p.ch <- timer:
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
	//nolint:revive
	for range p.ch {
		// No action needed, just draining.
	}
}

// Len returns the number of timers in the pool.
func (p *TimerPool) Len() int {
	return len(p.ch)
}
