package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
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

const sealOwnerPredicate = `id=? AND command_incarnation=? AND generation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND entity_id=? AND provider=? AND phase='enrichment-v1' AND status='staged' AND lease=? AND lease_until=? AND page=? AND after_item=? AND selected_id=? AND source_content_revision=?`
const sealTargetOwnerPredicate = sealOwnerPredicate

func sealScopeParams() []stmt.Param {
	return []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "generation", Type: stmt.Integer}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "entity", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}, {Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text}, {Name: "page", Type: stmt.Integer}, {Name: "after_item", Type: stmt.Text}, {Name: "selected_id", Type: stmt.Text}, {Name: "source_revision", Type: stmt.Integer}}
}
func (s sealOwnerScope) values() []any {
	return []any{s.ownerID, s.identity.Incarnation[:], s.generation, s.last, s.ack, s.entityID, s.provider, s.lease, s.leaseUntil, s.page, s.afterItem, s.selectedID, s.sourceRevision}
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
	inc := make([]byte, 16)
	for i := range inc {
		inc[i] = byte(16 + i)
	}
	return []any{"operation-one", inc, int64(31), int64(11), int64(11), int64(101), "fixture", "native-lease", "2100-11-05T12:00:00Z", int64(59), "opaque-page:61", "opaque:selection", int64(1)}
}

const sealObservedOwner stmt.ID = "enrichment.stage.owner_observed"

func enrichmentSealScopeDefinitions() []stmt.Definition {
	defs := []stmt.Definition{{ID: sealObservedOwner, Owner: "writer.enrichment", Kind: stmt.Read, CallSites: []string{"writer/enrichment_source_seal_scope.go:observeSealOwner"}, SQL: `SELECT o.command_incarnation,o.generation,o.last_command_sequence,o.acknowledged_command_sequence,o.entity_id,o.provider,o.phase,o.status,o.lease,o.lease_until,o.page,o.after_item,o.selected_id,o.source_content_revision,e.metadata_incarnation,e.revision,e.kind,p.incarnation,p.revision,p.role,p.subject_kind,p.provider_id,p.status,p.requested_id,p.observed_at,p.accepted_evidence_id,p.accepted_set_id,p.accepted_publication_id,p.accepted_track_id
 FROM metadata_provider_operations AS o INNER JOIN catalog_entities AS e ON e.id=o.entity_id INNER JOIN metadata_subject_providers AS p ON p.entity_id=o.entity_id AND p.provider=o.provider WHERE o.id=? AND p.role='primary'`, Params: []stmt.Param{{Name: "owner", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "native_selected_owner", Bindings: []any{"operation-one"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}, Seeks: []string{"entity_id=? AND provider=? AND role=?"}}}}
	return enrichmentStagePlanHints(defs)
}

// Complete current witness read is separate from scalar DML. There is no
// callback/interleaving between it and the covering CAS on this SAME frame.
// It is a domain precondition, not W's full principal/source/policy proof.
func observeSealOwner(ctx context.Context, turn *commandTurn, want sealOwnerScope) (int64, string, error) {
	if !want.valid() {
		return 0, "", ErrInvalid
	}
	step, err := sealStep(turn, sealObservedOwner)
	if err != nil {
		return 0, "", err
	}
	row, err := turn.queryRow(ctx, step, want.ownerID)
	if err != nil {
		return 0, "", err
	}
	var inc, einc, pinc []byte
	var g, last, ack, eid, page, r, er, kind int64
	var provider, phase, status, lease, until, after, selected, requested, observed string
	var p effectiveBeginSelection
	if err := row.Scan(&inc, &g, &last, &ack, &eid, &provider, &phase, &status, &lease, &until, &page, &after, &selected, &r, &einc, &er, &kind, &pinc, &p.Revision, &p.Role, &p.Kind, &p.ID, &p.Status, &requested, &observed, &p.Evidence, &p.Set, &p.Publication, &p.Track); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", ErrConflict
		}
		return 0, "", err
	}
	if len(pinc) != 16 {
		return 0, "", ErrConflict
	}
	copy(p.Incarnation[:], pinc)
	if !bytes.Equal(inc, want.identity.Incarnation[:]) || g != want.generation || last != want.last || ack != want.ack || eid != want.entityID || provider != want.provider || phase != effectiveBeginPhase || status != "staged" || lease != want.lease || until != want.leaseUntil || page != want.page || after != want.afterItem || selected != want.selectedID || r != want.sourceRevision || !bytes.Equal(einc, want.entityIncarnation[:]) || er != want.entityRevision || kind != 1 || p != want.selection || requested != want.selectionRequested || observed != want.selectionObserved {
		return 0, "", ErrConflict
	}
	return er, after, nil
}
