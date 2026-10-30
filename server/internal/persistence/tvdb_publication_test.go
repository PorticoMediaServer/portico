package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func tvdbSchemaDB(t *testing.T, recursive bool) *sql.DB {
	t.Helper()
	db, err := Open(freshDatabaseCopy(t, t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if recursive {
		tvdbSQL(t, db, `PRAGMA recursive_triggers=ON`)
	} else {
		tvdbSQL(t, db, `PRAGMA recursive_triggers=OFF`)
	}
	library := persistenceLibrary(t, db, "lib", "TV", "tv", "/synthetic")
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := persistenceShowTx(ctx, tx, library, "a", "A", 2000); err != nil {
			return err
		}
		_, err := persistenceShowTx(ctx, tx, library, "b", "B", 2001)
		return err
	})
	return db
}

func tvdbShowID(t *testing.T, db *sql.DB, key string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_shows WHERE local_key=?`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func tvdbSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}

func tvdbInt(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func tvdbText(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatal(q, err)
	}
	return s
}

func tvdbReject(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err == nil {
		t.Fatal("unexpected accepted mutation", q)
	}
}

func tvdbEachRecursion(t *testing.T, f func(*testing.T, *sql.DB)) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprint(on), func(t *testing.T) { f(t, tvdbSchemaDB(t, on)) })
	}
}

func tvdbSeedPages(t *testing.T, db *sql.DB, show int64) {
	tvdbSQL(t, db, `INSERT INTO tvdb_publication_sets(show_id,incarnation,generation,provider_id,episode_order,policy_incarnation,policy_revision) VALUES(?,'set-a',1,100,'official','policy',1)`, show)
	tvdbSQL(t, db, `INSERT INTO tvdb_publication_pages(show_id,set_incarnation,page,next_page) VALUES(?,'set-a',0,1)`, show)
	tvdbSQL(t, db, `INSERT INTO tvdb_publication_pages(show_id,set_incarnation,page,next_page) VALUES(?,'set-a',1,NULL)`, show)
	tvdbSQL(t, db, `INSERT INTO tvdb_episode_evidence(show_id,generation,provider_id,season_number,number,absolute_number,title,overview,payload,observed_at) VALUES(?,1,10,1,1,NULL,'First','Overview','{}','now')`, show)
	tvdbSQL(t, db, `INSERT INTO tvdb_evidence_page_ownership(show_id,generation,provider_id,set_incarnation,page) VALUES(?,1,10,'set-a',0)`, show)
	tvdbSQL(t, db, `UPDATE tvdb_publication_pages SET validated_revision=content_revision`)
	tvdbSQL(t, db, `UPDATE tvdb_publication_sets SET complete=1`)
}

func TestTVDBPublicationOwnedPageInvalidation(t *testing.T) {
	tvdbEachRecursion(t, func(t *testing.T, db *sql.DB) {
		show := tvdbShowID(t, db, "a")
		tvdbSeedPages(t, db, show)
		before := tvdbInt(t, db, `SELECT revision FROM tvdb_publication_sets WHERE show_id=?`, show)
		tvdbSQL(t, db, `UPDATE tvdb_episode_evidence SET title=title WHERE provider_id=10`)
		if tvdbInt(t, db, `SELECT content_revision>validated_revision FROM tvdb_publication_pages WHERE show_id=? AND page=0`, show) != 1 || tvdbInt(t, db, `SELECT complete FROM tvdb_publication_sets WHERE show_id=?`, show) != 0 {
			t.Fatal("same-value evidence update retained attestation")
		}
		if tvdbInt(t, db, `SELECT revision FROM tvdb_publication_sets WHERE show_id=?`, show) <= before {
			t.Fatal("set revision failed to advance")
		}
		tvdbSQL(t, db, `UPDATE tvdb_publication_pages SET validated_revision=content_revision WHERE show_id=?`, show)
		tvdbSQL(t, db, `UPDATE tvdb_publication_sets SET complete=1 WHERE show_id=?`, show)
		tvdbSQL(t, db, `UPDATE tvdb_evidence_page_ownership SET page=1 WHERE show_id=? AND provider_id=10`, show)
		if tvdbInt(t, db, `SELECT count(*) FROM tvdb_publication_pages WHERE show_id=? AND content_revision>validated_revision`, show) != 2 {
			t.Fatal("ownership move missed old or new page")
		}
		tvdbReject(t, db, `INSERT INTO tvdb_evidence_page_ownership(show_id,generation,provider_id,set_incarnation,page) VALUES(?,1,10,'set-a',0)`, show)
		tvdbSQL(t, db, `UPDATE tvdb_publication_pages SET validated_revision=content_revision WHERE show_id=?`, show)
		tvdbSQL(t, db, `DELETE FROM tvdb_episode_evidence WHERE show_id=? AND provider_id=10`, show)
		tvdbSQL(t, db, `INSERT INTO tvdb_episode_evidence(show_id,generation,provider_id,season_number,number,absolute_number,title,overview,payload,observed_at) VALUES(?,1,10,1,1,NULL,'First','Overview','{}','now')`, show)
		if tvdbInt(t, db, `SELECT count(*) FROM tvdb_evidence_page_ownership WHERE show_id=?`, show) != 0 || tvdbInt(t, db, `SELECT content_revision>validated_revision FROM tvdb_publication_pages WHERE show_id=? AND page=1`, show) != 1 {
			t.Fatal("delete/recreate evidence retained ownership or receipt")
		}
		before = tvdbInt(t, db, `SELECT revision FROM tvdb_publication_sets WHERE show_id=?`, show)
		tvdbSQL(t, db, `DELETE FROM tvdb_publication_pages WHERE show_id=? AND page=1`, show)
		tvdbSQL(t, db, `INSERT INTO tvdb_publication_pages(show_id,set_incarnation,page) VALUES(?,'set-a',1)`, show)
		if tvdbInt(t, db, `SELECT revision FROM tvdb_publication_sets WHERE show_id=?`, show) <= before {
			t.Fatal("page ABA retained set revision")
		}
	})
}

func TestTVDBPublicationHierarchyAndPolicyFences(t *testing.T) {
	tvdbEachRecursion(t, func(t *testing.T, db *sql.DB) {
		showA, showB := tvdbShowID(t, db, "a"), tvdbShowID(t, db, "b")
		library := int64(0)
		if err := db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='lib'`).Scan(&library); err != nil {
			t.Fatal(err)
		}
		var episode int64
		persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			episode, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Key: compactcatalog.EpisodeKey("a", "absolute", 0, 1), Title: "Published"})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFieldsTx(ctx, tx, episode, compactcatalog.Automatic, map[string]any{"show_id": showA, "numbering": "absolute", "number": 1, "local_identity_status": "parsed"}); err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE tvdb_jobs SET provider_id=100,episode_order='absolute',status='complete',lease='old',lease_until='future' WHERE show_id=?`, showA)
			return err
		})
		a := tvdbInt(t, db, `SELECT hierarchy_revision FROM tvdb_publication_heads WHERE show_id=?`, showA)
		b := tvdbInt(t, db, `SELECT hierarchy_revision FROM tvdb_publication_heads WHERE show_id=?`, showB)
		// This test exercises the schema's hierarchy trigger directly; fixture
		// catalogue rows above are created through compactcatalog.
		tvdbSQL(t, db, `UPDATE catalog_episodes SET show_id=?,number=2 WHERE entity_id=?`, showB, episode)
		if tvdbInt(t, db, `SELECT hierarchy_revision FROM tvdb_publication_heads WHERE show_id=?`, showA) <= a || tvdbInt(t, db, `SELECT hierarchy_revision FROM tvdb_publication_heads WHERE show_id=?`, showB) <= b {
			t.Fatal("episode move missed owner")
		}
		if tvdbText(t, db, `SELECT status||':'||lease FROM tvdb_jobs WHERE show_id=?`, showA) != "pending_apply:" {
			t.Fatal("coverage not rescheduled")
		}
		before := tvdbInt(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, episode)
		tvdbSQL(t, db, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, episode)
		if tvdbInt(t, db, `SELECT source_revision FROM tvdb_publication_heads WHERE show_id=?`, showB) <= b {
			t.Fatal("item source did not propagate")
		}
		if tvdbInt(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, episode) <= before {
			t.Fatal("item publication source revision did not advance")
		}
		before = tvdbInt(t, db, `SELECT revision FROM tvdb_provider_policies WHERE library_id='lib'`)
		tvdbSQL(t, db, `UPDATE tvdb_provider_policies SET enabled=0 WHERE library_id='lib'`)
		tvdbSQL(t, db, `UPDATE tvdb_provider_policies SET enabled=1 WHERE library_id='lib'`)
		if tvdbInt(t, db, `SELECT revision FROM tvdb_provider_policies WHERE library_id='lib'`) != before+2 {
			t.Fatal("policy ABA lost")
		}
		tvdbReject(t, db, `UPDATE tvdb_provider_policies SET algorithm='unknown' WHERE library_id='lib'`)
	})
}

func TestTVDBPublicationIdentityAndRevisionGuards(t *testing.T) {
	tvdbEachRecursion(t, func(t *testing.T, db *sql.DB) {
		showA, showB := tvdbShowID(t, db, "a"), tvdbShowID(t, db, "b")
		tvdbSeedPages(t, db, showA)
		tvdbSQL(t, db, `UPDATE tvdb_publication_heads SET hierarchy_revision=hierarchy_revision+2 WHERE show_id=?`, showA)
		tvdbSQL(t, db, `UPDATE tvdb_provider_policies SET revision=revision+2 WHERE library_id='lib'`)
		tvdbSQL(t, db, `UPDATE tvdb_publication_pages SET validated_revision=0 WHERE show_id=?`, showA)
		for _, invalid := range []struct {
			q    string
			args []any
		}{
			{`UPDATE tvdb_publication_heads SET incarnation='reused' WHERE show_id=?`, []any{showA}},
			{`UPDATE tvdb_publication_heads SET hierarchy_revision=hierarchy_revision-1 WHERE show_id=?`, []any{showA}},
			{`UPDATE tvdb_provider_policies SET incarnation='reused'`, nil},
			{`UPDATE tvdb_provider_policies SET revision=revision-1`, nil},
			{`UPDATE tvdb_publication_sets SET provider_id=200 WHERE show_id=?`, []any{showA}},
			{`UPDATE tvdb_publication_sets SET revision=revision-1 WHERE show_id=?`, []any{showA}},
			{`UPDATE tvdb_publication_pages SET content_revision=content_revision-1 WHERE show_id=? AND page=0`, []any{showA}},
		} {
			tvdbReject(t, db, invalid.q, invalid.args...)
		}
		old := tvdbText(t, db, `SELECT incarnation FROM tvdb_publication_heads WHERE show_id=?`, showB)
		persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return compactcatalog.DeleteEntityTx(ctx, tx, showB) })
		var recreated int64
		library, err := compactcatalog.LibraryHandle(context.Background(), db, "lib")
		if err != nil {
			t.Fatal(err)
		}
		persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			recreated, err = func() (int64, error) {
				id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey("b"), Title: "B", Year: 2001})
				if err != nil {
					return 0, err
				}
				return id, compactcatalog.SetFieldsTx(ctx, tx, id, compactcatalog.Automatic, map[string]any{"local_key": "b"})
			}()
			return err
		})
		if tvdbText(t, db, `SELECT incarnation FROM tvdb_publication_heads WHERE show_id=?`, recreated) == old {
			t.Fatal("show incarnation reused")
		}
	})
}

func TestTVDBPublicationQueryAndAliasStage(t *testing.T) {
	db := tvdbSchemaDB(t, true)
	show := tvdbShowID(t, db, "a")
	tvdbSQL(t, db, `INSERT INTO tvdb_publication_operations(id,show_id,show_incarnation,phase,library_id,query_title,query_year,job_generation,job_revision,lease_until,episode_order,policy_incarnation,policy_revision,algorithm,refresh_mode,source_revision,hierarchy_revision,selection_revision,descriptive_revision,input_digest,status,created_at) VALUES('op',?, 'show-inc','search','lib','Exact title',2000,1,2,'deadline','','policy-inc',3,'tvdb-series-exact-title-year-v1','fill_missing',4,5,6,7,?,'claimed','now')`, show, strings.Repeat("a", 64))
	tvdbSQL(t, db, `INSERT INTO tvdb_publication_candidates VALUES('op',0,100,'Other name',2000,'Overview','{}'); INSERT INTO tvdb_publication_candidate_aliases VALUES('op',0,0,'Exact title')`)
	if tvdbText(t, db, `SELECT query_title||':'||query_year||':'||refresh_mode||':'||lease_until FROM tvdb_publication_operations`) != "Exact title:2000:fill_missing:deadline" {
		t.Fatal("query tuple lost")
	}
	if tvdbInt(t, db, `SELECT count(*) FROM tvdb_series_candidates`)+tvdbInt(t, db, `SELECT count(*) FROM provider_evidence`) != 0 {
		t.Fatal("staging changed published identity")
	}
	tvdbReject(t, db, `INSERT INTO tvdb_publication_candidate_aliases VALUES('op',0,128,'Too many')`)
	tvdbReject(t, db, `INSERT INTO tvdb_publication_candidate_aliases VALUES('op',0,1,?)`, strings.Repeat("a", 2049))
	tvdbReject(t, db, `UPDATE tvdb_publication_operations SET refresh_mode='untyped'`)
	tvdbReject(t, db, `UPDATE tvdb_publication_operations SET query_title='Substituted'`)
	tvdbReject(t, db, `UPDATE tvdb_publication_operations SET lease_until='later'`)
	tvdbSQL(t, db, `DELETE FROM tvdb_publication_operations WHERE id='op'`)
	if tvdbInt(t, db, `SELECT count(*) FROM tvdb_publication_candidate_aliases`) != 0 {
		t.Fatal("alias stage survived operation cleanup")
	}
}
