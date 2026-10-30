package compactcatalog_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func readingProjectionFixture(t *testing.T, count int) (*catalogtest.Catalog, int64, []catalogtest.Item) {
	t.Helper()
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("reading", "Reading", "book", root)
	var items []catalogtest.Item
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("Book %03d.pdf", i))
		if err := os.WriteFile(path, readingtest.PDF(), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: path, Title: fmt.Sprintf("Book %03d", i)}, nil)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "pdf"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item.ID, asset)
			return err
		})
		items = append(items, item)
	}
	return c, library, items
}

func readingProjectionStep(t *testing.T, c *catalogtest.Catalog, limit int) int {
	t.Helper()
	var n int
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var err error
		n, err = compactcatalog.StepReadingAttribution(context.Background(), tx, limit)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func readingProjectionGeneration(t *testing.T, c *catalogtest.Catalog, library int64, ready bool) compactcatalog.ReadingGeneration {
	t.Helper()
	g, err := dbwork.BeginSnapshot(context.Background(), c.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Rollback()
	out, err := compactcatalog.ReadingProjectionReadyTx(context.Background(), g.Tx(), library)
	if ready && err != nil || !ready && !errors.Is(err, compactcatalog.ErrBuilding) {
		t.Fatalf("ready=%v generation=%+v err=%v", ready, out, err)
	}
	return out
}

func readingProjectionDrain(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if readingProjectionStep(t, c, 7) == 0 {
			return
		}
	}
	t.Fatal("Reading projection did not finish bounded progress")
}

func TestReadingProjectionBoundsResumeAndPublishesOnlyAfterCleanup(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 105)
	readingProjectionGeneration(t, c, library, false)
	// A cold library first adopts the current publication rule. This bounded
	// maintenance step must not scan titles or publish a ready generation.
	if n := readingProjectionStep(t, c, 1000); n != 1 {
		t.Fatalf("publication rule adoption visited %d inputs, want 1", n)
	}
	var rule, build, scanned int64
	if err := c.DB.QueryRow(`SELECT publication_rule_version,build_generation,after_item FROM reading_attribution_state WHERE library_id=?`, library).Scan(&rule, &build, &scanned); err != nil || rule != compactcatalog.ReadingProjectionRuleVersion || build != 0 || scanned != 0 {
		t.Fatalf("publication rule adoption: rule=%d build=%d after=%d err=%v", rule, build, scanned, err)
	}
	readingProjectionGeneration(t, c, library, false)
	if n := readingProjectionStep(t, c, 1000); n != 1 {
		t.Fatalf("generation capture visited %d inputs, want 1", n)
	}
	if n := readingProjectionStep(t, c, 1000); n != 100 {
		t.Fatalf("one worker call visited %d inputs, want 100", n)
	}
	var after, count int64
	if err := c.DB.QueryRow(`SELECT after_item FROM reading_attribution_state WHERE library_id=?`, library).Scan(&after); err != nil || after != items[99].ID {
		t.Fatal(after, err)
	}
	readingProjectionGeneration(t, c, library, false)
	// A new worker instance needs no process-local progress or startup reset.
	readingProjectionDrain(t, c)
	first := readingProjectionGeneration(t, c, library, true)
	if err := c.DB.QueryRow(`SELECT count(*) FROM reading_attribution_members WHERE library_id=? AND generation=?`, library, first.Ready).Scan(&count); err != nil || count != 105 {
		t.Fatal(count, err)
	}
	c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'series','Dune','',1,'owner','now')`, items[0].ID)
	readingProjectionGeneration(t, c, library, false)
	readingProjectionStep(t, c, 100) // Capture new input.
	readingProjectionStep(t, c, 100)
	readingProjectionStep(t, c, 100)
	readingProjectionStep(t, c, 100) // True EOF; enters cleanup in its own commit.
	readingProjectionGeneration(t, c, library, false)
	if n := readingProjectionStep(t, c, 7); n != 7 {
		t.Fatalf("cleanup exceeded or skipped its bound: %d", n)
	}
	readingProjectionGeneration(t, c, library, false)
	readingProjectionDrain(t, c)
	second := readingProjectionGeneration(t, c, library, true)
	if second.Namespace != first.Namespace || second.Input <= first.Input {
		t.Fatal(first, second)
	}
	var series string
	if err := c.DB.QueryRow(`SELECT series_value FROM reading_attribution_members WHERE library_id=? AND generation=? AND item_id=?`, library, second.Ready, items[0].ID).Scan(&series); err != nil || series != "Dune" {
		t.Fatal(series, err)
	}
}

func TestReadingProjectionReadyMembersCannotMutateAndInputsRestart(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 3)
	readingProjectionDrain(t, c)
	ready := readingProjectionGeneration(t, c, library, true)
	for _, statement := range []string{
		`UPDATE reading_attribution_members SET sort_key='replacement' WHERE library_id=? AND generation=?`,
		`DELETE FROM reading_attribution_members WHERE library_id=? AND generation=?`,
		`INSERT INTO reading_attribution_members(library_id,generation,item_id,numbered_order,number_value,sort_key) VALUES(?,?,999999,1,0,'new')`,
	} {
		if _, err := c.DB.Exec(statement, library, ready.Ready); err == nil {
			t.Fatal("ready members accepted mutation", statement)
		}
	}
	c.Exec(`UPDATE catalog_entities SET sort_key='first edit' WHERE id=?`, items[0].ID)
	readingProjectionStep(t, c, 1) // Capture first invalidation.
	readingProjectionStep(t, c, 1) // Partial first build.
	c.Exec(`UPDATE catalog_entities SET sort_key='second edit' WHERE id=?`, items[0].ID)
	readingProjectionGeneration(t, c, library, false)
	readingProjectionDrain(t, c)
	current := readingProjectionGeneration(t, c, library, true)
	var sort string
	if err := c.DB.QueryRow(`SELECT sort_key FROM reading_attribution_members WHERE library_id=? AND generation=? AND item_id=?`, library, current.Ready, items[0].ID).Scan(&sort); err != nil || sort != "second edit" {
		t.Fatal(sort, err)
	}
	var obsolete int
	if err := c.DB.QueryRow(`SELECT count(*) FROM reading_attribution_members WHERE library_id=? AND generation<>?`, library, current.Ready).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatal(obsolete, err)
	}
}

func TestReadingProjectionInvalidatesOldNewScopesAndSurvivesDeletion(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 2)
	other := c.Library("other-reading", "Other", "book", t.TempDir())
	readingProjectionDrain(t, c)
	readingProjectionGeneration(t, c, library, true)
	readingProjectionGeneration(t, c, other, true)
	c.Exec(`UPDATE catalog_entities SET library_id=? WHERE id=?`, other, items[0].ID)
	readingProjectionGeneration(t, c, library, false)
	readingProjectionGeneration(t, c, other, false)
	readingProjectionDrain(t, c)
	c.Exec(`DELETE FROM catalog_entities WHERE id=?`, items[0].ID)
	readingProjectionGeneration(t, c, other, false)
	readingProjectionDrain(t, c)
	ready := readingProjectionGeneration(t, c, other, true)
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM reading_attribution_members WHERE library_id=? AND generation=?`, other, ready.Ready).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	before := readingProjectionGeneration(t, c, library, true)
	c.Exec(`DELETE FROM reading_attribution_state WHERE library_id=?`, library)
	c.Exec(`INSERT INTO reading_attribution_state(library_id) VALUES(?)`, library)
	readingProjectionDrain(t, c)
	after := readingProjectionGeneration(t, c, library, true)
	if after.Namespace <= before.Namespace {
		t.Fatal("retired state namespace was reused", before, after)
	}
}

