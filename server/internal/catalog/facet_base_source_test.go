package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestFacetBaseSourceItemPolicyMatchesAuthoritativePredicate(t *testing.T) {
	db, _ := facetBaseFixture(t)
	_, err := db.Exec(`CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,retired INTEGER NOT NULL);
 CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL);
 CREATE TABLE catalog_attribute_terms(id INTEGER PRIMARY KEY,field_id INTEGER NOT NULL,value_key TEXT NOT NULL);
 CREATE TABLE catalog_item_attribute_edges(item_id INTEGER NOT NULL,term_id INTEGER NOT NULL,source_value TEXT NOT NULL,PRIMARY KEY(item_id,term_id)) WITHOUT ROWID;
 CREATE TABLE content_rating_ages(value_key TEXT PRIMARY KEY,minimum_age INTEGER NOT NULL) WITHOUT ROWID;
 INSERT INTO catalog_libraries VALUES(1,0),(2,1);
 INSERT INTO catalog_entities VALUES(1,1),(2,1),(3,2);
 INSERT INTO content_rating_ages VALUES('pg',8),('r',18),('nr',-1);
 INSERT INTO catalog_attribute_terms VALUES(1,1,'pg'),(2,2,'x|y'),(3,3,'tag'),(4,3,'unicode'),(5,3,'empty'),(6,1,'r'),(7,1,'nr'),(8,4,'irrelevant');
 INSERT INTO catalog_item_attribute_edges VALUES(1,1,'PG'),(1,2,' X|Y '),(1,3,' X '),(1,4,'Ä'),(1,5,''),(1,6,'R'),(2,7,'NR'),(3,8,'v');`)
	if err != nil {
		t.Fatal(err)
	}
	zero, eight, eighteen := 0, 8, 18
	policies := []identity.ContentRestrictions{
		{}, {MaximumAge: &zero}, {MaximumAge: &eight}, {MaximumAge: &eighteen}, {BlockUnrated: true},
		{BlockedLabels: []string{"x|y"}}, {BlockedLabels: []string{"x", "y"}},
		{MemberDeniedLabels: []string{" x "}}, {MemberDeniedLabels: []string{"x"}},
		{MemberDeniedLabels: []string{"Ä"}}, {MemberDeniedLabels: []string{"ä"}},
		{MemberDeniedLabels: []string{""}},
	}
	for _, r := range policies {
		for _, item := range []int64{1, 2, 3, 99} {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			clause, args := ItemRestrictionSQL("ci_test.item_id", r)
			var want bool
			if err = tx.QueryRow(`SELECT `+clause+` FROM (SELECT ? AS item_id) ci_test`, append(args, item)...).Scan(&want); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			c := FacetBaseItemPolicyCursor{Item: item}
			var done, got bool
			for n := 0; n < 10 && !done; n++ {
				done, got, err = StepFacetBaseItemPolicy(context.Background(), tx, r, &c)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
			}
			tx.Rollback()
			if !done || got != want {
				t.Fatalf("item=%d policy=%+v got=%v done=%v want=%v", item, r, got, done, want)
			}
		}
	}
}
func TestFacetBaseSourcePolicyConsumesBoundedRawEdges(t *testing.T) {
	db, _ := facetBaseFixture(t)
	_, err := db.Exec(`CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,retired INTEGER NOT NULL);
 CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL);
 CREATE TABLE catalog_attribute_terms(id INTEGER PRIMARY KEY,field_id INTEGER NOT NULL,value_key TEXT NOT NULL);
 CREATE TABLE catalog_item_attribute_edges(item_id INTEGER NOT NULL,term_id INTEGER NOT NULL,source_value TEXT NOT NULL,PRIMARY KEY(item_id,term_id)) WITHOUT ROWID;
 CREATE TABLE content_rating_ages(value_key TEXT PRIMARY KEY,minimum_age INTEGER NOT NULL) WITHOUT ROWID;
 INSERT INTO catalog_libraries VALUES(1,0);INSERT INTO catalog_entities VALUES(1,1);
 WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<9) INSERT INTO catalog_attribute_terms SELECT x,CASE WHEN x=9 THEN 3 ELSE 4 END,'v' FROM n;
 INSERT INTO catalog_item_attribute_edges SELECT 1,id,CASE WHEN id=9 THEN 'deny' ELSE 'v' END FROM catalog_attribute_terms;`)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	c := FacetBaseItemPolicyCursor{Item: 1}
	r := identity.ContentRestrictions{MemberDeniedLabels: []string{"deny"}}
	done, visible, err := StepFacetBaseItemPolicy(context.Background(), tx, r, &c)
	if err != nil || done || visible || c.AfterTerm != 8 {
		t.Fatal(done, visible, c, err)
	}
	done, visible, err = StepFacetBaseItemPolicy(context.Background(), tx, r, &c)
	if err != nil || !done || visible || c.AfterTerm != 9 {
		t.Fatal(done, visible, c, err)
	}
}
