package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Owner observer clock is always time.Now, even in a virtual admission-clock
// functional control. Never subtract one clock domain from the other.
var errN1Discard = errors.New("injected N1 discard cell rollback failure before native entry")

type n1OwnerWindow struct {
	Setup                 n1Interval `json:"setup"`
	Terminal              n1Interval `json:"terminal"`
	TerminalThroughNative n1Interval `json:"terminal_through_native_return"`
	Retirement            n1Interval `json:"custody_retirement"`
	Rollback              n1Interval `json:"rollback"`
	PoolClose             n1Interval `json:"quarantine_pool_close"`
	NativeReturnedNS      *int64     `json:"native_returned_ns"`
	ConnectionReturnedNS  *int64     `json:"connection_returned_ns"`
	CustodyRetiredNS      *int64     `json:"custody_retired_ns"`
	SetupComplete         bool       `json:"setup_complete"`
	CleanupComplete       bool       `json:"cleanup_complete"`
	NativeReturned        bool       `json:"connection_returned"`
	Retired               bool       `json:"custody_retired"`
	Bound                 bool       `json:"bound_to_this_call"`
	Reason                string     `json:"scope"`
}

func n1BindOwnerWindow(view ownerWindowObservation, origin, callStarted, callReturned time.Time) *n1OwnerWindow {
	return n1BindOwnerWindowForPurpose(view, origin, callStarted, callReturned, InventoryAccept)
}
func n1BindOwnerWindowForPurpose(view ownerWindowObservation, origin, callStarted, callReturned time.Time, purpose CommandID) *n1OwnerWindow {
	if view.commandID != purpose || view.setupEnteredAt.IsZero() || callStarted.IsZero() || callReturned.IsZero() || view.setupEnteredAt.Before(callStarted) || view.terminalReturnedAt.IsZero() || view.terminalReturnedAt.After(callReturned) {
		return nil
	}
	interval := func(a, b time.Time, scope string) n1Interval {
		return n1IntervalBetween(n1NS(origin, a), n1NS(origin, b), scope)
	}
	return &n1OwnerWindow{
		Setup:                 interval(view.setupEnteredAt, view.setupReturnedAt, "actual frame setup; failed setup includes its cleanup and overlaps Terminal; do not add"),
		Terminal:              interval(view.terminalEnteredAt, view.terminalReturnedAt, "actual complete terminal attempt through native return and custody retirement when their separate flags prove completion; partial otherwise"),
		TerminalThroughNative: interval(view.terminalEnteredAt, view.nativeReturnedAt, "required borrower/quarantine return; NOT idle native Close"),
		Retirement:            interval(view.custodyRetirementStartedAt, view.custodyRetirementReturnedAt, "actual custody retirement call; error/complete flag separate"),
		Rollback:              interval(view.rollbackStartedAt, view.rollbackReturnedAt, "actual frame rollback call"),
		PoolClose:             interval(view.poolCloseStartedAt, view.poolCloseReturnedAt, "actual frame quarantine pool-close call; decorator Close remains separate"),
		NativeReturnedNS:      n1NS(origin, view.nativeReturnedAt), ConnectionReturnedNS: n1NS(origin, view.connectionReturnedAt), CustodyRetiredNS: n1NS(origin, view.custodyRetiredAt),
		SetupComplete: view.setupComplete, CleanupComplete: view.cleanupComplete, NativeReturned: view.connectionReturned, Retired: view.retirementComplete, Bound: true,
		Reason: "W fixed-size copied lastOwnerWindow; setup entry bound to THIS sequential invocation; real monotonic observer clock, no callbacks/legacy-end reconstruction",
	}
}

func n1ObserveOwnerWindow(record *n1Attempt, w *executor, origin, callStarted time.Time) {
	if w == nil {
		return
	}
	if view, ok := w.lastOwnerWindow(); ok {
		record.OwnerWindow = n1BindOwnerWindow(view, origin, callStarted, time.Now())
		if record.OwnerWindow != nil && record.Settings.PolicyVerified == nil && view.policyVerified {
			verified := view.policyVerified
			record.Settings.PolicyVerified = &verified
			if verified {
				actual := view.settings
				record.Settings.Actual = &actual
			}
		}
	}
}

func n1OnlyExpectedDiscard(value string) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, "\n") {
		if part != errN1Discard.Error() {
			return false
		}
	}
	return true
}

func validateN1OwnerCell(cell string) error {
	switch cell {
	case "commit", "rollback", "discard", "startup", "reset", "restart":
		return nil
	}
	return ErrInvalid
}

type n1OwnerCellSummary struct {
	Chain           *n1SteadySummary `json:"restart_chain"`
	Cell            string           `json:"cell"`
	Complete        bool             `json:"complete"`
	Available       bool             `json:"available"`
	Reason          string           `json:"unavailable_reason"`
	AttemptID       string           `json:"attempt_id"`
	DeliberateFault bool             `json:"deliberate_error_cohort"`
}

