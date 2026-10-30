package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
)

const mbRetireRows = 24

type mbRetireTarget struct {
	kind, revision, provider, digest, state string
	generation                              int64
}

func mbBuildOwnerLive(ctx context.Context, tx *sql.Tx, owner, now string) (bool, error) {
	if owner == "" {
		return false, nil
	}
	var live bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_publication_operations o JOIN mb_jobs j ON j.kind=o.kind AND j.entity_id=o.entity_id AND j.generation=o.job_generation AND j.lease=o.lease WHERE o.id=? AND o.status IN('claimed','staged') AND o.lease<>'' AND o.lease_until>? AND j.lease_until>?)`, owner, now, now).Scan(&live)
	return live, err
}

// Every lookup here is a bounded key window. Maintained reference counters
// omit accepted and protected prefixes before the age index is examined.
func mbRetirePick(ctx context.Context, tx *sql.Tx, now, cutoff string) (*mbRetireTarget, error) {
	for _, state := range []string{"aborting", "retiring", "building", "ready"} {
		query := `SELECT kind,revision_id,provider_id,digest,state,generation,owner_operation FROM mb_evidence_builds WHERE state=?`
		args := []any{state}
		if state == "ready" {
			query = `SELECT kind,revision_id,provider_id,digest,state,generation,owner_operation FROM mb_evidence_builds INDEXED BY mb_evidence_builds_owned_due WHERE state='ready' AND owner_operation<>''`
			args = nil
		}
		if state == "building" || state == "ready" {
			query += ` AND expires_at<=?`
			args = append(args, now)
		}
		query += ` ORDER BY expires_at,build_id LIMIT 32`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		type candidate struct {
			t     mbRetireTarget
			owner string
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.t.kind, &c.t.revision, &c.t.provider, &c.t.digest, &c.t.state, &c.t.generation, &c.owner); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, c := range candidates {
			if state == "building" || state == "ready" {
				live, err := mbBuildOwnerLive(ctx, tx, c.owner, now)
				if err != nil {
					return nil, err
				}
				if live {
					continue
				}
			}
			var protected int64
			err = tx.QueryRowContext(ctx, `SELECT direct_refs+retention_refs`+func() string {
				if c.t.kind == "recording" {
					return "+release_refs"
				}
				return ""
			}()+` FROM mb_`+c.t.kind+`_accounting WHERE revision_id=?`, c.t.revision).Scan(&protected)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if protected == 0 {
				return &c.t, nil
			}
			if state == "ready" {
				// A real FK already protects this expired reservation. Clear one
				// obsolete owner per turn so protected prefixes cannot hide orphans.
				c.t.state = "unreserve"
				return &c.t, nil
			}
		}
	}
	for _, kind := range []string{"release", "recording"} {
		pred := `a.direct_refs=0 AND a.retention_refs=0`
		if kind == "recording" {
			pred += ` AND a.release_refs=0`
		}
		table, index := "mb_"+kind+"_accounting", "mb_"+kind+"_accounting_retire"
		if kind == "recording" {
			table = "mb_recording_accounting_base"
			index = "mb_recording_accounting_base_retire"
		}
		rows, err := tx.QueryContext(ctx, `SELECT e.revision_id,e.provider_id,e.digest,COALESCE(b.owner_operation,'') FROM `+table+` a INDEXED BY `+index+` JOIN mb_`+kind+`_evidence e ON e.revision_id=a.revision_id LEFT JOIN mb_evidence_builds b ON b.kind=? AND b.revision_id=e.revision_id WHERE `+pred+` AND a.observed_at<? ORDER BY a.observed_at,a.revision_id LIMIT 32`, kind, cutoff)
		if err != nil {
			return nil, err
		}
		type candidate struct{ revision, provider, digest, owner string }
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.revision, &c.provider, &c.digest, &c.owner); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, c := range candidates {
			live, err := mbBuildOwnerLive(ctx, tx, c.owner, now)
			if err != nil {
				return nil, err
			}
			if !live {
				return &mbRetireTarget{kind: kind, revision: c.revision, provider: c.provider, digest: c.digest, state: "ready"}, nil
			}
		}
	}
	return nil, nil
}

func mbRetireClaim(ctx context.Context, tx *sql.Tx, t *mbRetireTarget, now string) error {
	state := "retiring"
	if t.state == "building" || t.state == "aborting" {
		state = "aborting"
	}
	zero := strings.Repeat("0", 64)
	_, err := tx.ExecContext(ctx, `INSERT INTO mb_evidence_builds(build_id,kind,revision_id,provider_id,digest,state,phase,expected_shape_digest,expected_rows,materialized_rows,materialized_digest,owner_operation,expires_at) VALUES(?,?,?,?,?,?,'cleanup',?,1,1,?,'',?) ON CONFLICT(kind,revision_id) DO UPDATE SET state=excluded.state,phase='cleanup',generation=generation+1,owner_operation=''`, t.revision, t.kind, t.revision, t.provider, t.digest, state, zero, zero, now)
	return err
}

// Delete one normalized window, acknowledging the cursor by physical deletion.
// Empty phase probes are exact seeks; a nonempty phase ends this writer turn.
func mbRetireRowsTx(ctx context.Context, tx *sql.Tx, t *mbRetireTarget) (bool, error) {
	tables := []struct{ table, key, order string }{{"mb_recording_credits", "revision_id", "ordinal"}}
	if t.kind == "release" {
		tables = []struct{ table, key, order string }{{"mb_track_credits", "release_revision", "track_id,ordinal"}, {"mb_release_tracks", "release_revision", "track_id"}, {"mb_release_media", "release_revision", "position"}, {"mb_release_credits", "revision_id", "ordinal"}, {"mb_release_group_types", "release_revision", "ordinal"}}
	}
	for _, p := range tables {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+p.table+` WHERE rowid IN(SELECT rowid FROM `+p.table+` WHERE `+p.key+`=? ORDER BY `+p.order+` LIMIT ?)`, t.revision, mbRetireRows)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	if t.kind == "release" {
		var child, state string
		err := tx.QueryRowContext(ctx, `SELECT revision_id,state FROM mb_evidence_builds WHERE parent_build=? ORDER BY state,build_id LIMIT 1`, t.revision).Scan(&child, &state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil {
			var refs int64
			e := tx.QueryRowContext(ctx, `SELECT direct_refs+retention_refs+release_refs FROM mb_recording_accounting WHERE revision_id=?`, child).Scan(&refs)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return false, e
			}
			if refs == 0 {
				_, err = tx.ExecContext(ctx, `UPDATE mb_evidence_builds SET parent_build='',owner_operation='',state='aborting',phase='cleanup',generation=generation+1 WHERE kind='recording' AND revision_id=?`, child)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE mb_evidence_builds SET parent_build='' WHERE kind='recording' AND revision_id=?`, child)
			}
			return false, err
		}
	}
	// Every child set is empty. The guard prevents accidental FK fan-out.
	if err := dbwork.UsePlan(tx, mbEvidenceParentPlan); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mb_`+t.kind+`_evidence WHERE revision_id=?`, t.revision); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM mb_evidence_builds WHERE kind=? AND revision_id=? AND state IN('aborting','retiring')`, t.kind, t.revision)
	return true, err
}

