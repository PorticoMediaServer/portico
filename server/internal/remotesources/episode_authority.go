package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var ErrEpisodeAuthority = errors.New("episode thumbnail authority is unavailable or refused")

// ErrEpisodeAutomaticSourceTier is a positive applicability result, never a
// permission grant. Only this exact result can map to an examined scan step
// without generation. Unknown authority and explicit work never return it.
var ErrEpisodeAutomaticSourceTier = errors.New("automatic episode thumbnail is outside the saved source scan tier")

type EpisodePurpose string

const (
	EpisodeAutomatic          EpisodePurpose = "automatic"
	EpisodeExplicitOneEpisode EpisodePurpose = "explicit_one_episode"
)

type EpisodeTarget struct {
	LibraryID, LibrarySourceID, ObjectID, SourceRevision, EpisodeID string
}

type EpisodeAuthorityRequest struct {
	Target               EpisodeTarget
	Purpose              EpisodePurpose
	Operation, CostClass string
	JobID                string
	Owner                identity.Principal
}

// EpisodeState is read from the single library episode-mode writer and current
// episode artwork facts. A cost label, picker policy or default cannot supply
// these facts. Explicit work does not depend on provider-still presence.
type EpisodeState struct {
	PolicyRevision   int64
	Mode             string
	ArtworkRevision  string
	ProviderResolved bool
	ProviderStill    bool
	GenerationNeeded bool
}

type EpisodeStateReader interface {
	ReadEpisodeStateTx(context.Context, *sql.Tx, EpisodeTarget) (EpisodeState, error)
}

// EpisodeAnalysisFacts come only from the supported checked analysis schedule.
// They schedule automatic work; they are not actor/source/decode permission.
type EpisodeAnalysisFacts struct {
	Revision int64
	Open     bool
}

type EpisodeAnalysisReader interface {
	ReadEpisodeAnalysisTx(context.Context, *sql.Tx, EpisodeTarget) (EpisodeAnalysisFacts, error)
}

type EpisodeAuthorityResolver struct {
	Sources      *Service
	Identities   *identity.Service
	Episodes     EpisodeStateReader
	Analysis     EpisodeAnalysisReader
	LocalStorage *storage.Client
}

type episodeAuthoritySnapshot struct {
	Source           catalog.LibrarySource
	File             string
	Size, ModifiedNS int64
	PolicyRevision   int64
	Episode          EpisodeState
	Profile          SourceReadProfile
	RemoteGeneration string
	Observation      ObservationRef
	Analysis         EpisodeAnalysisFacts
	Limits           ReadLimits
}

// EpisodeAuthority is a captured recheck fence, not a bearer grant. Failure is
// terminal, including a canceled job/actor, changed source or contributing CAS
// revision. No reconnect may resolve it to the current replacement object.
type EpisodeAuthority struct {
	mu       sync.Mutex
	resolver EpisodeAuthorityResolver
	request  EpisodeAuthorityRequest
	captured episodeAuthoritySnapshot
	life     context.Context
	terminal error
}

func validEpisodeRequest(r EpisodeAuthorityRequest) bool {
	if r.Operation != "episode_thumbnail" || r.CostClass != "decode" || r.Target.LibraryID == "" || r.Target.LibrarySourceID == "" || r.Target.ObjectID == "" || r.Target.SourceRevision == "" || r.Target.EpisodeID == "" {
		return false
	}
	switch r.Purpose {
	case EpisodeAutomatic:
		return r.JobID != "" && r.Owner.Hash == ""
	case EpisodeExplicitOneEpisode:
		return r.JobID == "" && r.Owner.Hash != ""
	}
	return false
}

func episodePolicy(purpose EpisodePurpose, state EpisodeState, library catalog.ScanPolicy, remote bool, profile SourceReadProfile) error {
	if state.PolicyRevision < 1 || state.PolicyRevision != library.Revision {
		return ErrEpisodeAuthority
	}
	switch state.Mode {
	case "provider_then_made", "provider", "off":
	default:
		return ErrEpisodeAuthority
	}
	// The explicit one-off action bypasses automatic scheduling, never this
	// independently recorded source consent (including bounded ranged work).
	if remote && !profile.AllowAdvancedMediaAnalysis {
		return storage.ErrRemoteAnalysisDisabled
	}
	if purpose == EpisodeExplicitOneEpisode {
		return nil
	}
	if purpose != EpisodeAutomatic || state.Mode != "provider_then_made" || !state.ProviderResolved || !state.GenerationNeeded || state.ArtworkRevision == "" || len(state.ArtworkRevision) > 512 || state.ProviderStill || !library.Allows("episode_thumbnail") {
		return ErrEpisodeAuthority
	}
	if remote {
		if !validSourceReadProfile(profile) || !profile.Saved && profile.ScanDepth != "file_list_only" {
			return ErrEpisodeAuthority
		}
		if profile.ScanDepth == "file_list_only" || profile.ScanDepth == "basic" {
			return ErrEpisodeAutomaticSourceTier
		}
	}
	// Consent does not authorize a whole-file operation or relax any lower gate.
	return nil
}

func episodeAnalysisGate(purpose EpisodePurpose, facts EpisodeAnalysisFacts) error {
	if purpose == EpisodeExplicitOneEpisode {
		return nil
	}
	if purpose != EpisodeAutomatic || facts.Revision < 1 {
		return ErrEpisodeAuthority
	}
	if !facts.Open {
		return storage.ErrBusy
	}
	return nil
}

