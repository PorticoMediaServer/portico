package metadata

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// episodeThumbnailCapture binds a picture to one episode's actual available
// video and logical clock, as well as the owner's current artwork decision.
// It is internal state, never an item/source selector supplied by a client.
type episodeThumbnailCapture struct {
	Target                                                        RepairTarget
	LibraryID, LibrarySourceID, ObjectID, SourceRevision, AssetID string
	Incarnation, RootIdentity                                     string
	Generation                                                    int64
	DurationUS, StartUS, EndUS                                    int64
	Video                                                         decoder.PreviewVideoFacts
	RepairRevision, ArtworkFence                                  string
	Held                                                          bool
}

func thumbnailClockUS(seconds float64) (int64, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64((1<<53)-1)/1e6 {
		return 0, ErrRepairInput
	}
	scaled := seconds * 1e6
	if math.Abs(scaled-math.Round(scaled)) > 0.001 {
		return 0, ErrRepairInput
	}
	return int64(math.Round(scaled)), nil
}

func captureEpisodeThumbnailTx(ctx context.Context, tx *sql.Tx, target RepairTarget, sourceID, objectID, revision string) (episodeThumbnailCapture, error) {
	out := episodeThumbnailCapture{Target: target}
	if target.Kind != "item" {
		return out, ErrRepairInput
	}
	before, ent, err := readRepairSnapshot(ctx, tx, target)
	if err != nil {
		return out, err
	}
	if ent.itemKind != "episode" {
		return out, ErrRepairInput
	}
	out.LibraryID = ent.library
	entity, err := artworkEntity(ctx, tx, target.ID)
	if err != nil {
		return out, err
	}
	var duration, start, end float64
	var detail string
	// Resolve this episode's assets before their indexed inventory lookups.
	// Reversing that join can scan every inventory object for one capture.
	err = tx.QueryRowContext(ctx, `SELECT s.id,o.id,o.revision,a.token,s.incarnation,s.root_identity,s.generation,a.duration,l.start_seconds,COALESCE(l.end_seconds,a.duration),COALESCE((SELECT st.detail_json FROM asset_streams st WHERE st.asset_id=a.id AND st.type='video' ORDER BY st.stream_index LIMIT 1),'')
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id CROSS JOIN inventory_objects o ON o.asset_id=a.token JOIN library_sources s ON s.id=o.source_id
 WHERE l.entity_id=? AND s.library_id=? AND a.available=1 AND a.video_codec<>'' AND s.enabled=1 AND s.identity_confirmed=1 AND o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation
 AND (?='' OR s.id=?) AND (?='' OR o.id=?) AND (?='' OR o.revision=?)
 AND NOT EXISTS(SELECT 1 FROM episode_asset_boundaries b WHERE b.item_id=l.entity_id AND b.asset_id=a.token AND b.status='unknown_multi_episode')
 ORDER BY l.part_index,a.token,s.id LIMIT 1`, entity, ent.library, sourceID, sourceID, objectID, objectID, revision, revision).Scan(&out.LibrarySourceID, &out.ObjectID, &out.SourceRevision, &out.AssetID, &out.Incarnation, &out.RootIdentity, &out.Generation, &duration, &start, &end, &detail)
	if err != nil {
		return out, err
	}
	if out.DurationUS, err = thumbnailClockUS(duration); err != nil {
		return out, err
	}
	if out.StartUS, err = thumbnailClockUS(start); err != nil {
		return out, err
	}
	if out.EndUS, err = thumbnailClockUS(end); err != nil {
		return out, err
	}
	if out.EndUS <= out.StartUS || out.EndUS > out.DurationUS {
		return out, ErrRepairInput
	}
	var stream assets.Stream
	stream.DecodeDetail(detail)
	out.Video = decoder.PreviewVideoFacts{DynamicRange: stream.Detail.DynamicRange, DolbyVisionProfile: stream.Detail.DolbyVisionProfile, DolbyVisionCompatibility: stream.Detail.DolbyVisionCompatibility}
	if out.RepairRevision, err = repairRevision(ctx, tx, target, before, ent); err != nil {
		return out, err
	}
	if out.ArtworkFence, err = artworkFence(ctx, tx, target); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id WHERE a.kind='item' AND a.entity_id=? AND a.role IN('still','thumbnail') AND a.subject='' AND (a.locked=1 OR c.provider IN('local','upload') OR c.source_fence=?)) OR EXISTS(SELECT 1 FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role IN('still','thumbnail') AND subject='' AND (actor<>'' OR provider='local') AND status IN('pending','running','retry'))`, entity, out.ArtworkFence, entity).Scan(&out.Held)
	return out, err
}

// Both checks are mandatory: source permission and current artwork encoding
// policy are independent authorities. The size check captures the context used
// by the canonical encoder and compares it to current policy inside publication.
type episodeThumbnailPublicationAuthority interface {
	CheckTx(context.Context, *sql.Tx) error
}
type episodeThumbnailEncoding struct {
	material    artworkMaterial
	checkSizeTx func(context.Context, *sql.Tx) error
}

func (s *Service) prepareEpisodeThumbnail(ctx context.Context, raw []byte, checkSizeTx func(context.Context, *sql.Tx) error) (episodeThumbnailEncoding, error) {
	out := episodeThumbnailEncoding{checkSizeTx: checkSizeTx}
	if checkSizeTx == nil {
		return out, ErrRepairInput
	}
	var err error
	out.material, err = s.materializeArtwork(ctx, raw, map[string]bool{"jpeg": true})
	return out, err
}

func (s *Service) publishEpisodeThumbnail(ctx context.Context, captured episodeThumbnailCapture, picture episodeThumbnailEncoding, authority episodeThumbnailPublicationAuthority, manual bool, actor MBActor, authorize func(*sql.Tx) error) (RepairState, error) {
	if authority == nil || picture.checkSizeTx == nil || manual && (actor.Authority != "local" || actor.AccountID == "" || actor.ProfileID == "" || authorize == nil) {
		return RepairState{}, ErrRepairInput
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return RepairState{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if manual {
		if err = authorize(tx); err != nil {
			return RepairState{}, err
		}
	}
	if err = authority.CheckTx(ctx, tx); err != nil {
		return RepairState{}, err
	}
	if err = picture.checkSizeTx(ctx, tx); err != nil {
		return RepairState{}, err
	}
	current, err := captureEpisodeThumbnailTx(ctx, tx, captured.Target, captured.LibrarySourceID, captured.ObjectID, captured.SourceRevision)
	if err != nil {
		return RepairState{}, err
	}
	if current != captured {
		return RepairState{}, ErrRepairConflict
	}
	if !manual && current.Held {
		return s.readRepairState(ctx, tx, current.Target)
	}
	before, _, err := readRepairSnapshot(ctx, tx, current.Target)
	if err != nil {
		return RepairState{}, err
	}
	entity, err := artworkEntity(ctx, tx, current.Target.ID)
	if err != nil {
		return RepairState{}, err
	}
	stamp := s.publicationTime().Format(time.RFC3339Nano)
	if err = publishArtworkSource(ctx, tx, picture.material, stamp); err != nil {
		return RepairState{}, err
	}
	origin := "episode:" + publicationDigest([]any{current.LibrarySourceID, current.ObjectID, current.SourceRevision, current.Incarnation, current.Generation, current.StartUS, current.EndUS})
	candidate, err := insertArtworkCandidate(ctx, tx, current.Target, "still", "", "generated", picture.material.source, origin, "", "", current.ArtworkFence, stamp, 0)
	if err != nil {
		return RepairState{}, err
	}
	who := ""
	if manual {
		who = actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) VALUES('item',?,'still','',?,?,?,1,?,?) ON CONFLICT(kind,entity_id,role,subject) DO UPDATE SET candidate_id=excluded.candidate_id,digest=excluded.digest,locked=excluded.locked,revision=artwork_selections.revision+1,actor=excluded.actor,observed_at=excluded.observed_at`, entity, candidate, picture.material.source, manual, who, stamp)
	if err != nil {
		return RepairState{}, err
	}
	// A queued provider fetch cannot replace the just-committed explicit choice.
	if manual {
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',lease='',lease_until='',error='owner_artwork_changed' WHERE kind='item' AND entity_id=? AND role IN('still','thumbnail') AND subject='' AND status IN('pending','running','retry')`, entity); err != nil {
			return RepairState{}, err
		}
	}
	// The catalogue reads still/thumbnail directly from artwork_selections.
	if err = compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseRows, entity); err != nil {
		return RepairState{}, err
	}
	if err = recordRepair(ctx, tx, current.Target, before, current.RepairRevision, "make_episode_thumbnail", who); err != nil {
		return RepairState{}, err
	}
	out, err := s.readRepairState(ctx, tx, current.Target)
	if err != nil {
		return out, err
	}
	if manual {
		if err = authorize(tx); err != nil {
			return out, err
		}
	}
	// Do not re-resolve an automatic artwork fact after our own selection write;
	// source authority was checked in this same serialized writer transaction.
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if err = gated.Commit(); err != nil {
		return out, err
	}
	return out, s.ensureArtworkFiles(picture.material.installed()...)
}
