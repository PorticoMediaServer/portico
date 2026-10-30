package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Preserve the approved SRV023 final-publication exception and its historical
// limits. Prospective count facts now prepare/fold in separate raw11 turns;
// the final pin, domain publication and exact cap check remain atomic. These
// telemetry limits are not a qualified physical WAL profile.
var mbPublicationAccountingPlan = dbwork.WritePlan{
	Name: "musicbrainz-publication-accounting", Rows: 50_000, Pages: 4096, VMOps: 8_000_000,
	Why: "one atomic old/new accepted-release pin move, each already capped at 10,000 tracks; exact final budget refusal must roll back the pin and its accounting together",
}

// Adoption contention is stale publication authority with a bounded retry
// delay. It must not keep a rejected head job ahead of unrelated provider work.
var errMBAdoptionPending = errors.Join(errTVDBStale, errors.New("MusicBrainz adoption is pending"))

// resolveMBEntity maps a job's public id to its integer entity id. The worker
// holds a lease on live catalogue state; a vanished entity reads as a stale
// base, which the worker already releases cleanly.
func resolveMBEntity(ctx context.Context, tx *sql.Tx, public string) (int64, error) {
	id, err := entityid.Resolve(ctx, tx, public)
	if err != nil {
		if errors.Is(err, entityid.ErrNotFound) {
			return 0, errTVDBStale
		}
		return 0, err
	}
	return id, nil
}

func mbIdentityAllowed(ctx context.Context, tx *sql.Tx, j mbJob, canonical string) error {
	current, revision := j.base.RecordingID, j.base.RecordingRevision
	if j.kind == "album" {
		current, revision = j.base.ReleaseID, j.base.ReleaseRevision
	}
	if canonical == current {
		return nil
	}
	if !j.manual {
		if current == "" {
			return nil
		}
		return ErrMBConflict
	}
	var n int
	entity, err := resolveMBEntity(ctx, tx, j.id)
	if err != nil {
		return err
	}
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM mb_selection_receipts WHERE kind=? AND entity_id=? AND job_generation<=? AND candidate_id=? AND prior_revision_id=? AND status='pending' AND actor_authority<>'' AND actor_account_id<>'' AND actor_profile_id<>''`, j.kind, entity, j.generation, j.selected, revision).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrMBConflict
	}
	return nil
}
func (s *Service) mbPublishSong(ctx context.Context, j mbJob, result metadataprovider.RecordingLookup, observed string) (resultErr error) {
	stage, e := stageMBRecording(result)
	if e != nil {
		return errPublicationInput
	}
	proof, e := s.mbStageRecording(ctx, j, stage, observed)
	if e != nil {
		return e
	}
	if e = s.mbAttachStaged(ctx, j, proof); e != nil {
		return e
	}
	revision := proof.revision
	change, e := s.mbPrepareAccounting(ctx, j, "song", stage.value.ID, revision, "", proof.digest)
	if e != nil {
		if errors.Is(e, ErrMBConflict) {
			return s.mbStatus(ctx, j, "needs_selection", "accepted_identity_conflict")
		}
		return e
	}
	defer func() {
		if resultErr != nil {
			resultErr = s.mbAbortPrivateAccounting(ctx, change.Owner, resultErr)
		}
	}()
	ctx = mbFinalAccountingContext(ctx, change)
	timing := s.mbFinalTrackCounts(ctx, j, -1)
	var gated2 *dbwork.Write
	gated2, e = dbwork.Begin(dbwork.WithPlan(ctx, mbPublicationAccountingPlan), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	timing.start()
	defer timing.report()
	defer func() { gated2.Rollback(); timing.stop() }()
	tx := gated2.Tx()
	if e = s.activeMB(ctx, tx, j); e != nil {
		return e
	}
	if e = compactcatalog.RequireEntityAcquirableTx(ctx, tx, j.base.Entity); e != nil {
		if errors.Is(e, compactcatalog.ErrEntityMutating) {
			return errTVDBStale
		}
		return e
	}
	// Canonical input was compared with immutable normalized components
	// outside the writer. This PK/generation probe grants no catalog authority:
	// activeMB above still owns every live publication fence.
	if e = mbEvidenceReadyTx(ctx, tx, proof); e != nil {
		return e
	}
	r := stage.value
	if e = mbIdentityAllowed(ctx, tx, j, r.ID); e != nil {
		gated2.Rollback()
		timing.stop()
		return e
	}
	track, e := readMBTrack(ctx, tx, j, r.ID)
	if e != nil {
		return e
	}
	if track.status != "matched" {
		timing.newTracks = timing.oldTracks
	}
	if track.status == "matched" && track.releaseRevision != change.NewRelease {
		return errTVDBStale
	}

	entity, e := resolveMBEntity(ctx, tx, j.id)
	if e != nil {
		return e
	}
	e = mbaccounting.FlipTx(mbaccounting.WithTime(ctx, s.publicationTime()), tx, change, func() error {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_recording_aliases(requested_id,canonical_id,evidence_revision,observed_at) VALUES(?,?,?,?) ON CONFLICT(requested_id,evidence_revision) DO UPDATE SET observed_at=excluded.observed_at`, stage.requested, r.ID, revision, observed); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_song_links(item_id,recording_revision,recording_id,observed_at) VALUES(?,?,?,?) ON CONFLICT(item_id) DO UPDATE SET recording_revision=excluded.recording_revision,recording_id=excluded.recording_id,observed_at=excluded.observed_at`, entity, revision, r.ID, observed); e != nil {
			return e
		}
		// The song's release columns are catalogue facts: write them through the
		// catalogue API, mirroring the link row above (matched links carry the
		// release identity; anything else carries the status only).
		fields := map[string]any{"recording_id": r.ID, "release_status": track.status, "provider_observed_at": observed, "provider_title": r.Title, "provider_artist": mbArtist(r.ArtistCredit)}
		if track.status == "matched" {
			if _, e = tx.ExecContext(ctx, `UPDATE mb_song_links SET release_revision=?,release_id=?,track_id=?,release_status='matched' WHERE item_id=?`, track.releaseRevision, track.releaseID, track.trackID, entity); e != nil {
				return e
			}
			var group string
			if e = tx.QueryRowContext(ctx, `SELECT release_group_id FROM mb_release_evidence WHERE revision_id=?`, track.releaseRevision).Scan(&group); e != nil {
				return e
			}
			fields["release_id"], fields["track_id"], fields["release_group_id"] = track.releaseID, track.trackID, group
		} else if _, e = tx.ExecContext(ctx, `UPDATE mb_song_links SET release_status=? WHERE item_id=?`, track.status, entity); e != nil {
			return e
		}
		if e = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Automatic, fields); e != nil {
			return e
		}
		// The track is known by its recording from now on, so its state is kept
		// and given back under that identity too. Tracks are not joined by it: one
		// recording appears on many albums.
		if e = replaceMusicBrainzIdentityTx(ctx, tx, entity, "recording", r.ID); e != nil {
			return e
		}
		if e = catalog.RestoreTitleStateTx(ctx, tx, entity); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO metadata_details VALUES(?,'musicbrainz',?,?,?) ON CONFLICT(item_id,provider) DO UPDATE SET provider_id=excluded.provider_id,source_url=excluded.source_url,observed_at=excluded.observed_at`, entity, r.ID, "https://musicbrainz.org/recording/"+r.ID, observed); e != nil {
			return e
		}
		if e = s.finishMBPublication(ctx, tx, j, stage.requested, revision); e != nil {
			return e
		}
		return nil
	}, func() error { return checkMBEvidenceBudget(ctx, tx) })
	if e != nil {
		return e
	}
	e = s.commitMBCurrent(ctx, gated2, j)
	timing.stop()
	if e == nil {
		s.mbFinishAcceptedAccounting(ctx, change.Owner)
	}
	return e
}
func (s *Service) mbPublishAlbum(ctx context.Context, j mbJob, result metadataprovider.ReleaseLookup, observed string) error {
	stage, e := stageMBRelease(result)
	if e != nil {
		return errPublicationInput
	}
	proof, e := s.mbStageRelease(ctx, j, stage, observed)
	if e != nil {
		return e
	}
	if e = s.mbAttachStaged(ctx, j, proof); e != nil {
		return e
	}
	return s.mbPublishAlbumReady(ctx, j, stage, proof, observed)
}

