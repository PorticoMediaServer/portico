package segmentmarkers

import "testing"

func TestAutomaticSafeAuthorizesApprovalAndCrossEpisodeMeasurementOnly(t *testing.T) {
	for _, c := range []struct {
		name       string
		approved   bool
		confidence float64
		provenance string
		safe       bool
	}{
		{"owner approved low confidence detector", true, .2, "luminance_opening_boundary:v1", true},
		{"owner manual marker", true, 1, "owner:manual", true},
		{"intro confirmed by other episodes at the floor", false, .85, "season_audio_match:intro:v1", true},
		{"credits confirmed by other episodes", false, .95, "season_audio_match:credits:v1", true},
		{"cross-episode suggestion below the floor", false, .7, "season_audio_match:intro:v1", false},
		{"single-file picture guess above the floor", false, .97, "visual_credits:v1", false},
		{"single-file luminance guess above the floor", false, .97, "sustained_end_title_luminance:v1", false},
		{"unapproved chapter title heuristic at the floor", false, .85, "embedded_chapter_label:v1", false},
		{"an online database's timestamps", false, .99, "online:theintrodb:v1", false},
		{"unapproved ambiguous detector", false, .95, "paired_black_boundaries_semantic_ambiguous:v1", false},
		{"a provenance merely starting with the family name", false, .95, "season_audio_matcher:v1", false},
		{"unapproved owner provenance", false, 1, "owner:manual", false},
		{"unapproved without provenance", false, 1, "", false},
	} {
		if got := AutomaticSafe(c.approved, c.confidence, c.provenance); got != c.safe {
			t.Fatalf("%s: automaticSafe=%v want %v", c.name, got, c.safe)
		}
	}
}

func TestViewerVisibleHidesWeakAndAmbiguousGuesses(t *testing.T) {
	for _, c := range []struct {
		approved   bool
		confidence float64
		provenance string
		visible    bool
	}{
		{false, .35, "sustained_end_title_luminance:v1", false},
		{false, .25, "paired_black_boundaries_semantic_ambiguous:v1", false},
		{true, .25, "paired_black_boundaries_semantic_ambiguous:v1", true},
		{false, .65, "visual_credits:v1", true},
		{false, .7, "season_audio_match:intro:v1", true},
		{false, .85, "embedded_chapter_label:v1", true},
	} {
		if got := ViewerVisible(c.approved, c.confidence, c.provenance); got != c.visible {
			t.Fatalf("%+v: visible=%v", c, got)
		}
	}
}

func TestStrongestKeepsOneMarkerPerOverlap(t *testing.T) {
	chapter := Candidate{Marker{ID: "chapter", Kind: "intro", StartSeconds: 30, EndSeconds: 90}, false, .85}
	measured := Candidate{Marker{ID: "measured", Kind: "intro", StartSeconds: 31, EndSeconds: 89, AutomaticSafe: true}, false, .9}
	credits := Candidate{Marker{ID: "credits", Kind: "credits", StartSeconds: 60, EndSeconds: 120}, false, .65}
	later := Candidate{Marker{ID: "later", Kind: "intro", StartSeconds: 200, EndSeconds: 230}, false, .6}
	got := Strongest([]Candidate{chapter, measured, credits, later})
	if len(got) != 3 || got[0].ID != "measured" || got[1].ID != "credits" || got[2].ID != "later" {
		t.Fatalf("%+v", got)
	}
	approved := Candidate{Marker{ID: "approved", Kind: "intro", StartSeconds: 28, EndSeconds: 92}, true, .3}
	if got = Strongest([]Candidate{approved, measured}); len(got) != 1 || got[0].ID != "approved" {
		t.Fatalf("an owner's approval lost to a measurement: %+v", got)
	}
}

func TestViewerKindExcludesOwnerOnlyKinds(t *testing.T) {
	for _, kind := range []string{"intro", "recap", "credits", "commercial", "outro"} {
		if !ViewerKind(kind) {
			t.Fatalf("viewer kind %q rejected", kind)
		}
	}
	for _, kind := range []string{"chapter", "", "Intro", "advert", "preview"} {
		if ViewerKind(kind) {
			t.Fatalf("non-viewer kind %q projected", kind)
		}
	}
}
