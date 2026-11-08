package writer

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicResumeWalk(t *testing.T) basicWalk {
	t.Helper()
	walk := basicWalkThroughPin(t)
	for _, op := range []basicControlOperation{basicControlFreeze, basicControlSelectEntity, basicControlCopyFamily} {
		in := basicControlRequest{operation: op, source: walk.s, changeRevision: walk.revision, entityID: 31, shapeID: walk.shape}
		if op == basicControlCopyFamily {
			in.family = 1
		}
		if _, err := walk.chain.stage(in); err != nil {
			t.Fatal(op, err)
		}
		walk.revision++
	}
	return walk
}

func basicResumeAssertHidden(t *testing.T, walk basicWalk) {
	t.Helper()
	var pending, link, epoch, input, shape int64
	if err := walk.e.db.QueryRowContext(t.Context(), `SELECT (SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),(SELECT info_epoch FROM catalog_assets WHERE id=41),input_revision,target_shape_id FROM catalog_info_changes WHERE id=51`).Scan(&pending, &link, &epoch, &input, &shape); err != nil || pending != 1 || link != 51 || epoch != walk.s.expectedEpoch || input != walk.s.inputRevision || shape != walk.shape {
		t.Fatal("resume altered fences/identity/shape", pending, link, epoch, input, shape, err)
	}
}

type basicResumeTestState struct {
	header                              string
	runRevision, ack, receipts, streams int64
}

