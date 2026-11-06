package workload

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Synthetic values-only controls; no evidence of native latency/reset/outcomes.
func syntheticObservation() Observation {
	start := time.Now()
	holdStart := start.Add(time.Millisecond)
	commitStart := start.Add(2 * time.Millisecond)
	commitReturn := start.Add(3 * time.Millisecond)
	physical := start.Add(4 * time.Millisecond)
	returned := start.Add(5 * time.Millisecond)
	e := &writer.ExecutionEvidence{Purpose: writer.InventoryAccept, Class: governor.ScanMinimum, Durability: writer.Normal,
		HoldStartedAt: holdStart, PhysicalReturnedAt: &physical, ReturnedAt: returned, CommitAttemptedAt: &commitStart, CommitReturnedAt: &commitReturn, Commit: writer.CommitSucceeded,
		Policy: &sqliteconn.Settings{Engine: "3.53.4", TempStore: 1, JournalMode: "wal", ForeignKeys: 1, Synchronous: 1, CacheSize: -1024}, WALBefore: &writer.WALObservation{ObservedAt: start}, WALAfter: &writer.WALObservation{ObservedAt: returned}}
	return capture("ACCEPT", writer.InventoryAccept, writer.ReceiptOutcome{Evidence: e}, nil, nil, start, returned.Add(time.Millisecond))
}
func syntheticJoin(n uint64) Classification {
	fenced := false
	return Classification{Ordinal: n, UnitID: fmt.Sprintf("control:%d", n+1), Recipe: "InventoryAccept/ACCEPT", EvidenceRef: "synthetic-only:immutable-line", Position: qual.WALAppend, Fenced: &fenced}
}
func syntheticRun(observations ...Observation) []Record {
	c := testConfig()
	raw := []Record{{Kind: "start", Duration: time.Duration(max(1, len(observations))) * time.Millisecond, Streams: []Schedule{{Name: "writer", Every: time.Millisecond, Pattern: []string{"control"}}}}}
	for i := range observations {
		raw = append(raw, Record{Kind: "operation", Observation: &observations[i], Offer: &Offer{Stream: "writer", Slot: uint64(i + 1)}})
	}
	raw = append(raw, Record{Kind: "stream_end", Progress: &StreamProgress{Name: "writer", Scheduled: uint64(max(1, len(observations))), Offered: uint64(len(observations)), Calls: uint64(len(observations))}})
	raw = append(raw, Record{Kind: "final", DriversReturned: true, ScheduleComplete: true})
	for i := range raw {
		raw[i].Version = Version
		raw[i].Label = "DIRECTIONAL"
		raw[i].RunID = "control"
		raw[i].Cell = c.Cell
		raw[i].Sequence = uint64(i + 1)
		raw[i].UnitID = fmt.Sprintf("control:%d", i+1)
	}
	return raw
}

func TestWorkloadReducerCompleteAndUnknowns(t *testing.T) {
	cell := testConfig().Cell
	o := syntheticObservation()
	sample, err := RestartSample(cell, o, syntheticJoin(1))
	if err != nil || sample.Hold != 3*time.Millisecond || sample.Commit != time.Millisecond || sample.Class != "ScanMinimum" || sample.Durability != "NORMAL" || sample.Outcome != "committed" {
		t.Fatal(sample, err)
	}
	raw := syntheticRun(o)
	coverage := Reduce(cell, raw, map[string]Classification{raw[1].UnitID: syntheticJoin(1)})
	if coverage.Report == nil || coverage.Report.Attempts != 1 || coverage.Report.Commits != 1 {
		t.Fatal(coverage)
	}
	// Preserve a physically known failed-first attempt and unknown outcome/fence.
	o = syntheticObservation()
	o.Evidence.Commit = writer.CommitUnknown
	join := syntheticJoin(1)
	join.Position = qual.WALRestartFirst
	join.OutcomeReason = "synthetic uncertain COMMIT"
	fenced := true
	join.Fenced = &fenced
	sample, err = RestartSample(cell, o, join)
	if err != nil || !sample.Fenced || sample.Outcome != "unknown" {
		t.Fatal(sample, err)
	}
	report, err := qual.SummarizeRestarts(cell, []qual.RestartSample{sample}, 0)
	if err != nil || report.Restarts != 1 || report.Strata.Failed.Holds.Count != 1 || report.Strata.AllAttempts.Holds.Count != 1 || report.RestartsPerCommit != nil {
		t.Fatal(report, err)
	}
}

