package preparedmedia

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const selectionDownloadIndex = `CREATE INDEX IF NOT EXISTS prepared_media_download_pinned ON prepared_media_versions(item_id,asset_id,profile_id,source_revision,created_ms,id) WHERE state='published' AND length(digest)=64 AND digest NOT GLOB '*[^0-9a-f]*'`

func TestSelectionDownloadProfileExactSourceRevisionAndIndex(t *testing.T) {
	s, _, c, item := preparedFixture(t)
	ctx := context.Background()
	if _, e := s.db.Exec(selectionDownloadIndex); e != nil {
		t.Fatal(e)
	}
	// Many source versions and unrelated prepared versions must not alter the
	// exact profile/source choice or turn the lookup into a version list.
	for i := 0; i < 42; i++ {
		c.File(item.ID, fmt.Sprintf("/fixture/other-%02d.mp4", i), 60)
	}
	c.Drain()
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		source, e := selectSource(ctx, tx, item.Public, item.Token, false)
		if e != nil {
			return e
		}
		revision := hash(source)
		for i := 0; i < 45; i++ {
			job, id := fmt.Sprintf("job-selection-%02d", i), fmt.Sprintf("version-selection-%02d", i)
			profile, rev, digest := "portable-720p-high-v3", revision, strings.Repeat("a", 64)
			if i < 42 {
				profile = "unrelated"
			}
			if i == 42 {
				rev = "old-revision"
			}
			if i == 43 {
				digest = "invalid"
			}
			if _, e = tx.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES(?,?,?,?,?,'server','{}',?,'{}','succeeded','complete',?,?)`, job, item.ID, item.Token, "library", profile, rev, i, i); e != nil {
				return e
			}
			if _, e = tx.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,edition_id,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES(?,?,?,?,?,'server',?,'{}',0,'','evidence','transform',?,1,'{}','published',?,?)`, id, item.ID, item.Token, "library", profile, rev, digest, i, job); e != nil {
				return e
			}
		}
		v, ok, e := DownloadVersionTx(ctx, tx, item.Public, item.Token, "portable-720p-high-v3")
		if e != nil {
			return e
		}
		if !ok || v.ID != "version-selection-44" {
			t.Fatalf("exact profile lookup: %+v %v", v, ok)
		}
		if _, ok, e = DownloadVersionTx(ctx, tx, item.Public, item.Token, "missing"); e != nil || ok {
			t.Fatal(ok, e)
		}
		rows, e := tx.Query(`EXPLAIN QUERY PLAN SELECT id FROM prepared_media_versions WHERE item_id=? AND asset_id=? AND profile_id=? AND source_revision=? AND state='published' AND length(digest)=64 AND digest NOT GLOB '*[^0-9a-f]*' ORDER BY created_ms,id LIMIT 1`, item.ID, item.Token, "portable-720p-high-v3", revision)
		if e != nil {
			return e
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var detail string
			if e = rows.Scan(&a, &b, &c, &detail); e != nil {
				rows.Close()
				return e
			}
			plan += detail
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if !strings.Contains(plan, "prepared_media_download_pinned") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatal(plan)
		}
		if _, e = tx.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE token=?`, item.Token); e != nil {
			return e
		}
		if _, ok, e = DownloadVersionTx(ctx, tx, item.Public, item.Token, "portable-720p-high-v3"); e != nil || ok {
			t.Fatal("changed source remained selectable", ok, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestSelectionDownloadSinglePartEndpointSemantics(t *testing.T) {
	s, _, c, item := preparedFixture(t)
	ctx := context.Background()
	c.File(item.ID, "/fixture/other-version.mp4", 60)
	c.Drain()
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		single, e := DownloadSinglePartTx(ctx, tx, item.Public)
		if e != nil {
			return e
		}
		if !single {
			t.Fatal("same-part versions refused")
		}
		if _, e = tx.Exec(`UPDATE catalog_asset_links SET part_index=2 WHERE entity_id=? AND asset_id<>?`, item.ID, item.Asset); e != nil {
			return e
		}
		single, e = DownloadSinglePartTx(ctx, tx, item.Public)
		if e != nil {
			return e
		}
		if single {
			t.Fatal("multipart accepted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
