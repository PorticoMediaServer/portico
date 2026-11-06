package qual

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func restartFixture(n int) (RestartCell, []RestartSample) {
	c := RestartCell{"cell1", "host1", "reference", "db1", strings.Repeat("a", 64), "warm", "realistic"}
	ss := make([]RestartSample, n)
	for i := range ss {
		ss[i] = RestartSample{CellID: c.ID, UnitID: fmt.Sprint(i), Recipe: "scan.accept", Class: "ScanMinimum", Durability: "NORMAL", Ordinal: uint64(i + 1), Outcome: "committed", Position: WALAppend, Hold: time.Millisecond, Commit: time.Microsecond, EvidenceRef: fmt.Sprintf("raw:%d", i)}
	}
	return c, ss
}

func TestRestartReportRetainsTailsAndActualClass(t *testing.T) {
	c, ss := restartFixture(100)
	ss[0].Position = WALCreated
	ss[1].Position = WALNoFrames
	ss[50].Position, ss[50].Hold, ss[50].Commit = WALRestartFirst, 40*time.Millisecond, 35*time.Millisecond
	ss[50].Class, ss[50].IntendedClass, ss[50].Durability = "Foreground", "Background", "FULL"
	ss[99].Position, ss[99].Hold, ss[99].Commit = WALRestartFirst, 30*time.Millisecond, 28*time.Millisecond
	before := append([]RestartSample(nil), ss...)
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, ss) {
		t.Fatal("mutated raw input")
	}
	if r.Restarts != 2 || r.Creations != 1 || r.Commits != 100 || r.FrameCommits != 99 || r.NoFrameCommits != 1 || !r.ClassificationComplete || r.RestartsPerCommit == nil || *r.RestartsPerCommit != .02 || *r.RestartsPer1000Commits != 20 {
		t.Fatalf("cadence lost actual counts: %+v", r)
	}
	all, first, rest := r.Strata.Committed.Holds, r.Strata.PostRestart.Holds, r.Strata.Rest.Holds
	if all.Count != 100 || all.P99 != 30*time.Millisecond || all.Max != 40*time.Millisecond || all.Above10MS != 2 || all.Above25MS != 2 || all.P999 != nil {
		t.Fatalf("overall dropped restart tails or claimed small p999: %+v", all)
	}
	if first.Count != 2 || first.Max != all.Max || first.P999 != nil || rest.Count != 98 || rest.Max != time.Millisecond || r.Strata.Unclassified.Holds != nil {
		t.Fatalf("wrong conditional populations: %+v", r.Strata)
	}
	if r.Strata.PostRestart.Commits.Max != 35*time.Millisecond || len(r.ByClass) != 2 || r.ByClass[0].Class != "Foreground" || r.ByClass[0].Restarts != 1 || r.ByClass[0].Strata.Committed.Holds.Max != all.Max {
		t.Fatalf("lost per-class tail: %+v", r.ByClass)
	}
	checkAllAttempts := func(label string, got RestartLatency, count int, holdP99, holdMax, commitP99, commitMax time.Duration, exceedances int) {
		t.Helper()
		if got.Holds == nil || got.Commits == nil || got.Holds.Count != count || got.Commits.Count != count ||
			got.Holds.P99 != holdP99 || got.Holds.Max != holdMax || got.Commits.P99 != commitP99 || got.Commits.Max != commitMax ||
			got.Holds.Above10MS != exceedances || got.Holds.Above25MS != exceedances || got.Commits.Above10MS != exceedances || got.Commits.Above25MS != exceedances {
			t.Fatalf("AllAttempts dropped successful restart tails in %s: %+v", label, got)
		}
	}
	checkAllAttempts("overall", r.Strata.AllAttempts, 100, 30*time.Millisecond, 40*time.Millisecond, 28*time.Millisecond, 35*time.Millisecond, 2)
	checkAllAttempts("Foreground/FULL", r.ByClass[0].Strata.AllAttempts, 1, 40*time.Millisecond, 40*time.Millisecond, 35*time.Millisecond, 35*time.Millisecond, 1)
	checkAllAttempts("ScanMinimum/NORMAL", r.ByClass[1].Strata.AllAttempts, 99, 30*time.Millisecond, 30*time.Millisecond, 28*time.Millisecond, 28*time.Millisecond, 1)
	if len(r.Events) != 3 || r.Events[1].UnitID != ss[50].UnitID || r.Events[1].Class != "Foreground" || r.Events[1].IntendedClass != "Background" || r.Events[1].EvidenceRef != "raw:50" {
		t.Fatalf("actual absorber became intended class: %+v", r.Events)
	}
}

