package persistence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReadingClosureSchemaAndRawPlans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := OpenFresh(filepath.Join(t.TempDir(), "closure.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	persistenceLibrary(t, db, "closure-schema", "Reading", "book", "/read")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('closure-job','closure-schema','running','now')`)
	exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode) SELECT 'closure-job',id,generation,incarnation,root_identity,1,'first-import' FROM library_sources WHERE library_id='closure-schema'`)
	exec(`INSERT INTO inventory_reading_jobs(job_id,source_id,incarnation,generation,root_identity,root) SELECT job_id,source_id,root_incarnation,source_generation,root_identity,'/read' FROM inventory_runs WHERE job_id='closure-job'`)
	exec(`INSERT INTO inventory_reading_directories(job_id,relative_path,parent_path,top_level) VALUES('closure-job','A','.','A')`)
	exec(`INSERT INTO inventory_reading_seals(job_id,top_level,source_id,incarnation,generation,root_identity,root,coverage_revision,issue_revision,issue_highwater) SELECT job_id,'A',source_id,incarnation,generation,root_identity,root,0,0,0 FROM inventory_reading_jobs`)
	for _, q := range []string{
		`UPDATE inventory_reading_directories SET children=-1`,
		`UPDATE inventory_reading_directories SET listing_eof=2`,
		`UPDATE inventory_reading_directories SET propagated=2`,
		`UPDATE inventory_reading_directories SET reconciled=2`,
		`UPDATE inventory_reading_directories SET phase='invented'`,
		`UPDATE inventory_reading_jobs SET renewing=2`,
		`UPDATE inventory_reading_seals SET generation=2`,
		`DELETE FROM inventory_reading_jobs`,
		`DELETE FROM inventory_runs WHERE job_id='closure-job'`,
	} {
		if _, e := db.Exec(q); e == nil {
			t.Fatalf("unsafe state accepted: %s", q)
		}
	}
	// Every raw input (including rejected rows), finite upper fence, renewal
	// cursor and latest-seal probe is an index walk without a temporary sorter.
	for _, q := range []struct{ sql, index string }{
		{`SELECT id,relative_path FROM inventory_objects WHERE source_id='s' AND relative_path>='A/' AND relative_path<'A0' AND (relative_path,id)>('A/','after') AND (relative_path,id)<=('A/z','last') ORDER BY relative_path,id LIMIT 1`, "inventory_objects_reading_closure"},
		{`SELECT relative_path,id FROM inventory_objects WHERE source_id='s' AND relative_path>='A/' AND relative_path<'A0' ORDER BY relative_path DESC,id DESC LIMIT 1`, "inventory_objects_reading_closure"},
		{`SELECT relative_path FROM inventory_reading_directories WHERE job_id='closure-job' AND phase='verify' AND children=0 ORDER BY relative_path LIMIT 1`, "inventory_reading_directories_due"},
		{`SELECT relative_path FROM inventory_reading_directories WHERE job_id='closure-job' AND relative_path>'A' AND relative_path<='Z' ORDER BY relative_path LIMIT 1`, "PRIMARY KEY"},
		{`SELECT id FROM inventory_reading_seals WHERE job_id='closure-job' AND top_level='A' ORDER BY id DESC LIMIT 1`, "inventory_reading_seals_job"},
		{`SELECT sequence FROM inventory_scan_issues WHERE job_id='closure-job' AND protect=1 ORDER BY sequence DESC LIMIT 1`, "inventory_scan_issues_reading_coverage"},
	} {
		rs, e := db.Query(`EXPLAIN QUERY PLAN ` + q.sql)
		if e != nil {
			t.Fatal(e)
		}
		var plan string
		for rs.Next() {
			var a, b, c int
			var text string
			if e = rs.Scan(&a, &b, &c, &text); e != nil {
				t.Fatal(e)
			}
			plan += text
		}
		if e = rs.Err(); e != nil {
			t.Fatal(e)
		}
		rs.Close()
		if !strings.Contains(plan, q.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("unbounded raw plan: %s: %s", q.sql, plan)
		}
	}
	// A fresh revision gets a fresh never-reused ID; UPDATE cannot revive one.
	exec(`UPDATE inventory_reading_jobs SET coverage_revision=1`)
	exec(`INSERT INTO inventory_reading_seals(job_id,top_level,source_id,incarnation,generation,root_identity,root,coverage_revision,issue_revision,issue_highwater) SELECT job_id,'A',source_id,incarnation,generation,root_identity,root,0,1,0 FROM inventory_reading_jobs`)
	var n int
	if e = db.QueryRow(`SELECT max(id) FROM inventory_reading_seals`).Scan(&n); e != nil || n != 2 {
		t.Fatalf("seal lifetime: %d %v", n, e)
	}
}