func basicResumeReadState(t *testing.T, walk basicWalk) basicResumeTestState {
	t.Helper()
	var s basicResumeTestState
	if err := walk.e.db.QueryRowContext(t.Context(), `SELECT json_array(phase,job_revision,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,fault),(SELECT job_revision FROM inventory_runs WHERE job_id='basic-run'),(SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id='basic-run'),(SELECT count(*) FROM scan_command_receipts WHERE run_id='basic-run'),(SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset') FROM catalog_info_changes WHERE id=51`).Scan(&s.header, &s.runRevision, &s.ack, &s.receipts, &s.streams); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBasicResumeSavedCursorThroughAccept(t *testing.T) {
	walk := basicResumeWalk(t)
	// Drop the command-local cursor/revision. Only native header state and the
	// current inventory run original drive the remaining bounded steps.
	if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_lease_owner=5,job_lease_generation=7,job_lease_expires_ms=1,job_attempts=3,job_next_attempt_ms=1,fault='retry' WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	for turns := 0; ; turns++ {
		if turns > 32 {
			t.Fatal("resume did not drain")
		}
		got, err := walk.chain.stage(basicControlRequest{operation: basicControlResume, source: walk.s})
		if err != nil {
			t.Fatal("resume", turns, err)
		}
		basicResumeAssertHidden(t, walk)
		var attempts, owner, generation, expires int64
		var next sql.NullInt64
		var fault string
		if err := walk.e.db.QueryRowContext(t.Context(), `SELECT job_attempts,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_next_attempt_ms,fault FROM catalog_info_changes WHERE id=51`).Scan(&attempts, &owner, &generation, &expires, &next, &fault); err != nil {
			t.Fatal(err)
		}
		if owner != 0 || generation != 0 || expires != 0 || next.Valid || fault != "" {
			t.Fatal("reset", owner, generation, expires, next, fault)
		}
		if got.Continuation.NextFamily == 0 {
			if got.Continuation != (BasicContinuation{AfterEntity: 31}) || attempts != 0 {
				t.Fatal("completed cursor/retry tuple", got.Continuation, attempts)
			}
			break
		}
		if attempts != int64(4+turns) {
			t.Fatal("attempt count", attempts, turns)
		}
	}
	if got, err := walk.chain.stage(basicControlRequest{operation: basicControlResume, source: walk.s}); err != nil || got.Continuation != (BasicContinuation{AfterEntity: 31}) {
		t.Fatal("completed resume", got, err)
	}
	basicResumeAssertHidden(t, walk)
	var revision int64
	if err := walk.e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := walk.chain.stage(basicAbandonRequest(walk.s, revision, basicAbandonExpected(t, walk))); !errors.Is(err, ErrConflict) {
		t.Fatal("abandon after resumed copy", err)
	}
	if got, err := basicWalkAccept(t, walk, revision); err != nil || got.Outcome.State != Applied {
		t.Fatal("Accept after resume", got, err)
	}
	basicDispatchReturned(t, walk.e, walk.authority)
}

func TestBasicResumeRefusalsLeaveHeaderHidden(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"live_header_lease", `UPDATE catalog_info_changes SET job_lease_owner=5,job_lease_generation=7,job_lease_expires_ms=3999999999999 WHERE id=51`},
		{"partial_header_lease", `UPDATE catalog_info_changes SET job_lease_owner=5 WHERE id=51`},
		{"future_backoff", `UPDATE catalog_info_changes SET job_next_attempt_ms=3999999999999 WHERE id=51`},
		{"permanent_fault", `UPDATE catalog_info_changes SET fault='integrity' WHERE id=51`},
		{"attempt_bound", `UPDATE catalog_info_changes SET job_attempts=4294967295 WHERE id=51`},
		{"marked_abandon", `UPDATE catalog_info_changes SET copy_aux=-1 WHERE id=51`},
		{"wrong_shape_owner", `UPDATE catalog_info_changes SET target_owner_object='basic-object',target_owner_revision='object:019' WHERE id=51`},
		{"changed_input_revision", `UPDATE catalog_info_changes SET input_revision=input_revision+1 WHERE id=51`},
		{"changed_witness", `UPDATE catalog_info_input_witnesses SET policy_revision=policy_revision+1 WHERE change_id=51`},
		{"stale_asset", `UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=41`},
		{"lost_run_lease", `UPDATE inventory_runs SET job_lease_generation=job_lease_generation+1 WHERE job_id='basic-run'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			walk := basicResumeWalk(t)
			if _, err := walk.e.db.ExecContext(t.Context(), tc.sql); err != nil {
				t.Fatal(err)
			}
			before := basicResumeReadState(t, walk)
			got, err := walk.chain.stage(basicControlRequest{operation: basicControlResume, source: walk.s})
			if err == nil || got.Outcome.State == Applied {
				t.Fatal("refusal accepted", got, err)
			}
			if after := basicResumeReadState(t, walk); before != after {
				t.Fatal("refused resume changed native state", before, after)
			}
			if tc.name != "changed_input_revision" {
				basicResumeAssertHidden(t, walk)
			}
			basicDispatchReturned(t, walk.e, walk.authority)
		})
	}
}

func TestBasicResumeEligibilityBoundaries(t *testing.T) {
	h := basicResumeSnapshot{phase: 2, revision: 9, entity: 31, upper: 31, hasAfter: 1, shape: 61, cursor: basicCopyCursor{family: 2}, attempts: 3}
	if !h.eligible(100) {
		t.Fatal("valid header refused")
	}
	due := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
	for _, tc := range []struct {
		name   string
		change func(*basicResumeSnapshot)
		want   bool
	}{
		{"expired_at_now", func(h *basicResumeSnapshot) { h.owner, h.generation, h.expires = 5, 7, 100 }, true},
		{"lease_one_ms_live", func(h *basicResumeSnapshot) { h.owner, h.generation, h.expires = 5, 7, 101 }, false},
		{"lease_owner_only", func(h *basicResumeSnapshot) { h.owner = 5 }, false},
		{"lease_generation_only", func(h *basicResumeSnapshot) { h.generation = 7 }, false},
		{"expired_missing_owner", func(h *basicResumeSnapshot) { h.generation, h.expires = 7, 99 }, false},
		{"expired_missing_generation", func(h *basicResumeSnapshot) { h.owner, h.expires = 5, 99 }, false},
		{"populated_zero_expiry", func(h *basicResumeSnapshot) { h.owner, h.generation = 5, 7 }, false},
		{"lease_expiry_only", func(h *basicResumeSnapshot) { h.expires = 99 }, false},
		{"due_now", func(h *basicResumeSnapshot) { h.next = due(100) }, true},
		{"due_one_ms_future", func(h *basicResumeSnapshot) { h.next = due(101) }, false},
		{"due_negative", func(h *basicResumeSnapshot) { h.next = due(-1) }, false},
		{"retry_fault", func(h *basicResumeSnapshot) { h.fault = basicResumeRetryFault }, true},
		{"arbitrary_fault", func(h *basicResumeSnapshot) { h.fault = "integrity" }, false},
		{"last_attempt", func(h *basicResumeSnapshot) { h.attempts = basicResumeMaxAttempts - 1 }, true},
		{"attempts_exhausted", func(h *basicResumeSnapshot) { h.attempts = basicResumeMaxAttempts }, false},
		{"attempts_negative", func(h *basicResumeSnapshot) { h.attempts = -1 }, false},
		{"revision_overflow", func(h *basicResumeSnapshot) { h.revision = math.MaxInt64 }, false},
		{"revision_zero", func(h *basicResumeSnapshot) { h.revision = 0 }, false},
		{"entity_zero", func(h *basicResumeSnapshot) { h.entity, h.upper = 0, 0 }, false},
		{"upper_mismatch", func(h *basicResumeSnapshot) { h.upper++ }, false},
		{"no_after", func(h *basicResumeSnapshot) { h.hasAfter = 0 }, false},
		{"no_shape", func(h *basicResumeSnapshot) { h.shape = 0 }, false},
		{"before_freeze", func(h *basicResumeSnapshot) { h.phase = 0 }, false},
		{"before_entity", func(h *basicResumeSnapshot) { h.phase = 1 }, false},
		{"already_activated", func(h *basicResumeSnapshot) { h.phase = 4 }, false},
		{"initial_copy_cursor", func(h *basicResumeSnapshot) { h.cursor = basicCopyCursor{} }, true},
		{"family_one_dirty", func(h *basicResumeSnapshot) { h.cursor.family = 1 }, false},
		{"family_ten", func(h *basicResumeSnapshot) { h.cursor.family = 10 }, false},
		{"negative_copy_phase", func(h *basicResumeSnapshot) { h.cursor.phase = -1 }, false},
		{"negative_copy_id", func(h *basicResumeSnapshot) { h.cursor.id = -1 }, false},
		{"abandon_marker", func(h *basicResumeSnapshot) { h.cursor.aux = -1 }, false},
		{"invalid_copy_aux", func(h *basicResumeSnapshot) { h.cursor.aux = 2 }, false},
		{"phase_three_zero", func(h *basicResumeSnapshot) { h.phase = 3; h.cursor = basicCopyCursor{} }, true},
		{"phase_three_dirty", func(h *basicResumeSnapshot) { h.phase = 3 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := h
			tc.change(&c)
			if got := c.eligible(100); got != tc.want {
				t.Fatal("eligibility", got, "want", tc.want)
			}
		})
	}
}

func TestBasicResumeRequestClosed(t *testing.T) {
	_, s, original, identity := basicControlFixture(t)
	base := basicControlRequest{original: original, purpose: BasicPurposeStage, operation: basicControlResume, source: s}
	if _, err := newBasicStageCommand(identity, base); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*basicControlRequest){
		"seal":         func(in *basicControlRequest) { in.purpose = BasicPurposeSeal },
		"family":       func(in *basicControlRequest) { in.family = 2 },
		"revision":     func(in *basicControlRequest) { in.changeRevision = 1 },
		"entity":       func(in *basicControlRequest) { in.entityID = 31 },
		"shape":        func(in *basicControlRequest) { in.shapeID = 61 },
		"cursor":       func(in *basicControlRequest) { in.copy.family = 2 },
		"probe":        func(in *basicControlRequest) { in.probe = &basicProbeEffects{} },
		"abandon":      func(in *basicControlRequest) { in.abandon = &basicAbandonExpectation{} },
		"rows":         func(in *basicControlRequest) { in.rows = []basicStageMediaRow{} },
		"range":        func(in *basicControlRequest) { in.end = 1 },
		"raw_chapters": func(in *basicControlRequest) { in.rawChapters = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			change(&in)
			var err error
			if in.purpose == BasicPurposeSeal {
				_, err = newBasicSealCommand(identity, in)
			} else {
				_, err = newBasicStageCommand(identity, in)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatal("foreign payload accepted", err)
			}
		})
	}
	for _, d := range basicResumeDefinitions() {
		if problems := plan.LintSQL(d.SQL); d.ID != basicResumeLengths && len(problems) != 0 {
			t.Fatal(d.ID, problems)
		}
	}
}

func TestBasicResumeFacadeReplayWinsBeforeHeaderRefusals(t *testing.T) {
	walk := basicResumeWalk(t)
	cmd, err := newBasicStageCommand(walk.chain.identity, basicControlRequest{original: walk.chain.original, purpose: BasicPurposeStage, operation: basicControlResume, source: walk.s})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := cmd.receiptRequest()
	disclosure := inventoryTestDisclosure(t, walk.w, q.Scan)
	call := func() (BasicResult, error) {
		got, _, err := walk.w.effective.runBasic(t.Context(), walk.w, disclosure, basicInvocation{request: q, stage: cmd}, false)
		return got, err
	}
	got, err := call()
	if err != nil || got.Outcome.State != Applied || got.Outcome.Replayed {
		t.Fatal("fresh resume", got, err)
	}
	if _, err = walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_lease_owner=5,job_lease_generation=7,job_lease_expires_ms=3999999999999,fault='integrity' WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	before := basicResumeReadState(t, walk)
	replay, err := call()
	if err != nil || !replay.Outcome.Replayed || replay.Digest != got.Digest || replay.Continuation != got.Continuation {
		t.Fatal("receipt lost to live/faulted header", replay, err)
	}
	if after := basicResumeReadState(t, walk); before != after {
		t.Fatal("replay repeated reset/copy")
	}
	if _, ack, err := walk.w.effective.runBasic(t.Context(), walk.w, disclosure, basicInvocation{request: q}, true); err != nil || ack.AcknowledgedThrough != cmd.identity.Sequence {
		t.Fatal("ACK", ack, err)
	}
	retired, err := call()
	if err != nil || retired.Outcome.State != ResultRetired {
		t.Fatal("retired original ran fresh resume", retired, err)
	}
	basicResumeAssertHidden(t, walk)
	basicDispatchReturned(t, walk.e, walk.authority)
}

func TestBasicResumeNativeTextIsFundedBeforeFetch(t *testing.T) {
	walk := basicResumeWalk(t)
	if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET fault=? WHERE id=51`, strings.Repeat("é", 40000)); err != nil {
		t.Fatal(err)
	}
	before := basicResumeReadState(t, walk)
	_, err := walk.chain.stage(basicControlRequest{operation: basicControlResume, source: walk.s})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
		t.Fatal("native octets escaped partition", err)
	}
	if after := basicResumeReadState(t, walk); before != after {
		t.Fatal("unfunded native text changed state")
	}
	basicResumeAssertHidden(t, walk)
	basicDispatchReturned(t, walk.e, walk.authority)
}
