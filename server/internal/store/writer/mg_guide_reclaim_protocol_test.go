package writer

import (
	"errors"
	"testing"
)

func mgReclaimStateFixture() mgGuideReclaimState {
	return mgGuideReclaimState{
		request: "guide-request", source: "source", generation: "new", status: "staging",
		incarnation: [16]byte{1}, family: mgGuideReclaimNone, sequence: 4, acknowledged: 4, revision: 4,
	}
}

func mgReclaimIdentity(s mgGuideReclaimState) CommandIdentity {
	return CommandIdentity{Incarnation: s.incarnation, Sequence: s.sequence + 1, OperationID: [16]byte{9}}
}

func TestMGGuideReclaimProtocolDiscardsOnlyStagingIntent(t *testing.T) {
	s := mgReclaimStateFixture()
	next, err := planMGGuideDiscard(s, mgReclaimIdentity(s), "old")
	if err != nil || next.family != mgGuideReclaimProgrammes || next.cursor != "" || next.sequence != 5 || next.revision != 5 {
		t.Fatal("discard intent", next, err)
	}
	for _, test := range []struct {
		name  string
		stale func(*mgGuideReclaimState)
		head  string
		id    func(*CommandIdentity)
	}{
		{"current head", func(*mgGuideReclaimState) {}, "new", func(*CommandIdentity) {}},
		{"published", func(s *mgGuideReclaimState) { s.status = "published" }, "old", func(*CommandIdentity) {}},
		{"already superseded", func(s *mgGuideReclaimState) { s.status = "superseded" }, "old", func(*CommandIdentity) {}},
		{"cleanup already started", func(s *mgGuideReclaimState) { s.family = mgGuideReclaimProgrammes }, "old", func(*CommandIdentity) {}},
		{"cursor not clean", func(s *mgGuideReclaimState) { s.cursor = "programme-01" }, "old", func(*CommandIdentity) {}},
		{"above-floor receipt", func(s *mgGuideReclaimState) { s.acknowledged = 3 }, "old", func(*CommandIdentity) {}},
		{"sequence overflow", func(s *mgGuideReclaimState) { s.sequence, s.acknowledged = 1<<63-1, 1<<63-1 }, "old", func(id *CommandIdentity) { id.Sequence = 0 }},
		{"forged incarnation", func(*mgGuideReclaimState) {}, "old", func(id *CommandIdentity) { id.Incarnation = [16]byte{7} }},
		{"empty operation", func(*mgGuideReclaimState) {}, "old", func(id *CommandIdentity) { id.OperationID = [16]byte{} }},
		{"reused sequence", func(*mgGuideReclaimState) {}, "old", func(id *CommandIdentity) { id.Sequence = 4 }},
		{"nul head", func(*mgGuideReclaimState) {}, "o\x00ld", func(*CommandIdentity) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := s
			test.stale(&state)
			id := mgReclaimIdentity(state)
			test.id(&id)
			out, err := planMGGuideDiscard(state, id, test.head)
			if !errors.Is(err, ErrConflict) || out != (mgGuideReclaimTransition{}) {
				t.Fatal("discard admitted", out, err)
			}
		})
	}
}

