package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"io"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

// CompactArtworkStep re-makes one source's large slot after the large-edge
// policy shrank, without provider IO. The source identity, its selections and
// its history are untouched; only the source row names a new large file.
// Publication is atomic and old open readers remain valid.
func (s *Service) CompactArtworkStep(ctx context.Context) error {
	if s.cacheRoot == "" {
		return nil
	}
	encoded, _, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		return err
	}
	policy := encoded.Value(artworkEncodingPolicyKey{}).(artworkEncodingPolicy)
	source, err := s.nextArtworkCompaction(ctx, policy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.compactArtworkSource(ctx, source); err != nil && ctx.Err() == nil {
		var bad *artworkCompactionFailure
		if errors.As(err, &bad) {
			if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO artwork_compaction_failures VALUES(?,?,?) ON CONFLICT(digest) DO UPDATE SET reason=excluded.reason,failed_at=excluded.failed_at`, source, bad.reason, s.publicationTime().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			return recordArtworkRefusal(ctx, s.db, bad.cause)
		}
	}
	return err
}

type artworkCompactionFailure struct {
	reason string
	cause  error
}

func (e *artworkCompactionFailure) Error() string { return "artwork compaction: " + e.reason }

func (s *Service) compactArtworkSource(ctx context.Context, source string) error {
	ctx, checkPolicy, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		return err
	}
	edge, limit := artworkSlotLimits(ctx, artworkSlotLarge)
	fail := func(reason string, cause error) error { return &artworkCompactionFailure{reason, cause} }
	var large string
	var oldEdge, width, height, size int
	if err = s.db.QueryRowContext(ctx, `SELECT s.large_blob,s.large_edge,b.width,b.height,b.bytes FROM artwork_sources s JOIN artwork_blobs b ON b.digest=s.large_blob WHERE s.digest=?`, source).Scan(&large, &oldEdge, &width, &height, &size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if oldEdge <= edge {
		return nil
	}
	// A large file that already fits the smaller edge and allowance is kept:
	// re-encoding it would only lose quality.
	if max(width, height) <= edge && size <= limit*3/2 {
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE artwork_sources SET large_edge=?,large_outlier=? WHERE digest=? AND large_blob=? AND large_edge=?`, edge, size > limit, source, large, oldEdge)
		return err
	}
	f, err := s.openArtworkObject(large)
	if err != nil {
		return fail("file_unreadable", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, artworkMaxBytes+1))
	f.Close()
	if err != nil || len(raw) > artworkMaxBytes {
		return fail("file_unreadable", err)
	}
	release, err := imagework.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return fail("undecodable", err)
	}
	// Large from large, never from the small slot.
	out, err := encodeArtworkSlot(ctx, decoded, artworkSlotLarge)
	if errors.Is(err, errArtworkCapUnreachable) {
		return fail("cap_unreachable", err)
	}
	if err != nil {
		return err
	}
	release()
	installed, err := s.installArtworkFile(out.data, out.width, out.height)
	if err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if err = checkPolicy(ctx, tx); err != nil {
		return err
	}
	now := s.publicationTime().Format(time.RFC3339Nano)
	if err = recordArtworkBlob(ctx, tx, installed, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE artwork_sources SET large_blob=?,large_edge=?,large_outlier=? WHERE digest=? AND large_blob=? AND large_edge=?`, installed.digest, edge, out.outlier, source, large, oldEdge)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		// Selections move to a new revision so cached pages drop the old descriptor.
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_selections SET revision=revision+1 WHERE digest=?`, source); err != nil {
			return err
		}
		if err = countArtworkOutcome(ctx, tx, artworkSlotLarge, installed.mime, out.outlier); err != nil {
			return err
		}
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	// The old large file is collected once no source names it.
	return s.ensureArtworkFiles(installed)
}
