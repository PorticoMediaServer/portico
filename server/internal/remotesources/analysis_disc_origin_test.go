package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// Consent permits only otherwise supported analysis. It cannot turn a remote
// ISO origin into pre-Play analysis input or borrow the playback purpose.
func TestRemoteDiscOriginRefusesAnalysisPurposesWithConsent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, name := range []string{"episode.iso", "episode.ISO", "episode.img"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newObservationFixture(t)
			f.file = filepath.Join(filepath.Dir(f.file), name)
			facts := scanFacts("disc-analysis")
			facts.Facts.Object = name
			ref, err := f.record(t, facts, 0)
			if err != nil {
				t.Fatal(err)
			}
			identities, owner, _ := observationOwner(t, f)
			writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			profile, _ := SourceReadPreset("everything")
			if _, err = f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", Profile: profile, AdvancedMediaAnalysis: analysisConsentPointer(true)}); err != nil {
				t.Fatal(err)
			}
			if err = writer.Commit(); err != nil {
				t.Fatal(err)
			}
			view, err := dbwork.BeginSnapshot(ctx, f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			defer view.Rollback()
			check := func(ctx context.Context, tx *sql.Tx, actual SourceReadBinding) error {
				if actual.Observation != ref {
					t.Fatal("source admission retargeted the original observation")
				}
				return currentLocalOwnerTx(ctx, tx, identities, owner)
			}
			for _, purpose := range []SourceReadPurpose{SourceMediaAnalysis, SourceEpisodeAutomatic, SourceEpisodeExplicit} {
				lease, err := f.s.CaptureReadSourceTx(ctx, view.Tx(), SourceSessionAdmission{Purpose: purpose, Observation: ref, CheckTx: check, OnChanged: func() {}})
				if lease != nil || !errors.Is(err, mediaanalysis.ErrUnsupported) {
					t.Fatalf("%s admitted a disc origin: lease=%v err=%v", purpose, lease != nil, err)
				}
			}
			if len(f.s.serveLeases) != 0 || len(f.s.readLeases["remote"]) != 0 {
				t.Fatal("refused analysis retained a source reader or opened the provider")
			}
			resolver := EpisodeAuthorityResolver{Sources: f.s}
			if err = resolver.readEpisodeRemoteTx(ctx, view.Tx(), EpisodeTarget{}, &episodeAuthoritySnapshot{File: f.file}); !errors.Is(err, mediaanalysis.ErrUnsupported) {
				t.Fatal("episode origin capability admitted a disc", err)
			}
			playback, err := f.s.CaptureReadSourceTx(ctx, view.Tx(), SourceSessionAdmission{Purpose: SourcePlayback, Observation: ref, CheckTx: check, OnChanged: func() {}})
			if err != nil {
				t.Fatal("analysis-origin limitation blocked normal playback admission", err)
			}
			defer playback.Close()
		})
	}
}
