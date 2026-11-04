package metadata

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// These callbacks exercise the real command/storage path, not Remote's source
// authority or Metadata's production encoding-policy adapter.
type episodeCommandSource struct {
	episodeRuntimeSource
	closed int
}

func (s *episodeCommandSource) Close() error { s.closed++; return nil }

type episodeCommandSources struct {
	source     *episodeCommandSource
	invocation EpisodeThumbnailInvocation
	resolved   int
	supported  bool
	err        error
}

func (s *episodeCommandSources) ResolveEpisodeThumbnailTx(ctx context.Context, tx *sql.Tx, invocation EpisodeThumbnailInvocation) (EpisodeThumbnailSource, error) {
	s.resolved++
	s.invocation = invocation
	if _, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, invocation.Target.LibraryID); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.source == nil {
		return nil, nil
	}
	return s.source, nil
}

func (s *episodeCommandSources) EpisodeRangesSupportedTx(context.Context, *sql.Tx, EpisodeThumbnailTarget) (bool, error) {
	return s.supported, nil
}

func episodeCommandOwner() identity.Principal {
	return identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "profile", Role: "owner"}, Hash: "test-owner-session"}
}

type episodeClosedPreparationSource struct {
	*episodeCommandSource
	beforeOpen func()
}

func (s *episodeClosedPreparationSource) Open(ctx context.Context) (mediaanalysis.Input, error) {
	s.beforeOpen()
	return s.episodeCommandSource.Open(ctx)
}

type episodeClosedPreparationSources struct {
	*episodeCommandSources
	prepared *sql.Tx
	source   *episodeClosedPreparationSource
}

func (s *episodeClosedPreparationSources) ResolveEpisodeThumbnailTx(ctx context.Context, tx *sql.Tx, invocation EpisodeThumbnailInvocation) (EpisodeThumbnailSource, error) {
	s.prepared = tx
	if _, err := s.episodeCommandSources.ResolveEpisodeThumbnailTx(ctx, tx, invocation); err != nil {
		return nil, err
	}
	return s.source, nil
}

func TestEpisodeThumbnailCommandClosesPreparationBeforeMedia(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "request-reuse"}[reuse], func(t *testing.T) {
			s, _, target, _ := episodePublicationFixture(t)
			command := RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: capturePublication(t, s, target).RepairRevision}
			base := &episodeCommandSource{episodeRuntimeSource: episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}}
			sources := &episodeClosedPreparationSources{episodeCommandSources: &episodeCommandSources{source: base, supported: true}}
			checks := 0
			closed := func(stage string) {
				t.Helper()
				if sources.prepared == nil {
					t.Fatal("media started without a captured preparation")
				}
				var one int
				if err := sources.prepared.QueryRow(`SELECT 1`).Scan(&one); !errors.Is(err, sql.ErrTxDone) {
					t.Fatalf("preparation transaction is live at %s: %v", stage, err)
				}
				checks++
			}
			sources.source = &episodeClosedPreparationSource{base, func() { closed("Source.Open") }}
			decoded := 0
			s.EpisodeThumbnails = thumbnailRuntime(t, s, func() { decoded++ })
			s.EpisodeThumbnails.Sources = sources
			factory := s.EpisodeThumbnails.Decoder
			s.EpisodeThumbnails.Decoder = func(input mediaanalysis.Input) mediaanalysis.Decode {
				closed("decoder factory")
				return factory(input)
			}
			ctx := context.Background()
			if reuse {
				var end func()
				ctx, end = dbwork.WithRequestSnapshot(ctx)
				defer end()
			}
			if _, err := s.MakeEpisodeThumbnail(ctx, target, command, episodeCommandOwner(), func(*sql.Tx) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if checks != 2 || decoded != 1 {
				t.Fatalf("media boundary was not exercised: checks=%d decoded=%d", checks, decoded)
			}
		})
	}
}

