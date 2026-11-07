package artwork

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func selectionScope() Scope {
	return Scope{Kind: "entity", ID: 3, Incarnation: [16]byte{1}, SourceRevision: 7,
		PolicyRevision: 11, Provider: "tmdb", PreferredLanguage: "fr-CA"}
}

func selectionCandidate(role Role, id, locale string, score float64, ordinal int64) Candidate {
	return Candidate{Role: role, ImageID: id, Origin: "https://images.example/" + id,
		Locale: locale, Attribution: "provider", Score: score, Ordinal: ordinal}
}

var selectionLimits = Limits{Candidates: 160, TextBytes: 64 << 10}

func TestIPArtworkSelectsOnePerRoleWithLanguageAndLocks(t *testing.T) {
	input := []Candidate{
		selectionCandidate(Poster, "english", "en", 10, 0),
		selectionCandidate(Poster, "textless", "", 10, 1),
		selectionCandidate(Poster, "preferred", "fr-FR", 1, 2),
		selectionCandidate(Backdrop, "undetermined", "und", 10, 0),
		selectionCandidate(Backdrop, "no-text", "", 1, 1),
		selectionCandidate(Logo, "locked", "fr", 10, 0),
		selectionCandidate(Cover, "late-tie", "fr", 8, 9),
		selectionCandidate(Cover, "early-tie", "fr", 8, 2),
		selectionCandidate(Cover, "lower-score", "fr", 7, 0),
	}
	prepared, err := PrepareSelection(selectionScope(), input, []Role{Logo}, selectionLimits)
	if err != nil || !prepared.Valid() {
		t.Fatalf("prepare: %v", err)
	}
	want := []Candidate{input[2], input[4], input[7]}
	if !reflect.DeepEqual(prepared.Rows(), want) || prepared.Scope() != selectionScope() {
		t.Fatalf("got %+v, want %+v", prepared.Rows(), want)
	}
	// Caller ordering cannot settle a tie; the adapter's original ordinal does.
	for i, j := 0, len(input)-1; i < j; i, j = i+1, j-1 {
		input[i], input[j] = input[j], input[i]
	}
	shuffled, err := PrepareSelection(selectionScope(), input, []Role{Logo}, selectionLimits)
	if err != nil || !reflect.DeepEqual(shuffled.Rows(), want) {
		t.Fatalf("reordered page: %+v, %v", shuffled.Rows(), err)
	}
	input[0].ImageID = "changed-input"
	copy := prepared.Rows()
	copy[0].ImageID = "changed-output"
	if prepared.Rows()[0].ImageID != "preferred" {
		t.Fatal("preparation borrowed a mutable slice")
	}
}

func TestIPArtworkLanguageFallback(t *testing.T) {
	input := []Candidate{
		selectionCandidate(Poster, "other", "de", 9, 0),
		selectionCandidate(Poster, "english", "eng", 1, 1),
	}
	prepared, err := PrepareSelection(selectionScope(), input, nil, selectionLimits)
	if err != nil || prepared.Rows()[0].ImageID != "english" {
		t.Fatalf("English fallback: %+v, %v", prepared.Rows(), err)
	}
	scope := selectionScope()
	scope.PreferredLanguage = "" // Explicit textless preference, not a default.
	input = append(input, selectionCandidate(Poster, "textless", "", 0, 2))
	prepared, err = PrepareSelection(scope, input, nil, selectionLimits)
	if err != nil || prepared.Rows()[0].ImageID != "textless" {
		t.Fatalf("textless: %+v, %v", prepared.Rows(), err)
	}
}

