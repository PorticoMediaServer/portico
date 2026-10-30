package persistence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowsePredicateEpochSynchronousRollbackAndNoops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "epoch.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	epoch := func() int64 {
		var n int64
		if e := db.QueryRow(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := epoch()
	for _, step := range []struct {
		query string
		delta int64
	}{
		{`INSERT INTO content_rating_ages VALUES('epoch',7)`, 1},
		{`UPDATE content_rating_ages SET minimum_age=7 WHERE value_key='epoch'`, 0},
		{`INSERT INTO content_rating_ages VALUES('epoch',7) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`, 0},
		{`UPDATE content_rating_ages SET value_key='epoch2' WHERE value_key='epoch'`, 1},
		{`UPDATE content_rating_ages SET minimum_age=8 WHERE value_key='epoch2'`, 1},
		{`DELETE FROM content_rating_ages WHERE value_key='epoch2'`, 1},
		{`DELETE FROM content_rating_ages WHERE value_key='missing'`, 0},
		{`UPDATE catalog_generations SET generation=generation,retaining=retaining`, 0},
		{`UPDATE catalog_generations SET retaining=1`, 1},
		{`UPDATE catalog_generations SET retaining=0,generation=generation+1`, 1},
	} {
		if _, err := db.Exec(step.query); err != nil {
			t.Fatal(err)
		}
		after := epoch()
		if after != before+step.delta {
			t.Fatal(step.query, before, after)
		}
		before = after
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO content_rating_ages VALUES('rollback-epoch',3)`); err != nil {
		t.Fatal(err)
	}
	var inside int64
	if err = tx.QueryRow(`SELECT revision FROM catalog_browse_predicate_epoch`).Scan(&inside); err != nil || inside != before+1 {
		t.Fatal(inside, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if epoch() != before {
		t.Fatal("rollback leaked epoch")
	}
	if _, err = db.Exec(`UPDATE catalog_browse_predicate_epoch SET revision=0`); err == nil {
		t.Fatal("invalid epoch accepted")
	}
}

// This census is deliberately independent of generator metadata. Omitting a
// source or key-moving/source-spelling guard must fail even if happy-path totals
// still agree. Trigger bodies must remain O(1) synchronous scalar writes.
func TestBrowsePredicateEpochProducerCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "census.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	census := map[string][]string{
		"catalog_browse_rows": {"entity_id", "library_id", "kind"}, "catalog_browse_rows_previous": {"generation", "present", "entity_id", "library_id"},
		"catalog_browse_memberships": {"entity_id", "item_id", "source"}, "catalog_browse_memberships_previous": {"generation", "present", "entity_id", "item_id", "source"},
		"catalog_entities": {"id", "public_id", "library_id", "kind", "retired"}, "catalog_libraries": {"id", "library_id", "retired"}, "libraries": {"id", "kind", "root"},
		"catalog_attribute_terms": {"id", "field_id", "value_key"}, "catalog_item_attribute_edges": {"item_id", "term_id", "source_value"}, "content_rating_ages": {"value_key", "minimum_age"},
		"dvr_catalog_provenance": {"item_id", "recording_id", "state"}, "dvr_catalog_retirements": {"item_id"},
		"catalog_generations": {"generation", "retaining"}, "catalog_derivations": {"domain", "version", "rebuilding", "active_slot", "cleaning", "first_build"},
		"reading_attribution_state": {"namespace", "library_id", "input_generation", "ready_generation", "publication_rule_version"}, "reading_attribution_members": {"library_id", "generation", "item_id", "author_key", "series_key"},
		"reading_publications": {"item_id", "asset_id", "format"}, "catalog_asset_links": {"entity_id", "asset_id"}, "catalog_assets": {"id", "token", "path", "available", "size", "modified_ns"},
		"inventory_objects": {"id", "asset_id", "source_id", "retired", "state", "unsupported_reason", "root_incarnation", "size", "modified_ns"}, "library_sources": {"id", "library_id", "kind", "incarnation", "root", "configured_root", "enabled"},
		"compact_visibility_classes": {"id", "class_key", "active_generation", "retired"}, "compact_visibility_rows": {"class_id", "generation", "entity_id"},
		"profile_restrictions": {"profile_id", "revision", "maximum_age", "allow_unrated", "blocked_labels", "allow_dvr"}, "direct_profiles": {"id", "account_id", "deleted", "revision"},
		"direct_memberships": {"account_id", "incarnation", "revision", "disabled", "allowed_libraries"}, "access_limits": {"account_id", "revision", "body"}, "policy": {"server_id", "revision", "payload", "expires_at"},
	}
	var sourceDDL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='browse_predicate_library_sources_update'`).Scan(&sourceDDL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sourceDDL, "(OLD.health IN('root_changed','removing')) IS NOT (NEW.health IN('root_changed','removing'))") {
		t.Fatal("missing structural health refusal guard", sourceDDL)
	}
	if strings.Contains(sourceDDL, "OLD.health IS NOT NEW.health") {
		t.Fatal("transient health flaps must not invalidate catalogue predicates", sourceDDL)
	}
	for table, cols := range census {
		for _, op := range []string{"insert", "update", "delete"} {
			var ddl string
			if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=? AND tbl_name=?`, "browse_predicate_"+table+"_"+op, table).Scan(&ddl); err != nil {
				t.Fatal(table, op, err)
			}
			if !strings.Contains(ddl, "SET revision=revision+1 WHERE singleton=1") || strings.Contains(ddl, "SELECT ") {
				t.Fatal("unbounded or missing epoch write", ddl)
			}
			if op == "update" {
				for _, col := range cols {
					if !strings.Contains(ddl, "OLD."+col+" IS NOT NEW."+col) {
						t.Fatal("missing noop/key guard", table, col)
					}
				}
			}
		}
	}
}
