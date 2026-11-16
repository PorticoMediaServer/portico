package db_test

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"testing"
	"time"
)

func TestScopedPlaylistTotalIgnoresHiddenTitlesOutsideIt(t *testing.T) {
	s := newShelf(t)
	allowed := film("Visible scope film")
	allowed.Tags = []db.TagName{{Kind: db.TagGenre, Name: "Inside"}}
	outside := film("Outside list")
	outside.Tags = []db.TagName{{Kind: db.TagGenre, Name: "Outside"}, {Kind: db.TagGenre, Name: "Inside"}}
	hidden := film("Hidden scope film")
	hidden.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Blocked scope"}}
	s.add(s.films, allowed, hidden, outside)
	tags, err := s.d.Tags(ctx, db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited", BlockedTags: []string{tags[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.d.Items(ctx, v, db.ItemQuery{Library: s.films.PublicID, Limit: 200})
	if err != nil {
		t.Fatal(page, err)
	}
	var itemID string
	for _, it := range page.Items {
		if it.Title == "Visible scope film" {
			itemID = it.PublicID
		}
	}
	if itemID == "" {
		t.Fatal("no visible film")
	}
	list, err := s.d.CreateList(ctx, v, db.NewList{Kind: db.ListPlaylist, Name: "One", Items: []string{itemID}}, "", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.d.Items(ctx, v, db.ItemQuery{Library: s.films.PublicID, Kinds: []db.ItemKind{db.KindMovie}, List: list.PublicID, Sort: db.SortTitle, WithTotal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Total != 1 {
		t.Errorf("one visible list title: items=%d total=%d", len(got.Items), got.Total)
	}
	facets, err := s.d.Facets(ctx, v, db.ItemQuery{Library: s.films.PublicID, Kinds: []db.ItemKind{db.KindMovie}, List: list.PublicID}, db.FacetGenre)
	if err != nil {
		t.Fatal(err)
	}
	if len(facets) != 1 || facets[0].Label != "Inside" || facets[0].Count != 1 {
		t.Errorf("scoped genre facets: %+v", facets)
	}

	if len(facets) > 0 {
		tagged, err := s.d.Items(ctx, v, db.ItemQuery{Library: s.films.PublicID, Kinds: []db.ItemKind{db.KindMovie}, List: list.PublicID, Tag: facets[0].Value, Sort: db.SortTitle, WithTotal: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(tagged.Items) != 1 || tagged.Total != 1 {
			t.Errorf("tagged list total: %d rows, total %d", len(tagged.Items), tagged.Total)
		}
	}

}
