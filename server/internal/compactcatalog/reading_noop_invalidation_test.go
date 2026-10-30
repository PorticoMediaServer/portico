package compactcatalog_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestReadingIdempotentAssetAndLinkWritesKeepInputGeneration(t *testing.T) {
	c, library, items := readingProjectionFixture(t, 1)
	item := items[0]
	var asset int64
	var path, token string
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT a.id,a.token,a.path,a.size,a.modified_ns FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, item.ID).Scan(&asset, &token, &path, &size, &modified); err != nil {
		t.Fatal(err)
	}
	generation := func() int64 {
		t.Helper()
		var g int64
		if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	for _, tc := range []struct {
		name  string
		write func(context.Context, *sql.Tx) error
	}{
		{"asset", func(ctx context.Context, tx *sql.Tx) error {
			id, got, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: size, ModifiedNS: modified, Container: "pdf"})
			if err == nil && (id != asset || got != token) {
				t.Fatalf("asset identity changed: %d/%s", id, got)
			}
			return err
		}},
		{"link", func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := generation()
			c.Write(tc.write)
			if after := generation(); after != before {
				t.Fatalf("unchanged %s invalidated Reading: %d -> %d", tc.name, before, after)
			}
		})
	}
	before := generation()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, asset, false)
	})
	if generation() <= before {
		t.Fatal("real availability loss did not invalidate")
	}
	before = generation()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: size, ModifiedNS: modified, Container: "pdf"})
		return err
	})
	if generation() <= before {
		t.Fatal("asset reappearance did not invalidate")
	}
	before = generation()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{Edition: "New edition"})
	})
	if generation() <= before {
		t.Fatal("real link change did not invalidate")
	}
}

// Health is read live at browse time, so a scan's health flips must not restart
// the whole-library attribution build; a real source change still does.
func TestReadingSourceHealthFlipKeepsInputGeneration(t *testing.T) {
	c, library, _ := readingProjectionFixture(t, 1)
	generation := func() int64 {
		t.Helper()
		var g int64
		if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	var source string
	if err := c.DB.QueryRow(`SELECT s.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id WHERE cl.id=?`, library).Scan(&source); err != nil {
		t.Fatal(err)
	}
	before := generation()
	for _, health := range []string{"inventory_running", "healthy", "inventory_running", "healthy"} {
		c.Exec(`UPDATE library_sources SET health=? WHERE id=?`, health, source)
	}
	c.Exec(`UPDATE library_sources SET enabled=enabled,root=root WHERE id=?`, source)
	if after := generation(); after != before {
		t.Fatalf("health or same-value source writes restarted attribution: %d -> %d", before, after)
	}
	c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, source)
	if generation() <= before {
		t.Fatal("real source generation change did not invalidate")
	}
}

// The attribution page walks one library in id order inside the writer.
func TestReadingAttributionPageUsesLibraryIDIndex(t *testing.T) {
	c, library, _ := readingProjectionFixture(t, 1)
	rows, err := c.DB.Query(`EXPLAIN QUERY PLAN SELECT e.id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id WHERE e.library_id=? AND e.id>? ORDER BY e.id LIMIT ?`, library, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "catalog_entities_library_id") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("attribution page plan does not seek (library_id,id):\n%s", joined)
	}
}
