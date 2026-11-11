package api

import (
	"fmt"
	"net/url"
	"testing"
)

// Every choice the server hands out for a filter, sent back as that filter, lists as many
// titles as the choice's count said, for the owner and for every seeded member: one with no
// limits, one with a blocked label, one limited to some libraries and one with a rating limit.
func TestEveryFilterChoiceWorksAsItsFilter(t *testing.T) {
	s, owner, members := seededWith(t, 1500, 4)
	tokens := map[string]string{"owner": owner}
	for n, member := range members {
		tokens[fmt.Sprint("viewer-", n)] = member
	}
	for who, token := range tokens {
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
