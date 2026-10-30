package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// These are persistence tests against the production baseline and attachment
// writers. Physical acquisition and actor authorization are covered by the
// actual scanner/helper/HTTP integration tests, without replacement schema.
func TestReadingRetainedVersionsRestoreOnlyExactOwnerAndAsset(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	c.Library("reading", "Reading", "book", root)
	var item int64
	var assets [2]int64
	var tokens [2]string
	for i, publication := range []struct {
		name, format string
		bytes        []byte
	}{{"Original.epub", "epub", readingtest.EPUB()}, {"Alternate.pdf", "pdf", readingtest.PDF()}} {
		path := filepath.Join(root, publication.name)
		if err := os.WriteFile(path, publication.bytes, 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			assets[i], tokens[i], err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: publication.format})
			if err != nil {
				return err
			}
			if i == 0 {
				item, err = attachReadingTx(ctx, tx, "reading", "book", tokens[i], path)
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, item, assets[i], compactcatalog.Link{}); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item, assets[i])
			return err
		})
	}
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "one", ProfileID: "same-profile"})
	sibling := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "two", ProfileID: "same-profile"})
	c.Exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,cfi,section,progression,revision,observed_at) VALUES(?,?,?,'old-epub','epub','epubcfi(/6/4!/4/2:0)','EPUB/chapter.xhtml',.2,3,100)`, owner, item, assets[0])
	c.Exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,page_index,progression,revision,observed_at) VALUES(?,?,?,'old-pdf','pdf',0,.8,2,200)`, owner, item, assets[1])
	c.Exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,page_index,progression,revision,observed_at) VALUES(?,?,?,'sibling-pdf','pdf',0,.5,1,50)`, sibling, item, assets[1])
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := retainStateTx(ctx, tx, item); err != nil {
			return err
		}
		// Unlink really cascades the PDF publication and its current positions.
		if err := compactcatalog.UnlinkAssetTx(ctx, tx, item, assets[1]); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM reading_progress WHERE owner_key=? AND item_id=?`, owner, item); err != nil {
			return err
		}
		return restoreRetainedTx(ctx, tx, item)
	})
	var cfi, revision string
	if err := c.DB.QueryRow(`SELECT cfi,source_revision FROM reading_progress WHERE owner_key=? AND item_id=? AND asset_id=?`, owner, item, assets[0]).Scan(&cfi, &revision); err != nil || cfi != "epubcfi(/6/4!/4/2:0)" || revision != "old-epub" {
		t.Fatal(cfi, revision, err)
	}
	var n int
	if err := c.DB.QueryRow(`SELECT count(*) FROM retained_reading_progress WHERE source_id=?`, tokens[1]).Scan(&n); err != nil || n != 2 {
		t.Fatal("unmatched owner versions discarded", n, err)
	}
	// A newer live EPUB position must survive while the exact PDF reappears.
	c.Exec(`UPDATE reading_progress SET progression=.7,revision=4,source_revision='new-epub',observed_at=300 WHERE owner_key=? AND item_id=?`, owner, item)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.LinkAssetTx(ctx, tx, item, assets[1], compactcatalog.Link{}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item, assets[1]); err != nil {
			return err
		}
		return restoreRetainedTx(ctx, tx, item)
	})
	for _, tc := range []struct {
		owner, revision string
		asset           int64
		progression     float64
	}{{owner, "new-epub", assets[0], .7}, {owner, "old-pdf", assets[1], .8}, {sibling, "sibling-pdf", assets[1], .5}} {
		var progression float64
		if err := c.DB.QueryRow(`SELECT source_revision,progression FROM reading_progress WHERE owner_key=? AND item_id=? AND asset_id=?`, tc.owner, item, tc.asset).Scan(&revision, &progression); err != nil || revision != tc.revision || progression != tc.progression {
			t.Fatal(tc, revision, progression, err)
		}
	}
	if err := c.DB.QueryRow(`SELECT count(*) FROM retained_reading_progress`).Scan(&n); err != nil || n != 0 {
		t.Fatal("restored positions left retained duplicates", n, err)
	}
}
