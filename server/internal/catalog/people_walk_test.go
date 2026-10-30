package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A broad name search keeps relevance and prefix boosts on every page, with
// counts read from the maintained per-person credit counts and a lower-bound total.
func TestBroadPeopleSearchKeepsRelevanceOrder(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(lib, "/m/a.mkv", "Film", 2000)
	c.Drain()
	c.Exec(`INSERT INTO catalog_credit_labels(label) VALUES('Actor'),('Acting') ON CONFLICT DO NOTHING`)
	c.Exec(`WITH RECURSIVE n(i) AS(SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<1199)
	 INSERT INTO catalog_people(token,identity_key,name,sort_name) SELECT printf('tok-%04d',i),printf('fixture:%d',i),CASE WHEN i%2=0 THEN printf('Zed Person %04d',i) ELSE printf('Person %04d',i) END,CASE WHEN i%2=0 THEN printf('zed person %04d',i) ELSE printf('person %04d',i) END FROM n`)
	c.Exec(`INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal)
	 SELECT ?,p.id,p.id,(SELECT id FROM catalog_credit_labels WHERE label='Actor'),(SELECT id FROM catalog_credit_labels WHERE label='Acting'),p.name,'fixture',p.token,0 FROM catalog_people p`, film.ID)
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	page, err := s.People(viewer, "", "Person", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.People) != 20 {
		t.Fatalf("%d people", len(page.People))
	}
	for i, person := range page.People {
		if want := fmt.Sprintf("Person %04d", 2*i+1); person.Name != want || person.CreditCount != 1 || len(person.Roles) != 1 {
			t.Fatalf("%d: %+v, want %s with one credit", i, person, want)
		}
	}
	out, err := s.Search(context.Background(), SearchRequest{Viewer: viewer, Q: "Person", Group: "people", Limit: 12})
	if err != nil || len(out.Groups) != 1 || len(out.Groups[0].Items) != 12 || out.Groups[0].TotalCountExact || !out.Groups[0].HasMore {
		t.Fatalf("search people: %+v %v", out.Groups, err)
	}
	next, err := s.Search(context.Background(), SearchRequest{Viewer: viewer, Q: "Person", Group: "people", Limit: 12, Cursor: out.Groups[0].NextCursor})
	if err != nil || len(next.Groups[0].Items) != 12 || next.Groups[0].Items[0].Title != "Person 0025" {
		t.Fatalf("next people: %+v %v", next.Groups, err)
	}
	// Two distinct prefix-matching words raise relevance, even though this
	// name sorts after all of the other prefix matches. SearchDocument removes
	// duplicate words, so repeating "Person" would not exercise BM25 ordering.
	c.Exec(`UPDATE catalog_people SET name='Person Zzz Personhood',sort_name='person zzz personhood' WHERE token='tok-0000'`)
	page, err = s.People(viewer, "", "Person", 20)
	if err != nil || len(page.People) != 20 || page.People[0].ID != "tok-0000" {
		t.Fatalf("broad relevance: %+v %v", page.People, err)
	}
	out, err = s.Search(context.Background(), SearchRequest{Viewer: viewer, Q: "Person", Group: "people", Limit: 12})
	if err != nil || len(out.Groups) != 1 || len(out.Groups[0].Items) != 12 || out.Groups[0].Items[0].ID != "tok-0000" {
		t.Fatalf("broad search relevance: %+v %v", out.Groups, err)
	}
}

func TestPeopleCursorKeepsOrderingAndAcceptsOffsetsBeyond100000(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Drain()
	s := New(c.DB)
	r := SearchRequest{Profile: "p", ViewerFence: "f", Libraries: []string{"m"}, Q: "Person", Sort: "relevance", Direction: "desc", Limit: 40}
	g := searchGroupDefinition{id: "people", kind: "person", view: "person", source: "people", sorts: nameSorts}
	scope := s.groupCursorScope(r, g, "scope", "relevance")
	_, match, err := NormalizeSearch(r.Q)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		ordering string
		offset   int
		wantErr  bool
	}{
		{"people:relevance", 100000, false},
		{"people:relevance", 100040, false},
		{"people:relevance", -1, true},
		{"people:title", 40, true},
		{"offset", 40, true},
	} {
		r.Cursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: test.ordering, ID: strconv.Itoa(test.offset), Expires: time.Now().Add(time.Minute).Unix()}, ContentRevision{})
		if err != nil {
			t.Fatal(err)
		}
		out := SearchGroupResult{}
		err = s.searchPeopleGroup(context.Background(), r, g, match, "scope", "", ContentRevision{}, 40, &out)
		if test.wantErr {
			if !errors.Is(err, ErrCursor) {
				t.Fatalf("%s/%d: expected cursor refusal, got %v", test.ordering, test.offset, err)
			}
		} else if err != nil || len(out.Items) != 0 || out.HasMore || out.NextCursor != "" {
			t.Fatalf("%s/%d: %+v, %v", test.ordering, test.offset, out, err)
		}
	}
}
