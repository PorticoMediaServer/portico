package api

import (
	"sync/atomic"
	"time"
)

// rigClock is a clock a test moves while the server's own background work reads it.
type rigClock struct{ at atomic.Pointer[time.Time] }

func newRigClock(at time.Time) *rigClock {
	c := &rigClock{}
	c.Set(at)
	return c
}

func (c *rigClock) Now() time.Time      { return *c.at.Load() }
func (c *rigClock) Set(t time.Time)     { c.at.Store(&t) }
func (c *rigClock) Add(d time.Duration) { c.Set(c.Now().Add(d)) }