// RESET has no existing closed owner restart/truncation fixture. A salt change,
// PASSIVE, session reset, or new allocation would not supply that missing proof.
// RESTART is a different cell; it cannot borrow a fresh fixture/lease authority.
func runN1OwnerCell(t *testing.T, ctx context.Context, stream *n1Stream, cell, cohort string, clock func() time.Time, summary *n1OwnerCellSummary) {
	t.Helper()
	*summary = n1OwnerCellSummary{Cell: cell}
	if err := validateN1OwnerCell(cell); err != nil {
		summary.Reason = err.Error()
		return
	}
	if cell == "restart" {
		if clock == nil {
			clock = time.Now
		}
		chain := &n1SteadySummary{}
		t.Run("restart", func(t *testing.T) {
			runN1Steady(t, ctx, stream, 1, 0, cohort, n1SteadyControls{clock: clock, restart: true}, chain)
		})
		summary.Chain = chain
		summary.Available = chain.Restart != nil
		summary.Complete = chain.Complete && chain.Restart != nil && chain.Restart.Complete
		if !summary.Complete {
			summary.Reason = n1Join("component restart incomplete", errors.New(chain.StopReason))
		}
		return
	}
	if cell == "reset" {
		summary.Reason = "no closed source-bound fully-backfilled WAL restart/truncation fixture; reset remains unavailable"
		if err := stream.append("unavailable_cell", map[string]any{"owner_cell": summary}); err != nil {
			summary.Reason = n1Join(summary.Reason, err)
		}
		return
	}
	var record *n1Attempt
	population := "setup-retained-first-use"
	if cell == "startup" {
		population = "first-use-reopen"
	}
	t.Run(cell, func(t *testing.T) {
		runN1Attempt(t, ctx, stream, 1, n1Case{"active", "current", "present", "fixture"}, cohort, "", &record, n1AttemptOptions{population: population, clock: clock, ownerCell: cell})
	})
	summary.Available = record != nil && record.OwnerWindow != nil
	summary.DeliberateFault = cell == "discard"
	if record != nil {
		summary.AttemptID = record.ID
	}
	if !summary.Available {
		summary.Reason = "no call-bound owner observation"
		return
	}
	window := record.OwnerWindow
	expected := record.Outcome == "committed" && record.Error == ""
	if cell == "rollback" {
		expected = record.Outcome == "rolled_back" && record.Error == ""
	}
	if cell == "discard" {
		expected = record.Outcome == "rolled_back" && n1OnlyExpectedDiscard(record.Error) && record.DeliberateFailure == "rollback_before_native" && len(record.NativeCloses) > 0 && record.NativeCloses[0].Completed
	}
	summary.Complete = expected && window.Bound && window.Setup.ElapsedNS != nil && window.Terminal.ElapsedNS != nil && window.CleanupComplete && window.NativeReturned && window.NativeReturnedNS != nil && window.Retired && window.CustodyRetiredNS != nil && window.Retirement.ElapsedNS != nil && record.NativeJoined != nil && *record.NativeJoined && !record.LedgerOverflow && !record.NativeCloseOverflow
	if !summary.Complete {
		summary.Reason = "owner cell incomplete; actual outcome=" + record.Outcome + " deliberate=" + record.DeliberateFailure + " error=" + record.Error
	}
}

func TestN1OwnerWindowBindingAndPartials(t *testing.T) {
	origin := time.Now()
	view := ownerWindowObservation{ownerWindow: ownerWindow{commandID: InventoryAccept, setupEnteredAt: origin.Add(time.Millisecond), setupReturnedAt: origin.Add(8 * time.Millisecond), terminalEnteredAt: origin.Add(2 * time.Millisecond), terminalReturnedAt: origin.Add(8 * time.Millisecond), nativeReturnedAt: origin.Add(5 * time.Millisecond), custodyRetirementStartedAt: origin.Add(6 * time.Millisecond), custodyRetirementReturnedAt: origin.Add(7 * time.Millisecond), custodyRetiredAt: origin.Add(7 * time.Millisecond)}, cleanupComplete: true, connectionReturned: true, retirementComplete: true}
	got := n1BindOwnerWindow(view, origin, origin, origin.Add(9*time.Millisecond))
	if got == nil || !got.Bound || got.SetupComplete || *got.Setup.ElapsedNS != 7e6 || *got.Terminal.ElapsedNS != 6e6 || *got.Retirement.ElapsedNS != 1e6 {
		t.Fatal("failed setup/terminal overlap lost", got)
	}
	if n1BindOwnerWindow(view, origin, origin.Add(3*time.Millisecond), origin.Add(9*time.Millisecond)) != nil {
		t.Fatal("stale previous call attached to ErrBusy")
	}
	if n1BindOwnerWindow(view, origin, origin, origin.Add(7*time.Millisecond)) != nil {
		t.Fatal("future observation bound")
	}
	view.nativeReturnedAt = time.Time{}
	view.custodyRetiredAt = time.Time{}
	view.connectionReturned = false
	view.retirementComplete = false
	got = n1BindOwnerWindow(view, origin, origin, origin.Add(9*time.Millisecond))
	if got.NativeReturnedNS != nil || got.CustodyRetiredNS != nil || got.TerminalThroughNative.ElapsedNS != nil || got.NativeReturned || got.Retired {
		t.Fatal("missing owner end reconstructed", got)
	}
}

