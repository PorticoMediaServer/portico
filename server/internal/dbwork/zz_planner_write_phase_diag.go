package dbwork

import (
	"log"
	"time"
)

type plannerWriteSample struct {
	start, last time.Time
	phases      map[string]time.Duration
}

func plannerWritePhase(query string) *plannerWriteSample {
	if query != "INSERT INTO sign_ins(at) VALUES(?)" {
		return nil
	}
	now := time.Now()
	return &plannerWriteSample{start: now, last: now, phases: map[string]time.Duration{}}
}
func (d *plannerWriteSample) mark(name string) {
	if d == nil {
		return
	}
	now := time.Now()
	d.phases[name] = now.Sub(d.last)
	d.last = now
}
func (d *plannerWriteSample) finish() {
	if d == nil {
		return
	}
	elapsed := time.Since(d.start)
	if elapsed >= 50*time.Millisecond {
		d.mark("after-release")
		log.Printf("REDS-PLANNER-WRITE elapsed=%s phases=%v", elapsed, d.phases)
	}
}
