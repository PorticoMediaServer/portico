package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func emptyParentFixture(t *testing.T) (*catalogtest.Catalog, catalogtest.Item, catalogtest.Item, catalogtest.Item) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(library, "Show", 2020)
	season := c.Season(show, 1)
	for n := 2; n <= 41; n++ {
		c.Season(show, n)
	}
	ep := c.Episode(show, season, 1, "/tv/episode.mkv")
	c.Drain()
	return c, show, season, ep
}

func driveEmptyParent(t *testing.T, c *catalogtest.Catalog, show int64) {
	t.Helper()
	var root string
	if e := c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='empty-parent' AND subject_id=? AND status='active'`, show).Scan(&root); e != nil {
		t.Fatal("no durable empty Show consequence", e)
	}
	for turn := 0; turn < 48; turn++ {
		before := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show)
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
		if e != nil {
			t.Fatal(out, e)
		}
		after := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show)
		if before-after > 1 {
			t.Fatal("empty-parent turn deleted season list", before, after)
		}
		if out.State == "succeeded" {
			if out.Phase != "removed" || after != 0 || rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, show) != 0 {
				t.Fatal(out, after)
			}
			return
		}
	}
	t.Fatal("empty Show consequence did not finish")
}

func TestP252bSeasonLastEpisodeWaitsForOwnedSeasonThenResumes(t *testing.T) {
	c, show, season, ep := emptyParentFixture(t)
	var ref compactcatalog.MutationRef
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: season.ID, OwnerKind: "title", OwnerID: season.Public, OwnerGeneration: 1, Mode: "members"})
		return e
	})
	for turn := 0; turn < 12; turn++ {
		mutationTurn(t, c, ref)
		if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, ep.ID) == 0 {
			break
		}
	}
	if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, ep.ID) != 0 || rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE entity_id=?`, season.ID) != 1 {
		t.Fatal("last Episode deleted owned Season or rolled back")
	}
	var root string
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='empty-parent'`).Scan(&root)
	for turn := 0; turn < 3; turn++ {
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
		if e != nil || out.State != "running" {
			t.Fatal(out, e)
		}
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID); n != 41 {
		t.Fatal("waiting cleanup crossed owned first Season", n)
	}
	library := c.Handle("tv")
	e := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, _, e := compactcatalog.EnsureEntityTx(context.Background(), tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Parent: season.ID, Key: "late-episode", Title: "Late"})
		return e
	})
	if !errors.Is(e, compactcatalog.ErrEntityMutating) {
		t.Fatal("pending Show/Season allowed new acquisition", e)
	}
	for turn := 0; turn < 8; turn++ {
		if step := mutationTurn(t, c, ref); step.Complete {
			break
		}
	}
	if rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE entity_id=?`, season.ID) != 0 {
		t.Fatal("Season owner could not finish with same-family parent root")
	}
	var seq int
	var name, path string
	if e = c.DB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	if e = c.DB.Close(); e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c = catalogtest.New(t, db)
	e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, _, e := compactcatalog.EnsureEntityTx(context.Background(), tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Parent: show.ID, Key: "late-after-season", Title: "Late"})
		return e
	})
	if !errors.Is(e, compactcatalog.ErrEntityMutating) {
		t.Fatal("empty Show root lost acquisition fence after Season completion", e)
	}
	for _, set := range []string{"mode='remove'", "provider='musicbrainz'", "parent_mutation_id='forged'", "current_child='forged'"} {
		if _, e = c.DB.Exec(`UPDATE catalog_entity_mutations SET `+set+` WHERE mutation_id=?`, root); e == nil {
			t.Fatal("broad empty-parent authority accepted", set)
		}
	}
	driveEmptyParent(t, c, show.ID)
}

