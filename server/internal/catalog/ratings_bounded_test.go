package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRatingReclassificationResumesBeforePublishingVersion(t *testing.T) {
	db, _, _ := tl10RestrictionFixture(t)
	for i := 0; i < 25; i++ {
		if _, err := db.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES(?,1)`, fmt.Sprintf("unknown-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE configuration SET value='old' WHERE key=?`, ratingTableKey); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	more, err := s.ClassifyPendingRatings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !more || persistence.Get(db, ratingTableKey) != "old" {
		t.Fatal("table version published before its spelling walk finished")
	}
	for turns := 0; more && turns < 10; turns++ {
		more, err = s.ClassifyPendingRatings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	if more || persistence.Get(db, ratingTableKey) != identity.RatingTableVersion {
		t.Fatal("table walk did not finish")
	}
	var wrong int
	if err := db.QueryRow(`SELECT count(*) FROM content_rating_ages WHERE value_key LIKE 'unknown-%' AND minimum_age<>-1`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Fatalf("%d obsolete classifications remain", wrong)
	}
}

func TestRatingPolicyImmediatelyFencesCachedRevisions(t *testing.T) {
	db, s, _ := tl10RestrictionFixture(t)
	revisions := func() []ContentRevision {
		t.Helper()
		a, e := s.ContentRevision("a", "")
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.browseContentRevision("a", "")
		if e != nil {
			t.Fatal(e)
		}
		c, e := s.HomeRevision([]string{"a"}, "")
		if e != nil {
			t.Fatal(e)
		}
		d, e := s.homePagingRevision([]string{"a"}, "")
		if e != nil {
			t.Fatal(e)
		}
		eRev, _, e := searchRevision(context.Background(), db, SearchRequest{}, `["a"]`)
		if e != nil {
			t.Fatal(e)
		}
		return []ContentRevision{a, b, c, d, eRev}
	}
	before := revisions()
	var rawBefore, rawAfter int64
	if err := db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='a'`).Scan(&rawBefore); err != nil {
		t.Fatal(err)
	}
	// No worker is stepped: policy changes must fence delivery immediately,
	// while the existing source queue fans visibility out in bounded pages.
	if _, err := db.Exec(`UPDATE content_rating_ages SET minimum_age=0 WHERE value_key='r'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='a'`).Scan(&rawAfter); err != nil {
		t.Fatal(err)
	}
	if rawAfter != rawBefore {
		t.Fatal("rating publication still walks matching libraries")
	}
	for i, after := range revisions() {
		if after == before[i] {
			t.Fatalf("revision surface %d missed the policy change", i)
		}
	}
	var queued bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM compact_visibility_source_jobs WHERE kind=4 AND subject_text='r')`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("rating publication did not queue bounded visibility fanout")
	}
}
