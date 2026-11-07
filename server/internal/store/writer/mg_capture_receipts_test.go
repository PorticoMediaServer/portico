package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMGCaptureDraftNamespaceAndScope(t *testing.T) {
	w, now := mgCaptureFixture(t)
	for _, name := range []string{"incarnation", "sequence gap", "reused sequence", "pending ACK", "launch", "allocation token", "allocation generation", "source fence", "source revision", "guide pin", "member lifetime"} {
		t.Run(name, func(t *testing.T) {
			turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
			s := mgCaptureSnapshotFor(t, turn, "leader")
			c := mgCaptureTestCommand(s, mgCaptureKindRenew)
			switch name {
			case "incarnation":
				c.incarnation[0]++
			case "sequence gap":
				c.sequence++
			case "reused sequence":
				c.sequence--
			case "pending ACK":
				s.ackSequence--
			case "launch":
				s.launch.id[0]++
			case "allocation token":
				s.allocationToken = "stale"
			case "allocation generation":
				s.allocationGeneration++
			case "source fence":
				s.sourceFence = "stale"
			case "source revision":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET revision=revision+1 WHERE id='capture-source'`); err != nil {
					t.Fatal(err)
				}
			case "guide pin":
				s.guidePin = mgCaptureNullableText{}
			case "member lifetime":
				s.memberIncarnation = "33333333333333333333333333333333"
			}
			out, err := mgRenewCapture(t.Context(), turn, s, c, mgCaptureAllocation("leader", now), now+60000)
			mgCaptureZero(t, out, err)
		})
	}
	t.Run("fresh claim records exact launch allocation source", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		s := mgCaptureSnapshotFor(t, turn, "fresh")
		c := mgCaptureTestCommand(s, mgCaptureKindClaim)
		a := mgCaptureAllocation("fresh", now)
		got, err := mgClaimCapture(t.Context(), turn, s, c, mgCaptureClaimInput{"token", "instance", "artifact", a, now + 30000, mgCaptureID{9}, 3, "source-fence"})
		if err != nil {
			t.Fatal(err)
		}
		r := got.recording
		if r.launch.id != (mgCaptureID{9}) || !r.launch.valid || r.allocationGeneration != a.generation || r.allocationToken != a.token || r.sourceRevision != 3 || r.sourceFence != "source-fence" || r.lastSequence != c.sequence || r.incarnation != c.incarnation || r.guidePin != s.guidePin {
			t.Fatal("scope projection drift", got)
		}
		out, err := mgCancelCapture(t.Context(), turn, r, mgCaptureTestCommand(r, mgCaptureKindCancel), "owner-cancelled")
		mgCaptureZero(t, out, err)
	})
}
func TestMGCaptureReceiptHistory(t *testing.T) {
	w, now := mgCaptureFixture(t)
	t.Run("immutable history survives current row change and owner absence", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		s := mgCaptureSnapshotFor(t, turn, "leader")
		c := mgCaptureTestCommand(s, mgCaptureKindRenew)
		got, err := mgRenewCapture(t.Context(), turn, s, c, mgCaptureAllocation("leader", now), now+60000)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := mgSaveCaptureReceipt(t.Context(), turn, got)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.result != got.recording.mgCaptureSavedResult || receipt.revision != got.recording.revision || receipt.resultDigest == (mgCaptureDigest{}) {
			t.Fatal(receipt)
		}
		// A duplicate save is not a successful replay. Parent resolves the ORIGINAL.
		duplicate, err := mgSaveCaptureReceipt(t.Context(), turn, got)
		if err == nil || duplicate != (mgCaptureReceipt{}) {
			t.Fatal("duplicate save leaked", duplicate, err)
		}
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='cancelled',reason='owner-cancelled',cancel_generation=cancel_generation+1,revision=revision+1 WHERE id='leader'`); err != nil {
			t.Fatal(err)
		}
		history, found, err := mgLookupCaptureReceipt(t.Context(), turn, c)
		if err != nil || !found || history != receipt {
			t.Fatal("lookup decoded today's row", history, found, err)
		}
		// Raw fixture deletion simulates the parent's already-proven retirement/floor
		// boundary. This is not a retirement implementation or permission to delete.
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM dvr_recordings WHERE id='leader'`); err != nil {
			t.Fatal(err)
		}
		history, found, err = mgLookupCaptureReceipt(t.Context(), turn, c)
		if err != nil || !found || history != receipt {
			t.Fatal("history was live-owner FK-owned", history, found, err)
		}
		for _, name := range []string{"digest", "operation", "kind", "sequence operation reuse"} {
			changed := c
			switch name {
			case "digest":
				changed.requestDigest[0]++
			case "operation":
				changed.operation[0]++
			case "kind":
				changed.kind = mgCaptureKindCancel
			case "sequence operation reuse":
				changed.sequence++
			}
			out, found, err := mgLookupCaptureReceipt(t.Context(), turn, changed)
			if !errors.Is(err, ErrConflict) || found || out != (mgCaptureReceipt{}) {
				t.Fatal(name, out, found, err)
			}
		}
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_capture_command_receipts SET result_lease_until_ms=result_lease_until_ms+1 WHERE recording_id='leader'`); err != nil {
			t.Fatal(err)
		}
		out, found, err := mgLookupCaptureReceipt(t.Context(), turn, c)
		if err == nil || found || out != (mgCaptureReceipt{}) {
			t.Fatal("corrupt result digest accepted", out, found, err)
		}
	})
	t.Run("receipt statement failure rolls back accepted effect", func(t *testing.T) {
		defs := mgCaptureDefinitions()
		// Real native CHECK failure in the receipt INSERT, after the body changed.
		for i := range defs {
			if defs[i].ID == mgCaptureReceiptInsert {
				defs[i].SQL = replaceMGCaptureReceiptFailure(defs[i].SQL)
			}
		}
		turn := mgCaptureTestTurn(t, w, defs)
		s := mgCaptureSnapshotFor(t, turn, "leader")
		c := mgCaptureTestCommand(s, mgCaptureKindRenew)
		got, err := mgRenewCapture(t.Context(), turn, s, c, mgCaptureAllocation("leader", now), now+60000)
		if err != nil {
			t.Fatal(err)
		}
		out, err := mgSaveCaptureReceipt(t.Context(), turn, got)
		if err == nil || out != (mgCaptureReceipt{}) {
			t.Fatal(out, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var revision, sequence int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,last_command_sequence FROM dvr_recordings WHERE id='leader'`).Scan(&revision, &sequence); err != nil || revision != 5 || sequence != 1 {
			t.Fatal("failed save escaped rollback", revision, sequence, err)
		}
		var n int
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_capture_command_receipts`).Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	})
	t.Run("cancelled lookup releases no history", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		s := mgCaptureSnapshotFor(t, turn, "leader")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		out, found, err := mgLookupCaptureReceipt(ctx, turn, mgCaptureTestCommand(s, mgCaptureKindRenew))
		if !errors.Is(err, context.Canceled) || found || out != (mgCaptureReceipt{}) {
			t.Fatal(out, found, err)
		}
	})
}
func TestMGCaptureDraftPhysicalConstraints(t *testing.T) {
	w, now := mgCaptureFixture(t)
	t.Run("delayed exit keeps guide pin and cancellation debt", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		for _, q := range []string{
			`UPDATE dvr_recordings SET guide_pin_generation=NULL WHERE id='leader'`,
			`UPDATE dvr_recordings SET capture_physical_exit_sequence=1,capture_exit_launch_id=zeroblob(16),capture_exit_claim_generation=7 WHERE id='leader'`,
			`UPDATE dvr_recordings SET capture_shared_ready_sequence=1,artifact_digest='digest',bytes=42,captured_start_ms=1,captured_end_ms=2 WHERE id='leader'`,
		} {
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err == nil {
				t.Fatal("draft physical CHECK bypassed", q)
			}
		}
		// Native fixture checkpoint facts only; parent alone may accept real trusted
		// observations. Cancellation must preserve all accepted producer/gap facts.
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='finalizing',capture_writer_retired_sequence=1,capture_shared_ready_sequence=1,artifact_digest='digest',bytes=42,captured_start_ms=1,captured_end_ms=2,capture_gap_count=2,capture_producer_succeeded=0,capture_boundary_reached=1 WHERE id='leader'`); err != nil {
			t.Fatal(err)
		}
		before := mgCaptureSnapshotFor(t, turn, "leader")
		got, err := mgCancelCapture(t.Context(), turn, before, mgCaptureTestCommand(before, mgCaptureKindCancel), "owner-cancelled")
		if err != nil {
			t.Fatal(err)
		}
		expected := before.mgCaptureSavedResult
		expected.state, expected.reason = "cancelled", "owner-cancelled"
		expected.cancelGeneration++
		if got.recording.mgCaptureSavedResult != expected || got.retirement != mgCaptureRetirementPending {
			t.Fatal("logical cancel cleared proof/debt", got)
		}
		if _, err := mgSaveCaptureReceipt(t.Context(), turn, got); err != nil {
			t.Fatal(err)
		}
		var lease int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT lease_until_ms FROM live_allocations WHERE id='leader-allocation'`).Scan(&lease); err != nil || lease != now+120000 {
			t.Fatal("cancel released allocation", lease, err)
		}
		// Expiry and current cancellation do not make a stale launch/claim valid.
		for _, q := range []string{
			`UPDATE dvr_recordings SET capture_physical_exit_sequence=2,capture_exit_launch_id=zeroblob(16),capture_exit_claim_generation=7,capture_exit_cancel_generation=3 WHERE id='leader'`,
			`UPDATE dvr_recordings SET capture_physical_exit_sequence=2,capture_exit_launch_id=capture_launch_id,capture_exit_claim_generation=6,capture_exit_cancel_generation=3 WHERE id='leader'`,
		} {
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err == nil {
				t.Fatal("stale exit accepted", q)
			}
		}
	})
}

func replaceMGCaptureReceiptFailure(q string) string {
	// Replace one bound accepted_revision with literal zero, leaving all binds
	// present through a scalar arithmetic expression so native binding stays exact.
	columns, values, _ := strings.Cut(q, " VALUES(")
	names := strings.Split(strings.TrimSuffix(strings.TrimPrefix(columns, "INSERT INTO dvr_capture_command_receipts("), ")"), ",")
	parts := strings.Split(strings.TrimSuffix(values, ")"), ",")
	for i, name := range names {
		if name == "accepted_revision" {
			parts[i] = "(?*0)"
		}
	}
	return columns + " VALUES(" + strings.Join(parts, ",") + ")"
}
