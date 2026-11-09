package writer

import (
	"context"
	"errors"
	"testing"
)

func TestMGGuideDiscardWithdrawsStagingAndKeepsGeneration(t *testing.T) {
	w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
	witness := mgReclaimWitness(7, "old", false)
	out, _ := mgReclaimDiscard(t, w, op, witness)
	if out.operation.status != "superseded" || out.head != (mgGuideHead{source: "source", generation: "old", state: "active", revision: 7}) || out.appliedMS <= 0 {
		t.Fatal("discard result", out)
	}
	db := mgReclaimReader(t, path)
	got := mgReclaimReadDurable(t, db, op.request)
	if got.status != "superseded" || got.family != "programmes" || got.cursor != "" || got.sequence != op.progress.sequence+1 || got.acknowledged != got.sequence || got.revision != op.progress.revision+1 {
		t.Fatal("durable intent", got)
	}
	// Logical withdrawal only: every immutable row and the serving head remain.
	if got.programmes != 10 || got.channels != 2 || got.choices != 1 || got.generations != 1 || got.sourceHead != 0 {
		t.Fatal("discard removed data", got)
	}

	// Replay of the same body after the commit is a conflict with zero effects;
	// the original result belongs to the saved receipt, never a second body.
	turn := mgReclaimTurn(t, w, MGGuideDiscard)
	before := mgReclaimTurnChanges(t, turn)
	replay, err := mgDiscardGuideNative(t.Context(), turn, mgGuideDiscardInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness})
	if !errors.Is(err, ErrConflict) || replay != (mgGuideNativeResult{}) {
		t.Fatal("discard replay", replay, err)
	}
	if after := mgReclaimTurnChanges(t, turn); after != before {
		t.Fatal("replay made effects", before, after)
	}
	mgGuideRollback(t, turn)
}

func TestMGGuideReclaimDrainsChildBeforeParent(t *testing.T) {
	w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
	witness := mgReclaimWitness(7, "old", false)
	_, op = mgReclaimDiscard(t, w, op, witness)
	db := mgReclaimReader(t, path)

	// Step 1: the first bounded programme page.
	out, op := mgReclaimSlice(t, w, op, witness)
	if out.blocked != "" || out.removed != 8 || out.bytes <= 0 || out.state.family != mgGuideReclaimProgrammes || out.state.cursor != "programme-07" {
		t.Fatal("programme slice", out)
	}
	got := mgReclaimReadDurable(t, db, op.request)
	if got.programmes != 2 || got.channels != 2 || got.choices != 1 || got.generations != 1 {
		t.Fatal("child-before-parent drain", got)
	}
	// Step 2: the remaining partial page.
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 2 || out.state.cursor != "programme-09" || out.blocked != "" {
		t.Fatal("last programme page", out)
	}
	if got = mgReclaimReadDurable(t, db, op.request); got.programmes != 0 || got.channels != 2 {
		t.Fatal("programmes not drained", got)
	}
	// Step 3: an exhausted range advances the family as its own command.
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 0 || out.state.family != mgGuideReclaimChannels || out.state.cursor != "" {
		t.Fatal("channel transition", out)
	}
	// Step 4: two channels, then an empty range, then one choice.
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 2 || out.state.cursor != "channel-01" || out.state.family != mgGuideReclaimChannels {
		t.Fatal("channel slice", out)
	}
	if got = mgReclaimReadDurable(t, db, op.request); got.programmes != 0 || got.channels != 0 || got.choices != 1 {
		t.Fatal("channels not drained", got)
	}
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 0 || out.state.family != mgGuideReclaimChoices {
		t.Fatal("choice transition", out)
	}
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 1 || out.state.cursor != "choice-00" {
		t.Fatal("choice slice", out)
	}
	if got = mgReclaimReadDurable(t, db, op.request); got.choices != 0 {
		t.Fatal("choices not drained", got)
	}
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.removed != 0 || out.state.family != mgGuideReclaimGeneration || out.state.cursor != "" {
		t.Fatal("generation transition", out)
	}
	// Step 8: the closed external gate. Children are proven empty, the
	// generation row is retained and no write is planned.
	before := op
	out, op = mgReclaimSlice(t, w, op, witness)
	if out.blocked != mgGuideReclaimBlockedExternal || out.removed != 0 || out.bytes != 0 || op != before {
		t.Fatal("external gate", out, op)
	}
	if got = mgReclaimReadDurable(t, db, op.request); got.generations != 1 || got.family != "generation" {
		t.Fatal("generation was reclaimed", got)
	}
}

