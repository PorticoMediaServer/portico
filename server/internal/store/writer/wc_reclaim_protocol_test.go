package writer

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"reflect"
	"strings"
	"testing"
)

func wcReclaimRawFixture() (wcReclaimRawSlot, wcReclaimOwner) {
	raw := wcReclaimRawSlot{sequence: wcPresent(5), version: wcPresent(1), floor: wcPresent(4), oldCursor: wcPresent(0),
		removedSequence: wcPresent(1), removedCount: wcPresent(1), removedBytes: wcPresent(137), nextCursor: wcPresent(1),
		noNext: wcPresent(0), appliedMS: wcPresent(123), resultCount: wcPresent(1), kind: sql.NullString{String: wcReclaimKind, Valid: true},
		operation: make([]byte, 16), request: make([]byte, 32), digest: make([]byte, 32)}
	raw.operation[0], raw.request[0], raw.digest[0] = 2, 3, 4
	owner := wcReclaimOwner{playbackID: "playback-音", incarnation: [16]byte{1}, lastSequence: 5, acknowledgedSequence: 4, afterSequence: 1, rows: 1, variableBytes: 137, due: true, dueMS: 120}
	return raw, owner
}
func TestWCReclaimProtocolSlotShapes(t *testing.T) {
	raw, owner := wcReclaimRawFixture()
	slot, version, err := decodeWCReclaimSlotShape(raw, owner)
	if err != nil || !slot.present || version != 1 || slot.reference.Identity.Sequence != 5 || slot.removedCount != 1 || slot.removedSequence != 1 || slot.noNext {
		t.Fatal(slot, version, err)
	}
	// Original unacknowledged results are decodable, but not replaceable OLD slots.
	if validWCReclaimSlot(slot, owner) {
		t.Fatal("unacknowledged slot replaceable")
	}
	owner.acknowledgedSequence = 5
	if !validWCReclaimSlot(slot, owner) {
		t.Fatal("acknowledged old slot rejected")
	}
	zero := raw
	zero.removedSequence, zero.removedCount, zero.removedBytes = sql.NullInt64{}, wcPresent(0), wcPresent(0)
	zero.nextCursor, zero.noNext = wcPresent(0), wcPresent(1)
	owner.afterSequence = 0
	if s, _, e := decodeWCReclaimSlotShape(zero, owner); e != nil || !s.present || s.removedCount != 0 || !s.noNext {
		t.Fatal("zero removal scalar treated as absent", s, e)
	}
	if s, v, e := decodeWCReclaimSlotShape(wcReclaimRawSlot{}, owner); e != nil || s != (wcReclaimSlot{}) || v != 0 {
		t.Fatal("empty slot", s, v, e)
	}
	for _, name := range []string{"partial_null", "short_operation", "kind", "no_next_two", "zero_digest", "ahead", "cursor_mismatch"} {
		bad, o := wcReclaimRawFixture()
		switch name {
		case "partial_null":
			bad.resultCount = sql.NullInt64{}
		case "short_operation":
			bad.operation = []byte{2}
		case "kind":
			bad.kind.String = "drain_piece"
		case "no_next_two":
			bad.noNext = wcPresent(2)
		case "zero_digest":
			bad.digest = make([]byte, 32)
		case "ahead":
			o.lastSequence = 4
		case "cursor_mismatch":
			o.afterSequence = 2
		}
		if _, _, e := decodeWCReclaimSlotShape(bad, o); e == nil {
			t.Fatal("malformed native slot accepted", name)
		}
	}
}
func TestWCReclaimProtocolDefinitionsClosed(t *testing.T) {
	defs := wcReclaimProtocolDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		switch d.ID {
		case wcReclaimExactSlot:
			if !strings.Contains(d.SQL, "AND reclaim_command_sequence=?") || len(d.Params) != 10 {
				t.Fatal("lookup can mistake newer scalar for old proof")
			}
		case wcReclaimAckFloor:
			assignment := strings.Split(strings.Split(d.SQL, " SET ")[1], " WHERE ")[0]
			if assignment != "command_acknowledged_sequence=?" {
				t.Fatal("ACK changed retained scalar/debt/due/bytes", assignment)
			}
		}
	}
	if checkWCReclaimPurpose(ProgressAccept) == nil || checkWCReclaimPurpose(0) == nil {
		t.Fatal("accepted source alias or unknown purpose")
	}
}
func TestWCReclaimProtocolNativePrepare(t *testing.T) {
	// Existing canonical fixture, preparation only: no custody, floor, source
	// writes or alternate purpose. Governed execution awaits parent integration.
	e, _ := testCanonicalExecutor(t)
	wc008NativePrepare(t, e, wcReclaimProtocolDefinitions())
}
func TestWCReclaimProtocolFinishSuppressesInvalidOutputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := lookupWCReclaim(ctx, nil, ProgressAccept, wcProgressScope{}, ReclaimReceiptRequest{}); err == nil || !reflect.DeepEqual(out, wcReclaimLookup{}) {
		t.Fatal(out, err)
	}
	if out, err := applyWCReclaim(ctx, nil, ProgressAccept, wcReclaimRequest{}, ReclaimReceiptRequest{}, wcReclaimLookup{}, 0); err == nil || out != (wcReclaimApplied{}) {
		t.Fatal(out, err)
	}
	if floor, err := acknowledgeWCReclaim(ctx, nil, ProgressAccept, ReclaimReceiptRequest{}, wcReclaimLookup{}); err == nil || floor != 0 {
		t.Fatal(floor, err)
	}
}
