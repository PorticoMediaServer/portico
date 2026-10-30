package metadata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// EpisodeThumbnailTarget is captured from the actual catalogue, never decoded
// from the command body. SourceRevision identifies the selected inventory row.
type EpisodeThumbnailTarget struct {
	LibraryID, LibrarySourceID, ObjectID, SourceRevision, EpisodeID string
}

type EpisodeThumbnailPurpose string

const (
	EpisodeThumbnailAutomatic EpisodeThumbnailPurpose = "automatic"
	EpisodeThumbnailExplicit  EpisodeThumbnailPurpose = "explicit_one_episode"
)

type EpisodeThumbnailInvocation struct {
	Target    EpisodeThumbnailTarget
	Purpose   EpisodeThumbnailPurpose
	Operation string
	CostClass string
	JobID     string
	Owner     identity.Principal
}

// A resolved source retains its exact authority and resource/read lease through
// publication. Open is lazy; Authorize and CheckTx are mandatory, separate from
// range support. The source owner checks every I/O/reconnect on the opened input.
type EpisodeThumbnailSource interface {
	Authorize(context.Context, string, string, bool) error
	Open(context.Context) (mediaanalysis.Input, error)
	CheckTx(context.Context, *sql.Tx) error
	Close() error
}

type EpisodeThumbnailSources interface {
	// A nil source with no error means only a positively known automatic
	// source-tier exclusion. Unknown authority must return an error; a closed
	// window or contention returns storage.ErrBusy. Explicit never returns nil.
	ResolveEpisodeThumbnailTx(context.Context, *sql.Tx, EpisodeThumbnailInvocation) (EpisodeThumbnailSource, error)
	EpisodeRangesSupportedTx(context.Context, *sql.Tx, EpisodeThumbnailTarget) (bool, error)
}

type EpisodeThumbnailRuntime struct {
	Sources EpisodeThumbnailSources
	Decoder func(mediaanalysis.Input) mediaanalysis.Decode
	// ArtworkContext captures the canonical encoder policy and a check against
	// current stored policy in the publication transaction. There is no fallback
	// to the normalizer's default edge when this producer is absent.
	ArtworkContext func(context.Context) (context.Context, func(context.Context, *sql.Tx) error, error)
}

// The canonical size-policy adapter returns this only for a proven edge or
// ceiling change. Retrying re-encodes the already decoded frame, never the video.
var ErrEpisodeThumbnailEncodingStale = errors.New("artwork encoding policy changed")

func (r *EpisodeThumbnailRuntime) ready() bool {
	return r != nil && r.Sources != nil && r.Decoder != nil && r.ArtworkContext != nil
}

func (c episodeThumbnailCapture) sourceTarget() EpisodeThumbnailTarget {
	return EpisodeThumbnailTarget{c.LibraryID, c.LibrarySourceID, c.ObjectID, c.SourceRevision, c.Target.ID}
}

