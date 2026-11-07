package writer

import (
	"errors"
	"testing"
	"time"
)

func TestMPNextEdgesAndCurrentCustody(t *testing.T) {
	l := mpTestLayout(2)
	l.head.position = 1
	l.head.entry = "snap-1"
	if _, _, err := mpPlanAdvance(l, mpAdvanceInput{reason: "completion"}); !errors.Is(err, errMPEnd) {
		t.Fatal(err)
	}
	l.head.repeat = "one"
	next, changed, err := mpPlanAdvance(l, mpAdvanceInput{reason: "completion"})
	if err != nil || !changed || next.head.position != 1 {
		t.Fatal(next.head, changed, err)
	}
	l.head.repeat = "all"
	next, changed, err = mpPlanAdvance(l, mpAdvanceInput{reason: "next"})
	if err != nil || !changed || next.head.position != 0 {
		t.Fatal(next.head, changed, err)
	}
	base := time.UnixMilli(1000)
	a := mpActor{family: "family", device: "device", account: "account", profile: "profile"}
	l.head.session = "session"
	s := mpNextSession{id: "session", device: a.device, account: a.account, profile: a.profile, authority: "local", queue: "queue", entry: l.head.entry, state: "playing", media: "media", generation: 2, mediaGeneration: 3, lease: 2000}
	p := mpNextPrepared{family: a.family, device: a.device, currentEntry: l.head.entry, queueRevision: l.head.revision, custody: l.head.custody, sessionGeneration: 2, sessionState: "playing", expires: 1500, predecessorMedia: "media", predecessorMediaGeneration: 3}
	if reason := mpPreparedFence(l.head, s, p, a, base); reason != "" {
		t.Fatal(reason)
	}
	for name, mutate := range map[string]func(*mpQueueHead, *mpNextSession, *mpNextPrepared){"custody": func(h *mpQueueHead, _ *mpNextSession, _ *mpNextPrepared) { h.custody++ }, "source_session": func(_ *mpQueueHead, s *mpNextSession, _ *mpNextPrepared) { s.generation++ }, "physical_generation": func(_ *mpQueueHead, s *mpNextSession, _ *mpNextPrepared) { s.mediaGeneration++ }, "state": func(_ *mpQueueHead, s *mpNextSession, _ *mpNextPrepared) { s.state = "paused" }, "expiry": func(_ *mpQueueHead, _ *mpNextSession, p *mpNextPrepared) { p.expires = 1000 }, "family": func(_ *mpQueueHead, _ *mpNextSession, p *mpNextPrepared) { p.family = "other" }} {
		t.Run(name, func(t *testing.T) {
			h, session, prepared := l.head, s, p
			mutate(&h, &session, &prepared)
			if mpPreparedFence(h, session, prepared, a, base) == "" {
				t.Fatal("stale prepared commit admitted")
			}
		})
	}
}
func TestMPNextStopIsPhysicalAndExact(t *testing.T) {
	defs := mpNextDefinitions()
	byID := map[string]string{}
	for _, d := range defs {
		byID[string(d.ID)] = d.SQL
	}
	for _, part := range []string{"media_generation=?", "request_id=?", "exited_ms IS NULL", "playback_sessions", "mp_retired_generation", "mp_exited_generation", "mp_retire_request_id", "mp_exit_request_id", "mp_exit_supervisor_incarnation=?", "mp_exited_ms=?"} {
		if !containsMP(byID["mp.next.exit"], part) {
			t.Fatal("physical-exit predicate removed", part)
		}
	}
	if containsMP(byID["mp.next.cancel"], "DELETE") || containsMP(byID["mp.next.cancel"], "exited_ms") {
		t.Fatal("logical cancellation releases physical custody")
	}
}

func TestMPNextBoundedWithheldSelection(t *testing.T) {
	l := mpTestLayout(1000)
	options, err := mpAdvanceOptions(l, mpAdvanceInput{reason: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 201 || options[0].entry != "snap-1" || options[200].entry != "snap-201" {
		t.Fatal("withheld walk lost its public bound")
	}
	fixed, err := mpAdvanceOptions(l, mpAdvanceInput{reason: "entry", entry: "snap-90"})
	if err != nil || len(fixed) != 1 || fixed[0].entry != "snap-90" {
		t.Fatal("named target silently skipped", fixed, err)
	}
	l.head.repeat = "one"
	fixed, err = mpAdvanceOptions(l, mpAdvanceInput{reason: "completion"})
	if err != nil || len(fixed) != 1 || fixed[0].entry != "snap-0" {
		t.Fatal("repeat-one silently skipped", fixed, err)
	}
}

func TestMPNextPostPlayAttentionAndCounter(t *testing.T) {
	p := mpPostPlayValues{revision: 1, episodes: 3, autoplay: true, protection: true}
	if n, err := mpPostPlayCounter(1, true, false, p); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if _, err := mpPostPlayCounter(2, true, false, p); !errors.Is(err, errMPAttention) {
		t.Fatal("unattended edge escaped attention", err)
	}
	if n, err := mpPostPlayCounter(2, false, true, p); err != nil || n != 0 {
		t.Fatal("listening inherited episode counter", n, err)
	}
	p.autoplay = false
	if n, err := mpPostPlayCounter(2, true, false, p); err != nil || n != 3 {
		t.Fatal("manual edge inherited autoplay attention", n, err)
	}
}
func containsMP(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestMPPhysicalExitRequiresOriginalRequestAndTrustedRoot(t *testing.T) {
	supervisor := [16]byte{9}
	f := mpStopExitFact{requestID: "original / 音", requested: 10, physical: mpPhysicalStopFact{retired: 4, exited: 4, requestID: "original / 音", exitRequestID: "original / 音", supervisor: supervisor[:], exitedMS: 20}}
	if !mpStopExitMatches(f, "original / 音", 4, 20, supervisor) {
		t.Fatal("matching physical exit refused")
	}
	for i, change := range []func(*mpStopExitFact){func(f *mpStopExitFact) { f.requestID = "trimmed" }, func(f *mpStopExitFact) { f.physical.requestID = "new request" }, func(f *mpStopExitFact) { f.physical.exitRequestID = "foreign" }, func(f *mpStopExitFact) { f.physical.retired++ }, func(f *mpStopExitFact) { f.physical.exited-- }, func(f *mpStopExitFact) { f.physical.supervisor = []byte{1} }, func(f *mpStopExitFact) { f.physical.exitedMS++ }, func(f *mpStopExitFact) { f.requested = 21 }} {
		bad := f
		change(&bad)
		if mpStopExitMatches(bad, "original / 音", 4, 20, supervisor) {
			t.Fatal("foreign/unexited physical debt accepted", i)
		}
	}
	f.exited.Valid = true
	f.exited.Int64 = 20
	if !mpStopExitMatches(f, "original / 音", 4, 20, supervisor) {
		t.Fatal("exact repeated exit not idempotent")
	}
	f.exited.Int64 = 19
	if mpStopExitMatches(f, "original / 音", 4, 20, supervisor) {
		t.Fatal("rewrote observed exit time")
	}
	pending := f.physical
	pending.exited = 3
	pending.exitRequestID = ""
	pending.supervisor = nil
	pending.exitedMS = 0
	if !mpPhysicalRetirementValid(pending, 4) {
		t.Fatal("logical retirement lost physical debt")
	}
}
