package writer

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestQuantumReservesTerminalAndRefusesWithoutMutation(t *testing.T) {
	start := time.Now()
	limit := Work{10, 100, 10, 1000, 10 * time.Millisecond}
	terminal := Work{1, 10, 1, 100, 2 * time.Millisecond}
	q, err := NewQuantum(limit, terminal, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.TryStep(Work{8, 80, 8, 800, 6 * time.Millisecond}, start); err != nil {
		t.Fatal(err)
	}
	before := q.Remaining()
	for _, step := range []Work{
		{2, 1, 1, 1, time.Nanosecond}, {1, 11, 1, 1, time.Nanosecond},
		{1, 1, 2, 1, time.Nanosecond}, {1, 1, 1, 101, time.Nanosecond},
		{1, 1, 1, 1, 3 * time.Millisecond}, {math.MaxUint64, 1, 1, 1, time.Nanosecond},
		{1, 1, 1, 1, -time.Nanosecond}, {},
	} {
		if err := q.TryStep(step, start); !errors.Is(err, ErrQuantum) {
			t.Fatal(step, err)
		}
		if q.Remaining() != before {
			t.Fatal("refusal changed ledger")
		}
	}
	// Forecast fits, but elapsed actual time leaves no room for terminal return.
	if err := q.TryStep(Work{1, 1, 1, 1, time.Millisecond}, start.Add(8*time.Millisecond)); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	if q.Remaining() != before {
		t.Fatal("wall-time refusal changed ledger")
	}
	if err := q.TryStep(Work{1, 10, 1, 100, 2 * time.Millisecond}, start.Add(6*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if q.Remaining() != terminal {
		t.Fatal(q.Remaining())
	}
	if err := q.TryStep(Work{Hold: time.Nanosecond}, start.Add(6*time.Millisecond)); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
}

func TestQuantumRejectsUnknownBudgetsAndClockReversal(t *testing.T) {
	start := time.Now()
	good := Work{10, 100, 10, 1000, 10 * time.Millisecond}
	terminal := Work{Hold: time.Millisecond}
	for _, bad := range []Work{{}, {0, 100, 10, 1000, time.Millisecond}, {10, 0, 10, 1000, time.Millisecond},
		{10, 100, 0, 1000, time.Millisecond}, {10, 100, 10, 0, time.Millisecond}, {10, 100, 10, 1000, 26 * time.Millisecond},
		{10, 100, 10, 1000, -1}} {
		if _, err := NewQuantum(bad, terminal, start); !errors.Is(err, ErrQuantum) {
			t.Fatal(bad, err)
		}
	}
	if _, err := NewQuantum(good, Work{}, start); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	if _, err := NewQuantum(good, terminal, time.Time{}); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	q, _ := NewQuantum(good, terminal, start)
	if err := q.TryStep(Work{Hold: time.Millisecond}, start.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := q.TryStep(Work{Hold: time.Millisecond}, start); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	var zero Quantum
	if err := zero.TryStep(Work{Hold: time.Nanosecond}, start); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
}

func TestClosedCommandDurability(t *testing.T) {
	for id := InventoryAccept; id <= InventoryDirectoryEOF; id++ {
		d, err := Describe(id)
		if err != nil || d.ID != id || d.Name == "" {
			t.Fatal(d, err)
		}
		want := Normal
		if id == SecurityAccept {
			want = Full
		}
		if d.Durability != want {
			t.Fatal(d)
		}
	}
	for _, id := range []CommandID{0, InventoryDirectoryEOF + 1, 65535} {
		if _, err := Describe(id); !errors.Is(err, ErrInvalid) {
			t.Fatal(id, err)
		}
	}
	d, _ := Describe(SecurityAccept)
	d.Durability = Normal
	got, _ := Describe(SecurityAccept)
	if got.Durability != Full {
		t.Fatal("caller changed registry")
	}
}

func TestQuantumTerminalConsumesReserveExactlyOnce(t *testing.T) {
	start := time.Now()
	limit := Work{10, 100, 10, 1000, 10 * time.Millisecond}
	terminal := Work{1, 10, 1, 100, 2 * time.Millisecond}
	q, err := NewQuantum(limit, terminal, start)
	if err != nil {
		t.Fatal(err)
	}
	before := q.Remaining()
	if err := q.TryFinish(start.Add(9 * time.Millisecond)); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	if q.Remaining() != before {
		t.Fatal("terminal refusal mutated state")
	}
	if err := q.TryFinish(start.Add(8 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	after := q.Remaining()
	if after != subtract(limit, terminal) {
		t.Fatal(after)
	}
	if err := q.TryFinish(start.Add(8 * time.Millisecond)); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	if err := q.TryStep(Work{Hold: time.Nanosecond}, start.Add(8*time.Millisecond)); !errors.Is(err, ErrQuantum) {
		t.Fatal(err)
	}
	if q.Remaining() != after {
		t.Fatal("finished quantum changed")
	}
}
