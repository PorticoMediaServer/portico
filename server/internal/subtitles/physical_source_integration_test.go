package subtitles

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestSubtitleSourceUsesSelectedPhysicalRootAndRejectsRetiredIncarnation(t *testing.T) {
	f := newSubtitleFixture(t)
	if _, e := f.db.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('second-root','library','/second','/second','physical-v1')`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','second-root',?,'physical-v1','movie.mp4','rev','{}',1000,1)`, f.item.Token); e != nil {
		t.Fatal(e)
	}
	f.catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.item.Asset, map[string]any{"path": "/second/movie.mp4"})
	})
	f.catalog.Library("foreign-library", "Other", "movie", "/foreign")
	if _, e := f.db.Exec(`UPDATE library_sources SET incarnation='foreign-v1' WHERE library_id='foreign-library'`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('foreign-object','foreign-library',?,'foreign-v1','movie.mp4','rev','{}',1000,1)`, f.item.Token); e != nil {
		t.Fatal(e)
	}
	f.catalog.Drain()
	src, e := sourceQuery(context.Background(), f.db, f.item.Public, f.item.Token)
	if e != nil {
		t.Fatal(e)
	}
	if src.root != "/second" {
		t.Fatalf("wrong physical root %s", src.root)
	}
	if _, e = f.db.Exec(`UPDATE library_sources SET enabled=0 WHERE id='second-root'`); e != nil {
		t.Fatal(e)
	}
	if _, e = sourceQuery(context.Background(), f.db, f.item.Public, f.item.Token); e == nil {
		t.Fatal("disabled authorized root fell back to foreign-library sibling")
	}
	if _, e = f.db.Exec(`UPDATE library_sources SET enabled=1 WHERE id='second-root'`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE library_sources SET incarnation='physical-v2' WHERE id='second-root'`); e != nil {
		t.Fatal(e)
	}
	if _, e = sourceQuery(context.Background(), f.db, f.item.Public, f.item.Token); e == nil {
		t.Fatal("old physical incarnation accepted")
	}
}
