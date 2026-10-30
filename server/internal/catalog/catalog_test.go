package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSharedSourceDeletionPreservesSiblingAndBytes(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	lib, e := s.Create("Movies", "movie", root)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "Film (2008).mp4")
	if e = os.WriteFile(path, []byte("media"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job',?,'running','now'); INSERT INTO scan_queue(job_id,path,kind) VALUES('job',?,'file')`, lib.ID, path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CommitMovie(context.Background(), "job", lib.ID, path, assets.Facts{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 60}); e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	c.Drain()
	items, _, e := s.List(Viewer{Libraries: []string{lib.ID}}, lib.ID, "", 10)
	if e != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, e)
	}
	// A second movie backed by the same file.
	var asset int64
	var assetToken string
	if e = db.QueryRow(`SELECT l.asset_id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, c.ID(items[0].ID)).Scan(&asset, &assetToken); e != nil {
		t.Fatal(e)
	}
	sibling := c.Entity(compactcatalog.Entity{Library: c.Handle(lib.ID), Kind: compactcatalog.Movie, Key: "file:sibling", Title: "Other episode"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, sibling.ID, asset, compactcatalog.Link{})
	})
	if e = s.RemoveTitle(context.Background(), items[0].ID); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	got, e := s.Get("", sibling.Public)
	sources := fileInfoSourcesForTest(t, s, "", sibling.Public)
	if e != nil || len(got.Sources) != 0 || len(sources) != 1 || sources[0].ID != assetToken || !got.Available {
		t.Fatalf("sibling=%+v err=%v", got, e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("deleted shared bytes", e)
	}
}
