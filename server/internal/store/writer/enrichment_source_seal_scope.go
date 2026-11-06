package writer

import (
	"database/sql"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native effect preconditions, NOT disclosure authority or a verified plan.
// W's sole entry resolves history and verifies the FULL current source/policy/
// consent/language vector before invoking these private bodies in its turn.
type sealOwnerScope struct {
	ownerID, provider, lease, leaseUntil, afterItem, selectedID           string
	identity                                                              CommandIdentity
	request                                                               RequestDigest
	generation, last, ack, entityID, entityRevision, page, sourceRevision int64
	entityIncarnation                                                     [16]byte
	selection                                                             effectiveBeginSelection
	selectionRequested, selectionObserved                                 string
}

const sealOwnerPredicate = `o.id=? AND o.command_incarnation=? AND o.generation=?
 AND o.last_command_sequence=? AND o.acknowledged_command_sequence=? AND o.entity_id=? AND o.provider=?
 AND o.phase='enrichment-v1' AND o.status='staged' AND o.lease=? AND o.lease_until=?
 AND o.page=? AND o.after_item=? AND o.selected_id=? AND o.source_content_revision=?
 AND EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=o.entity_id AND e.kind=1 AND e.metadata_incarnation=? AND e.revision=?)
 AND EXISTS(SELECT 1 FROM metadata_subject_providers p WHERE p.entity_id=o.entity_id AND p.provider=o.provider AND p.role='primary'
 AND p.incarnation=? AND p.revision=? AND p.subject_kind='movie' AND p.provider_id=o.selected_id AND p.status='selected'
 AND p.requested_id=? AND p.observed_at=? AND p.accepted_evidence_id IS ? AND p.accepted_set_id IS ?
 AND p.accepted_publication_id IS ? AND p.accepted_track_id IS ?)`

func sealScopeParams() []stmt.Param {
	return []stmt.Param{
		{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "generation", Type: stmt.Integer},
		{Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "entity", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text},
		{Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text}, {Name: "page", Type: stmt.Integer},
		{Name: "after_item", Type: stmt.Text}, {Name: "selected_id", Type: stmt.Text}, {Name: "source_revision", Type: stmt.Integer},
		{Name: "entity_incarnation", Type: stmt.Blob}, {Name: "entity_revision", Type: stmt.Integer},
		{Name: "selection_incarnation", Type: stmt.Blob}, {Name: "selection_revision", Type: stmt.Integer},
		{Name: "requested_id", Type: stmt.Text}, {Name: "selection_observed", Type: stmt.Text},
		{Name: "accepted_evidence", Type: stmt.Text, Nullable: true}, {Name: "accepted_set", Type: stmt.Text, Nullable: true},
		{Name: "accepted_publication", Type: stmt.Text, Nullable: true}, {Name: "accepted_track", Type: stmt.Text, Nullable: true},
	}
}

func nullableSealText(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func (s sealOwnerScope) values() []any {
	return []any{s.ownerID, s.identity.Incarnation[:], s.generation, s.last, s.ack, s.entityID, s.provider,
		s.lease, s.leaseUntil, s.page, s.afterItem, s.selectedID, s.sourceRevision,
		s.entityIncarnation[:], s.entityRevision, s.selection.Incarnation[:], s.selection.Revision, s.selectionRequested, s.selectionObserved,
		nullableSealText(s.selection.Evidence), nullableSealText(s.selection.Set), nullableSealText(s.selection.Publication), nullableSealText(s.selection.Track)}
}

func (s sealOwnerScope) valid() bool {
	return s.ownerID != "" && s.provider != "" && s.selectedID != "" && s.generation > 0 && s.entityID > 0 && s.entityRevision > 0 && s.last >= 0 && s.last == s.ack &&
		s.identity.Sequence > s.last && s.identity.Sequence-s.last == 1 && s.sourceRevision > 0 && s.sourceRevision < math.MaxInt64 &&
		s.selection.Revision > 0 && s.selection.Role == "primary" && s.selection.Kind == "movie" && s.selection.Status == "selected" && s.selection.ID == s.selectedID &&
		s.lease != "" && s.leaseUntil != ""
}

func (s sealOwnerScope) checkLease(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || !s.valid() {
		return 0, ErrInvalid
	}
	expires, err := time.Parse(time.RFC3339Nano, s.leaseUntil)
	if err != nil {
		return 0, ErrInvalid
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return 0, err
	}
	if !now.Before(expires) || now.UnixMilli() < 0 {
		return 0, ErrConflict
	}
	return now.UnixMilli(), nil
}

func sealStep(turn *commandTurn, id stmt.ID) (statementStep, error) {
	if turn == nil || turn.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	s, ok := turn.steps[id]
	if !ok {
		return statementStep{}, invalid("seal_statement_whitelist")
	}
	return s.statementStep, nil
}

// Deterministic native component population, not disclosure/proof authority.
// HN qualifies these closed statements against the final complete producer.
func sealScopeCohort() []any {
	inc, entityInc := make([]byte, 16), make([]byte, 16)
	for i := range inc {
		inc[i] = byte(16 + i)
		entityInc[i] = byte(48 + i)
	}
	selection := make([]byte, 16)
	selection[0] = 71
	return []any{"operation-one", inc, int64(31), int64(11), int64(11), int64(101), "fixture", "native-lease", "2100-11-05T12:00:00Z", int64(59), "opaque-page:61", "opaque:selection", int64(1), entityInc, int64(203), selection, int64(73), "", "2026-11-06T12:00:00Z", nil, nil, nil, nil}
}
