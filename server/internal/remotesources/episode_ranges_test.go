package remotesources

import (
	"context"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Stored support here is a fixture fact. These cases prove the DB projection,
// not provider range behavior; real source I/O is qualified separately.
func TestEpisodeRangesFactsDoNotGrantOperationAuthority(t *testing.T) {
	f, resolver, request, _ := episodeReadFixture(t)
	resolver.Identities, resolver.Episodes, resolver.Analysis = nil, nil, nil
	for _, support := range []string{"conditional", "unknown", "sequential", "unsupported", "random"} {
		if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support=?`, support); err != nil {
			t.Fatal(err)
		}
		view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
		if err != nil {
			t.Fatal(err)
		}
		got, err := resolver.EpisodeRangesSupportedTx(context.Background(), view.Tx(), request.Target)
		if err != nil || got != (support == "conditional") {
			t.Fatal(support, got, err)
		}
		if _, err = resolver.ResolveEpisodeReadSourceTx(context.Background(), view.Tx(), request, func() {}); err == nil {
			t.Fatal("factual support replaced missing operation authority")
		}
		view.Rollback()
	}
	if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
		t.Fatal("range projection opened source I/O")
	}
	if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support='conditional'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO remote_analysis_status(path,code,observed_at) VALUES(?,'range_unsupported',1)`, f.file); err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.EpisodeRangesSupportedTx(context.Background(), view.Tx(), request.Target)
	view.Rollback()
	if got || err != nil {
		t.Fatal("per-object range failure was hidden by source support", got, err)
	}
	if got, err := resolver.EpisodeRangesSupportedTx(context.Background(), nil, request.Target); got || err == nil {
		t.Fatal("missing transaction reported support", got, err)
	}
}

func TestEpisodeRangesFactsRequireCurrentTargetAndObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []struct{ name, sql string }{
		{"catalog-revision", `UPDATE inventory_objects SET revision='replacement'`},
		{"library-root", `UPDATE library_sources SET root_identity='replacement'`},
		{"disabled", `UPDATE library_sources SET enabled=0`},
		{"source-root", `UPDATE remote_sources SET root=root||'/replacement'`},
		{"source-generation", `UPDATE remote_sources SET generation=generation+1`},
		{"missing-observation", `DELETE FROM remote_scan_observations`},
		{"not-video", `UPDATE catalog_assets SET video_codec=''`},
	} {
		t.Run(change.name, func(t *testing.T) {
			f, resolver, request, _ := episodeReadFixture(t)
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support='conditional'`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.db.Exec(change.sql); err != nil {
				t.Fatal(err)
			}
			view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			defer view.Rollback()
			if got, err := resolver.EpisodeRangesSupportedTx(context.Background(), view.Tx(), request.Target); got || err == nil {
				t.Fatal("stale target reported support", got, err)
			}
		})
	}
}

func TestEpisodeLocalRangeFactsNeedConfiguredConfinedReader(t *testing.T) {
	f, resolver, request, _ := episodeReadFixture(t)
	if _, err := f.s.db.Exec(`UPDATE library_sources SET kind='local'`); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(helper)
	managed, err := mounts.New(f.s.db, t.TempDir(), "", helper, client)
	if err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	check := func(want bool) {
		t.Helper()
		got, err := resolver.EpisodeRangesSupportedTx(context.Background(), view.Tx(), request.Target)
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	check(false)
	resolver.LocalStorage = storage.New("")
	check(false)
	resolver.LocalStorage = client
	check(false)
	// Configuration only: this factual reader never borrows a descriptor.
	f.s.mounts = managed
	check(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := resolver.EpisodeRangesSupportedTx(ctx, view.Tx(), request.Target); got || err == nil {
		t.Fatal("canceled projection admitted support", got, err)
	}
}