func TestN1OwnerWindowCells(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in owner-window controls")
	}
	for _, cell := range []string{"commit", "rollback", "discard", "startup"} {
		t.Run(cell, func(t *testing.T) {
			stream := n1ControlStream(t)
			now := time.Now().Add(time.Hour)
			summary := &n1OwnerCellSummary{}
			runN1OwnerCell(t, t.Context(), stream, cell, "native-counters", func() time.Time { now = now.Add(time.Microsecond); return now }, summary)
			if !summary.Complete || !summary.Available {
				t.Fatal("owner cell incomplete", summary)
			}
			records := n1SteadyControlWire(t, stream)
			if len(records) != 1 || records[0].OwnerWindow == nil || records[0].OwnerWindow.Setup.ElapsedNS == nil || records[0].OwnerWindow.Terminal.ElapsedNS == nil || records[0].Profile.Boundaries["Setup"].ElapsedNS == nil || records[0].Profile.Boundaries["Terminal"].ElapsedNS == nil {
				t.Fatal("owner intervals omitted")
			}
			if cell == "discard" && (records[0].OwnerWindow.NativeReturnedNS == nil || len(records[0].NativeCloses) != 1 || records[0].NativeCloses[0].ReturnedNS == nil || *records[0].NativeCloses[0].ReturnedNS > *records[0].OwnerWindow.NativeReturnedNS) {
				t.Fatal("discard actual native Close/required-return boundary missing")
			}
		})
	}
}

func TestN1OwnerRestartPreservesOriginalAndJoins(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component restart control")
	}
	stream := n1ControlStream(t)
	now := time.Now().Add(time.Hour)
	summary := &n1OwnerCellSummary{}
	runN1OwnerCell(t, t.Context(), stream, "restart", "native-counters", func() time.Time { now = now.Add(time.Microsecond); return now }, summary)
	records := n1SteadyControlWire(t, stream)
	if !summary.Complete || !summary.Available || summary.Chain == nil || summary.Chain.Restart == nil || !summary.Chain.Restart.Complete || !summary.Chain.Restart.OldNativeJoined || summary.Chain.Restart.SameFile == nil || !*summary.Chain.Restart.SameFile || !summary.Chain.Restart.NewBindingChecked || len(records) != 3 {
		t.Fatal("restart protocol incomplete", summary)
	}
	if *records[0].Settings.ConnectionID != 1 || *records[1].Settings.ConnectionID != 2 || *records[2].Settings.ConnectionID != 2 {
		t.Fatal("restart connection identity lost")
	}
	for i, record := range records {
		if record.OwnerWindow == nil || record.OwnerWindow.Setup.ElapsedNS == nil || record.OwnerWindow.Terminal.ElapsedNS == nil || record.Continuity.SteadyEligible || record.Reference == nil || *record.Reference != *records[0].Reference || *record.PhysicalAttemptOrdinal != i+1 {
			t.Fatal("restart identity/owner/physical join lost", record)
		}
	}
	if records[1].HistoricalResult == nil || records[2].AcknowledgedThrough == nil || records[2].CurrentSnapshot == nil || records[2].CurrentSnapshot.LastSequence != records[2].CurrentSnapshot.AcknowledgedSequence || len(summary.Chain.NativeCloses) != 2 {
		t.Fatal("saved-original resolution/ACK/join omitted")
	}
}

func TestN1OwnerResetRemainsUnavailable(t *testing.T) {
	stream := n1ControlStream(t)
	summary := &n1OwnerCellSummary{}
	runN1OwnerCell(t, t.Context(), stream, "reset", "timing", nil, summary)
	if summary.Complete || summary.Available || summary.Reason == "" || summary.AttemptID != "" {
		t.Fatal("unproven RESET made a sample")
	}
}

func TestN1OwnerExpectedFaultDoesNotFilterUnexpectedErrors(t *testing.T) {
	expected := errN1Discard.Error()
	if !n1OnlyExpectedDiscard(expected) || !n1OnlyExpectedDiscard(expected+"\n"+expected) {
		t.Fatal("known repeated owner/handler cause not recognized")
	}
	for _, value := range []string{"", expected + "\n" + ErrOverrun.Error(), ErrClosed.Error()} {
		if n1OnlyExpectedDiscard(value) {
			t.Fatal("unexpected owner failure accepted", value)
		}
	}
}