func TestP252bConcurrentSeasonOwnersDeferParentWithoutRollback(t *testing.T) {
	c, show, season, ep := emptyParentFixture(t)
	var other int64
	c.DB.QueryRow(`SELECT entity_id FROM catalog_seasons WHERE show_id=? AND entity_id<>? ORDER BY entity_id LIMIT 1`, show.ID, season.ID).Scan(&other)
	var a, b compactcatalog.MutationRef
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		a, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: other, OwnerKind: "title", OwnerID: "season-a", OwnerGeneration: 1, Mode: "members"})
		if e != nil {
			return e
		}
		b, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: season.ID, OwnerKind: "title", OwnerID: "season-b", OwnerGeneration: 1, Mode: "members"})
		return e
	})
	mutationTurn(t, c, a)
	mutationTurn(t, c, a)
	for turn := 0; turn < 12; turn++ {
		mutationTurn(t, c, b)
		if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, ep.ID) == 0 {
			break
		}
	}
	if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, ep.ID) != 0 {
		t.Fatal("competing Season lifetime rolled back last Episode")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='empty-parent'`); n != 0 {
		t.Fatal("parent admission bypassed competing Season", n)
	}
	if step := mutationTurn(t, c, a); !step.Complete {
		t.Fatal(step)
	}
	for turn := 0; turn < 8; turn++ {
		if step := mutationTurn(t, c, b); step.Complete {
			break
		}
	}
	driveEmptyParent(t, c, show.ID)
}

func TestP252bEmptyParentRefusesAppearedEpisodeWithoutDeletingMembers(t *testing.T) {
	c, show, _, ep := emptyParentFixture(t)
	library := c.Handle("tv")
	otherShow := c.Show(library, "Other", 2020)
	otherSeason := c.Season(otherShow, 1)
	other := c.Episode(otherShow, otherSeason, 1, "/tv/other.mkv")
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return RemoveTitleTx(ctx, tx, ep.ID) })
	var root string
	var season int64
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='empty-parent'`).Scan(&root)
	c.DB.QueryRow(`SELECT entity_id FROM catalog_seasons WHERE show_id=? ORDER BY entity_id LIMIT 1`, show.ID).Scan(&season)
	// Model an unsafe/raw caller racing the normal acquisition guard. The
	// dedicated consequence must still prove emptiness before every action.
	c.Exec(`UPDATE catalog_entities SET parent_id=? WHERE id=?`, season, other.ID)
	c.Exec(`UPDATE catalog_episodes SET show_id=?,season_id=? WHERE entity_id=?`, show.ID, season, other.ID)
	out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
	if e != nil || out.State != "succeeded" || out.Phase != "retained" {
		t.Fatal(out, e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID); n != 40 {
		t.Fatal("empty-parent adopted Season/member work", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_episodes WHERE entity_id=?`, other.ID); n != 1 {
		t.Fatal("empty-parent deleted appeared member")
	}
}

func TestP252bDirectEpisodeMissingAndLibraryParentCleanupIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, caller := range []string{"direct", "missing", "library"} {
		t.Run(caller, func(t *testing.T) {
			c, show, _, ep := emptyParentFixture(t)
			library := c.Handle("tv")
			if caller == "missing" {
				c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,id,?,incarnation,'episode.mkv','r','{}',1000,1 FROM library_sources WHERE library_id='tv' LIMIT 1`, "object:"+ep.Token, ep.Token)
				markMissing(t, c, "tv", ep.Token, time.Now().Add(-time.Hour))
				out, e := New(c.DB).EmptyMissing(context.Background(), "tv", time.Now(), nil)
				if e != nil || out.Titles != 1 {
					t.Fatal(out, e)
				}
			} else if caller == "library" {
				c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='tv'`)
				for turn := 0; turn < 8; turn++ {
					c.Write(func(ctx context.Context, tx *sql.Tx) error { _, e := removeLibraryTitlesTx(ctx, tx, library); return e })
					if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, ep.ID) == 0 {
						break
					}
				}
			} else {
				c.Write(func(ctx context.Context, tx *sql.Tx) error { return RemoveTitleTx(ctx, tx, ep.ID) })
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID); n != 40 {
				t.Fatal("direct Episode finalizer listed seasons", n)
			}
			if caller == "library" {
				if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='empty-parent'`); n != 0 {
					t.Fatal("library created extra work root", n)
				}
				for turn := 0; turn < 300; turn++ {
					var done bool
					c.Write(func(ctx context.Context, tx *sql.Tx) error {
						var e error
						done, e = removeLibraryBatchTx(ctx, tx, "tv")
						return e
					})
					if done {
						break
					}
				}
				if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, show.ID); n != 0 {
					t.Fatal("library container lane did not settle Show", n)
				}
			} else {
				driveEmptyParent(t, c, show.ID)
			}
		})
	}
}

func TestP252bGenericAlbumAdoptionCoalescesKnownRoots(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	var library, artist int64
	c.DB.QueryRow(`SELECT library_id,parent_id FROM catalog_entities WHERE id=?`, album).Scan(&library, &artist)
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "same-release"}
	var other, unknown int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		other, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artist, Key: "second-known-album", Title: "Other"})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, other, map[string]any{"artist_id": artist, "local_key": "second"}); e != nil {
			return e
		}
		unknown, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artist, Key: "unknown-album", Title: "Unknown"})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, unknown, map[string]any{"artist_id": artist, "local_key": "unknown"}); e != nil {
			return e
		}
		for _, id := range []int64{album, other} {
			if e = compactcatalog.RecordProviderIdentityTx(ctx, tx, id, p); e != nil {
				return e
			}
		}
		if e = enqueueIdentityAdoptionTx(ctx, tx, album, p); e != nil {
			return e
		}
		_, e = AdoptProviderIdentityTx(ctx, tx, other, p)
		return e
	})
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE status='active' AND mode='adopt'`); n != 1 {
		t.Fatal("known Album identity roots did not coalesce", n)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, e := AdoptProviderIdentityTx(ctx, tx, unknown, p); !errors.Is(e, compactcatalog.ErrEntityMutating) {
			t.Fatal("unknown key joined frozen root", e)
		}
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, unknown, p); !errors.Is(e, compactcatalog.ErrEntityMutating) {
			t.Fatal("provider freeze weakened", e)
		}
		return nil
	})
	var root string
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE status='active'`).Scan(&root)
	for turn := 0; turn < 24; turn++ {
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
		if e != nil {
			t.Fatal(e)
		}
		if out.State == "succeeded" {
			break
		}
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_albums WHERE entity_id IN(?,?)`, album, other); n != 1 {
		t.Fatal("coalesced root skipped known candidate", n)
	}
}

func TestP252bFreshEmptyShowKeepsForeignSeasonCustody(t *testing.T) {
	c, show, season, episode := emptyParentFixture(t)
	var ref compactcatalog.MutationRef
	var plan TitleDeletePlan
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := purgeEntityTx(ctx, tx, episode.ID); e != nil {
			return e
		}
		var e error
		plan, e = CaptureTitleDeleteTx(ctx, tx, show.ID)
		if e != nil {
			return e
		}
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: season.ID, OwnerKind: "title", OwnerID: "foreign-season", OwnerGeneration: 1, Mode: "members"})
		return e
	})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, e := DeleteEmptyApprovedContainerStepTx(ctx, tx, plan, "new-show:approval"); !errors.Is(e, compactcatalog.ErrEntityMutating) {
			t.Fatal("fresh Show consumed a foreign domain", e)
		}
		m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if e != nil {
			return e
		}
		if m.Status != "active" {
			t.Fatal("foreign Season was unwound", m.Status)
		}
		return nil
	})
}
