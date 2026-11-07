package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const searchReclaimerAck stmt.ID = "search.reclaim.ack.owner"

func searchReclaimerProtocolDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: searchReclaimerAck, Owner: "writer.search.reclaimer", CallSites: []string{"writer/search_reclaimer_protocol.go"}, Kind: stmt.Write,
		SQL:     `UPDATE search_reclaim_jobs SET acknowledged_command_sequence=? WHERE id=? AND publication_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`,
		Params:  []stmt.Param{{Name: "next_ack", Type: stmt.Integer}, {Name: "job", Type: stmt.Integer}, {Name: "publication", Type: stmt.Integer}, {Name: "incarnation", Type: stmt.Blob}, {Name: "expected_last", Type: stmt.Integer}, {Name: "expected_ack", Type: stmt.Integer}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"search_reclaim_jobs"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "unresolved_result", Bindings: []any{int64(1), int64(2), int64(13), []byte{61, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0)}}, {Name: "stale_floor", Bindings: []any{int64(1), int64(2), int64(13), []byte{61, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(1)}}},
	}}
}

// Native ACK adapter after the SAME-frame common historical classifier. It
// touches only this reclaimer's floor, never a publisher job or fresh lease.
func acknowledgeSearchReclaimer(ctx context.Context, t *commandTurn, q searchReclaimerReceiptRequest, owner receiptOwnerFacts, decision receiptDecision, saved searchFragmentReclaimResult) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if ctx == nil || t == nil || t.transaction == nil || t.transaction.descriptor.ID != SearchFragmentReclaim {
		return 0, ErrInvalid
	}
	if !owner.exists || owner.incarnation != q.Receipt.Identity.Incarnation {
		return 0, ErrResolveRequired
	}
	if decision == receiptRetired {
		if q.Receipt.Identity.Sequence > owner.acknowledgedSequence {
			return 0, ErrResolveRequired
		}
		return owner.acknowledgedSequence, nil
	}
	if decision != receiptReplay || saved.JobID != q.ReclaimerID || saved.PublicationID != q.PublicationID || q.Receipt.Identity.Sequence != owner.lastSequence {
		return 0, ErrResolveRequired
	}
	step, err := searchPublisherStep(t, searchReclaimerAck)
	if err != nil {
		return 0, err
	}
	result, err := t.exec(ctx, step, q.Receipt.Identity.Sequence, q.ReclaimerID, q.PublicationID, q.Receipt.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrConflict
	}
	return q.Receipt.Identity.Sequence, nil
}
