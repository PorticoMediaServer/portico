package httpapi

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// Both publications are real scanned files with their own folder provenance.
// The canonical writer then attaches them to one title exactly as an owner
// version attach does: no inventory, source, revision or availability fact is
// invented here. The assertion is projection only — the automatic baseline
// attribution follows the same availability-first primary version Reading List
// shows, and a primary change swaps the accepted facts without deleting them.
func TestReadingPrivatePathPrimaryVersionAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	first := readingTreeFile(t, r, "Author One/Original publication.epub")
	// Allocate the intended lower version through a real completed scan.
	// Directory enumeration/page boundaries do not promise creation order.
	readingScan(t, r.f, r.library)
	second := readingTreeFile(t, r, "Author Two/Original publication.pdf")
	readingScan(t, r.f, r.library)
	var entity int64
	if err := r.f.db.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, r.item).Scan(&entity); err != nil {
		t.Fatal(err)
	}
	attach := func(path string) (int64, string) {
		t.Helper()
		var asset, prior int64
		var token string
		if err := r.f.db.QueryRow(`SELECT a.id,a.token,p.item_id FROM catalog_assets a JOIN reading_publications p ON p.asset_id=a.id WHERE a.path=?`, path).Scan(&asset, &token, &prior); err != nil {
			t.Fatal(err)
		}
		err := dbwork.WithWriteTx(ctx, r.f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			if err := compactcatalog.UnlinkAssetTx(ctx, tx, prior, asset); err != nil {
				return err
			}
			if err := compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, entity, asset, strings.TrimPrefix(filepath.Ext(path), "."))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return asset, token
	}
	firstAsset, firstToken := attach(first)
	secondAsset, secondToken := attach(second)
	if firstAsset >= secondAsset {
		t.Fatalf("version fixture did not establish its intended asset order: %d >= %d", firstAsset, secondAsset)
	}
	// The root fixture publication is no longer a version under test; retire it
	// through the real scan. The two new siblings force a genuine republish of
	// each folder after the attach, not a hand-written source row.
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	readingTreeFile(t, r, "Author One/Sibling Book.epub")
	readingTreeFile(t, r, "Author Two/Sibling Book.pdf")
	readingScan(t, r.f, r.library)
	for _, tc := range []struct {
		asset  int64
		author string
	}{{firstAsset, "Author One"}, {secondAsset, "Author Two"}} {
		var value string
		if err := r.f.db.QueryRow(`SELECT author FROM reading_path_head WHERE item_id=? AND asset_id=? AND author_source='folder'`, entity, tc.asset).Scan(&value); err != nil || value != tc.author {
			t.Fatalf("version %d source fact: %q %v", tc.asset, value, err)
		}
	}
	row := func() reading.LibraryRow {
		t.Helper()
		list, err := r.s.List(ctx, r.p, r.library, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range list.Items {
			if v.ID == r.item {
				return v
			}
		}
		t.Fatal("title vanished from the authorized page")
		return reading.LibraryRow{}
	}
	// Establish the availability tie as well as the id order before testing
	// its projection. An unsettled/absent version must not satisfy the setup.
	var availableBefore int
	if err := r.f.db.QueryRow(`SELECT count(*) FROM inventory_item_availability WHERE item_id=? AND asset_id IN(?,?) AND available=1`, entity, firstToken, secondToken).Scan(&availableBefore); err != nil || availableBefore != 2 {
		t.Fatalf("version fixture did not establish two available versions: %d %v", availableBefore, err)
	}
	firstRow := row()
	if firstRow.Author == nil || firstRow.Author.Value != "Author One" || firstRow.Author.Source != compactcatalog.ReadingFolder {
		t.Fatalf("availability-first primary did not pick the available lower version: %+v", firstRow.Author)
	}
	if firstRow.Series != nil {
		t.Fatalf("one-title folder guess invented a series: %+v", firstRow.Series)
	}
	if firstRow.Format != "epub" || firstRow.SourceID != firstToken {
		t.Fatalf("primary version facts: %q %q", firstRow.Format, firstRow.SourceID)
	}
	// A genuine primary availability change: the chosen version's file goes
	// away, so the other accepted version becomes the automatic baseline.
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	secondRow := row()
	if secondRow.Author == nil || secondRow.Author.Value != "Author Two" || secondRow.Author.Source != compactcatalog.ReadingFolder {
		t.Fatalf("primary change did not swap accepted attribution: %+v", secondRow.Author)
	}
	if secondRow.Format != "pdf" || secondRow.SourceID != secondToken {
		t.Fatalf("new primary version facts: %q %q", secondRow.Format, secondRow.SourceID)
	}
	versions, err := r.s.Versions(ctx, r.p, r.item)
	if err != nil {
		t.Fatal("alternate versions unreadable", err)
	}
	seen, available := map[string]bool{}, map[string]bool{}
	for _, v := range versions {
		seen[v.SourceID], available[v.SourceID] = true, v.InventoryAvailable
	}
	if !seen[firstToken] || !seen[secondToken] || available[firstToken] || !available[secondToken] {
		t.Fatalf("version availability differs from the real inventory: %+v", available)
	}
}
