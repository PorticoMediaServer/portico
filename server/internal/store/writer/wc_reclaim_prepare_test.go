package writer

import (
	"math"
	"reflect"
	"testing"
)

func wcTestReclaimInput() wcReclaimInput {
	inc := [16]byte{1}
	return wcReclaimInput{
		owner: wcReclaimOwner{playbackID: "playback-one", incarnation: inc,
			lastSequence: 4, acknowledgedSequence: 4, rows: 1, variableBytes: 200, due: true, dueMS: 123},
		command:    CommandIdentity{Incarnation: inc, Sequence: 5, OperationID: [16]byte{2}},
		firstProbe: wcReclaimProbe{"playback-one", inc, 0, 4, 3},
		child:      &wcReclaimChild{wcReclaimKey{"playback-one", inc, 3}, 200},
		probe:      wcReclaimProbe{"playback-one", inc, 3, 4, 0},
	}
}

func wcTestReclaimSlot() wcReclaimSlot {
	return wcReclaimSlot{present: true,
		reference: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{3}},
			RequestDigest: RequestDigest{4}}, resultDigest: [32]byte{5}, floor: 1, noNext: true, appliedMS: 100}
}

func TestWCReclaimOneSourceChildAndDetachedResult(t *testing.T) {
	in := wcTestReclaimInput()
	before := *in.child
	out, err := prepareWCReclaim(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.remove || out.key != before.key || out.removedVariableBytes != 200 ||
		out.afterSequence != 3 || out.remainingRows != 0 || out.remainingVariableBytes != 0 || !out.noNext {
		t.Fatalf("wrong single-child transition: %+v", out)
	}
	if *in.child != before {
		t.Fatal("preparation mutated native observations")
	}
	*in.child = wcReclaimChild{}
	if out.key != before.key || out.removedVariableBytes != 200 {
		t.Fatal("result retained caller's mutable child")
	}
}

func TestWCReclaimScheduledZeroRemovalAndRemainingDebt(t *testing.T) {
	in := wcTestReclaimInput()
	in.child, in.owner.rows, in.owner.variableBytes = nil, 0, 0
	in.firstProbe.nextSequence = 0
	in.probe.afterSequence = 0
	out, err := prepareWCReclaim(in)
	if err != nil || out.remove || out.key != (wcReclaimKey{}) || !out.noNext || out.afterSequence != 0 {
		t.Fatalf("scheduled zero removal: %+v %v", out, err)
	}
	// The scalar result may describe zero removals once, but no due state may
	// manufacture another idle cleanup merely because the shared ACK advances.
	in.owner.due, in.owner.dueMS = false, 0
	wcAssertReclaimRefused(t, in)

	in = wcTestReclaimInput()
	in.owner.rows, in.owner.variableBytes = 2, 450
	in.probe.nextSequence = 4
	out, err = prepareWCReclaim(in)
	if err != nil || out.noNext || out.remainingRows != 1 || out.remainingVariableBytes != 250 || out.afterSequence != 3 {
		t.Fatalf("remaining debt: %+v %v", out, err)
	}
}

func TestWCReclaimRejectsWrongFullKeysFloorRangeAndDebt(t *testing.T) {
	changes := []struct {
		name string
		edit func(*wcReclaimInput)
	}{
		{"wrong_command_incarnation", func(in *wcReclaimInput) { in.command.Incarnation[0]++ }},
		{"no_operation", func(in *wcReclaimInput) { in.command.OperationID = [16]byte{} }},
		{"skipped_sequence", func(in *wcReclaimInput) { in.command.Sequence++ }},
		{"unresolved_predecessor", func(in *wcReclaimInput) { in.owner.acknowledgedSequence-- }},
		{"invalid_floor", func(in *wcReclaimInput) { in.owner.acknowledgedSequence++ }},
		{"wrong_playback", func(in *wcReclaimInput) { in.child.key.playbackID = "other" }},
		{"wrong_incarnation", func(in *wcReclaimInput) { in.child.key.incarnation[0]++ }},
		{"unacked_child", func(in *wcReclaimInput) { in.child.key.sequence = 5 }},
		{"already_removed", func(in *wcReclaimInput) { in.child.key.sequence = 0 }},
		{"negative_debt", func(in *wcReclaimInput) { in.owner.variableBytes = -1 }},
		{"debt_underflow", func(in *wcReclaimInput) { in.child.variableBytes++ }},
		{"zero_child_bytes", func(in *wcReclaimInput) { in.child.variableBytes = 0 }},
		{"missing_child_debt", func(in *wcReclaimInput) { in.owner.rows = 0 }},
		{"wrong_first_owner", func(in *wcReclaimInput) { in.firstProbe.playbackID = "other" }},
		{"wrong_first_incarnation", func(in *wcReclaimInput) { in.firstProbe.incarnation[0]++ }},
		{"wrong_first_cursor", func(in *wcReclaimInput) { in.firstProbe.afterSequence++ }},
		{"wrong_first_floor", func(in *wcReclaimInput) { in.firstProbe.throughACK-- }},
		{"negative_first", func(in *wcReclaimInput) { in.firstProbe.nextSequence = -1 }},
		{"first_at_cursor", func(in *wcReclaimInput) { in.firstProbe.afterSequence = 1; in.firstProbe.nextSequence = 1 }},
		{"first_above_floor", func(in *wcReclaimInput) { in.firstProbe.nextSequence = 5 }},
		{"missing_first", func(in *wcReclaimInput) { in.firstProbe.nextSequence = 0 }},
		{"earlier_first", func(in *wcReclaimInput) { in.firstProbe.nextSequence-- }},
		{"later_first", func(in *wcReclaimInput) { in.firstProbe.nextSequence++ }},
		{"wrong_probe_owner", func(in *wcReclaimInput) { in.probe.playbackID = "other" }},
		{"wrong_probe_incarnation", func(in *wcReclaimInput) { in.probe.incarnation[0]++ }},
		{"wrong_probe_cursor", func(in *wcReclaimInput) { in.probe.afterSequence-- }},
		{"wrong_probe_floor", func(in *wcReclaimInput) { in.probe.throughACK-- }},
		{"probe_at_cursor", func(in *wcReclaimInput) { in.probe.nextSequence = 3 }},
		{"probe_above_floor", func(in *wcReclaimInput) { in.probe.nextSequence = 5 }},
		{"phantom_next", func(in *wcReclaimInput) { in.probe.nextSequence = 4 }},
		{"hidden_remaining_child", func(in *wcReclaimInput) { in.owner.rows = 2; in.owner.variableBytes = 400 }},
		{"row_byte_disagreement", func(in *wcReclaimInput) { in.owner.variableBytes++ }},
		{"probe_without_first_child", func(in *wcReclaimInput) { in.child = nil; in.probe.afterSequence = 0; in.probe.nextSequence = 3 }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			in := wcTestReclaimInput()
			tc.edit(&in)
			wcAssertReclaimRefused(t, in)
		})
	}
}

