package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// This exercises inventory classification and normalized source storage only.
// Actual RAR5 image bytes must still pass the existing bounded reader before a
// Reading session or page can be exposed; no renamed ZIP fixture is used.
func TestCBRInventoryAndAttachmentRequireActualCompiledReader(t *testing.T) {
	for _, path := range []string{"Book.cbr", "Book.CBR"} {
		if got := readingFormat(path, "comic"); (got == "cbr") != readingcap.CBR() {
			t.Fatal("classifier and decoder capability differ", got)
		}
		if assets.SupportedForKind(path, "comic") != readingcap.CBR() {
			t.Fatal("inventory and publication classifier differ")
		}
		for _, kind := range []string{"book", "music", "audiobook"} {
			if readingFormat(path, kind) != "" || assets.SupportedForKind(path, kind) {
				t.Fatal("CBR admitted outside its comic library", kind)
			}
		}
	}
	c := catalogtest.Open(t)
	root := t.TempDir()
	c.Library("comics", "Comics", "comic", root)
	path := filepath.Join(root, "Book.cbr")
	var attached int64
	var attachErr error
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 23, ModifiedNS: 11, Container: "cbr"})
		if err != nil {
			return err
		}
		var token string
		if err = tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, asset).Scan(&token); err != nil {
			return err
		}
		attached, attachErr = attachReadingTx(ctx, tx, "comics", "comic", token, path)
		return nil
	})
	if !readingcap.CBR() {
		if attachErr == nil || attached != 0 {
			t.Fatal("untagged build attached a CBR", attachErr, attached)
		}
		return
	}
	if attachErr != nil {
		t.Fatal(attachErr)
	}
	var format string
	if err := c.DB.QueryRow(`SELECT format FROM reading_publications WHERE item_id=?`, attached).Scan(&format); err != nil || format != "cbr" {
		t.Fatal("actual original format not stored", format, err)
	}
	if _, err := c.DB.Exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,page_index,progression,revision,observed_at) SELECT 'test-owner',item_id,asset_id,'revision','cbr',0,0,1,1 FROM reading_publications WHERE item_id=?`, attached); err != nil {
		t.Fatal("fixed-page CBR coordinate refused", err)
	}
	if _, err := c.DB.Exec(`UPDATE reading_progress SET page_index=NULL,cfi='epubcfi(/6/2)' WHERE item_id=?`, attached); err == nil {
		t.Fatal("CBR accepted a fabricated EPUB coordinate")
	}
}
