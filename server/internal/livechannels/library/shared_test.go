package librarychannels

import (
	"errors"
	"testing"
)

// CH1: Library Channels are one shared lineup. A write that asks for an
// owner-only channel is a clear 400 on viewerAccess; an omitted field means
// shared; templates install shared; channels saved owner-only before the rule
// are converted by migration 0074.
func TestLibraryChannelsAreAlwaysShared(t *testing.T) {
	c := channel("shared", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "shuffle-bag", "none")
	c.ViewerAccess = "owner-only"
	var issue *ValidationError
	if err := Validate(c); !errors.As(err, &issue) || issue.Path != "viewerAccess" || !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner-only accepted: %v", err)
	}
	c.ViewerAccess = ""
	normalized, err := Normalize(c)
	if err != nil || normalized.ViewerAccess != "server-members" || Validate(normalized) != nil {
		t.Fatalf("omitted access: %+v %v", normalized.ViewerAccess, err)
	}

	f := openFixture(t)
	s, _ := New(f.db)
	templates, err := s.Templates(t.Context(), ownerAuthority(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range templates {
		if tpl.Config.ViewerAccess != "server-members" {
			t.Fatalf("template %s installs %q", tpl.ID, tpl.Config.ViewerAccess)
		}
	}
}

// F-web, 23 Sep: "Some of what plays" listed episode titles for TV and anime
// channels. A preview samples one episode per show and names the show.
func TestPreviewSamplesNameShowsNotEpisodes(t *testing.T) {
	f := justinsLibrary(t)
	c := channel("tv", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "sequential", "in-order")
	p := previewOf(t, f, c)
	got := map[string]bool{}
	for _, s := range p.Rules[0].Sample {
		if s.ShowTitle == "" || got[s.ShowTitle] {
			t.Fatalf("samples %+v", p.Rules[0].Sample)
		}
		got[s.ShowTitle] = true
	}
	if !got["Malcolm in the Middle"] || !got["The Other Show"] || len(got) != 2 || p.Rules[0].Eligible != 9 {
		t.Fatalf("shows %v, eligible %d", got, p.Rules[0].Eligible)
	}
	// A movie channel's samples are its titles, without a show.
	for _, s := range previewOf(t, f, channel("m", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "sequential", "none")).Rules[0].Sample {
		if s.ShowTitle != "" || s.Title == "" {
			t.Fatalf("movie sample %+v", s)
		}
	}
}
