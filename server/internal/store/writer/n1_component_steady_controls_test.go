package writer

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func n1SteadyControlWire(t *testing.T, stream *n1Stream) []*n1Attempt {
	t.Helper()
	raw, err := os.ReadFile(stream.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	var records []*n1Attempt
	starts := map[string]bool{}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var envelope struct {
			Sequence int        `json:"sequence"`
			Kind     string     `json:"kind"`
			ID       string     `json:"attempt_id"`
			Attempt  *n1Attempt `json:"attempt"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Sequence != i+1 {
			t.Fatal("record ordinal gap")
		}
		switch envelope.Kind {
		case "attempt_start":
			if starts[envelope.ID] {
				t.Fatal("duplicate call identity")
			}
			starts[envelope.ID] = true
		case "attempt":
			if !starts[envelope.ID] || envelope.Attempt == nil {
				t.Fatal("physical result without original pre-custody start")
			}
			delete(starts, envelope.ID)
			records = append(records, envelope.Attempt)
		default:
			t.Fatal("unexpected functional wire kind", envelope.Kind)
		}
	}
	if len(starts) != 0 {
		t.Fatal("unjoined attempt starts", starts)
	}
	return records
}

func TestN1ComponentSteadyWireCountsOriginalsAndEveryPhysicalTurn(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in functional component control")
	}
	stream := n1ControlStream(t)
	clock := time.Now().Add(time.Hour) // protocol only, not timing evidence
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 2, 1, "native-counters", n1SteadyControls{clock: func() time.Time { return clock }}, summary)
	if !summary.Complete || summary.OpenedDatabases == nil || *summary.OpenedDatabases != 1 || summary.Warmup != (n1OriginalCensus{1, 1, 1}) || summary.Measured != (n1OriginalCensus{2, 2, 2}) || summary.KnownPhysicalTurns != 9 || len(summary.WarmupOriginals) != 1 || summary.NativeJoined == nil || !*summary.NativeJoined {
		t.Fatal("incorrect original/physical census", summary)
	}
	for role, c := range summary.Turns {
		if c.Started != 3 || c.Physical != 3 || c.Returned != 3 || c.Committed != 3 || c.Errors != 0 {
			t.Fatal(role, c)
		}
	}
	records := n1SteadyControlWire(t, stream)
	if len(records) != 9 || len(summary.NativeCloses) != 1 || !summary.NativeCloses[0].Completed {
		t.Fatal("missing turns/native final close")
	}
	var previous ReceiptReference
	for i, r := range records {
		role := []string{"fresh_begin", "resolve", "ack"}[i%3]
		if r.TurnRole != role || r.PhysicalAttemptOrdinal == nil || *r.PhysicalAttemptOrdinal != i+1 || r.OriginalOrdinal == nil || *r.OriginalOrdinal != i/3+1 || r.DatabaseID != summary.DatabaseID || !r.Complete || r.Clock.HoldNS == nil || r.Clock.CommitNS == nil || r.Settings.Actual == nil || r.Settings.ConnectionID == nil || *r.Settings.ConnectionID != 1 || r.BeginSucceeded == nil || !*r.BeginSucceeded {
			t.Fatal("lost call-bound owner facts", i, r)
		}
		if r.NativeJoined != nil || r.Continuity.NativeClosesAfter == nil || *r.Continuity.NativeClosesAfter != 0 {
			t.Fatal("per-turn native final close fiction", r)
		}
		if i%3 == 0 {
			if r.Reference == nil || reflect.DeepEqual(*r.Reference, previous) {
				t.Fatal("original reused")
			}
			previous = *r.Reference
		} else if r.Reference == nil || *r.Reference != previous {
			t.Fatal("Resolve/ACK changed original")
		}
		if r.Continuity.SteadyEligible != (i >= 3) || (i < 3 && r.OriginalPopulation != "warmup") || (i >= 3 && r.OriginalPopulation != "measured") {
			t.Fatal("warmup pooled into measured originals")
		}
		if r.WALBefore.Token == nil || r.WALBefore.TokenAfter == nil || r.WALBefore.GateBeforeNS == nil || r.WALAfter.GateAfterNS == nil {
			t.Fatal("actual zero token omitted")
		}
		ids := map[string]bool{}
		for _, u := range r.Units {
			if u.ID != "" {
				ids[string(u.ID)] = true
			}
			if (u.UnitID == string(scanReceiptOwner) || u.UnitID == string(scanReceiptAck)) && u.Phase != "domain" {
				t.Fatal("history unit absent from complete registry", u)
			}
		}
		switch role {
		case "fresh_begin":
			if !ids[string(scanReceiptOwner)] || !ids[string(inventoryFreshLibrary)] || r.Result == nil || r.Result.Outcome != (ReceiptOutcome{}) {
				t.Fatal("fresh guards/zero component result lost", r)
			}
		case "resolve":
			if !ids[string(scanReceiptResults)] || r.HistoricalResult == nil || r.HistoricalDigest == nil {
				t.Fatal("actual resolution absent", r)
			}
		case "ack":
			if !ids[string(scanReceiptAck)] || r.AcknowledgedThrough == nil || *r.AcknowledgedThrough != r.Reference.Identity.Sequence || r.CurrentSnapshot == nil || r.CurrentSnapshot.AcknowledgedSequence != *r.AcknowledgedThrough {
				t.Fatal("ACK/snapshot absent", r)
			}
		}
	}
}

func TestN1ComponentSteadyFailureIsStickyAndUnfiltered(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in functional component control")
	}
	stream := n1ControlStream(t)
	clock := time.Now().Add(time.Hour)
	var query string
	for _, def := range inventoryCommandDefinitions() {
		if def.ID == scanReceiptResults {
			query = def.SQL
		}
	}
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 2, 1, "native-counters", n1SteadyControls{clock: func() time.Time { return clock }, failOriginal: 2, failRole: "resolve", failQuery: query}, summary)
	records := n1SteadyControlWire(t, stream)
	if summary.Complete || summary.StopReason == "" || summary.Warmup.Complete != 1 || summary.Measured != (n1OriginalCensus{1, 1, 0}) || summary.KnownPhysicalTurns != 5 || len(records) != 5 || summary.Turns["resolve"].RolledBack != 1 || summary.Turns["ack"].Started != 1 {
		t.Fatal("failed measured original dropped/retried", summary)
	}
	failed := records[4]
	if failed.Error == "" || failed.Complete || failed.Outcome != "rolled_back" || failed.TurnRole != "resolve" || failed.Clock.HoldNS == nil || failed.Clock.CommitNotAttempted == nil || !*failed.Clock.CommitNotAttempted || failed.Reference == nil || *failed.Reference != *records[3].Reference {
		t.Fatal("failed original lost physical/identity evidence", failed)
	}
}

func TestN1ComponentSteadyPreBeginFailureKeepsNullOwner(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in functional component control")
	}
	stream := n1ControlStream(t)
	clock := time.Now().Add(time.Hour)
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 2, 1, "native-counters", n1SteadyControls{clock: func() time.Time { return clock }, failOriginal: 2, failRole: "fresh_begin", failQuery: "PRAGMA foreign_keys"}, summary)
	records := n1SteadyControlWire(t, stream)
	if summary.Complete || summary.Measured.FreshBegins != 0 || summary.KnownPhysicalTurns != 3 || len(records) != 4 || summary.Turns["fresh_begin"].Unobserved != 1 {
		t.Fatal("pre-BEGIN gap renumbered or counted as fresh", summary)
	}
	r := records[3]
	if r.PhysicalAttemptOrdinal != nil || r.BeginSucceeded != nil || r.Clock.HoldNS != nil || r.Settings.Actual != nil || r.Settings.PolicyVerified != nil || r.Error == "" || r.Settings.AssertError == "" || r.Outcome != "unobserved" {
		t.Fatal("unreturned owner view reconstructed", r)
	}
	if r.OwnerWindow == nil || !r.OwnerWindow.Bound || r.OwnerWindow.SetupComplete || r.OwnerWindow.Setup.ElapsedNS == nil || r.OwnerWindow.Terminal.ElapsedNS == nil || r.OwnerWindow.NativeReturnedNS == nil || r.OwnerWindow.CustodyRetiredNS == nil {
		t.Fatal("failed setup lost new call-bound cleanup/retirement observation")
	}
}
