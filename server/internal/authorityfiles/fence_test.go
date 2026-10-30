package authorityfiles

import (
	"math"
	"testing"
)

func TestFenceRejectsStraddlingAndCompletedMutations(t *testing.T) {
	var f Fence
	base, ok := f.Baseline()
	if !ok || !f.Unchanged(base) {
		t.Fatal("idle fence refused")
	}
	release := f.Admit()
	if _, ok := f.Baseline(); ok {
		t.Fatal("in-flight mutation admitted")
	}
	if f.Unchanged(base) {
		t.Fatal("straddling mutation missed")
	}
	release()
	release()
	if f.Unchanged(base) {
		t.Fatal("completed ABA mutation missed")
	}
	next, ok := f.Baseline()
	if !ok || next == base || !f.Unchanged(next) {
		t.Fatal("new idle generation refused")
	}
}

func TestFenceNeverWrapsGeneration(t *testing.T) {
	f := Fence{generation: math.MaxUint64}
	release := f.Admit()
	release()
	if _, ok := f.Baseline(); ok {
		t.Fatal("exhausted generation admitted")
	}
	if f.Unchanged(math.MaxUint64) {
		t.Fatal("exhausted generation acknowledged")
	}
}
