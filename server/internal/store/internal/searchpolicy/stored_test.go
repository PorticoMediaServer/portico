package searchpolicy_test

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

func TestStoredWellFormedDoesNotAttestCompatibility(t *testing.T) {
	stored := searchpolicy.Profile{
		RecipeVersion:      "foreign-recipe",
		EngineVersion:      "foreign-engine",
		UnicodeFingerprint: searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("a1", 32),
	}
	before := stored
	if !searchpolicy.StoredWellFormed(stored) {
		t.Fatal("a well-formed foreign profile must remain RAW-admissible")
	}
	if got := searchpolicy.Compare(stored, searchpolicy.Engine{
		Version: searchpolicy.SQLiteVersion, SourceID: searchpolicy.SQLiteSourceID,
	}); got.State != searchpolicy.Stale || got.Reason != searchpolicy.ProfileMismatch {
		t.Fatalf("shape validation must not make a foreign profile current: %+v", got)
	}
	if stored != before {
		t.Fatal("shape validation changed the stored profile")
	}
}

func TestStoredWellFormedShapeBoundary(t *testing.T) {
	valid := searchpolicy.Profile{
		RecipeVersion:      strings.Repeat("r", 128),
		EngineVersion:      strings.Repeat("e", 512),
		UnicodeFingerprint: searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("09af", 16),
	}
	if !searchpolicy.StoredWellFormed(valid) {
		t.Fatal("the existing inclusive field bounds must remain accepted")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*searchpolicy.Profile)
	}{
		{"missing-recipe", func(p *searchpolicy.Profile) { p.RecipeVersion = "" }},
		{"oversized-recipe", func(p *searchpolicy.Profile) { p.RecipeVersion += "r" }},
		{"missing-engine", func(p *searchpolicy.Profile) { p.EngineVersion = "" }},
		{"oversized-engine", func(p *searchpolicy.Profile) { p.EngineVersion += "e" }},
		{"missing-fingerprint", func(p *searchpolicy.Profile) { p.UnicodeFingerprint = "" }},
		{"wrong-encoding", func(p *searchpolicy.Profile) { p.UnicodeFingerprint = "other:" + strings.Repeat("0", 64) }},
		{"short-digest", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = p.UnicodeFingerprint[:len(p.UnicodeFingerprint)-1]
		}},
		{"long-digest", func(p *searchpolicy.Profile) { p.UnicodeFingerprint += "0" }},
		{"uppercase-digest", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("A", 64)
		}},
		{"non-hex-digest", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("g", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := valid
			tc.mutate(&stored)
			if searchpolicy.StoredWellFormed(stored) {
				t.Fatal("malformed tuple accepted")
			}
		})
	}
	if searchpolicy.StoredWellFormed(searchpolicy.Profile{}) {
		t.Fatal("missing configuration accepted")
	}
}
