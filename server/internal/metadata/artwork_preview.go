package metadata

import (
	"context"
	"database/sql"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// One optional preview per target/role/subject can be in flight. Originals are
// never retained for browsing; the same bounded decoder publishes thumbnails.
func queueArtworkPreview(ctx context.Context, tx *sql.Tx, t RepairTarget, candidate, actor, now string) error {
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	var role, subject, origin, provider, fence string
	if err := tx.QueryRowContext(ctx, `SELECT role,subject,origin,provider,source_fence FROM artwork_candidates WHERE id=? AND kind=? AND entity_id=?`, candidate, t.Kind, entity).Scan(&role, &subject, &origin, &provider, &fence); err != nil {
		return err
	}
	current, err := artworkFence(ctx, tx, t)
	if err != nil {
		return err
	}
	if current != fence {
		return ErrRepairConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,actor,created_at,preview) VALUES(?,?,?,?,?,?,?,?,?,?,?,1) ON CONFLICT(kind,entity_id,role,subject,preview) DO UPDATE SET id=excluded.id,candidate_id=excluded.candidate_id,origin=excluded.origin,provider=excluded.provider,source_fence=excluded.source_fence,status='pending',attempts=0,next_attempt='',lease='',lease_until='',error='',size_refresh=0,actor=excluded.actor,created_at=excluded.created_at`, identity.Token(), t.Kind, entity, role, subject, candidate, origin, provider, fence, actor, now)
	return err
}

// Caller authenticates a current owner. This route only opens installed
// bytes: a preview is a published source like any other, served at the slot
// the caller asked for.
func (s *Service) PreviewArtwork(ctx context.Context, t RepairTarget, candidate string, size ArtworkSize) (*os.File, string, error) {
	if !artDigest(candidate) {
		return nil, "", ErrRepairInput
	}
	if size != ArtworkSmall && size != ArtworkLarge {
		return nil, "", ErrRepairInput
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, "", err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	current, err := artworkFence(ctx, tx, t)
	if err != nil {
		return nil, "", err
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return nil, "", err
	}
	var source string
	if err = tx.QueryRowContext(ctx, `SELECT p.digest FROM artwork_previews p JOIN artwork_candidates c ON c.id=p.candidate_id WHERE c.kind=? AND c.entity_id=? AND c.id=? AND p.source_fence=?`, t.Kind, entity, candidate, current).Scan(&source); err != nil {
		return nil, "", ErrArtworkPending
	}
	digest, mime, err := artworkSourceBlob(ctx, tx, source, size)
	if err != nil {
		return nil, "", ErrArtworkPending
	}
	f, err := s.openArtworkObject(digest)
	if err != nil {
		return nil, "", ErrArtworkPending
	}
	if err = gated.Commit(); err != nil {
		f.Close()
		return nil, "", err
	}
	return f, mime, nil
}
