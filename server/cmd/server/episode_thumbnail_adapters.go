package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

// The source resolver owns the retained actor/job, inventory and read authority.
// These adapters supply its actual metadata and supported scheduling facts from
// the same caller transaction; neither adapter authorizes a decode.
type episodeMetadataState struct{ service *metadata.Service }

func (a episodeMetadataState) ReadEpisodeStateTx(ctx context.Context, tx *sql.Tx, target remotesources.EpisodeTarget) (remotesources.EpisodeState, error) {
	if a.service == nil || tx == nil || target.LibraryID == "" || target.EpisodeID == "" {
		return remotesources.EpisodeState{}, remotesources.ErrEpisodeAuthority
	}
	state, err := a.service.ReadEpisodeThumbnailStateTx(ctx, tx, target.LibraryID, target.EpisodeID)
	if err != nil {
		return remotesources.EpisodeState{}, err
	}
	return remotesources.EpisodeState{
		PolicyRevision: state.PolicyRevision, Mode: state.Mode,
		ArtworkRevision: state.ArtworkRevision, ProviderResolved: state.ProviderResolved,
		ProviderStill: state.ProviderStill, GenerationNeeded: state.GenerationNeeded,
	}, nil
}

type episodeAnalysisSchedule struct{ now func() time.Time }

func (a episodeAnalysisSchedule) ReadEpisodeAnalysisTx(ctx context.Context, tx *sql.Tx, _ remotesources.EpisodeTarget) (remotesources.EpisodeAnalysisFacts, error) {
	at := time.Now()
	if a.now != nil {
		at = a.now()
	}
	// Only automatic Episode work asks the source resolver for this reader.
	// episode_thumbnail is an operation, never a generic maintenance task.
	facts, err := workpolicy.ReadHeavyWorkTx(ctx, tx, "analysis", at)
	if err != nil {
		return remotesources.EpisodeAnalysisFacts{}, err
	}
	return remotesources.EpisodeAnalysisFacts{
		Revision: facts.Revision,
		Open:     facts.WindowOpen && facts.PausedUntil <= at.UnixMilli() && facts.NextAllowedAt == nil,
	}, nil
}

var _ remotesources.EpisodeStateReader = episodeMetadataState{}
var _ remotesources.EpisodeAnalysisReader = episodeAnalysisSchedule{}

type episodeSourceAdapter struct {
	resolver remotesources.EpisodeAuthorityResolver
}

func episodeSourceTarget(target metadata.EpisodeThumbnailTarget) remotesources.EpisodeTarget {
	return remotesources.EpisodeTarget{LibraryID: target.LibraryID, LibrarySourceID: target.LibrarySourceID, ObjectID: target.ObjectID, SourceRevision: target.SourceRevision, EpisodeID: target.EpisodeID}
}

func (a episodeSourceAdapter) EpisodeRangesSupportedTx(ctx context.Context, tx *sql.Tx, target metadata.EpisodeThumbnailTarget) (bool, error) {
	return a.resolver.EpisodeRangesSupportedTx(ctx, tx, episodeSourceTarget(target))
}

func (a episodeSourceAdapter) ResolveEpisodeThumbnailTx(ctx context.Context, tx *sql.Tx, invocation metadata.EpisodeThumbnailInvocation) (metadata.EpisodeThumbnailSource, error) {
	var purpose remotesources.EpisodePurpose
	switch invocation.Purpose {
	case metadata.EpisodeThumbnailAutomatic:
		purpose = remotesources.EpisodeAutomatic
	case metadata.EpisodeThumbnailExplicit:
		purpose = remotesources.EpisodeExplicitOneEpisode
	default:
		return nil, remotesources.ErrEpisodeAuthority
	}
	life, cancel := context.WithCancel(ctx)
	source, err := a.resolver.ResolveEpisodeReadSourceTx(life, tx, remotesources.EpisodeAuthorityRequest{
		Target: episodeSourceTarget(invocation.Target), Purpose: purpose,
		Operation: invocation.Operation, CostClass: invocation.CostClass,
		JobID: invocation.JobID, Owner: invocation.Owner,
	}, cancel)
	if err != nil || source == nil {
		cancel()
		return nil, err // nil success is the resolver's one positive tier exclusion.
	}
	return &retainedEpisodeSource{EpisodeReadSource: source, cancel: cancel}, nil
}

type retainedEpisodeSource struct {
	*remotesources.EpisodeReadSource
	cancel context.CancelFunc
}

func (s *retainedEpisodeSource) Close() error {
	s.cancel()
	return s.EpisodeReadSource.Close()
}

func episodeThumbnailRuntime(meta *metadata.Service, sources *remotesources.Service, identities *identity.Service, local *storage.Client, inputs *subtitlevideo.Runtime) *metadata.EpisodeThumbnailRuntime {
	if meta == nil || sources == nil || identities == nil || inputs == nil {
		return nil
	}
	resolver := remotesources.EpisodeAuthorityResolver{
		Sources: sources, Identities: identities, LocalStorage: local,
		Episodes: episodeMetadataState{service: meta}, Analysis: episodeAnalysisSchedule{},
	}
	runtime := &metadata.EpisodeThumbnailRuntime{
		Sources:        episodeSourceAdapter{resolver: resolver},
		ArtworkContext: meta.CaptureArtworkContext,
	}
	if inputs.AnalysisConfigured() {
		runtime.Decoder = func(input mediaanalysis.Input) mediaanalysis.Decode { return inputs.AnalysisDecoder(input) }
	}
	return runtime
}

var _ metadata.EpisodeThumbnailSources = episodeSourceAdapter{}
var _ metadata.EpisodeThumbnailSource = (*retainedEpisodeSource)(nil)
