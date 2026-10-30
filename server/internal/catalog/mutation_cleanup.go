package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

const mutationCleanupResource = "cleanup"

// EnqueueMutationCleanupTx records actual acknowledged mutation debt on the
// existing operations scheduler. There is at most one active cleanup job.
// This internal housekeeping wake never refuses an authorized catalog delete.
func EnqueueMutationCleanupTx(ctx context.Context, tx *sql.Tx) error {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_operations WHERE kind=? AND resource=? AND state IN('queued','running','cancellation-requested','reconciling','paused'))`, catalogMutationKind, mutationCleanupResource).Scan(&active); err != nil {
		return err
	}
	if active {
		return nil
	}
	now := time.Now().UnixMilli()
	id := "cmgc-" + identity.Token()
	_, err := tx.ExecContext(ctx, `INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision) SELECT ?,?,?,'','domain','queued','catalog cleanup',1,0,?,?,?,'','','',revision FROM console_settings_revision WHERE singleton=1`, id, catalogMutationKind, mutationCleanupResource, now, now, now)
	if err != nil {
		return err
	}
	return operations.AppendOperationTx(tx, id)
}
func acknowledgeMutationTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET acknowledged=1 WHERE mutation_id=? AND generation=? AND status='terminal' AND acknowledged=0`, ref.ID, ref.Generation)
	if err != nil {
		return err
	}
	return EnqueueMutationCleanupTx(ctx, tx)
}
func ackOwnedSubjectTx(ctx context.Context, tx *sql.Tx, owner, id string, subject int64, mode string) error {
	var ref compactcatalog.MutationRef
	err := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind=? AND owner_id=? AND subject_id=? AND mode=? AND status='terminal' ORDER BY owner_generation DESC LIMIT 1`, owner, id, subject, mode).Scan(&ref.ID, &ref.Generation)
	if err != nil {
		return err
	}
	return acknowledgeMutationTx(ctx, tx, ref)
}
func ackSupersededMissingTx(ctx context.Context, tx *sql.Tx, public string, generation int64) error {
	var ref compactcatalog.MutationRef
	err := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='missing' AND owner_id=? AND owner_generation<>? AND status='terminal' AND outcome='stale' AND acknowledged=0 ORDER BY owner_generation DESC LIMIT 1`, public, generation).Scan(&ref.ID, &ref.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return acknowledgeMutationTx(ctx, tx, ref)
}

type mutationConsoleCustody struct {
	ID, State, Cursor string
	Revision          int64
}