func TestMGGuideReclaimProtocolBoundsOneSlice(t *testing.T) {
	s := mgReclaimStateFixture()
	s.status, s.family, s.cursor = "superseded", mgGuideReclaimProgrammes, "programme-07"
	page := mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", rows: 2, bytes: 900, last: "programme-09"}
	next, err := planMGGuideReclaimStep(s, mgReclaimIdentity(s), page)
	if err != nil || next.family != mgGuideReclaimProgrammes || next.cursor != "programme-09" || next.sequence != 5 {
		t.Fatal("partial page", next, err)
	}
	// An exhausted range advances the family as its OWN command and resets the
	// exact cursor; it never deletes the generation in the same step.
	empty := mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07"}
	next, err = planMGGuideReclaimStep(s, mgReclaimIdentity(s), empty)
	if err != nil || next.family != mgGuideReclaimChannels || next.cursor != "" || next.sequence != 5 {
		t.Fatal("family advance", next, err)
	}
	for _, test := range []struct {
		name  string
		stale func(*mgGuideReclaimState)
		page  mgGuideReclaimPage
	}{
		{"nine rows", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", rows: 9, bytes: 100, last: "programme-16"}},
		{"bytes over cap", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", rows: 1, bytes: mgGuideReclaimBytes + 1, last: "programme-08"}},
		{"cursor did not move", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", rows: 1, bytes: 10, last: "programme-07"}},
		{"page after mismatch", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-00", rows: 1, bytes: 10, last: "programme-08"}},
		{"lookahead on a short page", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", rows: 2, bytes: 10, more: true, last: "programme-09"}},
		{"empty page with bytes", func(*mgGuideReclaimState) {}, mgGuideReclaimPage{family: mgGuideReclaimProgrammes, after: "programme-07", bytes: 1}},
		{"generation family", func(s *mgGuideReclaimState) { s.family, s.cursor = mgGuideReclaimGeneration, "" }, mgGuideReclaimPage{family: mgGuideReclaimGeneration}},
		{"complete family", func(s *mgGuideReclaimState) { s.family, s.cursor = mgGuideReclaimComplete, "" }, mgGuideReclaimPage{family: mgGuideReclaimComplete}},
		{"none family", func(s *mgGuideReclaimState) { s.family, s.cursor = mgGuideReclaimNone, "" }, mgGuideReclaimPage{family: mgGuideReclaimNone}},
		{"staging status", func(s *mgGuideReclaimState) { s.status = "staging" }, page},
		{"above-floor receipt", func(s *mgGuideReclaimState) { s.acknowledged = 3 }, page},
		{"revision overflow", func(s *mgGuideReclaimState) { s.revision = 1<<63 - 1 }, page},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := s
			test.stale(&state)
			id := mgReclaimIdentity(state)
			out, err := planMGGuideReclaimStep(state, id, test.page)
			if !errors.Is(err, ErrConflict) || out != (mgGuideReclaimTransition{}) {
				t.Fatal("slice admitted", out, err)
			}
		})
	}
	// A caller-minted identity under another incarnation is not this lineage.
	forged := mgReclaimIdentity(s)
	forged.Incarnation = [16]byte{9}
	if next, err := planMGGuideReclaimStep(s, forged, page); !errors.Is(err, ErrConflict) || next != (mgGuideReclaimTransition{}) {
		t.Fatal("slice admitted another incarnation", next, err)
	}
}

func TestMGGuideReclaimProtocolKeepsExternalDeletionClosed(t *testing.T) {
	s := mgReclaimStateFixture()
	s.status, s.family, s.cursor = "superseded", mgGuideReclaimGeneration, ""
	blocked, err := planMGGuideReclaimGeneration(s, mgReclaimIdentity(s), 0)
	if err != nil || blocked != mgGuideReclaimBlockedExternal {
		t.Fatal("external gate", blocked, err)
	}
	if _, err := planMGGuideReclaimStep(s, mgReclaimIdentity(s), mgGuideReclaimPage{family: mgGuideReclaimGeneration}); !errors.Is(err, ErrConflict) {
		t.Fatal("step planner opened the generation family", err)
	}
	for _, test := range []struct {
		name     string
		children int64
		stale    func(*mgGuideReclaimState)
	}{
		{"children remain", 3, func(*mgGuideReclaimState) {}},
		{"negative children", -1, func(*mgGuideReclaimState) {}},
		{"wrong family", 0, func(s *mgGuideReclaimState) { s.family = mgGuideReclaimChoices }},
		{"cursor must be empty", 0, func(s *mgGuideReclaimState) { s.cursor = "choice-01" }},
		{"above-floor receipt", 0, func(s *mgGuideReclaimState) { s.acknowledged = 3 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := s
			test.stale(&state)
			out, err := planMGGuideReclaimGeneration(state, mgReclaimIdentity(state), test.children)
			if err == nil || out != "" {
				t.Fatal("generation gate admitted", out, err)
			}
		})
	}
}
