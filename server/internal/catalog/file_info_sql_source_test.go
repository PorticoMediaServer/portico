package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func orderedInfoHeadCheck(db *sql.DB, item, epoch int64) fileInfoBasisCheck {
	return func(ctx context.Context) error {
		head, err := compactcatalog.InfoHeadTx(ctx, db, item)
		if err != nil {
			return err
		}
		if !head.Ready() || head.Desired != epoch {
			return readprep.ErrStale
		}
		return nil
	}
}

func TestFileInfoFlatSQLSourceKeepsAcceptedOrderDuringRawScalarChurn(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("ordered", "Ordered", "movie", "/ordered")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "ordered", Title: "Ordered"}, nil)
	var first int64
	for i := 0; i < 31; i++ {
		asset, _ := c.File(item.ID, fmt.Sprintf("/ordered/%02d.mkv", i), float64(100+i))
		if i == 0 {
			first = asset
		}
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			if err := compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{"height": int64((i % 3) * 720), "size": int64(1000 + i%4), "available": i%4 != 0}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET part_index=? WHERE entity_id=? AND asset_id=?`, i%3, item.ID, asset)
			return err
		})
	}
	c.Drain()
	before := []fileInfoOccurrence{}
	rows, err := c.DB.Query(`SELECT l.entity_id,a.id FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY `+compactcatalog.BestVersionOrder("l", "a"), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var occurrence fileInfoOccurrence
		if err := rows.Scan(&occurrence.Item, &occurrence.Asset); err != nil {
			t.Fatal(err)
		}
		before = append(before, occurrence)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, item.ID)
	if err != nil || !head.Ready() {
		t.Fatal(head, err)
	}
	check := orderedInfoHeadCheck(c.DB, item.ID, head.Desired)
	read := func() fileInfoOrderedSummary {
		source, err := newFileInfoFlatSource(c.DB, item.ID, false, 64, check, nil)
		if err != nil {
			t.Fatal(err)
		}
		page, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if page := read(); !reflect.DeepEqual(page.Occurrences, before) || page.Total != 31 {
		t.Fatal("initial order", page, before)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, first, map[string]any{"height": int64(math.MaxInt64), "size": int64(math.MaxInt64), "available": true})
	})
	if page := read(); !reflect.DeepEqual(page.Occurrences, before) {
		t.Fatal("raw NEW reordered an accepted OLD basis", page.Occurrences, before)
	}
	// Exact primary-key comparator proof; the source never performs a whole-file
	// ORDER BY or places opaque tokens/technical strings in its shared summary.
	plan, err := c.DB.Query(`EXPLAIN QUERY PLAN `+fileInfoAcceptedOrderCompare, item.ID, first, item.ID, before[0].Asset)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
		if strings.Contains(detail, "SCAN l") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal("unbounded comparator plan", detail)
		}
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	plan.Close()
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "SEARCH l USING PRIMARY KEY") {
		t.Fatal("comparator did not use exact link keys", joined)
	}
	c.Drain()
	source, err := newFileInfoFlatSource(c.DB, item.ID, false, 64, check, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 1, Direction: fileInfoForward}); err == nil {
		t.Fatal("original opening silently adopted the new source head")
	}
}

func TestFileInfoFlatSQLSourceConsumesEmptyAndDeniedChildren(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("children", "Children", "movie", "/children")
	root := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "root", Title: "Root"}, nil)
	empty := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Parent: root.ID, Key: "empty", Title: "Empty"}, nil)
	denied := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Parent: root.ID, Key: "denied", Title: "Denied"}, nil)
	allowed := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Parent: root.ID, Key: "allowed", Title: "Allowed"}, nil)
	c.File(denied.ID, "/children/denied.mkv", 100)
	a, _ := c.File(allowed.ID, "/children/first.mkv", 101)
	b, _ := c.File(allowed.ID, "/children/second.mkv", 102)
	c.Drain()
	head, err := compactcatalog.InfoHeadTx(context.Background(), c.DB, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	source, err := newFileInfoFlatSource(c.DB, root.ID, true, 64, orderedInfoHeadCheck(c.DB, root.ID, head.Desired), func(_ context.Context, child int64) (bool, error) { checks++; return child != denied.ID, nil })
	if err != nil {
		t.Fatal(err)
	}
	page, err := orderedInfoTestRun(t, source, fileInfoOrderedSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Eligible != 2 || len(page.Occurrences) != 2 || page.Occurrences[0].Item != allowed.ID || page.Occurrences[1].Item != allowed.ID || checks != 3 {
		t.Fatal("hidden/empty child broke flat exhaustion", page, checks, empty.ID)
	}
	have := map[int64]bool{}
	for _, o := range page.Occurrences {
		have[o.Asset] = true
	}
	if !have[a] || !have[b] {
		t.Fatal("flat occurrence was lost", page)
	}
}
