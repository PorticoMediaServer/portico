package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func TestEpisodeRuntimeDoesNotAdvertiseAnUnconfiguredDecoder(t *testing.T) {
	c := catalogtest.Open(t)
	meta := metadata.New(c.DB, "")
	inputs, err := subtitlevideo.New(subtitlevideo.Options{
		DB: c.DB, Storage: &storage.Client{}, Directory: filepath.Join(t.TempDir(), "runtime"),
		FFmpeg: filepath.Join(t.TempDir(), "not-installed-ffmpeg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.Close()
	if inputs.AnalysisConfigured() {
		t.Fatal("unresolved executable became a configured decoder")
	}
	meta.EpisodeThumbnails = episodeThumbnailRuntime(meta, &remotesources.Service{}, &identity.Service{}, nil, inputs)
	if meta.EpisodeThumbnails == nil || meta.EpisodeThumbnails.ArtworkContext == nil || meta.EpisodeThumbnails.Decoder != nil {
		t.Fatal("unconfigured decoder advertised or genuine artwork policy discarded")
	}
	// No source/catalogue lookup is allowed to turn missing runtime tooling
	// into command availability, even when the other adapters are composed.
	available, err := meta.CanMakeEpisodeThumbnailTx(context.Background(), nil, metadata.RepairTarget{Kind: "item", ID: "not-consulted"})
	if err != nil || available {
		t.Fatal("missing decoder advertised episode generation", available, err)
	}
}

func TestEpisodeSourceStateUsesCurrentMetadataAndSinglePolicyWriter(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(library, "Show", 2026)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/one.mkv")
	c.Drain()
	ctx := context.Background()
	reader := episodeMetadataState{service: metadata.New(c.DB, "")}
	target := remotesources.EpisodeTarget{LibraryID: "tv", EpisodeID: episode.Public}
	var before remotesources.EpisodeState
	if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var err error
		before, err = reader.ReadEpisodeStateTx(ctx, tx, target)
		if err != nil {
			return err
		}
		if before.Mode != "provider_then_made" || before.PolicyRevision < 1 || len(before.ArtworkRevision) != 64 || before.ProviderResolved || before.ProviderStill || !before.GenerationNeeded {
			t.Fatalf("unknown provider facts were replaced: %+v", before)
		}
		if err = catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "tv", "off", before.PolicyRevision); err != nil {
			return err
		}
		// Reading through this same uncommitted transaction sees the sole
		// writer's new revision, rather than reopening an older snapshot.
		after, err := reader.ReadEpisodeStateTx(ctx, tx, target)
		if err == nil && (after.Mode != "off" || after.PolicyRevision != before.PolicyRevision+1 || after.ProviderResolved || after.ProviderStill) {
			t.Fatalf("mode/revision or unknown metadata lost: %+v", after)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadEpisodeStateTx(ctx, nil, target); err == nil {
		t.Fatal("nil transaction admitted")
	}
	if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		wrong := target
		wrong.LibraryID = "other"
		if _, err := reader.ReadEpisodeStateTx(ctx, tx, wrong); err == nil {
			t.Fatal("wrong library read as target metadata")
		}
		if _, err := (episodeMetadataState{}).ReadEpisodeStateTx(ctx, tx, target); err == nil {
			t.Fatal("absent metadata producer supplied a fact")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEpisodeSourceAnalysisUsesSupportedWindowAndPause(t *testing.T) {
	c := catalogtest.Open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	reader := episodeAnalysisSchedule{now: func() time.Time { return at }}
	if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if err := workpolicy.SaveTx(ctx, tx, workpolicy.Policy{Windows: []workpolicy.Window{
			{ID: "heavy", Enabled: true, Days: []string{"monday"}, StartMinute: 60, DurationMinutes: 180, Timezone: "UTC", Tasks: []string{"analysis"}},
		}}); err != nil {
			return err
		}
		first, err := reader.ReadEpisodeAnalysisTx(ctx, tx, remotesources.EpisodeTarget{})
		if err != nil {
			return err
		}
		if first.Revision < 1 || !first.Open {
			t.Fatalf("actual open analysis window lost: %+v", first)
		}
		at = at.Add(10 * time.Hour)
		outside, err := reader.ReadEpisodeAnalysisTx(ctx, tx, remotesources.EpisodeTarget{})
		if err != nil {
			return err
		}
		if outside.Open || outside.Revision != first.Revision {
			t.Fatalf("outside window admitted or revision changed: %+v", outside)
		}
		at = at.Add(-10 * time.Hour)
		if _, err = tx.ExecContext(ctx, `UPDATE maintenance_policy SET paused_until_ms=?,revision=revision+1 WHERE singleton=1`, at.Add(time.Hour).UnixMilli()); err != nil {
			return err
		}
		paused, err := reader.ReadEpisodeAnalysisTx(ctx, tx, remotesources.EpisodeTarget{})
		if err == nil && (paused.Open || paused.Revision != first.Revision+1) {
			t.Fatalf("current pause or policy revision ignored: %+v", paused)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadEpisodeAnalysisTx(ctx, nil, remotesources.EpisodeTarget{}); err == nil {
		t.Fatal("nil transaction supplied scheduling facts")
	}
}

func TestEpisodeRuntimeDoesNotInventMissingArtworkPolicy(t *testing.T) {
	c := catalogtest.Open(t)
	meta := metadata.New(c.DB, "")
	meta.EpisodeThumbnails = episodeThumbnailRuntime(meta, nil, nil, nil, nil)
	if meta.EpisodeThumbnails != nil {
		t.Fatal("partial composition advertised a source/decoder runtime")
	}
	// The command's actual availability producer remains closed before it
	// consults any catalogue or source facts, until the real dependencies exist.
	available, err := meta.CanMakeEpisodeThumbnailTx(context.Background(), nil, metadata.RepairTarget{Kind: "item", ID: "episode"})
	if err != nil || available {
		t.Fatalf("partial runtime advertised generation: %v %v", available, err)
	}
	if _, err := (episodeSourceAdapter{}).ResolveEpisodeThumbnailTx(context.Background(), nil, metadata.EpisodeThumbnailInvocation{Purpose: "made-up"}); err == nil {
		t.Fatal("unknown invocation purpose admitted")
	}
}

func TestEpisodeRuntimeCapturesActualArtworkPolicyAndPublicationGuard(t *testing.T) {
	c := catalogtest.Open(t)
	meta := metadata.New(c.DB, "")
	// This exercises the factory's policy binding only. Source/decoder inputs
	// are not opened, and these placeholders make no source-availability claim.
	runtime := episodeThumbnailRuntime(meta, &remotesources.Service{}, &identity.Service{}, nil, &subtitlevideo.Runtime{})
	if runtime == nil || runtime.ArtworkContext == nil {
		t.Fatal("complete composition lost the canonical artwork policy producer")
	}
	ctx := context.Background()
	if _, err := c.DB.Exec(`UPDATE metadata_runtime_settings SET artwork_max_edge=1280,revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	encoded, check, err := runtime.ArtworkContext(ctx)
	if err != nil || encoded == nil || check == nil {
		t.Fatal("actual stored policy was not captured", err)
	}
	if _, err = c.DB.Exec(`UPDATE metadata_runtime_settings SET artwork_max_edge=1920,revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = check(ctx, tx)
	tx.Rollback()
	if !errors.Is(err, metadata.ErrEpisodeThumbnailEncodingStale) {
		t.Fatal("factory lost the same-publication revision/edge guard", err)
	}
	if _, err = c.DB.Exec(`DELETE FROM metadata_runtime_settings WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if encoded, check, err = runtime.ArtworkContext(ctx); !errors.Is(err, sql.ErrNoRows) || encoded != nil || check != nil {
		t.Fatalf("missing policy became a placeholder edge: encoded=%t guard=%t err=%v", encoded != nil, check != nil, err)
	}
}
