package again

import "time"

// timerPool is a pool of timers.
type timerPool struct {
	ch chan *time.Timer
}

// newTimerPool creates a new timer pool.
func newTimerPool(size int, timeout time.Duration) *timerPool {
	// Create the pool.
	pool := &timerPool{
		ch: make(chan *time.Timer, size),
	}
	// Create timers and put them into the pool.
	for i := 0; i < size; i++ {
		t := time.NewTimer(timeout)
		t.Stop()
		pool.ch <- t
	}
	return pool
}

// Get retrieves a timer from the pool.
func (p *timerPool) get() *time.Timer {
	// Get a timer from the pool.
	return <-p.ch
}

// Put returns a timer back into the pool.
func (p *timerPool) put(t *time.Timer) {
	// Stop the timer and put it back into the pool.
	t.Stop()
	p.ch <- t
}
