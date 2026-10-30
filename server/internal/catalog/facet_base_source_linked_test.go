package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func facetLinkedFixture(t *testing.T, shows bool) (*sql.DB, FacetBaseScope) {
	t.Helper()
	db, scope := facetRestrictedFixture(t, shows)
	scope.Source = LinkedDecadeFacetBaseSource
	scope.Field = "decade"
	_, err := db.Exec(`ALTER TABLE catalog_entities ADD COLUMN retired INTEGER NOT NULL DEFAULT 0;
 CREATE TABLE catalog_shows(entity_id INTEGER PRIMARY KEY);
 CREATE TABLE catalog_seasons(entity_id INTEGER PRIMARY KEY);
 CREATE TABLE catalog_browse_memberships(entity_id INTEGER,item_id INTEGER,source TEXT,PRIMARY KEY(entity_id,item_id,source)) WITHOUT ROWID;
 CREATE TABLE catalog_asset_links(entity_id INTEGER,asset_id INTEGER,PRIMARY KEY(entity_id,asset_id)) WITHOUT ROWID;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(facetBaseSourceIntegrationDDL); err != nil {
		t.Fatal(err)
	}
	return db, scope
}
func facetLinkedBuild(t *testing.T, db *sql.DB, scope FacetBaseScope) int64 {
	t.Helper()
	ctx := context.Background()
	b := &FacetBases{DB: db, Producer: LinkedDecadeFacetBaseProducer{}, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureLinkedDecadeFacetBaseWitness(ctx, tx, scope)
		if err != nil {
			return err
		}
		id, err = b.Begin(ctx, tx, scope, w, "authority")
		return err
	})
	for n := 0; n < 200; n++ {
		done := false
		facetBaseTx(t, db, func(tx *sql.Tx) error { var err error; done, err = b.Step(ctx, tx, id); return err })
		if done {
			return id
		}
	}
	t.Fatal("linked source did not complete")
	return 0
}
func TestFacetBaseSourceLinkedDecadeMatchesVisibilityClassPredicate(t *testing.T) {
	db, scope := facetLinkedFixture(t, true)
	_, err := db.Exec(`INSERT INTO catalog_entities(id,library_id) VALUES(10,1),(11,1),(12,1),(13,1),(101,1),(102,1),(103,1),(104,1);
 INSERT INTO catalog_browse_rows VALUES(10,1,2,NULL,2002),(11,1,2,NULL,2005),(12,1,2,NULL,2009),(13,1,2,NULL,2001),(101,1,4,101,2002),(102,1,4,102,2003),(103,1,4,103,2009),(104,1,4,104,2004);
 INSERT INTO catalog_shows VALUES(10),(11),(12),(13);
 INSERT INTO catalog_browse_memberships VALUES(10,101,'episode'),(10,101,'manual'),(10,102,'episode'),(12,103,'episode'),(13,104,'episode');
 INSERT INTO catalog_asset_links VALUES(101,1),(102,2),(103,3);
 INSERT INTO catalog_item_attribute_edges VALUES(101,1,'PG'),(102,2,'R'),(103,2,'R'),(104,1,'PG');`)
	if err != nil {
		t.Fatal(err)
	}
	eight := 8
	r := identity.ContentRestrictions{MaximumAge: &eight}
	scope.Policy = FacetBasePolicy(r)
	key, _ := visibilityClassKey(scope.Library, r)
	if _, err = db.Exec(`INSERT INTO compact_visibility_classes(id,class_key,library_id,retired,active_generation) VALUES(1,?,1,0,1)`, key); err != nil {
		t.Fatal(err)
	}
	facetLinkedBuild(t, db, scope)
	w, _ := facetWindowFixture(t, db, nil)
	p := facetWindowInitial(t, w, scope, "", 10)
	clause, args := visibilityMembershipSQL("e.entity_id", r)
	var want int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.library_id=1 AND e.kind=2 AND e.year BETWEEN 1800 AND 2199 AND ce.retired=0 AND `+clause, args...).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 || len(p.Values) != 1 || p.Values[0].Value != "2000" || p.Values[0].Count != want || want != 1 {
		t.Fatal("changed linked/hidden/empty/duplicate-member oracle", p, want)
	}
}
func TestFacetBaseSourceLinkedNonshowUsesExactClassKindSet(t *testing.T) {
	db, scope := facetLinkedFixture(t, false)
	_, err := db.Exec(`INSERT INTO catalog_entities(id,library_id) VALUES(1,1),(2,1),(3,1),(4,1),(5,1),(6,1);
 INSERT INTO catalog_browse_rows VALUES(1,1,1,1,2000),(2,1,4,2,2001),(3,1,7,3,2002),(4,1,9,4,2003),(5,1,11,5,2004),(6,1,8,6,2005);
 INSERT INTO catalog_browse_memberships SELECT entity_id,entity_id,'self' FROM catalog_browse_rows;
 INSERT INTO catalog_item_attribute_edges SELECT entity_id,1,'PG' FROM catalog_browse_rows;`)
	if err != nil {
		t.Fatal(err)
	}
	eight := 8
	r := identity.ContentRestrictions{MaximumAge: &eight}
	scope.Policy = FacetBasePolicy(r)
	key, _ := visibilityClassKey(scope.Library, r)
	db.Exec(`INSERT INTO compact_visibility_classes(id,class_key,library_id,retired,active_generation) VALUES(1,?,1,0,1)`, key)
	facetLinkedBuild(t, db, scope)
	w, _ := facetWindowFixture(t, db, nil)
	p := facetWindowInitial(t, w, scope, "", 10)
	if p.Total != 1 || p.Values[0].Count != 5 {
		t.Fatal("unified nonshow decade with ordinary facet holders", p)
	}
}

func TestFacetBaseSourceLinkedDecadeRequiresCapturedSourceFrontier(t *testing.T) {
	db, scope := facetLinkedFixture(t, false)
	age := 13
	restrictions := identity.ContentRestrictions{MaximumAge: &age}
	scope.Policy = FacetBasePolicy(restrictions)
	key, _ := visibilityClassKey(scope.Library, restrictions)
	if _, err := db.Exec(`UPDATE compact_visibility_source_state SET sequence=5; INSERT INTO compact_visibility_classes VALUES(1,?,1,0,1,4)`, key); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := CaptureLinkedDecadeFacetBaseWitness(ctx, db, scope); !errors.Is(err, compactcatalog.ErrBuilding) {
		t.Fatal("stale published class admitted", err)
	}
	if _, err := db.Exec(`UPDATE compact_visibility_classes SET captured_sequence=5`); err != nil {
		t.Fatal(err)
	}
	witness, err := CaptureLinkedDecadeFacetBaseWitness(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE compact_visibility_classes SET captured_sequence=4`); err != nil {
		t.Fatal(err)
	}
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		err := (LinkedDecadeFacetBaseProducer{}).ValidatePublication(ctx, tx, FacetBaseSnapshot{Scope: scope, Witness: witness})
		if !errors.Is(err, compactcatalog.ErrBuilding) {
			t.Fatal("source witness not rechecked at publication", err)
		}
		return nil
	})
}