func TestWCReclaimAcknowledgedSlotMustMatchEveryOldFact(t *testing.T) {
	in := wcTestReclaimInput()
	in.oldSlot, in.expectedOldSlot = wcTestReclaimSlot(), wcTestReclaimSlot()
	if _, err := prepareWCReclaim(in); err != nil {
		t.Fatal(err)
	}
	// A newer SOURCE command may have advanced last/ACK without touching this
	// old cleanup slot. Only a later cleanup may replace its exact old context.
	changes := []func(*wcReclaimSlot){
		func(s *wcReclaimSlot) { s.present = false },
		func(s *wcReclaimSlot) { s.reference.Identity.Incarnation[0]++ },
		func(s *wcReclaimSlot) { s.reference.Identity.Sequence++ },
		func(s *wcReclaimSlot) { s.reference.Identity.OperationID[0]++ },
		func(s *wcReclaimSlot) { s.reference.RequestDigest[0]++ },
		func(s *wcReclaimSlot) { s.resultDigest[0]++ },
		func(s *wcReclaimSlot) { s.floor++ },
		func(s *wcReclaimSlot) { s.oldCursor++ },
		func(s *wcReclaimSlot) { s.removedSequence++ },
		func(s *wcReclaimSlot) { s.removedCount++ },
		func(s *wcReclaimSlot) { s.removedBytes++ },
		func(s *wcReclaimSlot) { s.nextCursor++ },
		func(s *wcReclaimSlot) { s.noNext = false },
		func(s *wcReclaimSlot) { s.appliedMS++ },
	}
	for i, edit := range changes {
		changed := in
		edit(&changed.oldSlot)
		if out, err := prepareWCReclaim(changed); err == nil || out != (wcReclaimPrepared{}) {
			t.Fatalf("old slot field %d was not fenced: %+v %v", i, out, err)
		}
	}
	in.oldSlot.reference.Identity.Sequence = 5
	in.expectedOldSlot = in.oldSlot
	wcAssertReclaimRefused(t, in) // exact equality is not an ACK proof
	in = wcTestReclaimInput()
	in.owner.afterSequence = 1
	wcAssertReclaimRefused(t, in) // no empty slot after a physical removal
	in = wcTestReclaimInput()
	in.oldSlot = wcTestReclaimSlot()
	in.oldSlot.removedCount, in.oldSlot.removedSequence, in.oldSlot.removedBytes = 1, 1, 200
	in.oldSlot.nextCursor, in.oldSlot.noNext = 1, false
	in.expectedOldSlot = in.oldSlot
	in.owner.afterSequence, in.owner.rows, in.owner.variableBytes = 1, 0, 0
	in.child, in.probe.afterSequence = nil, 1
	wcAssertReclaimRefused(t, in) // a known remaining child cannot disappear without a later slot
}

