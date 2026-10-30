package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// RatingAllowed is the catalogue restriction predicate (itemRestrictionClause)
// for one thing that carries at most a content rating and no labels, such as
// a live-source guide programme (Channels spec §8.1): the viewer's age
// ceiling and unrated rule, which already combine the profile's own
// restriction with its account's limit. An empty or unknown rating is unrated.
func RatingAllowed(r identity.ContentRestrictions, rating string) bool {
	age, rated := identity.RatingAge(rating)
	if r.MaximumAge != nil && rated && age > *r.MaximumAge {
		return false
	}
	if r.BlockUnrated && !rated {
		return false
	}
	return true
}
