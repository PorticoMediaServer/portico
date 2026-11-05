package schema

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func change001Fixture(t *testing.T) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", fixtureDSN(filepath.Join(t.TempDir(), "change001.sqlite"), true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := InitializeEmpty(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('l','Library','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'l','/namespace')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1,randomblob(16),1,1),(2,randomblob(16),1,1)`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return conn
}

func TestListeningStorageRetainsExactEdgesAndSubjects(t *testing.T) {
	conn := change001Fixture(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO listening_play_instants(profile_id,activity_generation,started_ms,subject_id,started_count,completed_count) VALUES('p',1,100,1,1,1),('p',1,900,1,1,1),('p',2,100,1,7,7)`,
		`INSERT INTO listening_profile_buckets(profile_id,activity_generation,grain,bucket_ms,completed_count) VALUES('p',1,'month',0,2)`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT sum(completed_count) FROM listening_play_instants WHERE profile_id='p' AND activity_generation=1 AND started_ms>=0 AND started_ms<500`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("exact millisecond edge = %d, %v", count, err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM catalog_subjects WHERE id=1`); err == nil {
		t.Fatal("retained listening subject was deleted")
	}
	if _, err := conn.ExecContext(ctx, `UPDATE listening_profile_buckets SET completed_count='bad'`); err == nil {
		t.Fatal("untyped counter accepted")
	}
}

func TestSearchRetirementKeepsHistoricalReceipt(t *testing.T) {
	conn := change001Fixture(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'v1','A-CJK-uni-bi-v1','3.53.4','locked','active',1)`,
		`INSERT INTO search_publications(id,entity_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens) VALUES(1,1,1,1,1,zeroblob(16),1,'A-CJK-uni-bi-v1','','','')`,
		`INSERT INTO search_reclaim_jobs(id,publication_id,live_publication_id,phase,command_incarnation) VALUES(1,1,1,'ready',zeroblob(16))`,
		`INSERT INTO search_fragments(id,publication_id,owner_entity,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation) VALUES(1,1,1,1,0,'opaque','','','',1)`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO search_fragments(publication_id,owner_entity,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation) VALUES(1,2,1,1,'opaque','','','',1)`); err == nil {
		t.Fatal("cross-subject fragment accepted")
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM search_publications WHERE id=1`); err == nil {
		t.Fatal("publication lost fragment/reclaim custody")
	}
	// Native token deletion is W's separate command contract. These rows have
	// never been indexed; this control proves only relational/result lifetime.
	for _, q := range []string{
		`DELETE FROM search_fragments WHERE id=1`,
		`UPDATE search_reclaim_jobs SET live_publication_id=NULL,phase='complete',last_command_sequence=1 WHERE id=1`,
		`DELETE FROM search_publications WHERE id=1`,
		`INSERT INTO search_reclaim_receipts(reclaim_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,publication_id,fragment_id,after_fragment_id,complete) VALUES(1,zeroblob(16),1,zeroblob(16),zeroblob(32),'image_reclaim',1,1,1,1,1,1)`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var image, fragment int
	if err := conn.QueryRowContext(ctx, `SELECT publication_id,fragment_id FROM search_reclaim_receipts WHERE reclaim_job_id=1`).Scan(&image, &fragment); err != nil || image != 1 || fragment != 1 {
		t.Fatalf("historical receipt = %d/%d, %v", image, fragment, err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM search_reclaim_jobs WHERE id=1`); err == nil {
		t.Fatal("reclaim owner lost its unacknowledged result")
	}
}
