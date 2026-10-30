package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func facetRestrictedFixture(t *testing.T, shows bool) (*sql.DB, FacetBaseScope) {
	t.Helper()
	db, scope := facetAcceptedFixture(t)
	scope.Source = RestrictedMemberFacetBaseSource
	_, err := db.Exec(`CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL);
 CREATE TABLE catalog_browse_rows(entity_id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL,kind INTEGER NOT NULL,item_id INTEGER,year INTEGER);
 CREATE INDEX browse_library_kind ON catalog_browse_rows(library_id,kind);
 CREATE TABLE catalog_attribute_terms(id INTEGER PRIMARY KEY,field_id INTEGER NOT NULL,value_key TEXT NOT NULL);
 CREATE TABLE catalog_item_attribute_edges(item_id INTEGER NOT NULL,term_id INTEGER NOT NULL,source_value TEXT NOT NULL,PRIMARY KEY(item_id,term_id)) WITHOUT ROWID;
 CREATE TABLE content_rating_ages(value_key TEXT PRIMARY KEY,minimum_age INTEGER NOT NULL) WITHOUT ROWID;
 CREATE TABLE catalog_facet_members(entity_id INTEGER,field TEXT,value TEXT,library_id INTEGER,PRIMARY KEY(entity_id,field,value)) WITHOUT ROWID;
 CREATE TABLE catalog_show_facet_episodes(episode_id INTEGER,show_id INTEGER,field TEXT,value TEXT,PRIMARY KEY(episode_id,field,value)) WITHOUT ROWID;
 CREATE TABLE catalog_terms(id INTEGER PRIMARY KEY,vocab INTEGER NOT NULL,label TEXT NOT NULL);
 CREATE TABLE catalog_term_sources(entity_id INTEGER NOT NULL,provider TEXT NOT NULL,source_id TEXT NOT NULL,term_id INTEGER NOT NULL,label_override TEXT,PRIMARY KEY(entity_id,provider,source_id)) WITHOUT ROWID;
 CREATE TABLE catalog_episodes(entity_id INTEGER PRIMARY KEY,show_id INTEGER,season_id INTEGER,number INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX catalog_episodes_show ON catalog_episodes(show_id,season_id,number,entity_id);
 CREATE TABLE compact_visibility_classes(id INTEGER PRIMARY KEY,class_key TEXT,library_id INTEGER,retired INTEGER,active_generation INTEGER,captured_sequence INTEGER NOT NULL DEFAULT 0);
 INSERT INTO content_rating_ages VALUES('pg',8),('r',18);
 INSERT INTO catalog_attribute_terms VALUES(1,1,'pg'),(2,1,'r'),(3,2,'x|y'),(4,3,'x');`)
	if err != nil {
		t.Fatal(err)
	}
	if shows {
		if _, err = db.Exec(facetBaselineBlock("current-show contribution staging")); err != nil {
			t.Fatal(err)
		}
		scope.Source = RestrictedShowFacetBaseSource
		if _, err = db.Exec(`UPDATE libraries SET kind='tv'; UPDATE catalog_libraries SET kind=2`); err != nil {
			t.Fatal(err)
		}
	}
	return db, scope
}
func facetRestrictedBuild(t *testing.T, db *sql.DB, scope FacetBaseScope) int64 {
	t.Helper()
	ctx := context.Background()
	b := &FacetBases{DB: db, Producer: RestrictedFacetBaseProducer{}, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		w, err := CaptureRestrictedFacetBaseWitness(ctx, tx, scope)
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
	t.Fatal("restricted source did not complete")
	return 0
}
func TestFacetBaseSourceRestrictedMembersExactLabelsAndPolicy(t *testing.T) {
	db, scope := facetRestrictedFixture(t, false)
	_, err := db.Exec(`INSERT INTO catalog_entities VALUES(1,1),(2,1);
 INSERT INTO catalog_browse_rows VALUES(1,1,1,1,2001),(2,1,1,2,2002);
 INSERT INTO catalog_item_attribute_edges VALUES(1,1,'PG'),(1,3,'X|Y'),(1,4,' x '),(2,2,'R'),(2,4,'x');
 INSERT INTO catalog_facet_members VALUES(1,'studio','Acme',1),(1,'studio','Zulu',1),(2,'studio','Acme',1);`)
	if err != nil {
		t.Fatal(err)
	}
	eight := 8
	for _, tc := range []struct {
		r      identity.ContentRestrictions
		values []string
		counts []int
	}{
		{identity.ContentRestrictions{MaximumAge: &eight}, []string{"Acme", "Zulu"}, []int{1, 1}},
		{identity.ContentRestrictions{MemberDeniedLabels: []string{" x "}}, []string{"Acme"}, []int{1}},
		{identity.ContentRestrictions{BlockedLabels: []string{"x|y"}}, []string{"Acme"}, []int{1}},
	} {
		scope.Policy = FacetBasePolicy(tc.r)
		facetRestrictedBuild(t, db, scope)
		w, _ := facetWindowFixture(t, db, nil)
		p := facetWindowInitial(t, w, scope, "", 10)
		if !reflect.DeepEqual(facetWindowValues(p), tc.values) {
			t.Fatal("policy source values", facetWindowValues(p), tc.values)
		}
		for i, f := range p.Values {
			if f.Count != tc.counts[i] {
				t.Fatal("policy source count", p.Values)
			}
		}
	}
	db.Exec(`INSERT INTO catalog_browse_rows VALUES(3,1,4,3,2002)`)
	if _, err = CaptureRestrictedFacetBaseWitness(context.Background(), db, scope); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal("mixed episode source silently dropped", err)
	}
}
func TestFacetBaseSourceRestrictedShowsVisibleWitnessAndYear(t *testing.T) {
	db, scope := facetRestrictedFixture(t, true)
	_, err := db.Exec(`INSERT INTO catalog_entities VALUES(10,1),(11,1),(12,1),(101,1),(102,1),(103,1),(104,1);
 INSERT INTO catalog_browse_rows VALUES(10,1,2,NULL,2002),(11,1,2,NULL,2005),(12,1,2,NULL,2009),(101,1,4,101,2002),(102,1,4,102,2003),(103,1,4,103,2009),(104,1,4,104,2004);
 INSERT INTO catalog_episodes(entity_id,show_id) VALUES(101,10),(102,10),(103,12),(104,10);
 UPDATE catalog_episodes SET season_id=1 WHERE entity_id=104;
 INSERT INTO catalog_item_attribute_edges VALUES(101,1,'PG'),(102,2,'R'),(103,2,'R'),(104,1,'PG');
 INSERT INTO catalog_terms VALUES(1,1,'Drama'),(2,1,'Hidden only'),(3,1,'Episode only');
 INSERT INTO catalog_term_sources VALUES(10,'p','drama',1,NULL),(101,'p','drama',1,NULL),(104,'p','drama',1,NULL),(102,'p','hidden',2,NULL),(101,'p','own',3,NULL),(12,'p','drama',1,NULL);
 INSERT INTO catalog_show_facet_episodes VALUES(101,10,'studio','Acme'),(102,10,'studio','Hidden Studio');`)
	if err != nil {
		t.Fatal(err)
	}
	eight := 8
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &eight})
	for _, tc := range []struct {
		field  string
		values []string
	}{
		{"genre", []string{"Drama", "Episode only"}}, {"studio", []string{"Acme"}}, {"year", []string{"2002"}}, {"decade", []string{"2000"}},
	} {
		scope.Field = tc.field
		facetRestrictedBuild(t, db, scope)
		w, _ := facetWindowFixture(t, db, nil)
		p := facetWindowInitial(t, w, scope, "", 10)
		if !reflect.DeepEqual(facetWindowValues(p), tc.values) {
			t.Fatal(tc.field, facetWindowValues(p), tc.values)
		}
		for _, f := range p.Values {
			if f.Count != 1 {
				t.Fatal("counted episodes/hidden/empty show", p.Values)
			}
		}
	}
	// Published decade's linked-episode class is a separate oracle. Presence
	// cannot silently fall back to our live no-asset-link show semantics.
	key, _ := visibilityClassKey(scope.Library, identity.ContentRestrictions{MaximumAge: &eight})
	db.Exec(`INSERT INTO compact_visibility_classes(id,class_key,library_id,retired,active_generation) VALUES(1,?,1,0,1)`, key)
	if _, err = CaptureRestrictedFacetBaseWitness(context.Background(), db, scope); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal("published decade incorrectly unified", err)
	}
}

