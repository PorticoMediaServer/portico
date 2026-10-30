package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestWriteAmplificationUsesTheWriterConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	before, err := writerChangeCount(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO libraries(id,name,kind,root) VALUES('film','Films','movie','/films')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := writerChangeCount(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if after-before < 1 {
		t.Fatalf("writer reported %d changes for a library insert", after-before)
	}
}

// Runs only against the explicitly supplied disposable fixture. The catalogue
// remains a mixed library while this adds a new, small movie library to replace
// the one the removal scenario already removed.
func TestMeasureExistingScaleFixtureWritesAndEmptyPersonalRanges(t *testing.T) {
	path := os.Getenv("PORTICO_SCALE_WRITE_DB")
	if path == "" {
		t.Skip("set an isolated scale fixture")
	}
	if !strings.Contains(path, "/_program/scratch/scale-check/") {
		t.Fatal("refusing a non-scale fixture")
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var present bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM libraries WHERE id='movies')`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/fixture/movies')`); err != nil {
			t.Fatal(err)
		}
	}
	service := catalog.New(db)
	viewer := catalog.Viewer{Profile: "empty-scale-profile", Fence: "empty-scale", Libraries: []string{"music"}}
	for _, field := range []string{"personalRating", "lastPlayed", "playCount"} {
		var first, deep time.Duration
		for _, start := range []int{0, 50000} {
			began := time.Now()
			ctx, cost := dbwork.Measure(context.Background())
			var page catalog.BrowseResult
			err = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
				var err error
				page, err = service.WithContext(ctx).BrowseEntities(viewer, catalog.BrowseRequest{Library: "music", Pivot: "songs", Limit: 40, Sort: []catalog.BrowseSortSelection{{Field: field, Direction: "desc"}}, Range: &catalog.BrowseRange{Start: start}})
				return err
			})
			if err != nil || len(page.Entries) == 0 {
				t.Fatalf("%s range %d entries=%d: %v", field, start, len(page.Entries), err)
			}
			elapsed := time.Since(began)
			if start == 0 {
				first = elapsed
			} else {
				deep = elapsed
			}
			t.Logf("field=%s start=%d latency=%s statements=%d", field, start, elapsed, cost().Statements)
			if start > 0 && cost().Statements > 30 {
				t.Fatalf("deep range issued %d statements", cost().Statements)
			}
		}
		if deep > first*10+100*time.Millisecond {
			t.Fatalf("%s deep=%s first=%s", field, deep, first)
		}
	}
	result, err := exerciseInsert(db, service)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("insert=%+v", result)
	// A version bump keeps core search groups answering at checkpoints.
	if _, err = db.Exec(`UPDATE catalog_derivations SET version=version-1 WHERE domain IN(17,18)`); err != nil {
		t.Fatal(err)
	}
	if _, err = exerciseDrain(db, service, "derive-version-bump-checked"); err != nil {
		t.Fatal(err)
	}
}
