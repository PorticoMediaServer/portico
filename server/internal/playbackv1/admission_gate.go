package playbackv1

import "sync"

// admissionGates serialize one account's new streams from admission to the
// session row that makes them count (the admission race, MS2 follow-up).
//
// Admission counts the account's live streams in its own transaction, but the
// stream it admits only becomes countable later, when the presentation's media
// session and the v1 row are written. Two starts of the same account that
// overlap there would both see the old count and both be admitted past
// maxStreams. Holding the account's gate from admission until the start
// returns makes the second start count the first. The server is one process,
// so a process-local gate is the whole truth; different accounts never wait for
// each other, and a replan or a zap (which admit as continuing a stream) take
// the same gate, which only orders them.
type admissionGates struct {
	mu    sync.Mutex
	gates map[string]*admissionGate
}

type admissionGate struct {
	sync.Mutex
	holders int
}

// hold takes account's gate and returns its release. The release is idempotent.
func (a *admissionGates) hold(account string) func() {
	a.mu.Lock()
	if a.gates == nil {
		a.gates = map[string]*admissionGate{}
	}
	g := a.gates[account]
	if g == nil {
		g = &admissionGate{}
		a.gates[account] = g
	}
	g.holders++
	a.mu.Unlock()
	g.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.Unlock()
			a.mu.Lock()
			if g.holders--; g.holders == 0 {
				delete(a.gates, account)
			}
			a.mu.Unlock()
		})
	}
}
