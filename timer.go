package again

import "time"

type timerPool struct {
	ch chan *time.Timer
}

// NewTimerPool creates a new timer pool.
func NewTimerPool(size int, timeout time.Duration) *timerPool {
	pool := &timerPool{
		ch: make(chan *time.Timer, size),
	}
	for i := 0; i < size; i++ {
		t := time.NewTimer(timeout)
		t.Stop()
		pool.ch <- t
	}
	return pool
}

// Get gets a timer from the pool.
func (p *timerPool) Get() *time.Timer {
	return <-p.ch
}

// Put puts a timer back into the pool.
func (p *timerPool) Put(t *time.Timer) {
	t.Stop()
	p.ch <- t
}
