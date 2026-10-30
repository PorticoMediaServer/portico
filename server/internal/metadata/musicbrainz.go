package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type MusicBrainzProvider interface {
	SearchRecordings(context.Context, string, string) ([]metadataprovider.Recording, error)
	SearchReleases(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error)
	Recording(context.Context, string) (metadataprovider.RecordingLookup, error)
	Release(context.Context, string) (metadataprovider.ReleaseLookup, error)
}

var mbIdentifier = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type mbJob struct {
	fingerprintStatus, acousticError                                   string
	incompleteCandidates                                               bool
	review, manual                                                     bool
	kind, id, selected, lease, title, artist, operation, until, digest string
	generation                                                         int64
	attempts                                                           int
	base                                                               mbBase
}

func mbArtist(credits []metadataprovider.ArtistCredit) string {
	var b strings.Builder
	for _, v := range credits {
		name := v.Name
		if name == "" {
			name = v.Artist.Name
		}
		b.WriteString(name)
		b.WriteString(v.JoinPhrase)
	}
	return b.String()
}
func (s *Service) MusicBrainzStep(ctx context.Context) (result error) {
	ctx = s.lookupContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		// database/sql can win the cancellation race and roll back a transaction
		// before its next statement/Commit observes the context. Keep the public
		// cancellation identity rather than reporting a broken transaction.
		if errors.Is(result, sql.ErrTxDone) && ctx.Err() != nil {
			result = ctx.Err()
		}
	}()
	if e := ctx.Err(); e != nil {
		return e
	}
	if held, _, e := s.mbAccountingMaintenance(ctx); e != nil || held {
		return e
	}
	if e := s.cleanupMusicBrainz(ctx); e != nil {
		return e
	}
	// Reconciliation is a bounded own-album operation, not a provider call.
	if done, e := s.mbReconcileAlbums(ctx); e != nil || done {
		return e
	}
	j, e := s.claimMB(ctx)
	if e != nil {
		return e
	}
	if j == nil {
		// No album or song work: enrich MusicBrainz-matched artists, a
		// bounded page per step, without delaying catalogue matching.
		return s.musicArtistStep(ctx)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		entity, err := entityid.Resolve(c, s.db, j.id)
		if err != nil {
			return
		}
		_, _ = dbwork.ExecWrite(c, s.db, dbwork.ClassBackgroundMedia, `UPDATE mb_jobs SET lease='',lease_until='' WHERE kind=? AND entity_id=? AND generation=? AND lease=?`, j.kind, entity, j.generation, j.lease)
	}()
	selected, problem, e := s.mbSelectionInput(ctx, *j)
	if e != nil {
		return s.mbResult(ctx, *j, e)
	}
	if problem != "" {
		return s.mbResult(ctx, *j, s.mbStatus(ctx, *j, "needs_selection", problem))
	}
	if j.review {
		selected = ""
	}
	if selected == "" {
		if j.kind == "album" && (len(j.title) > 512 || len(j.artist) > 512 || strings.TrimSpace(j.title) == "" || strings.TrimSpace(j.artist) == "") {
			return s.mbResult(ctx, *j, s.mbStatus(ctx, *j, "needs_selection", "unsupported_query"))
		}
		if s.mb == nil {
			return s.mbResult(ctx, *j, errors.New("unavailable"))
		}
		if j.kind == "album" {
			values, e := s.mb.SearchReleases(ctx, j.title, j.artist)
			if e != nil {
				return s.mbResult(ctx, *j, e)
			}
			return s.mbResult(ctx, *j, s.mbAlbumCandidates(ctx, *j, values))
		}
		return s.mbResult(ctx, *j, s.musicSongSearch(ctx, *j))
	}
	if j.kind == "album" {
		value, observed, e := s.mbRelease(ctx, selected)
		if e != nil {
			return s.mbResult(ctx, *j, e)
		}
		if !strings.EqualFold(value.RequestedID, selected) {
			return s.mbResult(ctx, *j, errPublicationInput)
		}
		return s.mbResult(ctx, *j, s.mbPublishAlbum(ctx, *j, value, observed))
	}
	value, observed, e := s.mbRecording(ctx, selected)
	if e != nil {
		return s.mbResult(ctx, *j, e)
	}
	if !strings.EqualFold(value.RequestedID, selected) {
		return s.mbResult(ctx, *j, errPublicationInput)
	}
	return s.mbResult(ctx, *j, s.mbPublishSong(ctx, *j, value, observed))
}
func (s *Service) mbSelectionInput(ctx context.Context, j mbJob) (string, string, error) {
	if j.review {
		return "", "", nil
	}
	if j.manual {
		if !mbIdentifier.MatchString(j.selected) {
			return "", "invalid_selection", nil
		}
		return strings.ToLower(j.selected), "", nil
	}
	if j.base.HintProblem != "" {
		return "", j.base.HintProblem, nil
	}
	field, current := "musicbrainz_trackid", j.base.RecordingID
	if j.kind == "album" {
		field, current = "musicbrainz_albumid", j.base.ReleaseID
	}
	hint := j.base.Hints[field]
	if current != "" {
		// A current accepted identity wins over unproved new tag aliases. A provider
		// crosswalk requires its own explicit reviewed acceptance; cache alias state
		// is never permission to silently rebind the accepted identity.
		if hint != "" && !strings.EqualFold(hint, current) {
			return "", "accepted_identity_conflict", nil
		}
		return current, "", nil
	}
	if hint != "" {
		return hint, "", nil
	}
	if j.kind == "song" && j.base.ReleaseRevision != "" {
		gated, e := dbwork.BeginSnapshot(ctx, s.db)
		if e != nil {
			return "", "", e
		}
		tx := gated.Tx()
		defer gated.Rollback()
		if e = s.activeMB(ctx, tx, j); e != nil {
			return "", "", e
		}
		p, e := readMBTrack(ctx, tx, j, "")
		if e != nil {
			return "", "", e
		}
		if p.status == "matched" {
			return p.recordingID, "", nil
		}
		if p.status != "not_found" && p.status != "unmatched" {
			return "", p.status, nil
		}
	}
	return "", "", nil
}
func (s *Service) mbStatus(ctx context.Context, j mbJob, status, problem string) error {
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = s.activeMB(ctx, tx, j); e != nil {
		return e
	}
	entity, e := resolveMBEntity(ctx, tx, j.id)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET status=?,error=?,revision=revision+1 WHERE kind=? AND entity_id=?`, status, problem, j.kind, entity); e != nil {
		return e
	}
	if e = finishMBOperation(ctx, tx, j, "applied", problem, s.publicationTime()); e != nil {
		return e
	}
	return s.commitMBCurrent(ctx, gated2, j)
}
func finishMBOperation(ctx context.Context, tx *sql.Tx, j mbJob, status, reason string, now time.Time) error {
	_, e := tx.ExecContext(ctx, `UPDATE mb_publication_operations SET status=?,reason=?,finished_at=?,accepted_recording_revision=NULL,accepted_release_revision=NULL,result_recording_revision=NULL,result_release_revision=NULL WHERE id=? AND status IN('claimed','staged')`, status, reason, now.Format(time.RFC3339), j.operation)
	if e != nil {
		return e
	}
	for _, table := range []string{"mb_operation_sources", "mb_operation_artists"} {
		if _, e = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE operation_id=?`, j.operation); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) mbResult(ctx context.Context, j mbJob, problem error) error {
	if problem == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(problem, errTVDBStale) {
		gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
		if e != nil {
			return e
		}
		tx := gated3.Tx()
		defer gated3.Rollback()
		// A vanished entity matches no job row; the operation still retires
		// below, so the worker keeps moving.
		entity, e := resolveMBEntity(ctx, tx, j.id)
		if e != nil {
			if !errors.Is(e, errTVDBStale) {
				return e
			}
			entity = -1
		}
		retryAt, reason := "", "base_changed"
		var retryDue time.Time
		if errors.Is(problem, errMBAdoptionPending) {
			retryDue = s.publicationTime().Add(30 * time.Second)
			retryAt = retryDue.Format(time.RFC3339)
			reason = "adoption_in_progress"
		}
		if errors.Is(problem, errMBAccountingPending) {
			retryDue = s.publicationTime().Add(time.Second)
			retryAt = retryDue.Format(time.RFC3339)
			reason = "accounting_in_progress"
		}
		if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt=CASE WHEN ?<>'' THEN ? ELSE next_attempt END WHERE kind=? AND entity_id=? AND generation=? AND lease=?`, retryAt, retryAt, j.kind, entity, j.generation, j.lease); e != nil {
			return e
		}
		if e = finishMBOperation(ctx, tx, j, "stale", reason, s.publicationTime()); e != nil {
			return e
		}
		if e = gated3.Commit(); e == nil && !retryDue.IsZero() {
			s.noteMBAdoptionRetry(retryDue)
		}
		return e
	}
	return s.mbFailure(ctx, j, problem)
}
func (s *Service) mbFailure(ctx context.Context, j mbJob, problem error) error {
	delay := time.Duration(30*(1<<min(j.attempts, 6))) * time.Second
	status, code := "pending", "unavailable"
	var p *metadataprovider.Error
	if errors.As(problem, &p) {
		switch p.Code {
		case "unavailable", "malformed", "not_found", "rate_limited", "unauthorized", "forbidden":
			code = p.Code
		}
		if !p.Retryable() {
			status = "unresolved"
		}
		if p.RetryAfter > delay {
			delay = min(p.RetryAfter, 24*time.Hour)
		}
	}
	if errors.Is(problem, errPublicationInput) {
		status, code = "unresolved", "malformed"
	}
	if errors.Is(problem, errMBBudget) {
		status, code = "unavailable", "evidence_budget"
	}
	if j.attempts >= 3 && status == "pending" {
		status = "unavailable"
	}
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if e = s.activeMB(ctx, tx, j); e != nil {
		gated4.Rollback()
		if errors.Is(e, errTVDBStale) {
			return s.mbResult(ctx, j, e)
		}
		return e
	}
	if p != nil && p.RetryAfter > 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO metadata_provider_cooldowns VALUES('musicbrainz',?) ON CONFLICT(provider) DO UPDATE SET next_attempt=max(next_attempt,excluded.next_attempt)`, s.publicationTime().Add(min(p.RetryAfter, 24*time.Hour)).Format(time.RFC3339)); e != nil {
			return e
		}
	}
	entity, e := resolveMBEntity(ctx, tx, j.id)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET status=?,attempts=attempts+1,next_attempt=?,error=?,revision=revision+1 WHERE kind=? AND entity_id=?`, status, s.publicationTime().Add(delay).Format(time.RFC3339), code, j.kind, entity); e != nil {
		return e
	}
	if e = finishMBOperation(ctx, tx, j, "failed", code, s.publicationTime()); e != nil {
		return e
	}
	return s.commitMBCurrent(ctx, gated4, j)
}
func (s *Service) mbRecording(ctx context.Context, id string) (metadataprovider.RecordingLookup, string, error) {
	ctx = s.lookupContext(ctx)
	var raw, observed string
	out := metadataprovider.RecordingLookup{RequestedID: strings.ToLower(id)}
	e := s.db.QueryRowContext(ctx, `SELECT r.payload,a.observed_at FROM mb_recording_aliases a JOIN mb_recording_evidence r ON r.revision_id=a.evidence_revision AND r.sealed=1 WHERE a.requested_id=? AND a.observed_at>? ORDER BY a.observed_at DESC,a.evidence_revision LIMIT 1`, out.RequestedID, s.publicationTime().Add(-30*24*time.Hour).Format(time.RFC3339)).Scan(&raw, &observed)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &out.Recording)
		return out, observed, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, "", e
	}
	if s.mb == nil {
		return out, "", errors.New("unavailable")
	}
	out, e = s.mb.Recording(ctx, id)
	return out, s.publicationTime().Format(time.RFC3339), e
}
func (s *Service) mbRelease(ctx context.Context, id string) (metadataprovider.ReleaseLookup, string, error) {
	ctx = s.lookupContext(ctx)
	var raw, observed string
	out := metadataprovider.ReleaseLookup{RequestedID: strings.ToLower(id)}
	e := s.db.QueryRowContext(ctx, `SELECT r.payload,a.observed_at FROM mb_release_aliases a JOIN mb_release_evidence r ON r.revision_id=a.evidence_revision AND r.sealed=1 WHERE a.requested_id=? AND a.observed_at>? ORDER BY a.observed_at DESC,a.evidence_revision LIMIT 1`, out.RequestedID, s.publicationTime().Add(-30*24*time.Hour).Format(time.RFC3339)).Scan(&raw, &observed)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &out.Release)
		return out, observed, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, "", e
	}
	if s.mb == nil {
		return out, "", errors.New("unavailable")
	}
	out, e = s.mb.Release(ctx, id)
	return out, s.publicationTime().Format(time.RFC3339), e
}
