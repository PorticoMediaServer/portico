package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func facetAcceptedFixture(t *testing.T) (*sql.DB, FacetBaseScope) {
	t.Helper()
	db, scope := facetBaseFixture(t)
	scope.Source = UnrestrictedFacetBaseSource
	_, err := db.Exec(`CREATE TABLE libraries(id TEXT PRIMARY KEY,kind TEXT NOT NULL);
 INSERT INTO libraries VALUES('films','movie');
 CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,library_id TEXT NOT NULL,kind INTEGER NOT NULL,retired INTEGER NOT NULL);
 INSERT INTO catalog_libraries VALUES(1,'films',1,0);
 CREATE TABLE catalog_facet_source_state(singleton INTEGER PRIMARY KEY,source_epoch INTEGER,projection_epoch INTEGER);
 INSERT INTO catalog_facet_source_state VALUES(1,1,1);
 CREATE TABLE catalog_generations(singleton INTEGER PRIMARY KEY,generation INTEGER,retaining INTEGER,visibility_sequence INTEGER NOT NULL DEFAULT 0);
 INSERT INTO catalog_generations VALUES(1,1,0,0);
 CREATE TABLE catalog_derivations(domain INTEGER PRIMARY KEY,version INTEGER,rebuilding INTEGER);
 INSERT INTO catalog_derivations VALUES(33,7,0),(36,1,0),(37,1,0),(38,1,0),(39,1,0),(40,1,0);
 CREATE TABLE catalog_dirty(domain INTEGER,entity_id INTEGER);
 CREATE TABLE catalog_derivation_failures(domain INTEGER,entity_id INTEGER);
 CREATE TABLE catalog_facet_source_fanouts(domain INTEGER,source_id INTEGER);
 CREATE TABLE catalog_upstream_sources(domain INTEGER,source_id INTEGER);
 CREATE TABLE catalog_facet_work(entity_id INTEGER PRIMARY KEY);
 CREATE TABLE catalog_facet_stage(entity_id INTEGER);
 CREATE TABLE catalog_facet_work_places(entity_id INTEGER);
 CREATE TABLE compact_visibility_source_state(id INTEGER PRIMARY KEY,sequence INTEGER NOT NULL);
 INSERT INTO compact_visibility_source_state VALUES(1,0);
 CREATE TABLE compact_visibility_source_jobs(first_pending INTEGER NOT NULL);
 CREATE INDEX compact_visibility_source_jobs_frontier ON compact_visibility_source_jobs(first_pending);
 CREATE TABLE catalog_browse_row_work(domain INTEGER PRIMARY KEY);
 CREATE TABLE catalog_browse_row_keys(domain INTEGER,field INTEGER,value_key TEXT,PRIMARY KEY(domain,field,value_key)) WITHOUT ROWID;
 CREATE TABLE catalog_item_metric_jobs(item_id INTEGER PRIMARY KEY);
 CREATE TABLE catalog_browse_counter_repairs(entity_id INTEGER PRIMARY KEY);
 CREATE VIEW catalog_facet_counts_serving AS SELECT * FROM catalog_facet_counts;
 CREATE TABLE catalog_facet_counts(library_id INTEGER,field TEXT,value TEXT,rating_key TEXT,label_key TEXT,total INTEGER,PRIMARY KEY(library_id,field,value,rating_key,label_key)) WITHOUT ROWID;`)
	if err != nil {
		t.Fatal(err)
	}
	return db, scope
}
func TestFacetBaseSourceUnrestrictedExactReducerAndPublication(t *testing.T) {
	db, scope := facetAcceptedFixture(t)
	for i := 0; i < 17; i++ {
		if _, err := db.Exec(`INSERT INTO catalog_facet_counts VALUES(1,'studio','Acme',?,'literal|bucket',1)`, fmt.Sprintf("rating%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`INSERT INTO catalog_facet_counts VALUES(1,'studio','Beta','','',3)`)
	ctx := context.Background()
	b := &FacetBases{DB: db, Producer: UnrestrictedFacetBaseProducer{}, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureUnrestrictedFacetBaseWitness(ctx, tx, scope)
		if err != nil {
			return err
		}
		id, err = b.Begin(ctx, tx, scope, w, "authority")
		return err
	})
	steps := 0
	for {
		done := false
		facetBaseTx(t, db, func(tx *sql.Tx) error { var err error; done, err = b.Step(ctx, tx, id); return err })
		steps++
		if done {
			break
		}
		var active int64
		if err := db.QueryRow(`SELECT COALESCE(active_base,0) FROM catalog_facet_base_classes`).Scan(&active); err != nil || active != 0 {
			t.Fatal("partial image activated", active, err)
		}
		if steps > 8 {
			t.Fatal("unbounded source progress")
		}
	}
	if steps < 3 {
		t.Fatal("outer LIMIT hid unbounded bucket aggregation", steps)
	}
	w, _ := facetWindowFixture(t, db, nil)
	p := facetWindowInitial(t, w, scope, "", 10)
	if p.Total != 2 || len(p.Values) != 2 || p.Values[0].Value != "Acme" || p.Values[0].Count != 17 || p.Values[1].Count != 3 {
		t.Fatal(p)
	}
	expected, err := New(db).facetsFromCounts(FacetRequest{Library: "films", Field: "studio"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Values, expected) {
		t.Fatal("changed unrestricted count/order semantics", p.Values, expected)
	}
}
func TestFacetBaseSourceUnrestrictedRejectsChangedOrRestrictedImage(t *testing.T) {
	db, scope := facetAcceptedFixture(t)
	ctx := context.Background()
	db.Exec(`INSERT INTO catalog_facet_counts VALUES(1,'studio','Acme','','',1)`)
	b := &FacetBases{DB: db, Producer: UnrestrictedFacetBaseProducer{}, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureUnrestrictedFacetBaseWitness(ctx, tx, scope)
		if err != nil {
			return err
		}
		id, err = b.Begin(ctx, tx, scope, w, "authority")
		return err
	})
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, err := b.Step(ctx, tx, id); return err })
	db.Exec(`UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1`)
	tx, _ := db.Begin()
	_, err := b.Step(ctx, tx, id)
	tx.Rollback()
	if !errors.Is(err, compactcatalog.ErrFacetProducerChanged) {
		t.Fatal("mixed source image accepted", err)
	}
	r := scope
	r.Policy = FacetBasePolicy(identity.ContentRestrictions{MemberDeniedLabels: []string{"tag"}})
	if _, err = CaptureUnrestrictedFacetBaseWitness(ctx, db, r); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal("restricted approximation", err)
	}
	db.Exec(`INSERT INTO catalog_facet_work VALUES(1)`)
	if _, err = CaptureUnrestrictedFacetBaseWitness(ctx, db, scope); !errors.Is(err, compactcatalog.ErrBuilding) {
		t.Fatal("staged source not fenced", err)
	}
}

