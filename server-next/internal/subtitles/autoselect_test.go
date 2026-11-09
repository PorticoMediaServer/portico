package subtitles

import (
	"testing"
)

func TestChooseAutomatic(t *testing.T) {
	tracks := []AutoCandidate{
		{ID: "en-sdh", Language: "eng", Title: "English [SDH]"},
		{ID: "en", Language: "en", Title: "English"},
		{ID: "en-forced", Language: "eng", Title: "Forced", Forced: true},
		{ID: "es", Language: "spa", Title: "Español"},
	}
	pick := func(c AutoChoice) string {
		if chosen := ChooseAutomatic(c, tracks); chosen != nil {
			return chosen.ID
		}
		return ""
	}
	if got := pick(AutoChoice{Enabled: true, Languages: []string{"en-GB"}}); got != "en" {
		t.Fatal("ordinary track not preferred over SDH and forced:", got)
	}
	if got := pick(AutoChoice{Enabled: true, Languages: []string{"de", "es"}}); got != "es" {
		t.Fatal("second preferred language not used:", got)
	}
	if got := pick(AutoChoice{Enabled: true, Languages: []string{"ja"}}); got != "" {
		t.Fatal("chose a language the viewer did not ask for:", got)
	}
	// Subtitles off: only the forced track beside audio in its own language.
	if got := pick(AutoChoice{AudioLanguage: "eng"}); got != "en-forced" {
		t.Fatal("forced track not shown beside matching audio:", got)
	}
	if got := pick(AutoChoice{AudioLanguage: "fra"}); got != "" {
		t.Fatal("forced track shown beside audio in another language:", got)
	}
	if got := pick(AutoChoice{}); got != "" {
		t.Fatal(got)
	}
	// Only an SDH track in the language: better than nothing.
	tracks = tracks[:1]
	if got := pick(AutoChoice{Enabled: true, Languages: []string{"en"}}); got != "en-sdh" {
		t.Fatal(got)
	}
}

func TestAutomaticUnknownForcedAndFileDefault(t *testing.T) {
	tracks := []AutoCandidate{{ID: "untagged", Forced: true, Language: "und"}}
	if got := ChooseAutomatic(AutoChoice{AudioLanguage: "eng"}, tracks); got == nil || got.ID != "untagged" {
		t.Fatal(got)
	}
	if got := ChooseAutomatic(AutoChoice{Off: true, AudioLanguage: "eng"}, tracks); got != nil {
		t.Fatal("explicit off ignored")
	}
	tracks = append(tracks, AutoCandidate{ID: "other", Forced: true})
	if got := ChooseAutomatic(AutoChoice{AudioLanguage: "eng"}, tracks); got != nil {
		t.Fatal("ambiguous forced track selected")
	}
	tracks = []AutoCandidate{{ID: "default", Default: true, Language: "eng"}}
	if got := ChooseAutomatic(AutoChoice{Enabled: true, NoLanguage: true}, tracks); got != nil {
		t.Fatal("explicit empty languages must keep subtitles off")
	}
	if got := ChooseAutomatic(AutoChoice{Enabled: true, Languages: []string{"fr"}}, tracks); got != nil {
		t.Fatal("file default overrode preferred language")
	}
}
