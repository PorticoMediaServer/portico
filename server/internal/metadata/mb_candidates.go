package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func (s *Service) mbAlbumCandidates(ctx context.Context, j mbJob, values []metadataprovider.ReleaseCandidate) error {
	rows, e := stageMBAlbumCandidates(values, j.title, j.artist)
	if e != nil {
		return fmt.Errorf("%w: candidate validation", errPublicationInput)
	}
	for n := range rows {
		var v metadataprovider.ReleaseCandidate
		if json.Unmarshal(rows[n].payload, &v) != nil {
			return errPublicationInput
		}
		rankMusicAlbum(&rows[n], j, v)
	}
	j.fingerprintStatus = "not_applicable"
	return s.mbStoreCandidates(ctx, j, rows)
}

func (s *Service) mbStoreCandidates(ctx context.Context, j mbJob, rows []mbCandidateEvidence) error {
	automatic, observation := rankMusicCandidates(rows, j)
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.activeMB(ctx, tx, j); e != nil {
		return e
	}
	// j.id stays the public id; mb_* tables hold the integer catalogue id.
	entity, e := entityid.Resolve(ctx, tx, j.id)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM mb_candidates WHERE kind=? AND entity_id=?`, j.kind, entity); e != nil {
		return e
	}
	for _, v := range rows {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_candidates(kind,entity_id,provider_id,entity_type,title,artist,edition,confidence,decision,query_digest,payload,observed_at) VALUES(?,?,?,?,?,?,?,?,'candidate',?,?,?)`, j.kind, entity, v.id, v.entityType, v.title, v.artist, v.edition, v.confidence, j.digest, string(v.payload), s.publicationTime().Format(time.RFC3339)); e != nil {
			return e
		}
		for n, reason := range v.reasons {
			if _, e = tx.ExecContext(ctx, `INSERT INTO mb_candidate_reasons VALUES(?,?,?,?,?)`, j.kind, entity, v.id, n, reason); e != nil {
				return e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO music_match_observations(kind,entity_id,query_digest,fingerprint_status,provider_error,confidence,margin,strong_signals,algorithm) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id) DO UPDATE SET query_digest=excluded.query_digest,fingerprint_status=excluded.fingerprint_status,provider_error=excluded.provider_error,confidence=excluded.confidence,margin=excluded.margin,strong_signals=excluded.strong_signals,algorithm=excluded.algorithm`, j.kind, entity, j.digest, observation.FingerprintStatus, observation.ProviderError, observation.Confidence, observation.Margin, observation.StrongSignals, observation.Algorithm); e != nil {
		return e
	}
	if automatic != "" {
		// Candidate evidence commits first, while the original operation/lease/base
		// remains active. Publication revalidates the very same claim after lookup.
		if e = s.commitMBCurrent(ctx, gated, j); e != nil {
			return e
		}
		if j.kind == "album" {
			v, observed, e := s.mbRelease(ctx, automatic)
			if e != nil {
				return e
			}
			if !musicVerifyRelease(rows, j, automatic, v.Release) {
				j.incompleteCandidates = true
				return s.mbStoreCandidates(ctx, j, rows)
			}
			return s.mbPublishAlbum(ctx, j, v, observed)
		}
		v, observed, e := s.mbRecording(ctx, automatic)
		if e != nil {
			return e
		}
		if !musicVerifyRecording(rows, j, automatic, v.Recording) {
			j.incompleteCandidates = true
			return s.mbStoreCandidates(ctx, j, rows)
		}
		return s.mbPublishSong(ctx, j, v, observed)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET status='needs_selection',error='',attempts=0,revision=revision+1 WHERE kind=? AND entity_id=?`, j.kind, entity); e != nil {
		return e
	}
	if j.kind == "album" && !j.review {
		// provider_match_status is a catalog_albums field: write it through
		// the catalogue API (which also queues derived work, so the
		// syncCatalogRow call goes). Keep the old guard for linked albums.
		var linked int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM mb_album_links WHERE album_id=?`, entity).Scan(&linked); e != nil {
			return e
		}
		if linked == 0 {
			if e = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Automatic, map[string]any{"provider_match_status": "unresolved"}); e != nil {
				return e
			}
		}
	}
	if e = finishMBOperation(ctx, tx, j, "applied", "owner_candidates", s.publicationTime()); e != nil {
		return e
	}
	return s.commitMBCurrent(ctx, gated, j)
}
