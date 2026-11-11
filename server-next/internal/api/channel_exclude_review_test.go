package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// Exclude turns a condition around: a film the condition does not match, including one with
// no value for the field (no content rating, no year), is in the excluded channel. The seed
// gives about one film in six no content rating.
func TestReviewExcludeKeepsTitlesWithNoValue(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	films := libraryOfKind(t, s, owner, "movies")
	var all ItemPage
	call(t, s, "POST", "/v1/items/query", owner, ItemQueryRequest{Library: films, Kind: []string{"movie"}, Total: true}, &all)
	titles := func(conds ...db.ChannelCondition) int {
		b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"},
			Match: "all", Conditions: conds}}
		var p ChannelPreview
		if w := call(t, s, "POST", "/v1/admin/library-channels/preview", owner, ChannelPreviewAsk{TimeZone: "UTC", Builder: b}, &p); w.Code != 200 {
			t.Fatalf("preview answered %d: %s", w.Code, w.Body)
		}
		return p.Titles
	}
	for _, c := range []db.ChannelCondition{
		{Field: "contentRating", Values: []string{"R"}},
		{Field: "contentRating", Mode: "all", Values: []string{"R"}},
		{Match: "any", Conditions: []db.ChannelCondition{{Field: "contentRating", Values: []string{"R"}}, {Field: "decade", Values: []string{"1990"}}}},
	} {
		in := titles(c)
		c.Exclude = true
		out := titles(c)
		if in+out != *all.Total {
			t.Errorf("%+v: included %d plus excluded %d is not the %d films", c, in, out, *all.Total)
		}
	}
	// "none" of a rating keeps an unrated film, as Exclude does.
	none := titles(db.ChannelCondition{Field: "contentRating", Mode: "none", Values: []string{"R"}})
	in := titles(db.ChannelCondition{Field: "contentRating", Values: []string{"R"}})
	if in+none != *all.Total {
		t.Errorf("rated R %d plus none of R %d is not the %d films", in, none, *all.Total)
	}
}

// An empty group neither empties nor widens a channel: it is as if it were not there.
func TestReviewAnEmptyGroupChangesNothing(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	films := libraryOfKind(t, s, owner, "movies")
	titles := func(conds ...db.ChannelCondition) int {
		b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"},
			Match: "all", Conditions: conds}}
		var p ChannelPreview
		if w := call(t, s, "POST", "/v1/admin/library-channels/preview", owner, ChannelPreviewAsk{TimeZone: "UTC", Builder: b}, &p); w.Code != 200 {
			t.Fatalf("preview answered %d: %s", w.Code, w.Body)
		}
		return p.Titles
	}
	decade := db.ChannelCondition{Field: "decade", Values: []string{"1990"}}
	want := titles(decade)
	for _, g := range []db.ChannelCondition{
		{Match: "any", Conditions: []db.ChannelCondition{}},
		{Match: "all", Conditions: []db.ChannelCondition{}},
		{Match: "any", Conditions: []db.ChannelCondition{}, Exclude: true},
	} {
		if got := titles(decade, g); got != want {
			t.Errorf("an empty group %+v left %d titles, not %d", g, got, want)
		}
	}
}

// A channel whose conditions hold more values together than any real channel would is refused
// when saved, with a sentence, instead of making its schedule take minutes or fail.
func TestTooManyConditionValuesAreRefusedAtSave(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	films := libraryOfKind(t, s, owner, "movies")
	var conditions []db.ChannelCondition
	for range 3 {
		var values []string
		for n := range 500 {
			values = append(values, fmt.Sprint(1900+n%100))
		}
		conditions = append(conditions, db.ChannelCondition{Field: "year", Values: values})
	}
	b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}, Conditions: conditions}}
	var e struct {
		Error Error `json:"error"`
	}
	if w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: new("Big"), Builder: &b}, &e); w.Code != 400 ||
		len(e.Error.Fields) == 0 || !strings.Contains(e.Error.Fields[0].Message, "1000 values") && !strings.Contains(e.Error.Fields[0].Message, "values together") {
		t.Fatalf("1,500 values answered %d %+v", w.Code, e.Error)
	}
}
