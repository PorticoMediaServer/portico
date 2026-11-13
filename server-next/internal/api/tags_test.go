package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// GET /v1/tags gives the owner every tag in use, and each limited profile exactly the tags on
// titles it may see: by rating, by library, and never the label it is kept from. Pages join
// up with nothing twice.
func TestTagsFollowWhatEachProfileMaySee(t *testing.T) {
	s := newTestServer(t)
	in := setup(t, s)
	owner := onMain(t, s, in).AccessToken
	started, err := s.tokens.CheckAccess(in.Session.AccessToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := seed.Import(ctx, s.db, seed.Config{Seed: 3, Items: 600}, nil); err != nil {
		t.Fatal(err)
	}
	// viewer-0 sees everything; viewer-1 is kept from a label, viewer-2 from libraries and
	// viewer-3 from titles rated above 13.
	viewers, err := seed.Profiles(ctx, s.db, 4)
	if err != nil {
		t.Fatal(err)
	}
	all := func(token string, limit string) []Tag {
		t.Helper()
		var out []Tag
		seen := map[string]bool{}
		for cursor := ""; ; {
			page := get[TagPage](t, s, token, "/v1/tags?limit="+limit+"&cursor="+cursor)
			for _, tag := range page.Items {
				if seen[tag.ID] {
					t.Fatalf("%s sent twice", tag.Name)
				}
				seen[tag.ID] = true
			}
			out = append(out, page.Items...)
			if cursor = page.NextCursor; cursor == "" {
				return out
			}
		}
	}
	everything := all(owner, "200")
	if paged := all(owner, "7"); len(paged) != len(everything) || len(everything) < 20 {
		t.Fatalf("the owner has %d tags in one page and %d in pages of 7", len(everything), len(paged))
	}
	labels, _ := s.db.Tags(ctx, db.TagLabel)
	for n, v := range viewers {
		token, _ := s.tokens.Access(auth.Claims{UserID: v.UserID, ProfileID: v.ProfileID, Role: "member", Boot: started.Boot},
			time.Now(), time.Now().Add(time.Hour))
		shown := map[string]bool{}
		for _, tag := range all(token, "200") {
			shown[tag.ID] = true
		}
		selected := get[TagPage](t, s, token, "/v1/tags?limit=200&ids="+everything[0].ID+","+labels[0].PublicID)
		wanted := map[string]bool{everything[0].ID: true, labels[0].PublicID: true}
		for _, tag := range selected.Items {
			if !wanted[tag.ID] || !shown[tag.ID] {
				t.Fatalf("selected tag lookup leaked or added %+v", tag)
			}
			delete(wanted, tag.ID)
		}
		for id := range wanted {
			if shown[id] {
				t.Fatalf("selected visible tag %s was omitted", id)
			}
		}
		for _, tag := range everything {
			titles, err := s.db.Items(ctx, v, db.ItemQuery{Tag: tag.ID, Limit: 1, WithTotal: true})
			if err != nil {
				t.Fatal(err)
			}
			if blocked := n == 1 && tag.ID == labels[0].PublicID; shown[tag.ID] != (titles.Total > 0 && !blocked) {
				t.Errorf("viewer-%d: %s %q shown %v, with %d titles it may see", n, tag.Kind, tag.Name, shown[tag.ID], titles.Total)
			}
		}
		if n > 0 && len(shown) >= len(everything) {
			t.Errorf("viewer-%d, who is limited, is shown all %d tags", n, len(shown))
		}
	}
	if code := status(t, s, "GET", "/v1/tags?kind=colour", owner, nil); code != 400 {
		t.Fatalf("an unknown kind answered %d", code)
	}
	if genres := get[TagPage](t, s, owner, "/v1/tags?kind=genre&limit=200"); len(genres.Items) == 0 || genres.Items[0].Kind != "genre" {
		t.Fatalf("genres: %+v", genres.Items)
	}
}
