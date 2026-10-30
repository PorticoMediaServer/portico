package metadata

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

func validArtworkEdge(edge int) bool { return edge == 1280 || edge == 1920 || edge == 2560 }

func scheduleArtworkSizeTx(ctx context.Context, tx *sql.Tx, before, after int) error {
	if !validArtworkEdge(before) || !validArtworkEdge(after) {
		return ErrRepairInput
	}
	_, err := tx.ExecContext(ctx, `UPDATE metadata_artwork_size_sweep SET running=?,cursor_kind='',cursor_id=0,cursor_role='',cursor_subject='' WHERE singleton=1`, after > before)
	return err
}

type artworkSizeRequest struct {
	kind, role, subject, candidate, digest string
	entity, revision                       int64
	edge                                   int
}

// GlobalArtworkSizeStep advances one durable 32-selection page and admits at
// most 32 exact retained refreshes. It performs no provider I/O or image decode.
// The ordinary worker rechecks current policy, target, candidate and selection.
func (s *Service) GlobalArtworkSizeStep(ctx context.Context) error {
	w, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer w.Rollback()
	tx := w.Tx()
	policy, err := readArtworkEncodingPolicyTx(ctx, tx)
	if err != nil {
		return err
	}
	var running bool
	var kind, role, subject string
	var entity int64
	if err = tx.QueryRowContext(ctx, `SELECT running,cursor_kind,cursor_id,cursor_role,cursor_subject FROM metadata_artwork_size_sweep WHERE singleton=1`).Scan(&running, &kind, &entity, &role, &subject); err != nil {
		return err
	}
	if running {
		rows, err := tx.QueryContext(ctx, `SELECT a.kind,a.entity_id,a.role,a.subject,a.candidate_id,a.digest,a.revision,c.provider FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id WHERE (a.kind,a.entity_id,a.role,a.subject)>(?,?,?,?) ORDER BY a.kind,a.entity_id,a.role,a.subject LIMIT 32`, kind, entity, role, subject)
		if err != nil {
			return err
		}
		type selection struct {
			artworkSizeRequest
			provider string
		}
		page := []selection{}
		for rows.Next() {
			var r selection
			if err = rows.Scan(&r.kind, &r.entity, &r.role, &r.subject, &r.candidate, &r.digest, &r.revision, &r.provider); err != nil {
				rows.Close()
				return err
			}
			page = append(page, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, r := range page {
			kind, entity, role, subject = r.kind, r.entity, r.role, r.subject
			// Upload originals are not retained; generated frames require their
			// source-authorized Episode engine, never this acquisition queue.
			if r.provider == "upload" || r.provider == "generated" {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_artwork_size_requests(kind,entity_id,role,subject,candidate_id,digest,selection_revision,edge) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,role,subject) DO UPDATE SET candidate_id=excluded.candidate_id,digest=excluded.digest,selection_revision=excluded.selection_revision,edge=excluded.edge,next_attempt=''`, r.kind, r.entity, r.role, r.subject, r.candidate, r.digest, r.revision, policy.edge); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_artwork_size_sweep SET running=?,cursor_kind=?,cursor_id=?,cursor_role=?,cursor_subject=? WHERE singleton=1`, len(page) == 32, kind, entity, role, subject); err != nil {
			return err
		}
	}
	stamp := s.publicationTime().Format(time.RFC3339Nano)
	rows, err := tx.QueryContext(ctx, `SELECT kind,entity_id,role,subject,candidate_id,digest,selection_revision,edge FROM metadata_artwork_size_requests INDEXED BY metadata_artwork_size_requests_due WHERE next_attempt<=? ORDER BY next_attempt,kind,entity_id,role,subject LIMIT 32`, stamp)
	if err != nil {
		return err
	}
	requests := []artworkSizeRequest{}
	for rows.Next() {
		var r artworkSizeRequest
		if err = rows.Scan(&r.kind, &r.entity, &r.role, &r.subject, &r.candidate, &r.digest, &r.revision, &r.edge); err != nil {
			rows.Close()
			return err
		}
		requests = append(requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range requests {
		remove := func() error {
			if _, err := tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',lease='',lease_until='',error='size_refresh_superseded' WHERE kind=? AND entity_id=? AND role=? AND subject=? AND preview=0 AND candidate_id=? AND selection_revision=? AND size_refresh=1 AND actor='' AND status IN('pending','running','retry')`, r.kind, r.entity, r.role, r.subject, r.candidate, r.revision); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM metadata_artwork_size_requests WHERE kind=? AND entity_id=? AND role=? AND subject=?`, r.kind, r.entity, r.role, r.subject)
			return err
		}
		deferUntil := func(delay time.Duration) error {
			_, err := tx.ExecContext(ctx, `UPDATE metadata_artwork_size_requests SET next_attempt=? WHERE kind=? AND entity_id=? AND role=? AND subject=?`, s.publicationTime().Add(delay).Format(time.RFC3339Nano), r.kind, r.entity, r.role, r.subject)
			return err
		}
		var candidate, digest, provider, fence string
		var revision int64
		var locked bool
		var largeEdge, acceptedEdge int
		err = tx.QueryRowContext(ctx, `SELECT a.candidate_id,a.digest,a.revision,a.locked,c.provider,c.source_fence,o.large_edge,max(o.accepted_width,o.accepted_height) FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id JOIN artwork_sources o ON o.digest=a.digest WHERE a.kind=? AND a.entity_id=? AND a.role=? AND a.subject=?`, r.kind, r.entity, r.role, r.subject).Scan(&candidate, &digest, &revision, &locked, &provider, &fence, &largeEdge, &acceptedEdge)
		// A refresh helps only when the large slot was made under a smaller edge
		// and the accepted image had more pixels than that edge kept.
		if errors.Is(err, sql.ErrNoRows) || err == nil && (r.edge != policy.edge || candidate != r.candidate || digest != r.digest || revision != r.revision || largeEdge >= policy.edge || acceptedEdge <= largeEdge || provider == "upload" || provider == "generated") {
			if err = remove(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		public, err := entityid.Public(ctx, tx, r.entity)
		if errors.Is(err, entityid.ErrNotFound) {
			if err = remove(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		target := RepairTarget{Kind: r.kind, ID: public}
		currentFence, err := artworkFence(ctx, tx, target)
		if err != nil {
			return err
		}
		if currentFence != fence {
			if err = remove(); err != nil {
				return err
			}
			continue
		}
		// The ordinary worker's retry budget remains authoritative. A sweep
		// never resets an exhausted acquisition merely because its intent is due.
		var exhausted bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_jobs WHERE kind=? AND entity_id=? AND role=? AND subject=? AND preview=0 AND candidate_id=? AND selection_revision=? AND size_refresh=1 AND actor='' AND status='failed' AND error<>'storage_unavailable')`, r.kind, r.entity, r.role, r.subject, r.candidate, r.revision).Scan(&exhausted); err != nil {
			return err
		}
		if exhausted {
			if err = remove(); err != nil {
				return err
			}
			continue
		}
		var busy bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_jobs WHERE kind=? AND entity_id=? AND role=? AND subject=? AND preview=0 AND (status IN('pending','running','retry') OR status='failed' AND error='storage_unavailable'))`, r.kind, r.entity, r.role, r.subject).Scan(&busy); err != nil {
			return err
		}
		if busy {
			if err = deferUntil(time.Minute); err != nil {
				return err
			}
			continue
		}
		// Empty actor is mandatory: resizing is not a new owner command and
		// must obey automatic provider consent and Episode mode inhibition.
		enabled, err := s.artworkJobEnabled(ctx, tx, artworkWork{target: target, role: r.role, provider: provider, actor: ""})
		if err != nil {
			return err
		}
		if !enabled {
			if err = deferUntil(5 * time.Minute); err != nil {
				return err
			}
			continue
		}
		if err = queueArtworkCandidate(ctx, tx, target, candidate, "", stamp, locked, true); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET size_refresh=1 WHERE kind=? AND entity_id=? AND role=? AND subject=? AND preview=0`, r.kind, r.entity, r.role, r.subject); err != nil {
			return err
		}
		// Keep the intent through publication or consent withdrawal/restart.
		if err = deferUntil(time.Minute); err != nil {
			return err
		}
	}
	return w.Commit()
}