// CanMakeEpisodeThumbnailTx supplies an actual runtime/video/range fact for the
// existing Artwork command. Automatic mode, tier, provider and window are not
// consulted. All other authority is rechecked when the owner invokes it.
func (s *Service) CanMakeEpisodeThumbnailTx(ctx context.Context, tx *sql.Tx, target RepairTarget) (bool, error) {
	if !s.EpisodeThumbnails.ready() || target.Kind != "item" {
		return false, nil
	}
	_, err := s.episodeThumbnailVideoTx(ctx, tx, target)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) episodeThumbnailVideoTx(ctx context.Context, tx *sql.Tx, target RepairTarget) (EpisodeThumbnailTarget, error) {
	// Keep the indexed inventory lookup after the selected episode's assets.
	rows, err := tx.QueryContext(ctx, `SELECT cl.library_id,s.id,o.id,o.revision FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id CROSS JOIN inventory_objects o ON o.asset_id=a.token JOIN library_sources s ON s.id=o.source_id WHERE e.public_id=pid_blob(?) AND e.kind=4 AND a.available=1 AND a.video_codec<>'' AND s.library_id=cl.library_id AND s.enabled=1 AND s.identity_confirmed=1 AND o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation ORDER BY l.part_index,a.token,s.id`, target.ID)
	if err != nil {
		return EpisodeThumbnailTarget{}, err
	}
	var targets []EpisodeThumbnailTarget
	for rows.Next() {
		c := EpisodeThumbnailTarget{EpisodeID: target.ID}
		if err = rows.Scan(&c.LibraryID, &c.LibrarySourceID, &c.ObjectID, &c.SourceRevision); err != nil {
			rows.Close()
			return c, err
		}
		targets = append(targets, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return EpisodeThumbnailTarget{}, err
	}
	for _, c := range targets {
		supported, err := s.EpisodeThumbnails.Sources.EpisodeRangesSupportedTx(ctx, tx, c)
		if err != nil {
			return c, err
		}
		if supported {
			return c, nil
		}
	}
	return EpisodeThumbnailTarget{}, sql.ErrNoRows
}

// MakeEpisodeThumbnail is the one-episode branch of the existing Repair route.
// It captures owner/artwork/source state in a read snapshot, closes that snapshot,
// lazily reads and decodes, then uses the ordinary artwork writer with fresh CAS.
func (s *Service) MakeEpisodeThumbnail(ctx context.Context, target RepairTarget, command RepairCommand, owner identity.Principal, authorize func(*sql.Tx) error) (RepairState, error) {
	if command.Action != "make_episode_thumbnail" || command.EffectiveCredits || !artDigest(command.ExpectedRevision) || command.Query != "" || command.Year != 0 || len(command.Relationships) != 0 || len(command.Fields) != 0 || command.CandidateID != "" || command.Provider != "" || command.Order != "" || command.HistoryID != 0 || command.Confirm || command.Role != "" || command.Subject != "" || command.Locked != nil || command.Intent != "" || command.OperationID != "" || owner.Authority != "local" || owner.AccountID == "" || owner.ProfileID == "" || owner.Hash == "" || authorize == nil {
		return RepairState{}, ErrRepairInput
	}
	if !s.EpisodeThumbnails.ready() {
		return RepairState{}, ErrArtworkPending
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return RepairState{}, err
	}
	defer g.Rollback()
	tx := g.Tx()
	if err = authorize(tx); err != nil {
		return RepairState{}, err
	}
	video, err := s.episodeThumbnailVideoTx(ctx, tx, target)
	if err != nil {
		return RepairState{}, err
	}
	captured, err := captureEpisodeThumbnailTx(ctx, tx, target, video.LibrarySourceID, video.ObjectID, video.SourceRevision)
	if err != nil {
		return RepairState{}, err
	}
	if captured.RepairRevision != command.ExpectedRevision {
		return RepairState{}, ErrRepairConflict
	}
	policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, captured.LibraryID)
	if err != nil {
		return RepairState{}, err
	}
	runtime := s.EpisodeThumbnails
	source, err := runtime.Sources.ResolveEpisodeThumbnailTx(ctx, tx, EpisodeThumbnailInvocation{Target: captured.sourceTarget(), Purpose: EpisodeThumbnailExplicit, Operation: catalog.EpisodeThumbnailOperation, CostClass: mediaanalysis.EpisodeThumbnailCostClass, Owner: owner})
	if err != nil {
		return RepairState{}, err
	}
	if source == nil {
		return RepairState{}, ErrArtworkPending
	}
	defer source.Close()
	if err = g.Commit(); err != nil {
		return RepairState{}, err
	}
	return s.generateEpisodeThumbnail(ctx, captured, policy.Mode, true, source, MBActor{Authority: owner.Authority, AccountID: owner.AccountID, ProfileID: owner.ProfileID}, authorize)
}

func (s *Service) generateEpisodeThumbnail(ctx context.Context, captured episodeThumbnailCapture, mode string, manual bool, source EpisodeThumbnailSource, actor MBActor, authorize func(*sql.Tx) error) (RepairState, error) {
	runtime := s.EpisodeThumbnails
	picture, err := mediaanalysis.MakeEpisodeThumbnail(ctx, mediaanalysis.EpisodeThumbnailRequest{Mode: mode, Manual: manual, DurationUS: captured.DurationUS, StartUS: captured.StartUS, EndUS: captured.EndUS, SourceVideo: captured.Video, Open: source.Open, Decoder: runtime.Decoder, Authorize: source.Authorize})
	if err != nil {
		return RepairState{}, err
	}
	if picture.Origin != "generated" {
		return RepairState{}, ErrRepairConflict
	}
	for attempt := 0; attempt < 3; attempt++ {
		encodedCtx, checkSizeTx, err := runtime.ArtworkContext(ctx)
		if err != nil {
			return RepairState{}, err
		}
		if encodedCtx == nil {
			return RepairState{}, ErrRepairInput
		}
		encoded, err := s.prepareEpisodeThumbnail(encodedCtx, picture.JPEG, checkSizeTx)
		if err != nil {
			return RepairState{}, err
		}
		out, err := s.publishEpisodeThumbnail(ctx, captured, encoded, source, manual, actor, authorize)
		if !errors.Is(err, ErrEpisodeThumbnailEncodingStale) {
			return out, err
		}
	}
	return RepairState{}, errors.Join(ErrArtworkPending, ErrEpisodeThumbnailEncodingStale)
}
