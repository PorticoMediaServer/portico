package writer

import (
	"testing"
	"time"
)

func TestInventoryBatchPolicy(t *testing.T) {
	var p inventoryBatchPolicy
	if p.limit(8) != 2 {
		t.Fatal("initial width", p)
	}
	for range 7 {
		p.observe(8, 2, time.Millisecond, 0, true)
	}
	if p.limit(8) != 2 {
		t.Fatal("grew before eight originals", p)
	}
	p.observe(8, 2, time.Millisecond, 0, true)
	if p.limit(8) != 3 {
		t.Fatal("did not grow", p)
	}
	p.observe(8, 3, 9*time.Millisecond, 0, true)
	if p.limit(8) != 1 {
		t.Fatal("slow hold did not shrink", p)
	}
	for range 1000 {
		p.observe(8, p.limit(8), time.Millisecond, 0, true)
	}
	if p.limit(8) != 8 {
		t.Fatal("maximum", p)
	}
	p.observe(8, 8, time.Millisecond, 9*time.Millisecond, true)
	if p.limit(8) != 4 {
		t.Fatal("contention did not shrink", p)
	}
	p.observe(8, 4, time.Millisecond, 0, false)
	if p.limit(8) != 2 {
		t.Fatal("failure did not shrink", p)
	}
}

func TestInventoryBatchPolicyNeedsCompleteEvidence(t *testing.T) {
	for _, sample := range []struct {
		files      int
		hold, wait time.Duration
		applied    bool
	}{
		{1, time.Millisecond, 0, true}, {2, 0, 0, true},
		{2, time.Millisecond, -1, true}, {2, 5 * time.Millisecond, 0, true},
		{2, time.Millisecond, 0, false},
	} {
		var p inventoryBatchPolicy
		for range 100 {
			p.observe(8, sample.files, sample.hold, sample.wait, sample.applied)
		}
		if p.limit(8) > 2 {
			t.Fatal("incomplete evidence grew", sample, p)
		}
	}
	for _, maximum := range []int{-1, 0, 1, 9} {
		var p inventoryBatchPolicy
		if p.limit(maximum) != 1 {
			t.Fatal("unqualified maximum", maximum, p)
		}
	}
	var p inventoryBatchPolicy
	for range 100 {
		p.observe(2, 2, time.Millisecond, 0, true)
	}
	if p.limit(2) != 2 || p.limit(1) != 1 {
		t.Fatal("profile maximum escaped", p)
	}
}
