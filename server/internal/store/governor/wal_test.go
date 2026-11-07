package governor

import (
	"math"
	"testing"
	"time"
)

func walSample(frames, backfill uint32) WALState {
	s := WALState{Frames: frames, Backfill: backfill, PageSize: 4096, Salt: [8]byte{1}}
	s.ActiveBytes = 32 + int64(frames)*4120
	s.PhysicalBytes = s.ActiveBytes
	s.UncheckpointedBytes = int64(frames-backfill) * 4120
	return s
}

func gate(t *testing.T) *WALGate {
	t.Helper()
	w, err := NewWALGate(WALPolicy{HardBytes: 1 << 20, ReserveBytes: 64 << 10, SoftDebtBytes: 256 << 10, ActivePressureBytes: 512 << 10, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func publish(t *testing.T, w *WALGate, s WALState, at time.Time) {
	t.Helper()
	if err := w.Publish(w.Observation(), s, at); err != nil {
		t.Fatal(err)
	}
}

func TestWALWriterRetirementInvalidatesHeadroom(t *testing.T) {
	g, a, c := setup(t)
	w := gate(t)
	g.(*ledger).config.WAL = w
	r := request(c, Background)
	r.Writer = true
	r.MaxHold = 5 * time.Millisecond
	r.Budget = UnitBudget{1, 100, 8}
	r.Need[WALBytes] = 32
	refused(t, g, r, FailSafe)
	publish(t, w, walSample(4, 4), c.now())
	before := w.Observation()
	p := grant(t, g, r)
	if w.Publish(before, walSample(4, 4), c.now()) != ErrWALObservation {
		t.Fatal("in-flight sample accepted")
	}
	during := w.Observation()
	g.Cancel(p)
	refused(t, g, r, Capacity)
	retire(t, a, p)
	refused(t, g, r, FailSafe)
	if w.Publish(during, walSample(4, 4), c.now()) != ErrWALObservation {
		t.Fatal("pre-return sample accepted after retirement")
	}
	publish(t, w, walSample(5, 5), c.now())
	retire(t, a, grant(t, g, r))
}

func TestWALPressureAndEmergencyReserve(t *testing.T) {
	c := &testClock{}
	r := request(c, Background)
	r.Writer = true
	r.Need[WALBytes] = 1
	w := gate(t)
	s := walSample(100, 0) // debt exceeds soft limit, even though file is below cap
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != Capacity {
		t.Fatal("ordinary work bypassed debt pressure")
	}
	r.Class = Foreground
	if w.reserve(r, c.now()) != 0 {
		t.Fatal("foreground reserve was not usable")
	}
	w.returned()
	r.Class = Background
	s = walSample(130, 129)
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != Capacity {
		t.Fatal("active prefix pressure ignored")
	}
	s.Backfill = s.Frames
	s.UncheckpointedBytes = 0
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != 0 {
		t.Fatal("copied tail was not eligible for reset/reuse")
	}
	w.returned()
	s = walSample(240, 240)
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != Capacity {
		t.Fatal("background borrowed emergency headroom")
	}
	r.Class = Recovery
	if w.reserve(r, c.now()) != 0 {
		t.Fatal("recovery reserve inaccessible")
	}
	w.returned()
	r.Need[WALBytes] = math.MaxInt64
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != Capacity {
		t.Fatal("overflowing WAL forecast accepted")
	}
	// An allocated reusable file is charged, but a small prefix may reuse it.
	r.Class = Background
	r.Need[WALBytes] = 1
	s = walSample(2, 2)
	s.PhysicalBytes = 1 << 20
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != 0 {
		t.Fatal("existing bounded file could not be reused")
	}
	w.returned()
	s.PhysicalBytes++
	publish(t, w, s, c.now())
	if w.reserve(r, c.now()) != FailSafe {
		t.Fatal("physical hard-cap miss not fenced")
	}
}

func TestWALFreshnessAndObservedReset(t *testing.T) {
	w := gate(t)
	c := &testClock{}
	r := request(c, Foreground)
	r.Deadline = c.now().Add(time.Hour)
	r.Need[WALBytes] = 1
	s := walSample(20, 20)
	publish(t, w, s, c.now())
	if w.reserve(r, c.now().Add(2*time.Second)) != FailSafe {
		t.Fatal("stale headroom accepted")
	}
	if w.reserve(r, c.now().Add(-time.Millisecond)) != FailSafe {
		t.Fatal("future observation accepted")
	}
	if w.ResetCount() != 0 {
		t.Fatal("copy counted as a reset")
	}
	publish(t, w, s, c.now())
	if w.ResetCount() != 0 {
		t.Fatal("PASSIVE completion counted as a reset")
	}
	s = walSample(1, 0)
	s.Salt[0] = 2
	publish(t, w, s, c.now())
	if w.ResetCount() != 1 {
		t.Fatal("actual salt/frame reuse not recorded")
	}
	s.Backfill = 2
	if w.Publish(w.Observation(), s, c.now()) != ErrWALObservation {
		t.Fatal("invalid backfill accepted")
	}
	if w.reserve(r, c.now()) != FailSafe {
		t.Fatal("invalid observation left old admission enabled")
	}
}

func TestWALRejectsMalformedStateAndPolicy(t *testing.T) {
	w := gate(t)
	s := walSample(10, 5)
	now := time.Now()
	for _, change := range []func(*WALState){
		func(x *WALState) { x.PageSize = 0 }, func(x *WALState) { x.PageSize = 1000 },
		func(x *WALState) { x.ActiveBytes++ }, func(x *WALState) { x.PhysicalBytes = 0 },
		func(x *WALState) { x.UncheckpointedBytes++ }, func(x *WALState) { x.Backfill = x.Frames + 1 },
	} {
		bad := s
		change(&bad)
		if w.Publish(w.Observation(), bad, now) != ErrWALObservation {
			t.Fatal("malformed sample accepted", bad)
		}
	}
	if w.Publish(w.Observation(), s, time.Time{}) != ErrWALObservation {
		t.Fatal("zero timestamp accepted")
	}
	if _, err := ReadWAL(t.TempDir() + "/absent"); err == nil {
		t.Fatal("missing WAL supplied authority")
	}
	for _, p := range []WALPolicy{{}, {HardBytes: 10, ReserveBytes: 10}, {HardBytes: 100, ReserveBytes: 10, SoftDebtBytes: 1, ActivePressureBytes: 99, MaxSampleAge: time.Second}} {
		if _, err := NewWALGate(p); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestEmptyWALReservesInitialHeader(t *testing.T) {
	for _, class := range []Class{Foreground, Recovery, ScanMinimum, Background} {
		w, err := NewWALGate(WALPolicy{HardBytes: 100, ReserveBytes: 20, SoftDebtBytes: 50, ActivePressureBytes: 50, MaxSampleAge: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		s := WALState{PageSize: 4096}
		r := Request{Class: class, Deadline: time.Now().Add(time.Minute)}
		limit := int64(100)
		if class == ScanMinimum || class == Background {
			limit -= 20
		}
		for _, physical := range []int64{0, 32} {
			s.PhysicalBytes = physical
			publish(t, w, s, time.Now())
			r.Need[WALBytes] = limit - 32 + 1
			if w.reserve(r, time.Now()) != Capacity {
				t.Fatal("first header growth was not reserved", class, physical)
			}
			publish(t, w, s, time.Now())
			r.Need[WALBytes] = limit - 32
			if w.reserve(r, time.Now()) != 0 {
				t.Fatal("exact empty headroom refused", class, physical)
			}
			w.returned()
		}
	}
}
