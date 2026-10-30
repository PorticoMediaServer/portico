package catalog

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestFacetAuthorityRequiresRequestGuard(t *testing.T) {
	ctx := context.Background()
	if !errors.Is(CheckFacetWindowAuthority(ctx, "caller", FacetBaseSnapshot{}), ErrFacetAuthorityUnavailable) {
		t.Fatal("missing guard authorized a read")
	}
	want := errors.New("private authority changed")
	guarded := WithFacetWindowAuthority(ctx, func(_ context.Context, caller readprep.Caller, _ FacetBaseSnapshot) error {
		if caller != "original" {
			return want
		}
		return nil
	})
	if err := CheckFacetWindowAuthority(guarded, "original", FacetBaseSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(CheckFacetWindowAuthority(guarded, "other", FacetBaseSnapshot{}), want) {
		t.Fatal("caller context was replaced")
	}
}

func TestFacetAuthoritySeparatesContentFromRestrictedVisibility(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	item := c.Movie(lib, "/films/a.mkv", "A", 2000)
	c.Genres(item.ID, "fixture", "Drama")
	c.Drain()
	scope := FacetBaseScope{LibraryID: lib, Library: "films", Field: "genre", Source: UnrestrictedFacetBaseSource, Policy: FacetBasePolicy(identity.ContentRestrictions{})}
	capture := func(s FacetBaseScope) string {
		t.Helper()
		v, err := CaptureFacetAuthority(context.Background(), c.DB, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ordinary := capture(scope)
	c.Genres(item.ID, "fixture", "Comedy")
	if capture(scope) != ordinary {
		t.Fatal("content-only genre edit invalidated unrestricted COMPLETE basis")
	}
	twelve := 12
	restricted := scope
	restricted.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &twelve})
	before := capture(restricted)
	genre := scope
	genre.Source = SearchVideoGenreSource + `:[{"id":` + strconv.FormatInt(lib, 10) + `,"library":"films"}]`
	unrestrictedGenre := capture(genre)
	privateGenre := genre
	privateGenre.Policy = restricted.Policy
	privateGenreBefore := capture(privateGenre)
	c.Movie(lib, "/films/new.mkv", "New content", 2001)
	c.Drain()
	if capture(restricted) != before || capture(genre) != unrestrictedGenre {
		t.Fatal("ordinary title materialization revoked a retained content basis")
	}
	if capture(privateGenre) != privateGenreBefore {
		t.Fatal("new movie first membership revoked active genre content basis")
	}
	c.Attributes(item.ID, "label", "Adult")
	if capture(restricted) == before {
		t.Fatal("restriction input did not fence old private counts")
	}
	if capture(scope) != ordinary {
		t.Fatal("unrestricted basis acquired a policy-dependent content fence")
	}
	if capture(genre) != unrestrictedGenre {
		t.Fatal("inactive genre snapshot acquired a restriction-content fence")
	}
}

func TestFacetBranchKeepsReadingAndContextualDispatch(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("films", "Films", "movie", "/films")
	c.Library("reading", "Reading", "book", "/reading")
	s := New(c.DB)
	for _, tc := range []struct {
		request FacetRequest
		simple  bool
	}{
		{FacetRequest{Library: "films", Field: "genre"}, true},
		{FacetRequest{Library: "films", Field: "genre", Pivot: "movies"}, false},
		{FacetRequest{Library: "films", Field: "genre", Query: &BrowseNode{Field: "year", Operator: "equals", Value: 2000}}, false},
		{FacetRequest{Library: "films", Field: "actor"}, false},
		{FacetRequest{Library: "reading", Field: "genre"}, false},
	} {
		got, err := s.IsSimpleFacetRequest(tc.request)
		if err != nil || got != tc.simple {
			t.Fatalf("%+v simple=%v err=%v", tc.request, got, err)
		}
	}
}
