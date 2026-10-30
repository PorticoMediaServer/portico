package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Path publication is idempotent for unchanged input: the second call over the
// same accepted values and object revision must report no update and leave the
// stored row untouched, so a rescan of unchanged files cannot churn facts.
func TestReadingPathPublicationSkipsUnchangedInput(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("reading", "Reading", "book", root)
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: "reading:ebook:shelf", Title: "Shelf Book", Added: "2026-10-23T00:00:00Z"}, nil)
	asset, token := c.File(item.ID, filepath.Join(root, "Shelf", "Book.epub"), 0)
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='reading'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,directory_path) VALUES('object',?,?,?,'Shelf/Book.epub','rev','{}',1,1,'Shelf')`, source, token, incarnation)
	c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'epub')`, item.ID, asset)
	values := compactcatalog.ReadingValues{Author: &compactcatalog.ReadingValue{Value: "Shelf", Source: compactcatalog.ReadingFolder}}
	var first, second bool
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if first, err = publishReadingPathValuesTx(ctx, tx, item.ID, asset, "object", "rev", values); err != nil {
			return err
		}
		second, err = publishReadingPathValuesTx(ctx, tx, item.ID, asset, "object", "rev", values)
		return err
	})
	if !first || second {
		t.Fatalf("updated first=%v second=%v want true then false", first, second)
	}
	var text, origin, object, revision string
	if err := c.DB.QueryRow(`SELECT author,author_source,object_id,source_revision FROM reading_path_head WHERE item_id=? AND asset_id=?`, item.ID, asset).Scan(&text, &origin, &object, &revision); err != nil {
		t.Fatal(err)
	}
	if text != "Shelf" || origin != "folder" || object != "object" || revision != "rev" {
		t.Fatalf("stored accepted row = %q/%q/%q/%q", text, origin, object, revision)
	}
}
