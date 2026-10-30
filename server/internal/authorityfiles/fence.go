// Package authorityfiles fences application-owned portable authority mutations.
// Tokens cover complete operations including temporary publication and cleanup;
// no lock is held during I/O and no state survives a process restart.
package authorityfiles

import (
	"math"
	"sync"
)

type Fence struct {
	mu         sync.Mutex
	generation uint64
	active     uint64
	exhausted  bool
}

var Process Fence

func (f *Fence) Admit() func() {
	f.mu.Lock()
	if f.generation == math.MaxUint64 || f.active == math.MaxUint64 {
		f.exhausted = true
	} else {
		f.generation++
		f.active++
	}
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.exhausted {
				f.active--
			}
		})
	}
}

func (f *Fence) Baseline() (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation, f.active == 0 && !f.exhausted
}

func (f *Fence) Unchanged(generation uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.exhausted && f.active == 0 && f.generation == generation
}