func TestIPArtworkRefusesWholeInvalidPage(t *testing.T) {
	valid := selectionCandidate(Poster, "valid", "fr", 10, 0)
	loser := selectionCandidate(Poster, "loser", "de", 0, 1)
	cases := map[string]func(*Scope, *[]Candidate, *[]Role, *Limits){
		"missing-budget":      func(_ *Scope, _ *[]Candidate, _ *[]Role, l *Limits) { *l = Limits{} },
		"candidate-budget":    func(_ *Scope, _ *[]Candidate, _ *[]Role, l *Limits) { l.Candidates = 1 },
		"missing-kind":        func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.Kind = "" },
		"missing-id":          func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.ID = 0 },
		"missing-incarnation": func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.Incarnation = [16]byte{} },
		"missing-source":      func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.SourceRevision = 0 },
		"missing-policy":      func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.PolicyRevision = 0 },
		"missing-provider":    func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.Provider = "" },
		"invalid-preference":  func(s *Scope, _ *[]Candidate, _ *[]Role, _ *Limits) { s.PreferredLanguage = "!!!" },
		"unknown-role":        func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Role = "thumbnail" },
		"negative-ordinal":    func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Ordinal = -1 },
		"nan":                 func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Score = math.NaN() },
		"infinite":            func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Score = math.Inf(1) },
		"bad-utf8":            func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Attribution = "\xff" },
		"nul":                 func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].ImageID = "id\x00" },
		"bad-language":        func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Locale = "!!!" },
		"empty-id":            func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].ImageID = "" },
		"duplicate-id":        func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].ImageID = valid.ImageID },
		"duplicate-position":  func(_ *Scope, p *[]Candidate, _ *[]Role, _ *Limits) { (*p)[1].Ordinal = valid.Ordinal },
		"invalid-lock":        func(_ *Scope, _ *[]Candidate, locks *[]Role, _ *Limits) { *locks = []Role{"unknown"} },
		"duplicate-lock":      func(_ *Scope, _ *[]Candidate, locks *[]Role, _ *Limits) { *locks = []Role{Poster, Poster} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			scope, input, locked, limits := selectionScope(), []Candidate{valid, loser}, []Role{Poster}, selectionLimits
			mutate(&scope, &input, &locked, &limits)
			got, err := PrepareSelection(scope, input, locked, limits)
			if err != ErrSelection || got.Valid() || len(got.Rows()) != 0 || got.Scope() != (Scope{}) {
				t.Fatalf("partial/accepted invalid page: %+v, %v", got, err)
			}
		})
	}
	for _, origin := range []string{"http://images.example/x", "https://user:secret@images.example/x",
		"https://images.example/x?api_key=secret", "https://images.example/x?", "https://images.example/x#secret",
		"//images.example/x", "file:///x", "https:///x", "https://images.example/%zz"} {
		bad := loser
		bad.Origin = origin
		got, err := PrepareSelection(selectionScope(), []Candidate{valid, bad}, nil, selectionLimits)
		if err != ErrSelection || got.Valid() || strings.Contains(err.Error(), "secret") {
			t.Fatalf("origin refusal: valid=%v err=%v", got.Valid(), err)
		}
	}
}

func TestIPArtworkChargesActualUTF8Occurrences(t *testing.T) {
	scope := selectionScope()
	input := []Candidate{selectionCandidate(Poster, "é", "fr", 0, 0)}
	input[0].Attribution = "éé"
	n := len(scope.Kind) + len(scope.Provider) + len(scope.PreferredLanguage)
	for _, c := range input {
		n += len(c.Role) + len(c.ImageID) + len(c.Origin) + len(c.Locale) + len(c.Attribution)
	}
	if got, err := PrepareSelection(scope, input, nil, Limits{1, int64(n)}); err != nil || !got.Valid() {
		t.Fatalf("exact bytes: %v", err)
	}
	if got, err := PrepareSelection(scope, input, nil, Limits{1, int64(n - 1)}); err != ErrSelection || got.Valid() {
		t.Fatalf("one byte over: %v", err)
	}
}

func TestIPArtworkEmptyCompletePageAndPersonNamespace(t *testing.T) {
	scope := selectionScope()
	scope.Kind = "person"
	got, err := PrepareSelection(scope, nil, nil, selectionLimits)
	if err != nil || !got.Valid() || len(got.Rows()) != 0 || got.Scope().Kind != "person" {
		t.Fatalf("complete empty page: %+v, %v", got, err)
	}
	// Keep unknown-language artwork distinct from genuinely textless artwork.
	if und, _ := baseLanguage("und"); und == "" {
		t.Fatal("undefined language became textless")
	}
}
