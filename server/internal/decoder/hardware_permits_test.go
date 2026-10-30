package decoder

import "testing"

// The permits type counts reservations for one exact measured cell. These tests use
// their own instance rather than the process-wide registry so one case cannot leak a
// ceiling or an in-flight count into another.
func newTestPermits() *HardwareSessionPermits {
	return &HardwareSessionPermits{ceilings: map[PermitCell]int{}, inflight: map[PermitCell]int{}}
}

func permitCell(identity string, backend HardwareBackend, device string) PermitCell {
	return PermitCell{Identity: identity, Backend: backend, Device: device}
}

// A driver maximum of one session is a real measured boundary, not a missing
// measurement: the first reservation is admitted and the next one is refused until
// the first is released.
func TestHardwarePermitMeasuredSingleSessionCeiling(t *testing.T) {
	p := newTestPermits()
	cell := permitCell("runtime-a", BackendNVENC, "GPU-0")
	if !p.RecordCeiling(cell, 1) {
		t.Fatal("a measured single-session ceiling was refused")
	}
	first, ok := p.Reserve(cell)
	if !ok {
		t.Fatal("the first reservation under a measured single-session ceiling was refused")
	}
	second, ok := p.Reserve(cell)
	if ok {
		t.Fatal("a second reservation exceeded the measured single-session ceiling")
	}
	second()
	if got := p.Inflight(cell); got != 1 {
		t.Fatalf("a refused reservation changed the in-flight count: %d", got)
	}
	first()
	if got := p.Inflight(cell); got != 0 {
		t.Fatalf("the released reservation was still counted: %d", got)
	}
	// A release belongs to the child it reserved; calling it again never frees a
	// second slot, and the slot it freed is available to the next caller.
	third, ok := p.Reserve(cell)
	if !ok {
		t.Fatal("a real release did not free the cell for the next reservation")
	}
	first()
	if got := p.Inflight(cell); got != 1 {
		t.Fatalf("an already-used release freed another reservation: %d", got)
	}
	third()
	if got := p.Inflight(cell); got != 0 {
		t.Fatalf("the replacement reservation was not released: %d", got)
	}
}

// A ceiling only exists for a complete, non-software cell and a real session count.
// A refused record never leaves a limit behind.
func TestHardwarePermitCeilingRefusesIncompleteOrUnmeasuredCells(t *testing.T) {
	p := newTestPermits()
	full := permitCell("runtime-a", BackendVAAPI, "renderD128")
	for _, c := range []struct {
		name     string
		cell     PermitCell
		sessions int
	}{
		{"no runtime identity", permitCell("", BackendVAAPI, "renderD128"), 1},
		{"no backend", permitCell("runtime-a", "", "renderD128"), 1},
		{"no device", permitCell("runtime-a", BackendVAAPI, ""), 1},
		{"software backend", permitCell("runtime-a", BackendSoftware, "renderD128"), 1},
		{"zero sessions", full, 0},
		{"negative sessions", full, -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			if p.RecordCeiling(c.cell, c.sessions) {
				t.Fatal("an incomplete or unmeasured ceiling was recorded")
			}
			first, ok := p.Reserve(c.cell)
			if !ok {
				t.Fatal("a refused ceiling limited its cell")
			}
			second, ok := p.Reserve(c.cell)
			if !ok {
				t.Fatal("a refused ceiling limited its cell")
			}
			first()
			second()
			if got := p.Inflight(c.cell); got != 0 {
				t.Fatalf("released reservations were still counted: %d", got)
			}
		})
	}
}

// A ceiling names one exact runtime, backend and adapter. A neighbour that differs in
// any of the three is a different cell and is never limited by this one.
func TestHardwarePermitCeilingDoesNotCrossLimitAnotherCell(t *testing.T) {
	p := newTestPermits()
	cell := permitCell("runtime-a", BackendNVENC, "GPU-0")
	if !p.RecordCeiling(cell, 1) {
		t.Fatal("a measured single-session ceiling was refused")
	}
	held, ok := p.Reserve(cell)
	if !ok {
		t.Fatal("the first reservation was refused")
	}
	for _, other := range []PermitCell{
		permitCell("runtime-b", BackendNVENC, "GPU-0"),
		permitCell("runtime-a", BackendQSV, "GPU-0"),
		permitCell("runtime-a", BackendNVENC, "GPU-1"),
	} {
		release, ok := p.Reserve(other)
		if !ok {
			t.Fatalf("neighbour cell %+v was limited by %+v", other, cell)
		}
		if got := p.Inflight(other); got != 1 {
			t.Fatalf("neighbour cell %+v was not counted on its own: %d", other, got)
		}
		release()
	}
	held()
	if got := p.Inflight(cell); got != 0 {
		t.Fatalf("the held cell was not released: %d", got)
	}
}

// Retiring a measured runtime drops only the ceilings that runtime measured. It never
// invalidates reservations already made for live children, and each release stays with
// the cell it reserved.
func TestHardwarePermitRetireKeepsLiveReservationsAndOtherCells(t *testing.T) {
	p := newTestPermits()
	retired := permitCell("runtime-a", BackendNVENC, "GPU-0")
	kept := permitCell("runtime-b", BackendNVENC, "GPU-0")
	if !p.RecordCeiling(retired, 1) || !p.RecordCeiling(kept, 1) {
		t.Fatal("a measured single-session ceiling was refused")
	}
	original, ok := p.Reserve(retired)
	if !ok {
		t.Fatal("the first reservation on the retired cell was refused")
	}
	other, ok := p.Reserve(kept)
	if !ok {
		t.Fatal("the first reservation on the kept cell was refused")
	}
	p.Retire(retired.Identity, "", "")
	if got := p.Inflight(retired); got != 1 {
		t.Fatalf("Retire changed a live reservation: %d", got)
	}
	// The retired cell no longer has a measured ceiling, so it is counted but not
	// limited; the other cell still is.
	next, ok := p.Reserve(retired)
	if !ok {
		t.Fatal("a retired ceiling still limited its cell")
	}
	if _, ok := p.Reserve(kept); ok {
		t.Fatal("Retire dropped another runtime's ceiling")
	}
	original()
	if got := p.Inflight(retired); got != 1 {
		t.Fatalf("the original release did not free exactly its own reservation: %d", got)
	}
	if got := p.Inflight(kept); got != 1 {
		t.Fatalf("the original release touched another cell: %d", got)
	}
	next()
	other()
	if got := p.Inflight(retired); got != 0 {
		t.Fatalf("the retired cell kept a reservation: %d", got)
	}
	if got := p.Inflight(kept); got != 0 {
		t.Fatalf("the kept cell kept a reservation: %d", got)
	}
}
