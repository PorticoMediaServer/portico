package db_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// twoShows puts two shows on a shelf. Alpha has season 2 only, Beta has season 1 only, so a
// merge moves Alpha's season across whole. Alpha's episode carries a rating of its own (18),
// as an episode's NFO file gives it; both shows are for 8.
func twoShows(t *testing.T) (s *shelf, alpha, beta string) {
	s = newShelf(t)
	one, two, eighteen := 1, 2, 18
	a, a2, b, b1 := key("alpha"), key("alpha/2"), key("beta"), key("beta/1")
	s.add(s.shows,
		db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{a}, Title: "Alpha"},
		db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{a2}, Parent: &a, Title: "Season 2", IndexNumber: &two},
		db.ImportItem{Kind: db.KindEpisode, Parent: &a2, Title: "Grown-up", IndexNumber: &one, ParentIndex: &two, ContentRating: "TV-MA", RatingAge: &eighteen},
		db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{b}, Title: "Beta"},
		db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{b1}, Parent: &b, Title: "Season 1", IndexNumber: &one},
		db.ImportItem{Kind: db.KindEpisode, Parent: &b1, Title: "Gentle", IndexNumber: &one, ParentIndex: &one})
	for _, show := range everything(t, s.d, s.v, db.ItemQuery{Library: s.shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 50}) {
		switch show.Title {
		case "Alpha":
			alpha = show.PublicID
		case "Beta":
			beta = show.PublicID
		}
	}
	for _, show := range []string{alpha, beta} {
		if err := s.d.RateShow(ctx, show, 8); err != nil {
			t.Fatal(err)
		}
	}
	return s, alpha, beta
}

// listsAndFinds is the episodes of the merged show that a profile lists, after checking that
// a search for the show's name finds exactly those.
func listsAndFinds(t *testing.T, s *shelf, v db.Viewer) []string {
	t.Helper()
	var listed, found []string
	for _, it := range everything(t, s.d, v, db.ItemQuery{Library: s.shows.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 50}) {
		if it.Title == "Grown-up" || it.Title == "Gentle" {
			listed = append(listed, it.Title)
		}
	}
	everyone := s.v
	s.v = v
	for _, it := range s.search("beta", "episode").Items {
		found = append(found, it.Title)
	}
	s.v = everyone
	slices.Sort(listed)
	slices.Sort(found)
	if !slices.Equal(listed, found) {
		t.Errorf("the profile lists %q and a search finds %q", listed, found)
	}
	return listed
}

// An episode with a rating of its own keeps it when its season moves under another show in a
// merge. A profile limited to 13 could not see the episode for 18 before, and cannot after.
func TestAMergedEpisodeKeepsItsOwnRating(t *testing.T) {
	s, alpha, beta := twoShows(t)
	thirteen := 13
	child, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "child", MaxRatingAge: &thirteen})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.MergeItems(ctx, alpha, beta); err != nil {
		t.Fatal(err)
	}
	if got := listsAndFinds(t, s, child); !slices.Equal(got, []string{"Gentle"}) {
		t.Errorf("after the merge the profile limited to 13 sees %q; want only the episode that is not for 18", got)
	}
}

// A label on the show that is merged away goes to the show that remains: a profile kept from
// that label saw none of that show before the merge and sees none of the merged show after.
func TestAMergeKeepsTheLabelOfTheShowMergedAway(t *testing.T) {
	s, alpha, beta := twoShows(t)
	if err := db.PutLabel(s.d, alpha, "Not for kids", false); err != nil {
		t.Fatal(err)
	}
	labels, err := s.d.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v, %v", labels, err)
	}
	child, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "child", BlockedTags: []string{labels[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.d.MergeItems(ctx, alpha, beta); err != nil {
		t.Fatal(err)
	}
	if got := listsAndFinds(t, s, child); len(got) != 0 {
		t.Errorf("after the merge the profile kept from the label sees %q; want nothing of the merged show", got)
	}
	if wrong, _, err := db.CoveringLabels(s.d); err != nil || len(wrong) > 0 {
		t.Errorf("stored labels: %v, %v", wrong, err)
	}
}
