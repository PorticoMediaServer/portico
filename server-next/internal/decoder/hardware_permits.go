package decoder

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// PermitCell names exactly what a reservation belongs to: the media runtime that
// will run, the backend, and the adapter. It is a comparable struct rather than a
// joined string, so retiring one cell can never match a neighbour whose device id
// merely ends with the same character.
type PermitCell struct {
	Identity string
	Backend  HardwareBackend
	Device   string
}

// Valid reports whether this cell is specific enough to hold a measured ceiling.
// An unknown runtime, backend or adapter never becomes a limit.
func (c PermitCell) Valid() bool {
	return c.Identity != "" && c.Backend != "" && c.Backend != BackendSoftware && c.Device != ""
}

// HardwareSessionPermits is the one place that decides whether another hardware
// encode may start on one adapter, for every launch path that starts one. It
// counts reservations, not sessions observed after the fact: a caller reserves
// before it launches and releases only once the child has been waited, reaped or
// cancelled, so a slot is never given away while a converter is still running.
//
// A ceiling is only ever a measured, configuration-bound driver maximum for one
// exact cell. Until one exists, reservations are counted and nothing is refused:
// this type never turns "N sessions started" or a generic refusal into a limit.
type HardwareSessionPermits struct {
	mu       sync.Mutex
	ceilings map[PermitCell]int
	inflight map[PermitCell]int
}

var hardwareSessionPermits = &HardwareSessionPermits{ceilings: map[PermitCell]int{}, inflight: map[PermitCell]int{}}

// HardwarePermits is the process-wide registry every encoder launch path shares.
func HardwarePermits() *HardwareSessionPermits { return hardwareSessionPermits }

// RecordCeiling stores a measured ceiling for one exact cell. Only a maximum the
// driver actually enforced for that cell may be recorded; an incomplete cell, a
// session count or a generic refusal is refused here rather than published.
func (p *HardwareSessionPermits) RecordCeiling(cell PermitCell, sessions int) bool {
	if !cell.Valid() || sessions < 1 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ceilings[cell] = sessions
	return true
}

// Reserve asks for one slot on one exact cell. It never waits: a caller that
// cannot reserve falls back as it already does for a busy backend. The release is
// safe to call once, and only after the child has been waited or cancelled and
// reaped.
func (p *HardwareSessionPermits) Reserve(cell PermitCell) (release func(), ok bool) {
	if !cell.Valid() {
		// Nothing is known about this cell, so nothing is limited.
		return func() {}, true
	}
	p.mu.Lock()
	if ceiling := p.ceilings[cell]; ceiling > 0 && p.inflight[cell] >= ceiling {
		p.mu.Unlock()
		return func() {}, false
	}
	p.inflight[cell]++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.inflight[cell] > 1 {
				p.inflight[cell]--
				return
			}
			delete(p.inflight, cell)
		})
	}, true
}

// Retire drops every ceiling measured for the cells an exact match names. A zero
// field is a wildcard. Reservations already made are untouched: they belong to
// live children.
func (p *HardwareSessionPermits) Retire(identity string, backend HardwareBackend, device string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for cell := range p.ceilings {
		if identity != "" && cell.Identity != identity {
			continue
		}
		if backend != "" && cell.Backend != backend {
			continue
		}
		if device != "" && cell.Device != device {
			continue
		}
		delete(p.ceilings, cell)
	}
}

// Inflight answers how many reservations are held for one exact cell.
func (p *HardwareSessionPermits) Inflight(cell PermitCell) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inflight[cell]
}

var permitIdentities = struct {
	sync.Mutex
	known map[string]string
}{known: map[string]string{}}

// PermitIdentity names the media runtime behind one executable: its resolved
// path, size and modification time, the same facts the hardware detector binds a
// probe to. It is read from the executable this job actually launches, never from
// whatever the process-wide toolchain happens to be, so a replaced binary cannot
// inherit another runtime's ceiling.
func PermitIdentity(executable string) string {
	if executable == "" {
		return ""
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return ""
	}
	// The executable is observed on every call: a binary replaced at the same path
	// must not inherit the previous runtime's cell. When the observation changed,
	// the ceilings measured for the previous one are retired at once.
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	identity := absolute + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
	permitIdentities.Lock()
	defer permitIdentities.Unlock()
	if previous, ok := permitIdentities.known[absolute]; ok && previous == identity {
		return identity
	} else if ok && previous != "" {
		hardwareSessionPermits.Retire(previous, "", "")
	}
	permitIdentities.known[absolute] = identity
	return identity
}

// ForgetPermitIdentity drops a cached runtime identity, so the next reservation
// re-reads the executable it launches. A re-test calls it with the executable it
// probed, which retires the ceiling whenever the binary was replaced underneath.
func ForgetPermitIdentity(executable string) {
	if executable == "" {
		return
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return
	}
	permitIdentities.Lock()
	defer permitIdentities.Unlock()
	previous := permitIdentities.known[absolute]
	delete(permitIdentities.known, absolute)
	if previous != "" {
		hardwareSessionPermits.Retire(previous, "", "")
	}
}

// WaitForHardwareSlot is a convenience for callers that can tolerate a short
// wait: it retries Reserve until the context ends, for launch paths whose queue
// is already established elsewhere.
func (p *HardwareSessionPermits) WaitForHardwareSlot(ctx context.Context, cell PermitCell) (func(), bool) {
	release, ok := p.Reserve(cell)
	if ok || !cell.Valid() {
		return release, ok
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return func() {}, false
		case <-ticker.C:
			if release, ok = p.Reserve(cell); ok {
				return release, true
			}
		}
	}
}
