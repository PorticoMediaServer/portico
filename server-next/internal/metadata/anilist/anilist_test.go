package anilist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist/anilisttest"
)

func TestAniListSearchDetailsAndFailures(t *testing.T) {
	s := anilisttest.New(t)
	c := anilist.New(s.URL, 0)
	ctx := context.Background()
	q := metadata.Query{Kind: "show", Title: "The Quiet Sky", Locale: metadata.Locale{Language: "en"}}
	found, err := c.Search(ctx, q)
	if err != nil || len(found) != 1 || found[0].Ref.ID != "42" || found[0].PosterURL != s.URL+"/poster.png" {
		t.Fatalf("search: %+v, %v", found, err)
	}
	q.IncludeAdult = true
	if found, err := c.Search(ctx, q); err != nil || len(found) != 2 {
		t.Fatalf("adult search: %+v, %v", found, err)
	}
	r, err := c.Details(ctx, metadata.Ref{Kind: "show", ID: "42"}, metadata.Locale{Language: "ja"})
	if err != nil || r.Title != "静かな空" || r.Overview != "A quiet journey & a return." || r.ReleaseDate != "2024-04-07" || r.Rating != 8.2 || len(r.Pictures) != 2 || len(r.Credits) != 1 {
		t.Fatalf("details: %+v, %v", r, err)
	}
	if _, err := c.Children(ctx, r.Ref, 1, metadata.Locale{}); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("episodes: %v", err)
	}
	if _, err := c.Details(ctx, metadata.Ref{Kind: "show", ID: "99"}, metadata.Locale{}); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	s.Fail.Store(true)
	if _, err := c.Details(ctx, r.Ref, metadata.Locale{}); err == nil || errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("GraphQL outage: %v", err)
	}
}