func TestReadingProjectionProductionWorkerSeparatesScanAndCleanupCommits(t *testing.T) {
	c, library, _ := readingProjectionFixture(t, 2)
	w := compactcatalog.NewWorker(c.DB)
	for i := 0; i < 1000; i++ {
		if _, err := w.Step(context.Background(), 500); err != nil {
			t.Fatal(err)
		}
		var phase string
		var ready int64
		if err := c.DB.QueryRow(`SELECT phase,ready_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&phase, &ready); err != nil {
			t.Fatal(err)
		}
		if phase == "ready" || ready != 0 {
			t.Fatal("production worker scanned and published without a separate cleanup commit")
		}
		if phase == "cleanup" {
			readingProjectionDrain(t, c)
			readingProjectionGeneration(t, c, library, true)
			return
		}
	}
	t.Fatal("production worker never committed the scan EOF transition")
}

func TestReadingProjectionAssetInvalidationUsesTheUniqueReadingPublication(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 1)
	readingProjectionDrain(t, c)
	readingProjectionGeneration(t, c, library, true)
	movies := c.Library("movies", "Movies", "movie", t.TempDir())
	video := c.Movie(movies, filepath.Join(t.TempDir(), "Movie.mp4"), "Movie", 2026)
	c.Exec(`UPDATE catalog_assets SET available=0 WHERE id=?`, video.Asset)
	readingProjectionGeneration(t, c, library, true)
	c.Exec(`UPDATE catalog_assets SET available=0 WHERE id=(SELECT asset_id FROM reading_publications WHERE item_id=?)`, items[0].ID)
	readingProjectionGeneration(t, c, library, false)
	readingProjectionDrain(t, c)
	readingProjectionGeneration(t, c, library, true)
}

func TestReadingProjectionPacedWorkerBoundsRawInputs(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 12)
	w := compactcatalog.NewWorker(c.DB)
	w.PaceToHold(20 * time.Millisecond)
	var previousBuild, previousAfter int64
	scanned := false
	for i := 0; i < 1000; i++ {
		if _, err := w.Step(context.Background(), compactcatalog.BatchMax); err != nil {
			t.Fatal(err)
		}
		var build, after, ready int64
		if err := c.DB.QueryRow(`SELECT build_generation,after_item,ready_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&build, &after, &ready); err != nil {
			t.Fatal(err)
		}
		if build == previousBuild && after > previousAfter {
			var raw int
			if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_entities WHERE library_id=? AND id>? AND id<=?`, library, previousAfter, after).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if raw > 4 {
				t.Fatalf("paced Reading writer visited %d raw inputs, want at most four", raw)
			}
			scanned = true
		}
		previousBuild, previousAfter = build, after
		if ready > 0 {
			var count int
			if err := c.DB.QueryRow(`SELECT count(*) FROM reading_attribution_members WHERE library_id=? AND generation=?`, library, ready).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if !scanned || count != len(items) {
				t.Fatalf("bounded projection lost progress: scanned=%v members=%d want=%d", scanned, count, len(items))
			}
			return
		}
	}
	t.Fatal("paced Reading projection never published all inputs")
}
