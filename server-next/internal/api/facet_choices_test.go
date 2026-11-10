package api

import (
	"fmt"
	"net/url"
	"testing"
)

// Every choice the server hands out for a filter, sent back as that filter, lists as many
// titles as the choice's count said, for a profile with no limits and one with a blocked label.
func TestEveryFilterChoiceWorksAsItsFilter(t *testing.T) {
	s, owner, members := seededWith(t, 1500, 4)
	for who, token := range map[string]string{"owner": owner, "viewer-1": members[1]} {
		for _, library := range get[Libraries](t, s, token, "/v1/libraries").Items {
			kind := map[string]string{"movies": "movie", "shows": "show", "music": "album"}[library.Kind]
			if kind == "" {
				continue
			}
			list := "/v1/items?library=" + library.ID + "&kind=" + kind
			for _, facet := range []string{"genre", "studio", "network", "label", "year", "contentRating", "resolution"} {
				choices := get[Facets](t, s, token, "/v1/facets?facet="+facet+"&library="+library.ID+"&kind="+kind).Values
				for n, choice := range choices {
					if n == 5 {
						break
					}
					path := fmt.Sprintf("%s&%s=%s&total=true", list, facet, url.QueryEscape(choice.Value))
					if got := *get[ItemPage](t, s, token, path).Total; got != choice.Count {
						t.Errorf("%s: %s choice %q (%s) counts %d, and as the filter lists %d", who, facet, choice.Label, choice.Value, choice.Count, got)
					}
				}
			}
		}
	}
}