func TestMGGuideReclaimRefusesHeadAndTypedPins(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *executor)
		reason string
	}{
		{"current head", func(t *testing.T, w *executor) {
			execMG(t, w, `UPDATE live_sources SET revision=8,active_generation='new' WHERE id='source'`, nil)
		}, mgGuideReclaimBlockedHead},
		{"reminder pin", func(t *testing.T, w *executor) {
			execMG(t, w, `INSERT INTO live_reminders(id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc) VALUES('reminder','owner','live-source','source','channel-00','new','programme-00',1,'2026-11-07T00:00:00Z')`, nil)
		}, mgGuideReclaimBlockedReminder},
		{"recording pin", func(t *testing.T, w *executor) {
			execMG(t, w, `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation)
VALUES('recording','owner','owner','account','profile','source','channel-00','programme-00','new','{}','{}',0,1,0,1,'scheduled',0,0,x'00000000000000000000000000000000',x'00000000000000000000000000000000',x'0000000000000000000000000000000000000000000000000000000000000000','00000000000000000000000000000000','00000000000000000000000000000000','new')`, nil)
		}, mgGuideReclaimBlockedRecording},
		{"operation pin", func(t *testing.T, w *executor) {
			execMG(t, w, `INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,expected_generation,status,command_incarnation,create_operation_id,birth_request_digest,expected_choice_count,manifest_digest,stage_digest)
VALUES('other-request','other-input','source','new',7,'old','published',x'00000000000000000000000000000000',x'00000000000000000000000000000000',x'0000000000000000000000000000000000000000000000000000000000000000',1,x'0000000000000000000000000000000000000000000000000000000000000000',x'0000000000000000000000000000000000000000000000000000000000000000')`, nil)
		}, mgGuideReclaimBlockedOperation},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
			witness := mgReclaimWitness(7, "old", false)
			_, op = mgReclaimDiscard(t, w, op, witness)
			test.mutate(t, w)
			if test.reason == mgGuideReclaimBlockedHead {
				witness = mgReclaimWitness(8, "new", false)
			}
			before := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request)
			turn := mgReclaimTurn(t, w, MGGuideReclaim)
			changes := mgReclaimTurnChanges(t, turn)
			out, err := mgReclaimGuideNative(t.Context(), turn, mgGuideReclaimInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness})
			if err != nil || out.blocked != test.reason || out.removed != 0 || out.bytes != 0 {
				t.Fatal("pin admitted a slice", out, err)
			}
			if after := mgReclaimTurnChanges(t, turn); after != changes {
				t.Fatal("blocked slice made effects", changes, after)
			}
			mgGuideRollback(t, turn)
			if after := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request); after != before {
				t.Fatal("blocked slice changed durable state", before, after)
			}
		})
	}
}

func TestMGGuideReclaimStaleForgedRetiredAndCancelled(t *testing.T) {
	w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
	witness := mgReclaimWitness(7, "old", false)
	_, op = mgReclaimDiscard(t, w, op, witness)

	attempt := func(name string, mutate func(*testing.T, *executor), input mgGuideReclaimInput, ctx context.Context, want error) {
		t.Helper()
		if mutate != nil {
			mutate(t, w)
		}
		before := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request)
		turn := mgReclaimTurn(t, w, MGGuideReclaim)
		changes := mgReclaimTurnChanges(t, turn)
		out, err := mgReclaimGuideNative(ctx, turn, input)
		if !errors.Is(err, want) || out != (mgGuideReclaimResult{}) {
			t.Fatal(name, out, err)
		}
		if after := mgReclaimTurnChanges(t, turn); after != changes {
			t.Fatal(name, "made effects", changes, after)
		}
		mgGuideRollback(t, turn)
		if after := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request); after != before {
			t.Fatal(name, "changed durable state", before, after)
		}
	}

	valid := mgGuideReclaimInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness}
	stale := valid
	stale.witness = mgReclaimWitness(6, "old", false)
	attempt("stale authority", nil, stale, t.Context(), ErrConflict)
	forgedIncarnation := valid
	forgedIncarnation.identity.Incarnation = [16]byte{7}
	attempt("forged incarnation", nil, forgedIncarnation, t.Context(), ErrConflict)
	forgedSequence := valid
	forgedSequence.identity.Sequence = op.progress.sequence
	attempt("forged sequence", nil, forgedSequence, t.Context(), ErrConflict)
	emptyOperation := valid
	emptyOperation.identity.OperationID = [16]byte{}
	attempt("empty operation", nil, emptyOperation, t.Context(), ErrConflict)

	retired := valid
	attempt("retirement floor", func(t *testing.T, w *executor) {
		execMG(t, w, `INSERT INTO live_operation_retirements(request_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms,last_command_sequence,acknowledged_command_sequence) VALUES('guide-request',?,?,?, 'source',?,x'0000000000000000000000000000000000000000000000000000000000000000',1,?,?)`,
			[]any{op.incarnation[:], op.createOperation[:], op.birthDigest[:], op.progress.sequence, op.progress.sequence, op.progress.sequence})
	}, retired, t.Context(), ErrConflict)

	removed := valid
	removed.witness = mgReclaimWitness(7, "old", true)
	attempt("removed source", func(t *testing.T, w *executor) {
		execMG(t, w, `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now' WHERE id='source'`, nil)
	}, removed, t.Context(), ErrDisclosureDenied)

	// A cancelled lifetime releases no output and makes no effect.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	attempt("cancelled lifetime", nil, valid, cancelled, context.Canceled)
}

