package db_test

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"path/filepath"
	"testing"
)

func TestGenresDoNotSpendTheirCandidatesOnHiddenTitles(t *testing.T) {
	s := newShelf(t)
	for n := range 51 {
		it := film(fmt.Sprintf("Genre scope %02d", n))
		it.Tags = []db.TagName{{Kind: db.TagGenre, Name: "Scope drama"}}
		if n < 50 {
			it.Tags = append(it.Tags, db.TagName{Kind: db.TagLabel, Name: "Blocked genre"})
		}
		s.add(s.films, it)
	}
	tags, err := s.d.Tags(ctx, db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.d.Genres(ctx, v, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.Name == "Scope drama" && g.Count == 1 {
			return
		}
	}
	t.Errorf("visible 51st film's genre missing: %+v", got)
}

func TestSearchTotalsRecheckCurrentRatingBeforeFollowerCatchesUp(t *testing.T) {
	s := newShelf(t)
	it := film("Uniquevisibilityscope")
	it.RatingAge = new(6)
	s.add(s.films, it)
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", MaxRatingAge: new(13)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.d.Items(ctx, v, db.ItemQuery{Library: s.films.PublicID, Limit: 200})
	if err != nil {
		t.Fatal(page, err)
	}
	var itemID string
	for _, it := range page.Items {
		if it.Title == "Uniquevisibilityscope" {
			itemID = it.PublicID
		}
	}
	if itemID == "" {
		t.Fatal("no film")
	}
	// A write lock freezes only the disposable search follower; readers and main writes continue.
	index, err := sql.Open("sqlite", filepath.Join(s.dir, "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	conn, err := index.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	// RateShow performs the metadata rating write and appends the change, including for a movie.
	if err := s.d.RateShow(ctx, itemID, 18); err != nil {
		t.Fatal(err)
	}
	got, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Uniquevisibilityscope", Kind: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got.Groups {
		if g.Kind == "movie" && (g.Total != 0 || len(g.Items) != 0) {
			t.Errorf("stale count: total=%d items=%d", g.Total, len(g.Items))
		}
	}
}

func TestHiddenPeopleDoNotConsumeTheVisibleSearchBudget(t *testing.T) {
	defer db.SetSearchCheckCap(3)()
	s := newShelf(t)
	for n := range 55 {
		it := film(fmt.Sprintf("Secret film %d", n), fmt.Sprintf("Sample Hidden %02d", n))
		it.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Blocked"}}
		s.add(s.films, it)
	}
	s.add(s.films, film("Visible film", "Sample Visible"))
	tags, err := s.d.Tags(ctx, db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Sample", Kind: "person"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range result.Groups {
		if g.Kind == "person" {
			found = g.Total == 1 && len(g.People) == 1
			if !found {
				t.Errorf("people: total=%d, rows=%d, minimum=%v", g.Total, len(g.People), g.TotalIsMinimum)
			}
		}
	}
	if !found {
		t.Error("visible person absent")
	}
	suggestions, err := s.d.SearchSuggestions(ctx, v, "Sample", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 1 || suggestions[0].Title != "Sample Visible" {
		t.Errorf("suggestions: %+v", suggestions)
	}
}

func TestPeopleSortBudgetCountsVisibleMatches(t *testing.T) {
	defer db.SetSearchCheckCap(3)()
	s := newShelf(t)
	s.add(s.films, film("Visible first", "Sample First"))
	for n := range 5 {
		it := film(fmt.Sprintf("Secret film %d", n), fmt.Sprintf("Sample Hidden %d", n))
		it.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Blocked"}}
		s.add(s.films, it)
	}
	s.add(s.films, film("Visible last", "Sample Last"))
	tags, _ := s.d.Tags(ctx, db.TagLabel)
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Sample", Kind: "person", Sort: db.SortTitle})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range result.Groups {
		if g.Kind == "person" && (g.Total != 2 || len(g.People) != 2) {
			t.Errorf("sorted visible people: total=%d rows=%d", g.Total, len(g.People))
		}
	}
}

func TestPeopleVisibleBudgetStillMarksLargeCountsAndRefusesWideSorts(t *testing.T) {
	defer db.SetSearchCheckCap(3)()
	s := newShelf(t)
	hidden := film("Secret film", "Sample Hidden")
	hidden.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Blocked"}}
	s.add(s.films, hidden)
	for n := range 4 {
		s.add(s.films, film(fmt.Sprintf("Visible %d", n), fmt.Sprintf("Sample Visible %d", n)))
	}
	tags, _ := s.d.Tags(ctx, db.TagLabel)
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Sample", Kind: "person"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range result.Groups {
		if g.Kind == "person" && (g.Total != 3 || !g.TotalIsMinimum || len(g.People) != 4) {
			t.Errorf("visible cap: %+v", g)
		}
	}
	if _, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Sample", Kind: "person", Sort: db.SortTitle}); err == nil {
		t.Error("wide visible sort accepted")
	}
}

func TestSearchHiddenWorkReturnsACursorWithoutLosingVisiblePeople(t *testing.T) {
	defer db.SetSearchHiddenCheckCap(10)()
	s := newShelf(t)
	for n := range 25 {
		it := film(fmt.Sprintf("Secret %d", n), fmt.Sprintf("Sample Hidden %02d", n))
		it.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Blocked"}}
		s.add(s.films, it)
	}
	s.add(s.films, film("Visible", "Sample Visible"))
	tags, _ := s.d.Tags(ctx, db.TagLabel)
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	for page := range 4 {
		got, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Sample", Kind: "person", Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range got.Groups {
			if g.Kind != "person" {
				continue
			}
			if page == 0 && (g.Total != 1 || len(g.People) != 0 || g.NextCursor == "") {
				t.Fatalf("first page not bounded: %+v", g)
			}
			if len(g.People) > 0 {
				if len(g.People) != 1 || g.People[0].Name != "Sample Visible" {
					t.Fatal(g)
				}
				return
			}
			cursor = g.NextCursor
		}
		if cursor == "" {
			t.Fatal("visible person lost behind hidden work limit")
		}
	}
	t.Fatal("visible person not reached")
}

func TestSearchBulkBacklogCountsOnlyCurrentVisibleTitles(t *testing.T) {
	s := newShelf(t)
	for n := range 101 {
		it := film(fmt.Sprintf("Bulkscope %03d", n))
		it.RatingAge = new(6)
		s.add(s.films, it)
	}
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", MaxRatingAge: new(13)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.d.Items(ctx, v, db.ItemQuery{Library: s.films.PublicID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	index, err := sql.Open("sqlite", filepath.Join(s.dir, "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	conn, err := index.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	changed := 0
	for _, it := range page.Items {
		if len(it.Title) >= 9 && it.Title[:9] == "Bulkscope" {
			if err := s.d.RateShow(ctx, it.PublicID, 18); err != nil {
				t.Fatal(err)
			}
			changed++
		}
	}
	if changed != 101 {
		t.Fatal(changed)
	}
	got, err := s.d.Search(ctx, v, db.SearchQuery{Text: "Bulkscope", Kind: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got.Groups {
		if g.Kind == "movie" && (g.Total != 0 || g.TotalIsMinimum || len(g.Items) != 0) {
			t.Fatalf("bulk hidden count: %+v", g)
		}
	}
}
