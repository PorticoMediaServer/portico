package read

import (
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBHomeLeafCompleteReachability(t *testing.T) {
	synctest.Test(t, testRBHomeLeafCompleteReachability)
}
func testRBHomeLeafCompleteReachability(t *testing.T) {
	f := newRBHomeFixture(t)
	for n := int64(0); n < 75; n++ {
		homeFixtureEntity(t, f, 4000+n, 1, 4, int64(2100), int64(2000), int64(2100), nil)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id BETWEEN 3000 AND 4063`)
	in := HomeLeafInput{View: f.input, Work: 2000}
	in.View.Kind = 2
	var got []int64
	for n := 0; n < 5; n++ {
		page, _, e := f.reader.homeLeafWindow(t.Context(), homeRequest(), time.Second, f.output, in)
		if e != nil || page.RawRows > 64 || page.RawPositions > 64 {
			t.Fatal(page, e)
		}
		for _, c := range page.Candidates {
			got = append(got, c.Item.ID)
		}
		if page.Exhausted {
			break
		}
		if page.Next == nil {
			t.Fatal("lost member continuation")
		}
		in.After = page.Next
	}
	if len(got) != 11 || got[0] != 4064 || got[10] != 4074 {
		t.Fatal("late allowed member lost", got)
	}
	f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=2000`)
	page, _, e := f.reader.homeLeafWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, HomeLeafPage{}) {
		t.Fatal("work source cursor drift", page, e)
	}
}

func TestRBHomeLeafPublicLIVE(t *testing.T) { synctest.Test(t, testRBHomeLeafPublicLIVE) }
func testRBHomeLeafPublicLIVE(t *testing.T) {
	f := newRBHomeFixture(t)
	in := HomeLeafInput{View: f.input, Work: 2000}
	in.View.Kind = 2
	page, cost, e := f.reader.HomeLeafWindow(t.Context(), homeRequest(), time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || len(page.Candidates) != 0 || cost.LookupStatements != 0 {
		t.Fatal("LIVE bypass", page, cost, e)
	}
}