func (r EpisodeAuthorityResolver) resolveTx(ctx context.Context, tx *sql.Tx, request EpisodeAuthorityRequest) (episodeAuthoritySnapshot, error) {
	var out episodeAuthoritySnapshot
	if tx == nil || r.Sources == nil || r.Sources.db == nil || r.Episodes == nil || !validEpisodeRequest(request) {
		return out, ErrEpisodeAuthority
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if request.Purpose == EpisodeExplicitOneEpisode {
		if err := currentLocalOwnerTx(ctx, tx, r.Identities, request.Owner); err != nil {
			return out, err
		}
		if err := identity.DirectAllowed(tx, request.Owner, request.Target.LibraryID); err != nil {
			return out, err
		}
	}
	t := request.Target
	captured, err := readEpisodeTargetTx(ctx, tx, t)
	if err != nil {
		return out, err
	}
	out = captured
	s := &out.Source
	if request.Purpose == EpisodeAutomatic {
		live, e := catalog.InventoryFenceTx(ctx, tx, request.JobID)
		if e != nil {
			return out, e
		}
		if live.ID != s.ID || live.LibraryID != s.LibraryID || live.Generation != s.Generation || live.Incarnation != s.Incarnation || live.RootIdentity != s.RootIdentity || live.ResolvedPath != s.ResolvedPath {
			return out, storage.ErrRemoteChanged
		}
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, s.LibraryID)
	if err != nil {
		return out, err
	}
	if request.Purpose == EpisodeAutomatic {
		var jobPolicyRevision int64
		if err = tx.QueryRowContext(ctx, `SELECT policy_revision FROM inventory_runs WHERE job_id=?`, request.JobID).Scan(&jobPolicyRevision); err != nil {
			return out, err
		}
		if jobPolicyRevision != policy.Revision {
			return out, ErrEpisodeAuthority
		}
	}
	out.PolicyRevision = policy.Revision
	out.Episode, err = r.Episodes.ReadEpisodeStateTx(ctx, tx, t)
	if err != nil {
		return out, err
	}
	switch s.Kind {
	case "remote":
		if err = r.readEpisodeRemoteTx(ctx, tx, t, &out); err != nil {
			return out, err
		}
	case "local":
	default:
		return out, ErrEpisodeAuthority
	}
	if err = episodePolicy(request.Purpose, out.Episode, policy, s.Kind == "remote", out.Profile); err != nil {
		return out, err
	}
	if request.Purpose == EpisodeAutomatic {
		if r.Analysis == nil {
			return out, ErrEpisodeAuthority
		}
		out.Analysis, err = r.Analysis.ReadEpisodeAnalysisTx(ctx, tx, t)
		if err != nil {
			return out, err
		}
	}
	if err = episodeAnalysisGate(request.Purpose, out.Analysis); err != nil {
		return out, err
	}
	// Actual source-wide resource custody is the retained SourceReadLease. Its
	// physical epoch does not exist at this DB-only policy capture boundary.
	out.Limits = out.Profile.Limits
	if request.Purpose == EpisodeExplicitOneEpisode {
		// Known automatic modes, scan policy and artwork are not explicit-source
		// permission. The Episode command separately retains its actual art CAS.
		out.PolicyRevision = 0
		out.Episode = EpisodeState{}
	}
	return out, nil
}

func (r EpisodeAuthorityResolver) ResolveEpisodeAuthorityTx(ctx context.Context, tx *sql.Tx, request EpisodeAuthorityRequest) (*EpisodeAuthority, error) {
	captured, err := r.resolveTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	return &EpisodeAuthority{resolver: r, request: request, captured: captured, life: ctx}, nil
}

func (a *EpisodeAuthority) checkTx(ctx context.Context, tx *sql.Tx) error {
	if a.terminal != nil {
		return a.terminal
	}
	if a.life == nil || a.resolver.Sources == nil {
		a.terminal = ErrEpisodeAuthority
		return a.terminal
	}
	err := a.life.Err()
	if err == nil {
		var current episodeAuthoritySnapshot
		current, err = a.resolver.resolveTx(ctx, tx, a.request)
		if err == nil && current != a.captured {
			err = storage.ErrRemoteChanged
		}
	}

	if err != nil {
		a.terminal = err
	}
	return err
}

// CheckTx must also run in the actual artwork publication transaction. A check
// before opening the transaction cannot close a concurrent revocation race.
func (a *EpisodeAuthority) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if a == nil {
		return ErrEpisodeAuthority
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkTx(ctx, tx)
}

func (a *EpisodeAuthority) Check(ctx context.Context) error {
	if a == nil {
		return ErrEpisodeAuthority
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal != nil {
		return a.terminal
	}
	if a.resolver.Sources == nil || a.resolver.Sources.db == nil {
		a.terminal = ErrEpisodeAuthority
		return a.terminal
	}
	gated, err := dbwork.BeginSnapshot(ctx, a.resolver.Sources.db)
	if err != nil {
		a.terminal = err
		return err
	}
	defer gated.Rollback()
	return a.checkTx(ctx, gated.Tx())
}

// Authorize has the Episode helper's exact adapter signature. The helper passes
// its actual invocation on every boundary; the captured purpose cannot change.
func (a *EpisodeAuthority) Authorize(ctx context.Context, operation, cost string, manual bool) error {
	if a == nil {
		return ErrEpisodeAuthority
	}
	if operation != "episode_thumbnail" || cost != "decode" || manual != (a.request.Purpose == EpisodeExplicitOneEpisode) {
		a.mu.Lock()
		a.terminal = ErrEpisodeAuthority
		a.mu.Unlock()
		return ErrEpisodeAuthority
	}
	return a.Check(ctx)
}