func TestRestartUnknownsAndFailuresCannotBecomeWarmCommits(t *testing.T) {
	c, ss := restartFixture(5)
	ss[0].Position = WALCreated
	ss[1].Position, ss[1].UnknownReason, ss[1].Hold = WALUnknown, "sampling gap", 90*time.Millisecond
	ss[2].Position, ss[2].Outcome, ss[2].OutcomeReason = WALRestartFirst, "rolled_back", "native failure after header"
	ss[2].Hold, ss[2].Commit, ss[2].Fenced = 100*time.Millisecond, 0, true
	ss[3].Outcome, ss[3].OutcomeReason, ss[3].Hold = "unknown", "ambiguous COMMIT", 110*time.Millisecond
	r, err := SummarizeRestarts(c, ss, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.ClassificationComplete || r.RestartsPerCommit != nil || r.RestartsPer1000Commits != nil || r.Refusals != 3 || r.Fences != 1 || r.Restarts != 1 || r.UnknownPositions != 1 || r.UnknownOutcomes != 1 || r.Commits != 3 || r.FrameCommits != 2 {
		t.Fatalf("incomplete cadence became measured zero/ratio: %+v", r)
	}
	if r.Strata.AllAttempts.Holds.Count != 5 || r.Strata.AllAttempts.Holds.Max != 110*time.Millisecond || r.Strata.Committed.Holds.Max != 90*time.Millisecond || r.Strata.Unclassified.Holds.Count != 1 || r.Strata.Rest.Holds.Count != 2 || r.Strata.PostRestart.Holds != nil || r.Strata.Failed.Holds.Count != 2 || len(r.Events) != 4 {
		t.Fatalf("unknown/failed hold disappeared or padded rest: %+v", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(encoded), `"RestartsPerCommit":null`) || !strings.Contains(string(encoded), `"P999":null`) {
		t.Fatalf("missing evidence serialized as zero: %s %v", encoded, err)
	}
	var decoded RestartReport
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(r, decoded) {
		t.Fatal("wire evidence lost", err)
	}
}

func TestRestartFencedCommittedAppendsRetainEventsAndClass(t *testing.T) {
	c, ss := restartFixture(3)
	ss[0].Fenced, ss[0].Class, ss[0].Durability = true, "Foreground", "FULL"
	ss[0].IntendedClass, ss[0].Hold, ss[0].Commit = "Background", 40*time.Millisecond, 35*time.Millisecond
	ss[1].Fenced, ss[1].Class = true, "Background"
	ss[1].Hold, ss[1].Commit = 12*time.Millisecond, 11*time.Millisecond
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fences != 2 || r.Commits != 3 || r.Strata.Failed.Holds != nil || !reflect.DeepEqual(r.Events, ss[:2]) {
		t.Fatalf("fenced committed append lost event identity or true outcome: %+v", r)
	}
	if len(r.ByClass) != 3 {
		t.Fatal("missing class breakdown", r.ByClass)
	}
	for _, g := range r.ByClass {
		wantFences := 0
		if g.Class == "Background" || g.Class == "Foreground" {
			wantFences = 1
		}
		if g.Fences != wantFences || g.Strata.Committed.Holds == nil || g.Strata.Committed.Holds.Count != 1 || g.Strata.Failed.Holds != nil {
			t.Fatalf("fence lost per-class count or committed attribution: %+v", g)
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RestartReport
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(r, decoded) {
		t.Fatal("fence event/class evidence lost on wire", err)
	}
}

func assertRestartCadenceUnavailable(t *testing.T, r RestartReport) {
	t.Helper()
	if r.ClassificationComplete || r.RestartsPerCommit != nil || r.RestartsPer1000Commits != nil {
		t.Fatalf("single unknown published complete numeric cadence: %+v", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(encoded), `"RestartsPerCommit":null`) || !strings.Contains(string(encoded), `"RestartsPer1000Commits":null`) {
		t.Fatalf("unknown cadence ratios must both stay null: %s %v", encoded, err)
	}
}

func TestRestartPositionUnknownAloneWithholdsCadence(t *testing.T) {
	c, ss := restartFixture(2)
	ss[1].Position, ss[1].UnknownReason = WALUnknown, "position observation gap"
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertRestartCadenceUnavailable(t, r)
	if r.UnknownPositions != 1 || r.UnknownOutcomes != 0 || r.Commits != 2 || r.FrameCommits != 1 || r.Strata.Committed.Holds.Count != 2 || r.Strata.Unclassified.Holds == nil || r.Strata.Unclassified.Holds.Count != 1 || r.Strata.Failed.Holds != nil || len(r.Events) != 1 || r.Events[0] != ss[1] {
		t.Fatalf("position-only unknown lost its population or identity: %+v", r)
	}
}

func TestRestartOutcomeUnknownAloneWithholdsCadence(t *testing.T) {
	c, ss := restartFixture(2)
	ss[1].Outcome, ss[1].OutcomeReason = "unknown", "ambiguous commit return"
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertRestartCadenceUnavailable(t, r)
	if r.UnknownPositions != 0 || r.UnknownOutcomes != 1 || r.Commits != 1 || r.FrameCommits != 1 || r.Strata.Committed.Holds.Count != 1 || r.Strata.Unclassified.Holds != nil || r.Strata.Failed.Holds == nil || r.Strata.Failed.Holds.Count != 1 || len(r.Events) != 1 || r.Events[0] != ss[1] {
		t.Fatalf("outcome-only unknown lost its population or identity: %+v", r)
	}
}

func TestRestartFailedFirstWriteRetainsEventAndNoDenominator(t *testing.T) {
	c, ss := restartFixture(1)
	ss[0].Outcome, ss[0].OutcomeReason, ss[0].Position = "rolled_back", "failed first write", WALRestartFirst
	ss[0].Commit, ss[0].Hold = 0, 30*time.Millisecond
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Restarts != 1 || r.Commits != 0 || r.RestartsPerCommit != nil || r.Strata.Committed.Holds != nil || r.Strata.Failed.Holds.Max != ss[0].Hold || len(r.Events) != 1 {
		t.Fatalf("failed first write moved to successful population: %+v", r)
	}
}

func TestRestartKnownZeroCadenceIsDistinctFromUnavailable(t *testing.T) {
	c, ss := restartFixture(2)
	ss[0].Position = WALCreated
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil || r.Kind != "restart-cadence-descriptive-only" || !r.ClassificationComplete || r.Restarts != 0 || r.RestartsPerCommit == nil || *r.RestartsPerCommit != 0 || r.Creations != 1 || r.Strata.PostRestart.Holds != nil {
		t.Fatalf("known zero cadence confused with missing/reset evidence: %+v %v", r, err)
	}
}

func TestRestartP999NeedsOwnPopulation(t *testing.T) {
	c, ss := restartFixture(Phase1HoldsPerHost)
	ss[len(ss)-1].Position, ss[len(ss)-1].Hold = WALRestartFirst, 80*time.Millisecond
	r, err := SummarizeRestarts(c, ss, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Strata.Committed.Holds.P999 == nil || r.Strata.Committed.Holds.Max != 80*time.Millisecond || r.Strata.PostRestart.Holds.P999 != nil || r.Strata.Rest.Holds.P999 != nil {
		t.Fatalf("subgroup borrowed overall population or tail lost: %+v", r.Strata)
	}
}

func TestRestartReportRejectsMalformedAndMixedCells(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RestartCell, []RestartSample)
	}{
		{"host", func(c *RestartCell, _ []RestartSample) { c.HostID = "" }},
		{"manifest", func(c *RestartCell, _ []RestartSample) { c.ManifestSHA256 = strings.Repeat("z", 64) }},
		{"cadence", func(c *RestartCell, _ []RestartSample) { c.CadenceMode = "" }},
		{"mixed_cell", func(_ *RestartCell, ss []RestartSample) { ss[1].CellID = "other-host-or-cohort" }},
		{"duplicate_unit", func(_ *RestartCell, ss []RestartSample) { ss[1].UnitID = ss[0].UnitID }},
		{"duplicate_ordinal", func(_ *RestartCell, ss []RestartSample) { ss[1].Ordinal = ss[0].Ordinal }},
		{"out_of_order", func(_ *RestartCell, ss []RestartSample) { ss[0].Ordinal = 10 }},
		{"omitted_attempt", func(_ *RestartCell, ss []RestartSample) { ss[1].Ordinal = 3 }},
		{"missing_evidence", func(_ *RestartCell, ss []RestartSample) { ss[1].EvidenceRef = "" }},
		{"missing_hold", func(_ *RestartCell, ss []RestartSample) { ss[1].Hold = 0 }},
		{"commit_outside_hold", func(_ *RestartCell, ss []RestartSample) { ss[1].Commit = ss[1].Hold + 1 }},
		{"missing_commit", func(_ *RestartCell, ss []RestartSample) { ss[1].Commit = 0 }},
		{"invented_class", func(_ *RestartCell, ss []RestartSample) { ss[1].Class = "idle" }},
		{"invented_intent", func(_ *RestartCell, ss []RestartSample) { ss[1].IntendedClass = "idle" }},
		{"durability", func(_ *RestartCell, ss []RestartSample) { ss[1].Durability = "OFF" }},
		{"unknown_position", func(_ *RestartCell, ss []RestartSample) { ss[1].Position = "checkpoint_completed" }},
		{"missing_reason", func(_ *RestartCell, ss []RestartSample) { ss[1].Position = WALUnknown }},
		{"missing_outcome", func(_ *RestartCell, ss []RestartSample) { ss[1].Outcome = "" }},
		{"missing_failure_reason", func(_ *RestartCell, ss []RestartSample) { ss[1].Outcome = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ss := restartFixture(2)
			tc.mutate(&c, ss)
			if _, err := SummarizeRestarts(c, ss, 0); err == nil {
				t.Fatal("malformed or mixed evidence accepted")
			}
		})
	}
	c, ss := restartFixture(1)
	if _, err := SummarizeRestarts(c, nil, 0); err == nil {
		t.Fatal("empty cell accepted")
	}
	if _, err := SummarizeRestarts(c, ss, -1); err == nil {
		t.Fatal("negative refusals accepted")
	}
}
