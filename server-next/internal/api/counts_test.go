package api

import (
	"fmt"
	"testing"
)

// Every count a card or a title page shows a restricted profile agrees with the list it
// describes: a show's seasons, a season's episodes, an artist's albums, an album's tracks. A
// count that included a hidden title would tell the profile it exists.
func TestCountsAgreeWithWhatARestrictedProfileCanList(t *testing.T) {
	s, owner, members := seededWith(t, 1500, 4)
	// The seeder's members: viewer-1 has a label blocked, viewer-3 a rating limit.
	hiddenSomewhere := false
	for _, who := range []int{1, 3} {
		token := members[who]
		// check compares one holder's counts, on its card and on its page, with its list of
		// children, then does the same for each child that holds others.
		var check func(card Item, depth int)
		check = func(card Item, depth int) {
			if card.Counts == nil {
				return
			}
			children := get[ItemPage](t, s, token, "/v1/items?parent="+card.ID+"&limit=200&total=true")
			if *children.Total != card.Counts.Children {
				t.Errorf("viewer-%d: %s %q says %d children and lists %d", who, card.Kind, card.Title, card.Counts.Children, *children.Total)
			}
			if page := get[Item](t, s, token, "/v1/items/"+card.ID); *page.Counts != *card.Counts {
				t.Errorf("viewer-%d: %s %q counts %+v on its card and %+v on its page", who, card.Kind, card.Title, *card.Counts, *page.Counts)
			}
			if all := get[Item](t, s, owner, "/v1/items/"+card.ID); all.Counts.Children > card.Counts.Children {
				hiddenSomewhere = true
			}
			if depth == 0 {
				for _, child := range children.Items {
					check(child, 1)
				}
			}
		}
		for _, library := range get[Libraries](t, s, token, "/v1/libraries").Items {
			for _, kind := range []string{"show", "artist"} {
				for _, card := range get[ItemPage](t, s, token, fmt.Sprintf("/v1/items?library=%s&kind=%s&limit=200", library.ID, kind)).Items {
					check(card, 0)
				}
			}
		}
	}
	if !hiddenSomewhere {
		t.Fatal("no holder in the seeded library had a child hidden from a restricted profile, so nothing was tested")
	}
}
