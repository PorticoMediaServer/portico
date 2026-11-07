package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The root adapter follows W's existing original-result ACK rule. It verifies
// the complete closed operation payload before moving only the historical
// floor/due/revision. Receipt rows, payloads and retained-byte quota stay held.
// Other closed purpose result types must supply their own sole-codec verifier.
func acknowledgeUORootOperation(ctx context.Context, turn *commandTurn, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.turn != turn || nowMS <= 0 || !lookup.reference.valid() {
		return 0, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o, q := lookup.owner, lookup.reference
	if lookup.decision == receiptRetired && q.identity.Sequence <= o.facts.acknowledgedSequence {
		return o.facts.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.identity.Sequence != o.facts.lastSequence || q.identity.Sequence <= o.facts.acknowledgedSequence || o.rows <= 0 || o.variableBytes <= 0 || o.revision == math.MaxInt64 {
		return 0, ErrResolveRequired
	}
	if _, err := restoreUORootOperation(ctx, turn, lookup); err != nil {
		return 0, err
	}
	nextDue := nowMS
	var oldDue any
	if o.due.Valid {
		oldDue = o.due.Int64
		nextDue = min(nextDue, o.due.Int64)
	}
	floor, err = wc008DraftInteger(ctx, turn, uoRootAckPoint, q.identity.Sequence, nextDue, o.revision+1, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence, oldDue)
	if err != nil {
		return 0, err
	}
	if floor != q.identity.Sequence {
		return 0, ErrResolveRequired
	}
	return floor, turn.checkLifetimes(ctx)
}

const uoRootAckPoint stmt.ID = "uo.root.ack.point"

func uoRootAckDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{{ID: uoRootAckPoint, Kind: stmt.Write, Owner: "store/writer UO root ACK", CallSites: []string{"store/writer/uo_root_ack.go"}, SQL: `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING acknowledged_command_sequence`, Params: []stmt.Param{i("floor"), i("due"), i("next_revision"), {Name: "root", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, i("last"), i("old_floor"), i("old_revision"), i("rows"), i("bytes"), i("cursor"), {Name: "old_due", Type: stmt.Integer, Nullable: true}}, Plan: stmt.Plan{Hot: true}}}
}
