package read

import (
	"testing"
	"testing/synctest"
	"time"
)

// Four is the reviewed physical byte-point recipe, independently of the
// implementation constant. Invisible and obsolete positions consume it too.
func TestRSPeopleFourPositionWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSPeopleFixture(t, false)
		in := PersonCreditsInput{View: f.input, Person: 1}
		page, _, e := r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(e)
		}
		if page.RawRows != 4 || page.RawPositions > 4 || page.Next == nil || page.Exhausted {
			t.Fatal("four-position raw window changed", page.RawRows, page.RawPositions, page.Next, page.Exhausted)
		}
	})
}
