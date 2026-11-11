package db_test

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A total is the size of the list as filtered, for every kind of profile: it equals the
// number of rows the filtered list pages out, it is never less than nothing, and with a
// filter that leaves some titles out it is smaller than the unfiltered total. The totals of
// a whole library are counted by shortcuts that know nothing of filters; this is what holds
// them to standing aside when there is one.
func TestAFilteredTotalIsTheSizeOfTheFilteredList(t *testing.T) {
	l := newLibrary(t, 1500)
	viewers := map[string]db.Viewer{"unrestricted": l.all, "label": l.viewers[7], "library": l.viewers[8], "rating": l.viewers[9]}
	filters := map[string]db.Condition{
		"released in the nineties": {Field: "released", Values: []string{"1990-01-01..1999-12-31"}},
		"rated seven or more":      {Field: "communityRating", Values: []string{"7.."}},
		"not a comedy":             {Field: "genre", Mode: db.ModeNone, Values: []string{"Comedy"}},
	}
	narrowed := map[string]bool{}
	for who, v := range viewers {
		for _, lib := range l.libs {
			for _, kind := range []db.ItemKind{db.KindMovie, db.KindShow, db.KindEpisode, db.KindAlbum, db.KindTrack, db.KindBook} {
				q := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, WithTotal: true, Limit: 200}
				whole, err := l.d.Items(ctx, v, q)
				if errors.Is(err, db.ErrNotFound) {
					continue // a library this profile may not open
				}
				if err != nil {
					t.Fatal(err)
				}
				for name, filter := range filters {
					q.Conditions = []db.Condition{filter}
					page, err := l.d.Items(ctx, v, q)
					if err != nil {
						t.Fatalf("%s, %s: %v", who, name, err)
					}
					listed := len(everything(t, l.d, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Conditions: q.Conditions, Limit: 200}))
					if page.Total != listed || page.Total < 0 || page.Total > whole.Total {
						t.Errorf("%s, %s kind %d, %s: total %d, the list holds %d, the unfiltered total is %d",
							who, lib.Kind, kind, name, page.Total, listed, whole.Total)
					}
					if page.Total < whole.Total {
						narrowed[who] = true
					}
				}
			}
		}
	}
	for who := range viewers {
		if !narrowed[who] {
			t.Errorf("no filter left anything out for the %s profile, so this test compared nothing", who)
		}
	}
}