func TestFacetBaseSourcePlanSelectsPublicLibraryKindsAndDecadePresence(t *testing.T) {
	db, scope := facetRestrictedFixture(t, false)
	ctx := context.Background()
	age := 13
	r := identity.ContentRestrictions{MaximumAge: &age}
	for _, tc := range []struct {
		public   string
		internal int
		source   string
		reading  bool
	}{
		{"movie", 1, RestrictedMemberFacetBaseSource, false}, {"tv", 2, RestrictedShowFacetBaseSource, false}, {"anime", 2, RestrictedShowFacetBaseSource, false}, {"music", 5, RestrictedMemberFacetBaseSource, false}, {"book", 20, "", true},
	} {
		if _, err := db.Exec(`UPDATE libraries SET kind=?;`, tc.public); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE catalog_libraries SET kind=?`, tc.internal); err != nil {
			t.Fatal(err)
		}
		for _, policy := range []identity.ContentRestrictions{{}, r} {
			plan, err := CaptureSimpleFacetBasePlan(ctx, db, scope.LibraryID, scope.Library, "genre", policy)
			if tc.reading {
				if !errors.Is(err, ErrFacetBaseUnavailable) {
					t.Fatal("Reading public kind accepted", tc.public, err)
				}
				continue
			}
			want := tc.source
			if !policy.Active() {
				want = UnrestrictedFacetBaseSource
			}
			if err != nil || plan.Scope.Source != want {
				t.Fatal(tc.public, plan.Scope.Source, want, err)
			}
		}
		if tc.reading {
			continue
		}
		if _, err := db.Exec(`DELETE FROM compact_visibility_classes`); err != nil {
			t.Fatal(err)
		}
		plan, err := CaptureSimpleFacetBasePlan(ctx, db, scope.LibraryID, scope.Library, "decade", r)
		if err != nil || plan.Scope.Source != tc.source {
			t.Fatal("unpublished decade", tc.public, plan.Scope.Source, err)
		}
		key, _ := visibilityClassKey(scope.Library, r)
		if _, err = db.Exec(`INSERT INTO compact_visibility_classes(id,class_key,library_id,retired,active_generation) VALUES(1,?,1,0,1)`, key); err != nil {
			t.Fatal(err)
		}
		plan, err = CaptureSimpleFacetBasePlan(ctx, db, scope.LibraryID, scope.Library, "decade", r)
		if err != nil || plan.Scope.Source != LinkedDecadeFacetBaseSource {
			t.Fatal("published decade", tc.public, plan.Scope.Source, err)
		}
	}
}
