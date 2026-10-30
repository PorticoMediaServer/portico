package readprep

import (
	"errors"
	"math"
	"testing"
)

func TestBudgetCompositionRetainsOverheadAndStickyFailure(t *testing.T) {
	s := storeFor(t, Config{MaxBytes: 1024, Workers: 1})
	e := &entry{}
	b := &Budget{s: s, e: e}
	if err := b.Resize(900); err != nil {
		t.Fatal(err)
	}
	child, err := b.WithOverhead(200)
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Resize(100); err != nil {
		t.Fatal(err)
	}
	if s.Stats().Bytes != 300 || e.reserved != 300 {
		t.Fatal(s.Stats(), e.reserved)
	}
	nested, err := child.WithOverhead(100)
	if err != nil {
		t.Fatal(err)
	}
	if err = nested.Resize(50); err != nil || e.reserved != 350 {
		t.Fatal(err, e.reserved)
	}
	if ok, err := nested.TryResize(800); ok || err != nil || b.err != nil || e.reserved != 350 {
		t.Fatal(ok, err, b.err, e.reserved)
	}
	if err = nested.Resize(800); !errors.Is(err, ErrCapacity) || !errors.Is(b.err, ErrCapacity) {
		t.Fatal(err, b.err)
	}
	if _, err = child.WithOverhead(-1); !errors.Is(err, ErrCapacity) || !errors.Is(b.err, ErrCapacity) {
		t.Fatal("nested view replaced first sticky failure", err, b.err)
	}
	if err = b.Resize(0); !errors.Is(err, ErrCapacity) || e.reserved != 350 {
		t.Fatal("child failure lost", err, e.reserved)
	}
	// This private test reservation has no content entry/handle; release exactly
	// its own bytes before the ordinary store cleanup.
	s.mu.Lock()
	s.used -= e.reserved
	e.reserved = 0
	s.mu.Unlock()
}
func TestBudgetCompositionRejectsOverflow(t *testing.T) {
	s := storeFor(t, Config{Workers: 1})
	b := &Budget{s: s, e: &entry{}}
	child, e := b.WithOverhead(math.MaxInt64)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = child.WithOverhead(1); !errors.Is(e, ErrInvalid) || !errors.Is(b.err, ErrInvalid) {
		t.Fatal(e, b.err)
	}
}
