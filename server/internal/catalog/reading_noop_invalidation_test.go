package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestReadingUnchangedInventoryObservationKeepsReadyGeneration(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("read", "Read", "book", root)
	s := New(c.DB)
	sources, err := s.LibrarySources(context.Background(), "read")
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%d: %v", len(sources), err)
	}
	source := sources[0]
	relative := "Book.pdf"
	v := storage.Snapshot{Name: "Book.pdf", Path: filepath.Join(root, "Book.pdf"), Size: 123, ModifiedNS: 456, ObjectIdentity: "inode:one"}
	observe := func(job string) error {
		return dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			_, err := s.observeInventoryTx(context.Background(), tx, job, source, relative, v, "", ScanPolicy{Tier: "file_list_only"}, nil)
			return err
		})
	}
	if err := observe("first"); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE reading_attribution_state SET publication_rule_version=?,build_generation=input_generation,ready_generation=input_generation,phase='ready' WHERE library_id=?`, compactcatalog.ReadingProjectionRuleVersion, library)
	var before int64
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := observe("second"); err != nil {
		t.Fatal(err)
	}
	var input, ready int64
	var phase, seen string
	if err := c.DB.QueryRow(`SELECT input_generation,ready_generation,phase FROM reading_attribution_state WHERE library_id=?`, library).Scan(&input, &ready, &phase); err != nil {
		t.Fatal(err)
	}
	if input != before || ready != before || phase != "ready" {
		t.Fatalf("unchanged rescan retired ready generation: before=%d input=%d ready=%d phase=%s", before, input, ready, phase)
	}
	if err := c.DB.QueryRow(`SELECT seen_job FROM inventory_objects WHERE source_id=? AND relative_path='Book.pdf' AND retired=0`, source.ID).Scan(&seen); err != nil || seen != "second" {
		t.Fatalf("seen_job=%q: %v", seen, err)
	}
	// A known UPDATE losing its row must fail closed; it cannot create a replacement.
	c.Exec(`CREATE TRIGGER reject_known_inventory_update BEFORE UPDATE ON inventory_objects WHEN NEW.seen_job='zero' BEGIN SELECT RAISE(IGNORE); END`)
	if err := observe("zero"); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("zero-row known UPDATE: %v", err)
	}
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND relative_path='Book.pdf' AND retired=0 AND seen_job='second'`, source.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("known observation replaced/reinserted: count=%d err=%v", count, err)
	}
	var object string
	if err := c.DB.QueryRow(`SELECT id FROM inventory_objects WHERE source_id=? AND relative_path='Book.pdf' AND retired=0`, source.ID).Scan(&object); err != nil {
		t.Fatal(err)
	}
	relative = "Renamed.pdf"
	v.Name = relative
	v.Path = filepath.Join(root, relative)
	if err := observe("renamed"); err != nil {
		t.Fatal(err)
	}
	var movedID string
	if err := c.DB.QueryRow(`SELECT id FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0`, source.ID, relative).Scan(&movedID); err != nil || movedID != object {
		t.Fatalf("relocation lost known identity: %q -> %q err=%v", object, movedID, err)
	}
}

func TestReadingUnchangedPublicationAttachmentDoesNotInvalidate(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("read", "Read", "book", root)
	var item int64
	attach := func() {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var token string
			if err := tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE path=?`, filepath.Join(root, "Book.pdf")).Scan(&token); err != nil {
				return err
			}
			var err error
			item, err = attachReadingTx(ctx, tx, "read", "book", token, filepath.Join(root, "Book.pdf"))
			return err
		})
	}
	c.Exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('asset',?,1,1,'pdf','','',0,0,0)`, filepath.Join(root, "Book.pdf"))
	attach()
	var before int64
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&before); err != nil {
		t.Fatal(err)
	}
	attach()
	var after int64
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("idempotent publication/link attachment invalidated: %d -> %d (item %d)", before, after, item)
	}
}

func TestReadingPublicationFormatChangeStillInvalidates(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("read", "Read", "book", root)
	c.Exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('asset',?,1,1,'pdf','','',0,0,0)`, filepath.Join(root, "Book.pdf"))
	var item int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		item, err = attachReadingTx(ctx, tx, "read", "book", "asset", filepath.Join(root, "Book.pdf"))
		return err
	})
	c.Exec(`UPDATE reading_publications SET format='epub' WHERE item_id=?`, item)
	var before, after int64
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&before); err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := attachReadingTx(ctx, tx, "read", "book", "asset", filepath.Join(root, "Book.pdf"))
		return err
	})
	var format string
	if err := c.DB.QueryRow(`SELECT format FROM reading_publications WHERE item_id=?`, item).Scan(&format); err != nil || format != "pdf" {
		t.Fatalf("format=%q err=%v", format, err)
	}
	if err := c.DB.QueryRow(`SELECT input_generation FROM reading_attribution_state WHERE library_id=?`, library).Scan(&after); err != nil || after <= before {
		t.Fatalf("format change did not invalidate: %d -> %d err=%v", before, after, err)
	}
}
