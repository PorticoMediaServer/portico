package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"

	// The upload path accepts WebP in addition to the formats the provider
	// acquisition path decodes. Nothing re-encodes to WebP; JPEG stays JPEG and alpha-capable input stays PNG.
	_ "golang.org/x/image/webp"
)

// UploadBytes bounds one uploaded image. The HTTP layer enforces the same bound
// on the request body so an oversized upload is refused before it is buffered.
const UploadBytes = 10 << 20

var ErrArtworkUpload = errors.New("the image could not be accepted")

// ErrArtworkCapUnreachable refuses an image that cannot be stored within its
// byte cap's allowance at the quality floor.
var ErrArtworkCapUnreachable = errors.New("the image cannot be stored within its size limit")

// sniffArtworkUpload identifies the container from the bytes themselves. A
// declared content type or filename extension is never trusted.
func sniffArtworkUpload(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case len(raw) >= 12 && bytes.Equal(raw[0:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// UploadArtwork accepts owner-supplied bytes for one role, stores them in the
// server's private artwork store exactly like a provider candidate, then selects
// and locks them. No provider is contacted and no external URL is retained.
func (s *Service) UploadArtwork(ctx context.Context, t RepairTarget, role, subject, expectedRevision string, raw []byte, actor MBActor, authorize func(*sql.Tx) error) (RepairState, error) {
	if len(expectedRevision) != 64 || actor.AccountID == "" || authorize == nil || len(subject) > 160 {
		return RepairState{}, ErrRepairInput
	}
	if s.cacheRoot == "" {
		return RepairState{}, ErrArtworkPending
	}
	if len(raw) == 0 || len(raw) > UploadBytes {
		return RepairState{}, ErrArtworkUpload
	}
	mime := sniffArtworkUpload(raw)
	if mime == "" {
		return RepairState{}, ErrArtworkUpload
	}
	ctx, checkPolicy, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		return RepairState{}, err
	}
	// Decoding happens before any transaction is opened: a slow or hostile image
	// never holds a database write lock.
	material, err := s.materializeArtwork(ctx, raw, map[string]bool{"jpeg": true, "png": true, "webp": true})
	if err != nil {
		if errors.Is(err, imagework.ErrBusy) || errors.Is(err, ErrArtworkSpace) || errors.Is(err, errArtworkStorage) || ctx.Err() != nil {
			return RepairState{}, err
		}
		if errors.Is(err, errArtworkCapUnreachable) {
			// A picture that cannot meet its cap at the quality floor is refused,
			// never softened or shrunk. The refusal is counted.
			if e := recordArtworkRefusal(ctx, s.db, err); e != nil {
				return RepairState{}, e
			}
			return RepairState{}, ErrArtworkCapUnreachable
		}
		return RepairState{}, ErrArtworkUpload
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return RepairState{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = authorize(tx); err != nil {
		return RepairState{}, err
	}
	if err = checkPolicy(ctx, tx); err != nil {
		if errors.Is(err, ErrEpisodeThumbnailEncodingStale) {
			return RepairState{}, ErrRepairConflict
		}
		return RepairState{}, err
	}
	before, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return RepairState{}, err
	}
	base, err := repairRevision(ctx, tx, t, before, ent)
	if err != nil {
		return RepairState{}, err
	}
	if base != expectedRevision {
		return RepairState{}, ErrRepairConflict
	}
	if !allowedArtworkRole(t.Kind, ent.itemKind, role) {
		return RepairState{}, ErrRepairInput
	}
	fence, err := artworkFence(ctx, tx, t)
	if err != nil {
		return RepairState{}, err
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return RepairState{}, err
	}
	now := s.publicationTime().Format(time.RFC3339Nano)
	who := actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	if err = publishArtworkSource(ctx, tx, material, now); err != nil {
		return RepairState{}, err
	}
	var acceptedWidth, acceptedHeight int
	if err = tx.QueryRowContext(ctx, `SELECT accepted_width,accepted_height FROM artwork_sources WHERE digest=?`, material.source).Scan(&acceptedWidth, &acceptedHeight); err != nil {
		return RepairState{}, err
	}
	candidate, err := insertArtworkCandidate(ctx, tx, t, role, subject, "upload", material.source, "upload:"+material.source, "", "Uploaded by the library owner", fence, now, 1000)
	if err != nil {
		return RepairState{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_uploads VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, candidate, t.Kind, entity, role, subject, material.source, mime, len(raw), acceptedWidth, acceptedHeight, who, now); err != nil {
		return RepairState{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_previews VALUES(?,?,?,?) ON CONFLICT(candidate_id) DO UPDATE SET digest=excluded.digest,source_fence=excluded.source_fence,created_at=excluded.created_at`, candidate, material.source, fence, now); err != nil {
		return RepairState{}, err
	}
	// An acquisition already in flight for this role must not replace the bytes
	// the owner just supplied.
	if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',lease='',lease_until='',error='owner_upload' WHERE kind=? AND entity_id=? AND role=? AND subject=? AND preview=0 AND status IN('pending','running','retry')`, t.Kind, entity, role, subject); err != nil {
		return RepairState{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_selections VALUES(?,?,?,?,?,?,1,1,?,?) ON CONFLICT(kind,entity_id,role,subject) DO UPDATE SET candidate_id=excluded.candidate_id,digest=excluded.digest,locked=1,revision=artwork_selections.revision+1,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, role, subject, candidate, material.source, who, now); err != nil {
		return RepairState{}, err
	}
	if err = projectSelectedArtwork(ctx, tx, t, role); err != nil {
		return RepairState{}, err
	}
	if err = recordRepair(ctx, tx, t, before, base, "upload_artwork", who); err != nil {
		return RepairState{}, err
	}
	out, err := s.readRepairState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	if err = authorize(tx); err != nil {
		return out, err
	}
	if err = gated.Commit(); err != nil {
		return out, err
	}
	return out, s.ensureArtworkFiles(material.installed()...)
}

// DeleteUploadedArtwork withdraws one uploaded candidate. Only an upload can be
// withdrawn this way; provider candidates belong to their provider's evidence.
// When the withdrawn image was selected, the best remaining candidate for that
// role is queued so the role does not silently lose its image.
func (s *Service) DeleteUploadedArtwork(ctx context.Context, t RepairTarget, role, candidate, expectedRevision string, actor MBActor, authorize func(*sql.Tx) error) (RepairState, error) {
	if len(expectedRevision) != 64 || actor.AccountID == "" || authorize == nil || !artDigest(candidate) {
		return RepairState{}, ErrRepairInput
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return RepairState{}, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if err = authorize(tx); err != nil {
		return RepairState{}, err
	}
	before, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return RepairState{}, err
	}
	base, err := repairRevision(ctx, tx, t, before, ent)
	if err != nil {
		return RepairState{}, err
	}
	if base != expectedRevision {
		return RepairState{}, ErrRepairConflict
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return RepairState{}, err
	}
	var subject, provider string
	err = tx.QueryRowContext(ctx, `SELECT c.subject,c.provider FROM artwork_candidates c JOIN artwork_uploads u ON u.candidate_id=c.id WHERE c.id=? AND c.kind=? AND c.entity_id=? AND c.role=?`, candidate, t.Kind, entity, role).Scan(&subject, &provider)
	if errors.Is(err, sql.ErrNoRows) {
		return RepairState{}, ErrRepairInput
	}
	if err != nil {
		return RepairState{}, err
	}
	if provider != "upload" {
		return RepairState{}, ErrRepairInput
	}
	var selected string
	err = tx.QueryRowContext(ctx, `SELECT candidate_id FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=?`, t.Kind, entity, role, subject).Scan(&selected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RepairState{}, err
	}
	now := s.publicationTime().Format(time.RFC3339Nano)
	who := actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	if selected == candidate {
		if _, err = tx.ExecContext(ctx, `DELETE FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=?`, t.Kind, entity, role, subject); err != nil {
			return RepairState{}, err
		}
		fence, e := artworkFence(ctx, tx, t)
		if e != nil {
			return RepairState{}, e
		}
		var replacement string
		e = tx.QueryRowContext(ctx, `SELECT id FROM artwork_candidates WHERE kind=? AND entity_id=? AND role=? AND subject=? AND id<>? AND source_fence=? ORDER BY rank DESC,id LIMIT 1`, t.Kind, entity, role, subject, candidate, fence).Scan(&replacement)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return RepairState{}, e
		}
		if replacement != "" {
			if err = queueArtworkCandidate(ctx, tx, t, replacement, who, now, false, true); err != nil {
				return RepairState{}, err
			}
		}
	}
	for _, q := range []string{`DELETE FROM artwork_uploads WHERE candidate_id=?`, `DELETE FROM artwork_previews WHERE candidate_id=?`, `DELETE FROM artwork_candidates WHERE id=?`} {
		if _, err = tx.ExecContext(ctx, q, candidate); err != nil {
			return RepairState{}, err
		}
	}
	if err = recordRepair(ctx, tx, t, before, base, "delete_uploaded_artwork", who); err != nil {
		return RepairState{}, err
	}
	out, err := s.readRepairState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	if err = authorize(tx); err != nil {
		return out, err
	}
	return out, gated2.Commit()
}

func allowedArtworkRole(kind, itemKind, role string) bool {
	if !validArtworkRole(role) {
		return false
	}
	for _, v := range artworkRolesFor(kind, itemKind) {
		if v == role {
			return true
		}
	}
	return false
}