func TestEpisodeThumbnailCommandUsesStoredModeWithoutChangingAutomaticPolicy(t *testing.T) {
	for _, mode := range []string{"off", "provider", "provider_then_made"} {
		t.Run(mode, func(t *testing.T) {
			s, c, target, other := episodePublicationFixture(t)
			ctx := context.Background()
			c.Exec(`UPDATE library_metadata_agents SET agent='local' WHERE library_id='episodes'`)
			if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				if err := catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "episodes", mode, 1); err != nil {
					return err
				}
				return catalog.ApplyScanPolicyTx(ctx, tx, "episodes", catalog.ScanPolicy{Tier: "file_list_only"})
			}); err != nil {
				t.Fatal(err)
			}
			read := func() (EpisodeThumbnailState, catalog.ScanPolicy) {
				t.Helper()
				g, err := dbwork.BeginSnapshot(ctx, c.DB)
				if err != nil {
					t.Fatal(err)
				}
				defer g.Rollback()
				facts, err := s.ReadEpisodeThumbnailStateTx(ctx, g.Tx(), "episodes", target.ID)
				if err != nil {
					t.Fatal(err)
				}
				policy, err := catalog.ScanPolicyTx(ctx, g.Tx(), "episodes")
				if err != nil {
					t.Fatal(err)
				}
				return facts, policy
			}
			before, scan := read()
			if before.Mode != mode || before.PolicyRevision != scan.Revision || !before.ProviderResolved || before.ProviderStill || !before.GenerationNeeded || scan.Tier != "file_list_only" {
				t.Fatalf("wrong real stored facts: %+v scan=%+v", before, scan)
			}
			source := &episodeCommandSource{episodeRuntimeSource: episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}}
			sources := &episodeCommandSources{source: source, supported: true}
			decoded := 0
			s.EpisodeThumbnails = thumbnailRuntime(t, s, func() { decoded++ })
			s.EpisodeThumbnails.Sources = sources
			s.Admission = func(context.Context, *sql.Tx, string) (bool, error) {
				t.Fatal("explicit command consulted automatic schedule")
				return false, nil
			}
			command := RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: capturePublication(t, s, target).RepairRevision}
			owner := episodeCommandOwner()
			ownerChecks := 0
			out, err := s.MakeEpisodeThumbnail(ctx, target, command, owner, func(*sql.Tx) error { ownerChecks++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			wantTarget := EpisodeThumbnailTarget{"episodes", "episodes", target.ID, "revision-a", target.ID}
			if sources.resolved != 1 || sources.invocation.Target != wantTarget || sources.invocation.Purpose != EpisodeThumbnailExplicit || sources.invocation.JobID != "" || sources.invocation.Owner != owner || sources.invocation.Operation != catalog.EpisodeThumbnailOperation || sources.invocation.CostClass != "decode" {
				t.Fatalf("command lost exact actor/one-target invocation: %+v", sources.invocation)
			}
			if decoded != 1 || source.opens != 1 || source.authorizations != 3 || source.calls != 1 || source.input.closed != 1 || source.closed != 1 || ownerChecks != 3 || len(out.Snapshot.Artwork) != 1 || !out.Snapshot.Artwork[0].Locked {
				t.Fatalf("wrong command/publication lifecycle: decodes=%d owner=%d source=%+v art=%+v", decoded, ownerChecks, source, out.Snapshot.Artwork)
			}
			after, afterScan := read()
			if after.Mode != mode || after.PolicyRevision != before.PolicyRevision || !reflect.DeepEqual(scan, afterScan) || !after.ProviderResolved || after.ProviderStill || after.GenerationNeeded || after.ArtworkRevision == before.ArtworkRevision {
				t.Fatalf("command changed policy or lost selected-state provenance: before=%+v after=%+v scan=%+v", before, after, afterScan)
			}
			var count int
			if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections WHERE entity_id=?`, c.ID(other.ID)).Scan(&count); err != nil || count != 0 {
				t.Fatal("another episode changed", count, err)
			}
			if _, err = s.MakeEpisodeThumbnail(ctx, target, command, owner, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrRepairConflict) || sources.resolved != 1 || decoded != 1 {
				t.Fatal("stale command repeated media work", sources.resolved, decoded, err)
			}
		})
	}
}

func TestEpisodeThumbnailCommandRefusesBeforeOpeningMedia(t *testing.T) {
	denied := errors.New("current owner or source authority refused")
	for _, refusal := range []string{"owner", "source", "ranges", "nil_source"} {
		t.Run(refusal, func(t *testing.T) {
			s, c, target, _ := episodePublicationFixture(t)
			source := &episodeCommandSource{episodeRuntimeSource: episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}}
			sources := &episodeCommandSources{source: source, supported: refusal != "ranges"}
			if refusal == "source" {
				sources.err = denied
			}
			if refusal == "nil_source" {
				sources.source = nil
			}
			s.EpisodeThumbnails = thumbnailRuntime(t, s, func() { t.Fatal("refused command decoded") })
			s.EpisodeThumbnails.Sources = sources
			command := RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: capturePublication(t, s, target).RepairRevision}
			_, err := s.MakeEpisodeThumbnail(context.Background(), target, command, episodeCommandOwner(), func(*sql.Tx) error {
				if refusal == "owner" {
					return denied
				}
				return nil
			})
			if err == nil || source.opens != 0 || source.calls != 0 {
				t.Fatal("refused command opened or published", source, err)
			}
			var count int
			if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections`).Scan(&count); err != nil || count != 0 {
				t.Fatal("refused command selected artwork", count, err)
			}
		})
	}
}

func TestEpisodeThumbnailProviderPolicyReadsSingleStoredMode(t *testing.T) {
	s, c, target, _ := episodePublicationFixture(t)
	ctx := context.Background()
	for _, mode := range []string{"off", "provider", "provider_then_made"} {
		if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, "episodes")
			if err != nil {
				return err
			}
			return catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "episodes", mode, policy.Revision)
		}); err != nil {
			t.Fatal(err)
		}
		g, err := dbwork.BeginSnapshot(ctx, c.DB)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"still", "thumbnail"} {
			for _, provider := range []string{"tmdb", "tvdb"} {
				ok, err := EpisodeProviderStillAllowedTx(ctx, g.Tx(), "episodes", "episode", role, "", provider)
				if err != nil || ok != (mode != "off") {
					t.Fatalf("mode=%s role=%s provider=%s allowed=%v err=%v", mode, role, provider, ok, err)
				}
			}
		}
		if ok, err := EpisodeProviderStillAllowedTx(ctx, g.Tx(), "missing", "episode", "still", "", "tmdb"); err == nil || ok {
			t.Fatal("unknown library allowed provider work", ok, err)
		}
		if _, err := s.ReadEpisodeThumbnailStateTx(ctx, g.Tx(), "wrong-library", target.ID); !errors.Is(err, ErrRepairInput) {
			t.Fatal("state reader accepted another library's episode", err)
		}
		g.Rollback()
	}
}