func TestMGGuideCleanupAdvancesNextSupersededOperation(t *testing.T) {
	w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
	witness := mgReclaimWitness(7, "old", false)
	_, op = mgReclaimDiscard(t, w, op, witness)
	second := mgReclaimSecondOperation(t, w)
	_, _ = mgReclaimDiscard(t, w, second, witness)

	selection := mgReclaimTurn(t, w, MGGuideAdvanceCleanup)
	candidate, err := mgNextGuideCleanupNative(t.Context(), selection, "source", "")
	if err != nil || !candidate.found || candidate.request != "guide-request" || candidate.family != mgGuideReclaimProgrammes || candidate.head != (mgGuideHead{source: "source", generation: "old", state: "active", revision: 7}) {
		t.Fatal("first candidate", candidate, err)
	}
	if candidate.incarnation != op.incarnation || candidate.sequence != op.progress.sequence {
		t.Fatal("candidate lineage", candidate)
	}
	mgGuideRollback(t, selection)

	turn := mgReclaimTurn(t, w, MGGuideAdvanceCleanup)
	identity := CommandIdentity{Incarnation: candidate.incarnation, Sequence: candidate.sequence + 1, OperationID: [16]byte{42}}
	out, err := mgAdvanceGuideCleanupNative(t.Context(), turn, mgGuideCleanupInput{source: "source", request: candidate.request, identity: identity, witness: witness})
	if err != nil || out.removed != 8 || out.state.cursor != "programme-07" || out.blocked != "" {
		t.Fatal("cleanup slice", out, err)
	}
	mgGuideCommit(t, turn)

	selection = mgReclaimTurn(t, w, MGGuideAdvanceCleanup)
	next, err := mgNextGuideCleanupNative(t.Context(), selection, "source", "guide-request")
	if err != nil || !next.found || next.request != "guide-request-2" {
		t.Fatal("keyset advance", next, err)
	}
	nothing, err := mgNextGuideCleanupNative(t.Context(), selection, "source", "zzz")
	if err != nil || nothing.found {
		t.Fatal("past the keyset", nothing, err)
	}
	mgGuideRollback(t, selection)

	// A stale witness or another operation's identity is a conflict, no effect.
	before := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request)
	for _, test := range []struct {
		name   string
		input  mgGuideCleanupInput
		reason string
	}{
		{"stale witness", mgGuideCleanupInput{source: "source", request: candidate.request, identity: identity, witness: mgReclaimWitness(6, "old", false)}, "stale"},
		{"stale cursor", mgGuideCleanupInput{source: "source", after: "guide-request", request: candidate.request, identity: identity, witness: witness}, "cursor"},
		{"forged sequence", mgGuideCleanupInput{source: "source", request: candidate.request, identity: CommandIdentity{Incarnation: candidate.incarnation, Sequence: candidate.sequence, OperationID: [16]byte{42}}, witness: witness}, "sequence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := mgReclaimTurn(t, w, MGGuideAdvanceCleanup)
			changes := mgReclaimTurnChanges(t, turn)
			out, err := mgAdvanceGuideCleanupNative(t.Context(), turn, test.input)
			if !errors.Is(err, ErrConflict) || out != (mgGuideReclaimResult{}) {
				t.Fatal(test.reason, out, err)
			}
			if after := mgReclaimTurnChanges(t, turn); after != changes {
				t.Fatal(test.reason, "made effects")
			}
			mgGuideRollback(t, turn)
		})
	}
	if after := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request); after != before {
		t.Fatal("cleanup refusal changed durable state", before, after)
	}
}
