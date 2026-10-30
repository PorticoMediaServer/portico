package catalog

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// CON-19: server-authored row titles are {code, params, fallback}; the
// fallback is US English sentence case and the code is the client catalogue id.
func TestServerRowTitlesCarryCodeParamsAndSentenceCaseFallback(t *testing.T) {
	// The fixed rows, as Home builds them (no libraries: no recent rows).
	specs, err := (&Service{}).homeSpecs(HomeRequest{CommunityActivity: true, LiveAvailable: true})
	if err != nil || len(specs) != len(homeRowTitleCodes) {
		t.Fatalf("specs %d %v", len(specs), err)
	}
	for _, spec := range specs {
		text := spec.titleText()
		if text.Code != homeRowTitleCodes[spec.ID] || text.Code == "" || text.Fallback != spec.Title || text.Params != nil {
			t.Fatalf("%s: %+v", spec.ID, text)
		}
	}
	recent := homeRowSpec{ID: "recent_lib", Title: "Recently added in Movies", TitleParams: map[string]string{"library": "Movies"}}.titleText()
	if recent.Code != "home.row.recentlyAddedIn" || !reflect.DeepEqual(recent.Params, map[string]string{"library": "Movies"}) {
		t.Fatalf("recent: %+v", recent)
	}
	if bare := (homeRowSpec{ID: "recent_lib", Title: "Recently added"}).titleText(); bare.Code != "home.row.recentlyAdded" || bare.Params != nil {
		t.Fatalf("recent without a library: %+v", bare)
	}
	related := relatedRowText("because_you_watched", "Because you watched Alien")
	if related.Code != "home.row.becauseYouWatched" || related.Params["title"] != "Alien" || related.Fallback != "Because you watched Alien" {
		t.Fatalf("related: %+v", related)
	}
	if unknown := relatedRowText("genre", "Something"); unknown.Code != "" || unknown.Fallback != "Something" {
		t.Fatalf("unknown relation: %+v", unknown)
	}
	// Sentence case, except named features, which keep title case (Muse
	// agreement §4): no word after the first starts with a capital letter.
	named := map[string]bool{"Continue Watching": true, "Continue Listening": true, "My List": true}
	for _, spec := range specs {
		if named[spec.Title] {
			continue
		}
		for _, word := range strings.Fields(spec.Title)[1:] {
			if unicode.IsUpper([]rune(word)[0]) {
				t.Fatalf("%s is not sentence case: %q", spec.ID, spec.Title)
			}
		}
	}
}