func readMutationConsoleTx(ctx context.Context, tx *sql.Tx, resource string) (c mutationConsoleCustody, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id,state,domain_id,revision FROM console_operations WHERE kind=? AND resource=? AND state IN('queued','running','reconciling')`, catalogMutationKind, resource).Scan(&c.ID, &c.State, &c.Cursor, &c.Revision)
	return
}
func completeMutationConsoleTx(ctx context.Context, tx *sql.Tx, c mutationConsoleCustody, resource, outcome string) error {
	result, err := tx.ExecContext(ctx, `UPDATE console_operations SET state='succeeded',phase=?,revision=revision+1,updated_ms=? WHERE id=? AND kind=? AND resource=? AND revision=? AND state=?`, "catalog "+outcome, time.Now().UnixMilli(), c.ID, catalogMutationKind, resource, c.Revision, c.State)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return compactcatalog.ErrMutationStale
	}
	return operations.AppendOperationTx(tx, c.ID)
}

// cleanupMutationTx changes at most one leaf (plus its parent's maintained
// debt), or <=16 root acknowledgement facts plus one console cursor. It never
// performs catalog/evidence deletion or derives management authority.
func cleanupMutationTx(ctx context.Context, tx *sql.Tx, c mutationConsoleCustody) (done bool, err error) {
	var leaf string
	err = tx.QueryRowContext(ctx, `SELECT mutation_id FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_gc_leaf WHERE status='terminal' AND acknowledged=1 AND child_count=0 ORDER BY mutation_id LIMIT 1`).Scan(&leaf)
	if err == nil {
		// Parent consumes current_child before acknowledging. The indexed FK is
		// an additional fail-closed check, never a scan to select a different leaf.
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_entity_mutations WHERE mutation_id=? AND status='terminal' AND acknowledged=1 AND child_count=0 AND NOT EXISTS(SELECT 1 FROM catalog_entity_mutations parent WHERE parent.current_child=?)`, leaf, leaf)
		return false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Administrative children become consumable only after the actual
	// final receipt atomically acknowledges their first-admission anchor.
	var parent string
	e := tx.QueryRowContext(ctx, `SELECT mutation_id FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_gc_leaf WHERE status='terminal' AND acknowledged=1 AND child_count>0 ORDER BY child_count,mutation_id LIMIT 1`).Scan(&parent)
	if e == nil {
		var child string
		var terminal bool
		var debt int
		if e = tx.QueryRowContext(ctx, `SELECT mutation_id,status='terminal',child_count FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_parent_key WHERE parent_mutation_id=? ORDER BY mutation_id LIMIT 1`, parent).Scan(&child, &terminal, &debt); e != nil {
			return false, e
		}
		if !terminal || debt != 0 {
			return false, compactcatalog.ErrMutationStale
		}
		_, e = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET acknowledged=1 WHERE mutation_id=? AND status='terminal'`, child)
		return false, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	type root struct {
		ref                               compactcatalog.MutationRef
		owner, id, cause, outcome, public string
		generation                        int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT mutation_id,generation,owner_kind,owner_id,cause_operation,outcome,public_id,owner_generation FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_ack_roots WHERE status='terminal' AND acknowledged=0 AND parent_mutation_id IS NULL AND mutation_id>? ORDER BY mutation_id LIMIT 16`, c.Cursor)
	if err != nil {
		return false, err
	}
	var roots []root
	for rows.Next() {
		var r root
		if err = rows.Scan(&r.ref.ID, &r.ref.Generation, &r.owner, &r.id, &r.cause, &r.outcome, &r.public, &r.generation); err != nil {
			rows.Close()
			return false, err
		}
		roots = append(roots, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(roots) == 0 {
		// Exhaustion and this console operation's terminal outcome are one CAS
		// writer; a later producer necessarily creates a new wake, without a gap.
		return true, completeMutationConsoleTx(ctx, tx, c, mutationCleanupResource, "cleanup complete")
	}
	cursor := c.Cursor
	for _, r := range roots {
		cursor = r.ref.ID
		ack := false
		switch r.owner {
		case "administration":
			operation, digest, ok := strings.Cut(r.id, ":")
			if ok && r.cause == operation {
				err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_receipts WHERE scope='media-delete' AND operation_id=? AND digest=?)`, operation, digest).Scan(&ack)
			}
		case "missing":
			// A same-cutoff stale refusal remains until actual missing membership is
			// gone/newer, or a fresh admitted command supersedes it explicitly.
			if r.outcome != "stale" {
				ack = true
			} else {
				err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_missing_titles m ON m.entity_id=e.id WHERE e.public_id=pid_blob(?) AND m.missing_since_ms<=?)`, r.public, r.generation).Scan(&ack)
			}
		case "identity", "mb-adoption":
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_operations WHERE kind=? AND resource=? AND state='succeeded')`, catalogMutationKind, r.ref.ID).Scan(&ack)
			// Manual title/merge and library outcomes are acknowledged only by the
			// actual synchronous/domain owner at consumption, never from a pointer.
		}
		if err != nil {
			return false, err
		}
		if ack {
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET acknowledged=1 WHERE mutation_id=? AND generation=? AND status='terminal' AND acknowledged=0`, r.ref.ID, r.ref.Generation); err != nil {
				return false, err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE console_operations SET domain_id=? WHERE id=? AND kind=? AND resource=? AND revision=? AND state=?`, cursor, c.ID, catalogMutationKind, mutationCleanupResource, c.Revision, c.State)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return false, compactcatalog.ErrMutationStale
	}
	return false, nil
}

// AcknowledgeAdministrationTx is called in the existing final receipt writer.
// The first actual member is a durable owner anchor, even if the receipt's
// normal retention cap later removes that response before cleanup runs.
func AcknowledgeAdministrationTx(ctx context.Context, tx *sql.Tx, owner string) error {
	operation, digest, ok := strings.Cut(owner, ":")
	if !ok {
		return compactcatalog.ErrMutationStale
	}
	var saved bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_receipts WHERE scope='media-delete' AND operation_id=? AND digest=?)`, operation, digest).Scan(&saved); e != nil {
		return e
	}
	if !saved {
		return compactcatalog.ErrMutationStale
	}
	var ref compactcatalog.MutationRef
	e := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND owner_generation=1 AND parent_mutation_id IS NULL LIMIT 1`, owner).Scan(&ref.ID, &ref.Generation)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return acknowledgeMutationTx(ctx, tx, ref)
}

// supersedeAdministrationTx is called only after the new request's live
// authorization/policy and exact current approved plan have passed. It can
// abandon an older administrative intent on this subject/ancestor, never an
// unrelated merge/library/provider command, and never performs physical work.
func supersedeAdministrationTx(ctx context.Context, tx *sql.Tx, entity int64, newOwner string) (bool, error) {
	var ref compactcatalog.MutationRef
	e := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id,depth) AS (SELECT ?1,0 UNION ALL SELECT e.parent_id,a.depth+1 FROM ancestors a JOIN catalog_entities e ON e.id=a.id WHERE e.parent_id IS NOT NULL AND a.depth<4)
 SELECT m.mutation_id,m.generation FROM ancestors a CROSS JOIN catalog_entity_mutations m INDEXED BY catalog_entity_mutations_entity_fk ON m.entity_id=a.id WHERE m.owner_kind='administration' AND m.owner_id<>?2 AND m.status IN('active','blocked') ORDER BY a.depth LIMIT 1`, entity, newOwner).Scan(&ref.ID, &ref.Generation)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if e != nil {
		return false, e
	}
	return abandonAdministrationMutationTx(ctx, tx, m)
}

