package reading

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPreparationRawWindowAdvancesAcrossIneligibleTitles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("raw", "Raw", "movie", t.TempDir())
	for i := 0; i < 130; i++ {
		c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: fmt.Sprintf("raw:%d", i), Title: "Movie", Added: "2026-10-29T00:00:00Z"}, nil)
	}
	ctx := dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)
	primary := (&Service{}).PrimaryPublicationSQL
	fail := func(context.Context, string, string, string) error {
		t.Fatal("ineligible title reached preparation")
		return nil
	}
	counts := &TextCountPreparation{DB: c.DB, Prepare: fail, Index: fail, PrimaryPublicationSQL: primary, Window: 8}
	covers := &CoverPreparation{DB: c.DB, Prepare: fail, PrimaryPublicationSQL: primary, Window: 8}
	counts.Step(ctx)
	covers.Step(ctx)
	if !counts.passing || !counts.indexPassing || !covers.passing {
		t.Fatal("ineligible window incorrectly finished the raw pass")
	}
	first := counts.cursor
	if first == 0 || counts.indexCursor != first || covers.cursor != first {
		t.Fatalf("raw cursors: counts=%d index=%d covers=%d", first, counts.indexCursor, covers.cursor)
	}
	counts.Step(ctx)
	covers.Step(ctx)
	if counts.cursor <= first || counts.indexCursor <= first || covers.cursor <= first {
		t.Fatal("an empty eligible set repeated its raw prefix")
	}
	for i := 0; i < 30 && (counts.passing || counts.indexPassing || covers.passing); i++ {
		counts.Step(ctx)
		covers.Step(ctx)
	}
	if counts.passing || counts.indexPassing || covers.passing {
		t.Fatal("finite raw pass did not finish")
	}
}

func TestPreparationRawWindowUsesPKBeforeEligibility(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	rows, err := c.DB.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+preparationWindowSQL, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var a, b, d int
		var plan string
		if err = rows.Scan(&a, &b, &d, &plan); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plan, "SEARCH catalog_entities USING INTEGER PRIMARY KEY") {
			found = true
		}
		if strings.Contains(plan, "SCAN") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatal(plan)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("raw head is not a PK range seek")
	}
	// The second query cannot widen the raw range when no title is eligible.
	for name, sql := range map[string]string{"cover": coverPendingSQL("0"), "counts": textCountPendingSQL("0"), "index": textIndexPendingSQL("0")} {
		if !strings.Contains(sql, "e.id>? AND e.id<=?") {
			t.Fatalf("%s lost its raw upper bound", name)
		}
	}
}
