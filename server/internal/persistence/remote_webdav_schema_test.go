package persistence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWebDAVSetupFactsSchemaRequiresExplicitCurrentObservations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO remote_sources(id,kind,name,root) VALUES('source','rclone','Fixture','/fixture')`); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err = db.QueryRow(`SELECT webdav_leaf_count IS NULL FROM remote_sources WHERE id='source'`).Scan(&missing); err != nil || !missing {
		t.Fatal("unclassified source received an inferred leaf count", err)
	}
	for _, n := range []int{0, 1, 32} {
		if _, err = db.Exec(`UPDATE remote_sources SET webdav_leaf_count=? WHERE id='source'`, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int{-1, 33} {
		if _, err = db.Exec(`UPDATE remote_sources SET webdav_leaf_count=? WHERE id='source'`, n); err == nil {
			t.Fatal("invalid leaf count accepted", n)
		}
	}
	insert := `INSERT INTO remote_source_webdav_tests(source_id,source_generation,config_digest,leaf_remote,object_path,object_size,range_support,observed_ms) VALUES(?,?,?,?,?,?,?,?)`
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		source, digest, leaf, object, support string
		size, at                              int64
	}{
		{"missing", digest, "leaf:", "one.bin", "conditional", 64, 1},
		{"source", "not-a-digest", "leaf:", "one.bin", "conditional", 64, 1},
		{"source", digest, "", "one.bin", "conditional", 64, 1},
		{"source", digest, "leaf:", "", "conditional", 64, 1},
		{"source", digest, "leaf:", "one.bin", "conditional", 0, 1},
		{"source", digest, "leaf:", "one.bin", "unknown", -1, 1},
		{"source", digest, "leaf:", "one.bin", "future", 64, 1},
		{"source", digest, "leaf:", "one.bin", "conditional", 64, 0},
	} {
		if _, err = db.Exec(insert, test.source, "1", test.digest, test.leaf, test.object, test.size, test.support, test.at); err == nil {
			t.Fatal("invalid observation accepted", test.support)
		}
	}
	if _, err = db.Exec(insert, "source", "1", digest, "leaf:", "one.bin", 64, "conditional", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(insert, "source", "1", digest, "leaf:", "two.bin", 64, "conditional", 1); err == nil {
		t.Fatal("duplicate current leaf accepted")
	}
	if _, err = db.Exec(insert, "source", "1", digest, "unreproducible:", "", 0, "unknown", 1); err != nil {
		t.Fatal("unknown required fabricated object facts", err)
	}
	if _, err = db.Exec(`DELETE FROM remote_sources WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = db.QueryRow(`SELECT count(*) FROM remote_source_webdav_tests`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("orphaned private setup facts", rows, err)
	}
}