// One raw reverse edge, not a filtered descendant walk. Only an old actual
// empty administrative Season inside the freshly approved empty Show may
// be abandoned. A different domain's first hold remains a typed conflict.
func supersedeEmptyAdministrationSeasonTx(ctx context.Context, tx *sql.Tx, show int64, newOwner string) (bool, error) {
	var ref compactcatalog.MutationRef
	e := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_subject_ancestor_1_fk WHERE subject_ancestor_1=? AND status IN('active','blocked') ORDER BY status,family_id LIMIT 1`, show).Scan(&ref.ID, &ref.Generation)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if e != nil {
		return false, e
	}
	if m.OwnerKind != "administration" || m.OwnerID == newOwner || m.Kind != compactcatalog.Season || m.Mode != "members" || m.Target != 0 || m.Child != "" {
		return false, compactcatalog.ErrEntityMutating
	}
	var empty bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_seasons s ON s.entity_id=e.id JOIN catalog_entities parent ON parent.id=s.show_id WHERE e.id=? AND e.kind=3 AND e.library_id=? AND parent.library_id=e.library_id AND pid(e.public_id)=? AND e.parent_id=? AND s.show_id=? AND NOT EXISTS(SELECT 1 FROM catalog_episodes INDEXED BY catalog_episodes_season_entity WHERE season_id=e.id))`, m.Entity, m.Library, m.Public, show, show).Scan(&empty); e != nil {
		return false, e
	}
	if !empty {
		return false, compactcatalog.ErrEntityMutating
	}
	return abandonAdministrationMutationTx(ctx, tx, m)
}

func abandonAdministrationMutationTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (bool, error) {
	step, e := staleMutationStepTx(ctx, tx, m)
	if e != nil {
		return false, e
	}
	if step.Complete {
		var active bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE family_id=? AND status IN('active','blocked'))`, m.Family).Scan(&active); e != nil {
			return false, e
		}
		if !active {
			// The new live management command consumed the old command's abandoned
			// custody. No original full-request success receipt/count is fabricated.
			var anchor compactcatalog.MutationRef
			if e = tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND owner_generation=? AND parent_mutation_id IS NULL LIMIT 1`, m.OwnerID, m.OwnerGeneration).Scan(&anchor.ID, &anchor.Generation); e != nil {
				return false, e
			}
			if e = acknowledgeMutationTx(ctx, tx, anchor); e != nil {
				return false, e
			}
		}
	}
	return true, EnqueueMutationCleanupTx(ctx, tx)
}