// The complete final publication remains one indivisible writer transaction.
// Keeping the typed/proven token explicit makes its acceptance hold measurable
// without timing acquisition or the separately committed construction quanta.
func (s *Service) mbPublishAlbumReady(ctx context.Context, j mbJob, stage mbReleaseEvidence, proof mbBuildProof, observed string) (resultErr error) {
	if proof.kind != "release" || proof.digest != stage.digest {
		return errPublicationInput
	}
	revision := proof.revision
	var e error
	newTracks := 0
	for _, medium := range stage.value.Media {
		newTracks += len(medium.Tracks)
	}
	change, e := s.mbPrepareAccounting(ctx, j, "album", stage.value.ID, "", revision, proof.digest)
	if e != nil {
		if errors.Is(e, ErrMBConflict) {
			return s.mbStatus(ctx, j, "needs_selection", "accepted_identity_conflict")
		}
		return e
	}
	defer func() {
		if resultErr != nil {
			resultErr = s.mbAbortPrivateAccounting(ctx, change.Owner, resultErr)
		}
	}()
	ctx = mbFinalAccountingContext(ctx, change)
	timing := s.mbFinalTrackCounts(ctx, j, newTracks)
	var gated4 *dbwork.Write
	gated4, e = dbwork.Begin(dbwork.WithPlan(ctx, mbPublicationAccountingPlan), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	timing.start()
	defer timing.report()
	defer func() { gated4.Rollback(); timing.stop() }()
	tx := gated4.Tx()
	if e = s.activeMB(ctx, tx, j); e != nil {
		return e
	}
	if e = compactcatalog.RequireEntityAcquirableTx(ctx, tx, j.base.Entity); e != nil {
		if errors.Is(e, compactcatalog.ErrEntityMutating) {
			return errTVDBStale
		}
		return e
	}
	if e = mbEvidenceReadyTx(ctx, tx, proof); e != nil {
		return e
	}
	r := stage.value
	if e = mbIdentityAllowed(ctx, tx, j, r.ID); e != nil {
		gated4.Rollback()
		timing.stop()
		return e
	}
	e = mbaccounting.FlipTx(mbaccounting.WithTime(ctx, s.publicationTime()), tx, change, func() error {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_release_aliases(requested_id,canonical_id,evidence_revision,observed_at) VALUES(?,?,?,?) ON CONFLICT(requested_id,evidence_revision) DO UPDATE SET observed_at=excluded.observed_at`, stage.requested, r.ID, revision, observed); e != nil {
			return e
		}
		entity, e := resolveMBEntity(ctx, tx, j.id)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_album_links(album_id,release_revision,release_id,requested_id,observed_at) VALUES(?,?,?,?,?) ON CONFLICT(album_id) DO UPDATE SET release_revision=excluded.release_revision,release_id=excluded.release_id,requested_id=excluded.requested_id,observed_at=excluded.observed_at`, entity, revision, r.ID, stage.requested, observed); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_album_reconciliations(album_id,release_revision,membership_revision,status) VALUES(?,?,?,'pending') ON CONFLICT(album_id) DO UPDATE SET release_revision=excluded.release_revision,membership_revision=excluded.membership_revision,cursor='',status='pending',revision=revision+1`, entity, revision, j.base.MembershipRevision); e != nil {
			return e
		}
		// The album with its link, status and label: catalogue facts go through
		// the catalogue API. The release's label fills an empty owner field; a
		// locked or written label stays.
		fields := map[string]any{"provider_match_status": "matched", "release_id": r.ID, "release_group_id": r.ReleaseGroup.ID, "release_date": r.Date, "release_country": r.Country, "provider_observed_at": observed}
		if label := mbReleaseLabel(r); label != "" {
			var current string
			err := tx.QueryRowContext(ctx, `SELECT label FROM catalog_albums WHERE entity_id=?`, entity).Scan(&current)
			if errors.Is(err, sql.ErrNoRows) {
				current = ""
			} else if err != nil {
				return err
			}
			if current == "" {
				fields["label"] = label
			}
		}
		if e = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Automatic, fields); e != nil {
			return e
		}
		if e = catalog.EnqueueMBAdoptionTx(ctx, tx, entity, j.base.Incarnation, revision, r.ID, j.operation); e != nil {
			if errors.Is(e, compactcatalog.ErrEntityMutating) {
				return errMBAdoptionPending
			}
			return e
		}
		if e = s.finishMBPublication(ctx, tx, j, stage.requested, revision); e != nil {
			return e
		}
		return nil
	}, func() error { return checkMBEvidenceBudget(ctx, tx) })
	if e != nil {
		return e
	}
	e = s.commitMBCurrent(ctx, gated4, j)
	timing.stop()
	if e == nil {
		s.mbFinishAcceptedAccounting(ctx, change.Owner)
	}
	return e
}

func (s *Service) finishMBPublication(ctx context.Context, tx *sql.Tx, j mbJob, requested, revision string) error {
	entity, e := resolveMBEntity(ctx, tx, j.id)
	if e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE mb_jobs SET status='matched',selected_id=?,attempts=0,error='',revision=revision+1 WHERE kind=? AND entity_id=?`, requested, j.kind, entity); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE mb_candidates SET decision=CASE WHEN provider_id=? THEN 'accepted' ELSE 'superseded' END WHERE kind=? AND entity_id=?`, requested, j.kind, entity); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE mb_selection_receipts SET accepted_revision_id=?,status='applied',finished_at=? WHERE kind=? AND entity_id=? AND job_generation<=? AND candidate_id=? AND status='pending'`, revision, s.publicationTime().Format(publicationClockFormat), j.kind, entity, j.generation, j.selected); e != nil {
		return e
	}
	return finishMBOperation(ctx, tx, j, "applied", "published", s.publicationTime())
}
func trackArtist(t metadataprovider.ReleaseTrack) string {
	if len(t.ArtistCredit) > 0 {
		return mbArtist(t.ArtistCredit)
	}
	return mbArtist(t.Recording.ArtistCredit)
}

// mbReleaseLabel is the release's first named label, for the album's owner
// label field. Empty when the payload carries none.
func mbReleaseLabel(r metadataprovider.Release) string {
	for _, info := range r.LabelInfo {
		if info.Label != nil && strings.TrimSpace(info.Label.Name) != "" {
			return strings.TrimSpace(info.Label.Name)
		}
	}
	return ""
}

// replaceMusicBrainzIdentityTx records an entity's accepted MusicBrainz
// identity of one kind, replacing an earlier one (an owner's new choice).
func replaceMusicBrainzIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, kind, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_external_ids WHERE entity_id=? AND provider='musicbrainz' AND provider_kind=? AND provider_id<>?`, entity, kind, id); err != nil {
		return err
	}
	return compactcatalog.RecordProviderIdentityTx(ctx, tx, entity, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: kind, ID: id})
}
