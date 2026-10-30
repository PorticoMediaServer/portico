package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestEpisodeThumbnailStoredCreationDefaults(t *testing.T) {
	c := catalogtest.Open(t)
	for _, kind := range []string{"tv", "anime", "movie", "music", "audiobook", "book", "comic"} {
		c.Library(kind, kind, kind, "/"+kind)
		g, err := dbwork.BeginSnapshot(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		p, err := EpisodeThumbnailPolicyTx(context.Background(), g.Tx(), kind)
		g.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		if p.Mode != DefaultEpisodeThumbnailMode(kind) || p.Revision != 1 {
			t.Fatalf("stored %s default is %+v", kind, p)
		}
	}
}

func TestEpisodeThumbnailStoredModeCASAndUnrelatedScanWrites(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "TV", "tv", "/tv")
	ctx := context.Background()
	write := func(fn func(*sql.Tx) error) error { return dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, fn) }
	if err := write(func(tx *sql.Tx) error { return ApplyEpisodeThumbnailModeTx(ctx, tx, "tv", "provider", 1) }); err != nil {
		t.Fatal(err)
	}
	if err := write(func(tx *sql.Tx) error { return ApplyEpisodeThumbnailModeTx(ctx, tx, "tv", "off", 1) }); !errors.Is(err, ErrLibraryConfigurationConflict) {
		t.Fatal("stale mode save accepted", err)
	}
	if err := write(func(tx *sql.Tx) error {
		return ApplyScanPolicyTx(ctx, tx, "tv", ScanPolicy{Tier: "custom", Operations: []string{"probe"}, Trickplay: TrickplaySettings{TileWidth: 160}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := write(func(tx *sql.Tx) error {
		p, err := EpisodeThumbnailPolicyTx(ctx, tx, "tv")
		if err != nil {
			return err
		}
		if p.Mode != "provider" || p.Revision != 3 {
			t.Fatalf("scan save reset thumbnail mode/revision: %+v", p)
		}
		if err = ApplyEpisodeThumbnailModeTx(ctx, tx, "tv", "provider", p.Revision); err != nil {
			return err
		}
		after, err := EpisodeThumbnailPolicyTx(ctx, tx, "tv")
		if err == nil && after != p {
			t.Fatalf("equal mode changed revision: %+v -> %+v", p, after)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"provider_then_generated", "provider_only", "", "other"} {
		if err := write(func(tx *sql.Tx) error { return ApplyEpisodeThumbnailModeTx(ctx, tx, "tv", mode, 3) }); !errors.Is(err, ErrAdminQuery) {
			t.Fatal("mode alias accepted", mode, err)
		}
		if _, err := c.DB.Exec(`UPDATE library_scan_policies SET episode_thumbnail_mode=? WHERE library_id='tv'`, mode); err == nil {
			t.Fatal("baseline admitted non-canonical mode", mode)
		}
	}
}
