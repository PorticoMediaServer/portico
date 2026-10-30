package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// B67: the published visibility class lists the same shows as the open and
// cold (authoritative) pages: a show with no episodes is never listed.
func TestVisibilityClassMembershipMatchesHierarchyPages(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	full := c.Show(tv, "A Full", 2020)
	empty := c.Show(tv, "B Empty", 2020)
	season := c.Season(full, 1)
	episode := c.Episode(full, season, 1, "/tv/e1.mkv")
	c.Attributes(episode.ID, "contentRating", "PG")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8)`)
	c.Drain()

	s := New(c.DB)
	ctx := context.Background()
	maxAge := 13
	r := parityRestriction(&maxAge, true)
	names := catalogtest.Names{"full": full, "empty": empty}
	list := func(label string, v Viewer) string {
		out, err := s.Content(ContentRequest{Viewer: v, ServerID: "s", Library: "tv", Profile: "p", ViewerFence: v.Fence, View: "browse", Limit: 50})
		ids := []string{}
		for _, sec := range out.Sections {
			for _, e := range sec.Entries {
				ids = append(ids, sec.ID+":"+names.Of(e.ID))
			}
		}
		if err != nil {
			t.Fatal(label, err)
		}
		return strings.Join(ids, ",")
	}
	open := list("open", Viewer{Profile: "p", Fence: "o", Libraries: []string{"tv"}})
	cold := list("restricted-cold", Viewer{Profile: "p", Fence: "r", Libraries: []string{"tv"}, Restrictions: r})
	if err := s.RebuildVisibilityClass(ctx, "tv", r); err != nil {
		t.Fatal(err)
	}
	class := list("restricted-class", Viewer{Profile: "p", Fence: "r2", Libraries: []string{"tv"}, Restrictions: r})
	if open != "items:full" || cold != open || class != open {
		t.Fatalf("show lists differ: open %q cold %q class %q", open, cold, class)
	}
}

// parityRestriction is a local copy of restrictionOf for isolated test-only.sh runs.
func parityRestriction(max *int, blockUnrated bool) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, Revision: 2}
}