func TestWorkloadReducerRejectsNullableAndMissingCommit(t *testing.T) {
	for _, mutate := range []func(*Observation, *Classification){
		func(o *Observation, _ *Classification) { o.Evidence = nil },
		func(o *Observation, _ *Classification) { o.HoldNS = nil },
		func(o *Observation, _ *Classification) { o.CommitNS = nil },
		func(o *Observation, _ *Classification) { o.Evidence.Policy = nil },
		func(o *Observation, _ *Classification) { o.Evidence.Policy.ForeignKeys = 0 },
		func(o *Observation, _ *Classification) { o.Evidence.Commit = writer.CommitNotAttempted },
		func(o *Observation, _ *Classification) { o.Evidence.WALAfter = nil },
		func(o *Observation, _ *Classification) { o.Stub = true },
		func(_ *Observation, j *Classification) { j.Fenced = nil },
		func(_ *Observation, j *Classification) { j.Position = qual.WALUnknown },
		func(_ *Observation, j *Classification) { j.EvidenceRef = "" },
	} {
		o, j := syntheticObservation(), syntheticJoin(1)
		mutate(&o, &j)
		if _, err := RestartSample(testConfig().Cell, o, j); err == nil {
			t.Fatal("incomplete observation projected")
		}
	}
	// Absence is zero only when the owner actually proves no COMMIT attempt.
	o, j := syntheticObservation(), syntheticJoin(1)
	o.Evidence.Commit = writer.CommitNotAttempted
	o.Evidence.CommitAttemptedAt = nil
	o.Evidence.CommitReturnedAt = nil
	o.CommitNS = nil
	j.OutcomeReason = "synthetic failure before COMMIT"
	sample, err := RestartSample(testConfig().Cell, o, j)
	if err != nil || sample.Commit != 0 || sample.Outcome != "rolled_back" {
		t.Fatal(sample, err)
	}
}

func TestWorkloadCoverageNeverRenumbersAroundHole(t *testing.T) {
	cell := testConfig().Cell
	raw := syntheticRun(syntheticObservation(), Observation{Phase: "ACCEPT", CallError: "zero public result on error"}, syntheticObservation())
	joins := map[string]Classification{}
	for i := range 3 {
		joins[raw[i+1].UnitID] = syntheticJoin(uint64(i + 1))
	}
	// Same ordinal map, hole has no native evidence. Successes cannot become 1,2.
	c := Reduce(cell, raw, joins)
	if c.Report != nil || c.Unprojectable != 1 || c.WriterCalls != 3 || c.PhysicalAttempts != 2 {
		t.Fatal(c)
	}
	// Even if someone renumbers after dropping the middle join, coverage fails.
	delete(joins, raw[2].UnitID)
	j := syntheticJoin(2)
	j.UnitID = raw[3].UnitID
	joins[raw[3].UnitID] = j
	if c = Reduce(cell, raw, joins); c.Report != nil || c.Unprojectable == 0 {
		t.Fatal(c)
	}
	for _, mutate := range []func([]Record){
		func(r []Record) { r[len(r)-1].ScheduleComplete = false },
		func(r []Record) { r[1].Cell.DatabaseID = "different-db" },
		func(r []Record) { r[1].Observation.Stub = true },
		func(r []Record) { r[1].Sequence = 999 },
	} {
		raw = syntheticRun(syntheticObservation())
		mutate(raw)
		if c = Reduce(cell, raw, map[string]Classification{raw[1].UnitID: syntheticJoin(1)}); c.Report != nil {
			t.Fatal(c)
		}
	}
	for _, raw = range [][]Record{nil, syntheticRun()} {
		if c = Reduce(cell, raw, nil); c.Report != nil {
			t.Fatal(c)
		}
	}
}

func TestWorkloadCaptureErrorRetainsOriginalAndNoInventedEvidence(t *testing.T) {
	original := writer.ReceiptReference{Identity: writer.CommandIdentity{Sequence: 17}}
	o := captureRequested(original, "ACCEPT", writer.InventoryAccept, writer.ReceiptOutcome{}, writer.InventoryResult{}, writer.ErrRefused, time.Now(), time.Now())
	if o.Requested == nil || *o.Requested != original || !o.RefusalError || o.Evidence != nil || o.HoldNS != nil || o.CommitNS != nil {
		t.Fatal(o)
	}
	if _, err := RestartSample(testConfig().Cell, o, syntheticJoin(1)); err == nil {
		t.Fatal("zero facade error became rollback/refusal evidence")
	}
	start := time.Now()
	end := start.Add(time.Millisecond)
	// Physical return outside this call cannot be repaired by facade wall time.
	o = syntheticObservation()
	bad := end.Add(time.Second)
	o.Evidence.PhysicalReturnedAt = &bad
	o = capture("ACCEPT", writer.InventoryAccept, writer.ReceiptOutcome{Evidence: o.Evidence}, nil, errors.New("synthetic failure"), start, end)
	if o.HoldNS != nil || len(o.EvidenceProblems) == 0 {
		t.Fatal(o)
	}
}