func TestWCReclaimInt64AndCommandSequenceGaps(t *testing.T) {
	in := wcTestReclaimInput()
	in.owner.lastSequence, in.owner.acknowledgedSequence = math.MaxInt64-1, math.MaxInt64-1
	in.command.Sequence = math.MaxInt64
	in.owner.variableBytes, in.child.variableBytes = math.MaxInt64, math.MaxInt64
	in.child.key.sequence = math.MaxInt64 - 10 // intervening cleanup commands leave gaps
	in.firstProbe.throughACK, in.firstProbe.nextSequence = in.owner.acknowledgedSequence, in.child.key.sequence
	in.probe.afterSequence, in.probe.throughACK = in.child.key.sequence, in.owner.acknowledgedSequence
	out, err := prepareWCReclaim(in)
	if err != nil || out.afterSequence != math.MaxInt64-10 || out.remainingVariableBytes != 0 {
		t.Fatalf("bounded int64 transition: %+v %v", out, err)
	}
	in.owner.lastSequence, in.owner.acknowledgedSequence = math.MaxInt64, math.MaxInt64
	in.probe.throughACK = math.MaxInt64
	wcAssertReclaimRefused(t, in) // exhaustion cannot wrap to a fresh identity
}

func TestWCReclaimLedgerAcrossSourceAndCleanupCommands(t *testing.T) {
	in := wcTestReclaimInput()
	children := []wcReclaimChild{}
	for seq := int64(1); seq <= 4; seq++ {
		children = append(children, wcReclaimChild{wcReclaimKey{"playback-one", in.owner.incarnation, seq}, 300 + seq})
	}
	var removed int64
	seen := map[int64]bool{}
	for step := 0; len(children) != 0; step++ {
		var total int64
		for _, c := range children {
			total += c.variableBytes
		}
		in.owner.rows, in.owner.variableBytes = int64(len(children)), total
		in.command.Sequence = in.owner.lastSequence + 1
		in.command.OperationID = [16]byte{byte(step + 10)}
		in.child = &children[0]
		in.firstProbe = wcReclaimProbe{in.owner.playbackID, in.owner.incarnation,
			in.owner.afterSequence, in.owner.acknowledgedSequence, children[0].key.sequence}
		in.probe = wcReclaimProbe{in.owner.playbackID, in.owner.incarnation,
			in.child.key.sequence, in.owner.acknowledgedSequence, 0}
		if len(children) > 1 {
			in.probe.nextSequence = children[1].key.sequence
		}
		out, err := prepareWCReclaim(in)
		if err != nil || !out.remove || out.key != children[0].key || seen[out.key.sequence] {
			t.Fatalf("step %d: %+v %v", step, out, err)
		}
		seen[out.key.sequence] = true
		removed += out.removedVariableBytes
		children = children[1:]
		if out.remainingRows != int64(len(children)) || out.remainingVariableBytes != total-out.removedVariableBytes {
			t.Fatal("physical removal and ledger diverged")
		}
		in.oldSlot = wcReclaimSlot{present: true,
			reference: ReceiptReference{Identity: in.command, RequestDigest: RequestDigest{1}}, resultDigest: [32]byte{2},
			floor: in.owner.acknowledgedSequence, oldCursor: in.owner.afterSequence,
			removedSequence: out.key.sequence, removedCount: 1, removedBytes: out.removedVariableBytes,
			nextCursor: out.afterSequence, noNext: out.noNext, appliedMS: int64(step)}
		in.expectedOldSlot = in.oldSlot
		in.owner.afterSequence = out.afterSequence
		in.owner.lastSequence, in.owner.acknowledgedSequence = in.command.Sequence, in.command.Sequence
		if step == 1 {
			// A later source command uses the shared sequence but preserves the
			// cleanup slot. The removal cursor must skip no physical child.
			in.owner.lastSequence++
			in.owner.acknowledgedSequence++
			children = append(children, wcReclaimChild{
				wcReclaimKey{in.owner.playbackID, in.owner.incarnation, in.owner.lastSequence}, 900})
		}
	}
	if removed != 2110 || len(seen) != 5 || in.owner.afterSequence == in.owner.acknowledgedSequence {
		t.Fatalf("final cursor/debit used command floor: debit=%d cursor=%d ack=%d", removed, in.owner.afterSequence, in.owner.acknowledgedSequence)
	}
}