func (s *Service) mbRetireEvidence(ctx context.Context) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if err := mbaccounting.IdleTx(ctx, w.Tx()); err != nil {
		return err
	}
	now := s.publicationTime().Format(time.RFC3339)
	cutoff := s.publicationTime().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	target, err := mbRetirePick(ctx, w.Tx(), now, cutoff)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	if target.state == "unreserve" {
		if _, err = w.Tx().ExecContext(ctx, `UPDATE mb_evidence_builds SET owner_operation='' WHERE kind=? AND revision_id=? AND generation=? AND state='ready'`, target.kind, target.revision, target.generation); err != nil {
			return err
		}
		return w.Commit()
	}
	if target.state != "aborting" && target.state != "retiring" {
		if err = mbRetireClaim(ctx, w.Tx(), target, now); err != nil {
			return err
		}
	}
	if _, err = mbRetireRowsTx(ctx, w.Tx(), target); err != nil {
		return err
	}
	return w.Commit()
}

// Compact operation/alias cleanup never cascades an unbounded artist set or
// multiplies evidence retirement. Each nonempty phase ends its own writer.
func (s *Service) mbCleanupReceipts(ctx context.Context) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if err := mbaccounting.IdleTx(ctx, w.Tx()); err != nil {
		return err
	}
	tx := w.Tx()
	now := s.publicationTime().Format(time.RFC3339)
	cutoff := s.publicationTime().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	var operation string
	err = tx.QueryRowContext(ctx, `SELECT id FROM mb_publication_operations INDEXED BY mb_operations_terminal_age WHERE status IN('applied','stale','failed') AND finished_at<? ORDER BY finished_at,created_at,id LIMIT 1`, cutoff).Scan(&operation)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id FROM mb_publication_operations INDEXED BY mb_operations_terminal_order WHERE status IN('applied','stale','failed') ORDER BY created_at DESC,id DESC LIMIT 1 OFFSET 4000`).Scan(&operation)
	}
	if errors.Is(err, sql.ErrNoRows) {
		rows, e := tx.QueryContext(ctx, `SELECT id FROM mb_publication_operations INDEXED BY mb_operations_expired WHERE status IN('claimed','staged') AND lease_until<=? ORDER BY lease_until,id LIMIT 32`, now)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		err = sql.ErrNoRows
		for _, id := range ids {
			live, e := mbBuildOwnerLive(ctx, tx, id, now)
			if e != nil {
				return e
			}
			if !live {
				operation = id
				err = nil
				break
			}
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if operation != "" {
		for _, table := range []string{"mb_operation_artists", "mb_operation_sources"} {
			result, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE operation_id=? ORDER BY ordinal LIMIT ?)`, operation, mbRetireRows)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				return w.Commit()
			}
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM mb_publication_operations WHERE id=?`, operation); e != nil {
			return e
		}
		return w.Commit()
	}
	for _, kind := range []string{"release", "recording"} {
		result, e := tx.ExecContext(ctx, `DELETE FROM mb_`+kind+`_aliases WHERE rowid IN(SELECT rowid FROM mb_`+kind+`_aliases INDEXED BY mb_`+kind+`_aliases_retire WHERE observed_at<? ORDER BY observed_at,requested_id,evidence_revision LIMIT ?)`, cutoff, mbRetireRows)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n > 0 {
			return w.Commit()
		}
	}
	return nil
}
