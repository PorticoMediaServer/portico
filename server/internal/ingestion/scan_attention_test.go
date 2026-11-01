package ingestion

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A file the library couldn't turn into an item stays visible: a scan that
// finishes while any episode name still needs assignment ends "with warnings"
// and leaves its source "degraded", even when this scan observed nothing new
// (an incremental scan skipped the folder). Without such files it is clean.
func TestAScanNeverReadsCleanWhileFilesNeedAttention(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "attention.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	catalogue := catalogtest.New(t, db)
	catalogue.Library("tv", "TV", "tv", root)
	var source string
	if err = db.QueryRow(`SELECT id FROM library_sources WHERE library_id='tv'`).Scan(&source); err != nil {
		t.Fatal(err) // a library brings its first source
	}
	finish := func(job string) (status, health string) {
		t.Helper()
		exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'tv','running','2026-09-24T00:00:00Z')`, job)
		exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,1,'inc','id',1)`, job, source)
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		if err = finishScanTx(ctx, tx, job, source); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// Guard the actual issued witness, including the no-issue case. A
		// healthy source must not enumerate its entire resolved inventory.
		found := false
		for _, query := range trace() {
			if !strings.HasPrefix(query, "SELECT") || !strings.Contains(query, "episodic_sources") {
				continue
			}
			found = true
			rows, e := db.Query("EXPLAIN QUERY PLAN "+query, source)
			if e != nil {
				t.Fatal(e)
			}
			plan := ""
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				plan += detail + " | "
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(plan, "SEARCH e USING INDEX episodic_sources_issues") || !strings.Contains(plan, "SEARCH o USING INDEX inventory_objects_asset") || strings.Contains(plan, "inventory_objects_source") || strings.Contains(plan, "SCAN e ") || strings.Contains(plan, "SCAN o ") {
				t.Fatalf("scan attention lost its issues-only/asset seeks: %s", plan)
			}
		}
		if !found {
			t.Fatal("scan attention must use one separately planned bounded witness")
		}
		if err = db.QueryRow(`SELECT j.status,s.health FROM jobs j, library_sources s WHERE j.id=? AND s.id=?`, job, source).Scan(&status, &health); err != nil {
			t.Fatal(err)
		}
		return status, health
	}
	if status, health := finish("clean"); status != "complete" || health != "healthy" {
		t.Fatalf("a clean library: %s %s", status, health)
	}
	path := filepath.Join(root, "Show/Season 01/Show - S01E05 - pt1.mkv")
	var assetToken string
	catalogue.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		assetToken = token
		return err
	})
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'attention',id,?,incarnation,'Show/unknown.mkv','r','{}',1,1 FROM library_sources WHERE id=?`, assetToken, source)
	exec(`INSERT INTO episodic_sources(library_id,asset_id,manual,issue,source_name) VALUES('tv',?,0,'multipart_episode_requires_assignment','Show - S01E05 - pt1.mkv')`, assetToken)
	if status, health := finish("unassignable"); status != "complete_with_warnings" || health != "degraded" {
		t.Fatalf("a file needing assignment read as %s %s", status, health)
	}
	exec(`UPDATE episodic_sources SET issue='' WHERE asset_id=?`, assetToken)
	if status, health := finish("assigned"); status != "complete" || health != "healthy" {
		t.Fatalf("after assignment: %s %s", status, health)
	}
}
