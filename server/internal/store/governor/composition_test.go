package governor

import (
	"testing"
	"time"
)

type compositionProxy struct{ Governor }

func TestVerifyCompositionRequiresExactNonNilTriple(t *testing.T) {
	w := gate(t)
	otherGate := gate(t)
	makeGovernor := func(wal *WALGate) (Governor, *Authority) {
		t.Helper()
		g, a, err := New(Config{WAL: wal, MaxGrantsPerClass: 1, Now: func() time.Time { panic("composition consulted clock") }})
		if err != nil {
			t.Fatal(err)
		}
		return g, a
	}
	g, a := makeGovernor(w)
	foreign, otherAuthority := makeGovernor(w)
	missing, missingAuthority := makeGovernor(nil)
	var typedNil *ledger
	for _, test := range []struct {
		name string
		a    *Authority
		g    Governor
		wal  *WALGate
	}{
		{"nil-authority", nil, g, w},
		{"empty-authority", &Authority{}, g, w},
		{"nil-governor", a, nil, w},
		{"typed-nil-governor", a, typedNil, w},
		{"nil-gate", a, g, nil},
		{"foreign-governor", a, foreign, w},
		{"foreign-authority", otherAuthority, g, w},
		{"foreign-gate", a, g, otherGate},
		{"missing-config-gate", missingAuthority, missing, w},
		{"delegating-proxy", a, compositionProxy{g}, w},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.a.VerifyComposition(test.g, test.wal); err != ErrComposition {
				t.Fatal("unbound writer composition accepted", err)
			}
		})
	}
	// These locks and the panicking clock prove validation does not depend on
	// admission/sampling or change custody, readiness, generations or epochs.
	l := g.(*ledger)
	l.mu.Lock()
	w.mu.Lock()
	defer l.mu.Unlock()
	defer w.mu.Unlock()
	state, generation := l.state.Load(), w.version
	if err := a.VerifyComposition(g, w); err != nil {
		t.Fatal(err)
	}
	if l.state.Load() != state || w.version != generation || w.valid || len(l.live) != 0 {
		t.Fatal("composition validation changed state")
	}
}