func TestFacetBaseSourceUnrestrictedKeepsLongRawCursor(t *testing.T) {
	db, scope := facetAcceptedFixture(t)
	key := strings.Repeat("pipe|", 2000)
	if _, err := db.Exec(`INSERT INTO catalog_facet_counts VALUES(1,'studio','Acme',?,'',2)`, key); err != nil {
		t.Fatal(err)
	}
	b := &FacetBases{DB: db, Producer: UnrestrictedFacetBaseProducer{}, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureUnrestrictedFacetBaseWitness(context.Background(), tx, scope)
		if err != nil {
			return err
		}
		id, err = b.Begin(context.Background(), tx, scope, w, "authority")
		return err
	})
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, err := b.Step(context.Background(), tx, id); return err })
	var length int
	if err := db.QueryRow(`SELECT length(cursor) FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&length); err != nil || length < 10000 {
		t.Fatal("raw source key truncated", length, err)
	}
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		done, err := b.Step(context.Background(), tx, id)
		if !done && err == nil {
			t.Fatal("last value did not complete")
		}
		return err
	})
	w, _ := facetWindowFixture(t, db, nil)
	p := facetWindowInitial(t, w, scope, "", 10)
	if p.Total != 1 || p.Values[0].Count != 2 {
		t.Fatal(p)
	}
}