func TestWCReclaimRejectSkippedAcknowledgedChildren(t *testing.T) {
	// SOURCE 1..6; cleanup 7 removes 1; SOURCE 8; cleanup 9 removes 2;
	// SOURCE 10; cleanup 11 removes 3; SOURCE 12. Selecting 8 must not
	// strand acknowledged children 4, 5 and 6 behind the removal cursor.
	in := wcTestReclaimInput()
	in.owner.lastSequence, in.owner.acknowledgedSequence = 12, 12
	in.owner.afterSequence, in.owner.rows, in.owner.variableBytes = 3, 6, 1200
	in.command.Sequence = 13
	in.oldSlot = wcTestReclaimSlot()
	in.oldSlot.reference.Identity.Sequence = 11
	in.oldSlot.floor, in.oldSlot.oldCursor = 10, 2
	in.oldSlot.removedSequence, in.oldSlot.removedCount, in.oldSlot.removedBytes = 3, 1, 200
	in.oldSlot.nextCursor, in.oldSlot.noNext = 3, false
	in.expectedOldSlot = in.oldSlot
	in.firstProbe.afterSequence, in.firstProbe.throughACK, in.firstProbe.nextSequence = 3, 12, 4
	in.child.key.sequence = 8
	in.probe.afterSequence, in.probe.throughACK, in.probe.nextSequence = 8, 12, 10
	out, err := prepareWCReclaim(in)
	if err == nil || out != (wcReclaimPrepared{}) {
		t.Fatalf("accepted skipped ACKed children 4,5,6: %+v %v", out, err)
	}
	// The same native owner/slot/debt permits the actual first child. Sequence
	// gaps remain valid; the cursor advances to the observed child, not ACK.
	in.child.key.sequence = 4
	in.probe.afterSequence, in.probe.nextSequence = 4, 5
	out, err = prepareWCReclaim(in)
	if err != nil || !out.remove || out.key.sequence != 4 || out.afterSequence != 4 ||
		out.remainingRows != 5 || out.remainingVariableBytes != 1000 || out.noNext {
		t.Fatalf("refused actual first ACKed child: %+v %v", out, err)
	}
}

func wcAssertReclaimRefused(t *testing.T, in wcReclaimInput) {
	t.Helper()
	out, err := prepareWCReclaim(in)
	if err == nil || !reflect.DeepEqual(out, wcReclaimPrepared{}) {
		t.Fatalf("invalid transition returned output: %+v %v", out, err)
	}
}
