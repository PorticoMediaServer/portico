package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestRatingAllowedMatchesTheCatalogueRules(t *testing.T) {
	thirteen := 13
	kid := identity.ContentRestrictions{MaximumAge: &thirteen, BlockUnrated: true}
	for rating, want := range map[string]bool{"TV-14": false, "R": false, "PG": true, "TV-G": true, "": false, "unknown-thing": false} {
		if got := RatingAllowed(kid, rating); got != want {
			t.Fatalf("kid %q: %v, want %v", rating, got, want)
		}
	}
	lenient := identity.ContentRestrictions{MaximumAge: &thirteen}
	if !RatingAllowed(lenient, "") || RatingAllowed(lenient, "TV-MA") {
		t.Fatal("unrated allowed when the profile allows unrated; TV-MA refused")
	}
	if !RatingAllowed(identity.ContentRestrictions{}, "NC-17") {
		t.Fatal("an unrestricted profile refused a rating")
	}
}
