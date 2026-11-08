package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestN1PairedPopulationSelection(t *testing.T) {
	validDir := filepath.Join(t.TempDir(), "population")
	validPin := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		dir     string
		pin     string
		want    bool
		wantErr bool
	}{
		{name: "absent", want: false},
		{name: "directory_only", dir: validDir, wantErr: true},
		{name: "pin_only", pin: validPin, wantErr: true},
		{name: "relative_directory", dir: "population", pin: validPin, wantErr: true},
		{name: "short_pin", dir: validDir, pin: strings.Repeat("a", 63), wantErr: true},
		{name: "uppercase_pin", dir: validDir, pin: strings.Repeat("A", 64), wantErr: true},
		{name: "non_hex_pin", dir: validDir, pin: strings.Repeat("g", 64), wantErr: true},
		{name: "valid_pair", dir: validDir, pin: validPin, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, err := validateN1PairedPopulationSelection(tc.dir, tc.pin)
			if enabled != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("enabled=%v err=%v, want enabled=%v err=%v", enabled, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestN1PairedPopulationAbsentIsLegacyNil(t *testing.T) {
	t.Setenv(n1PairedPopulationDirectoryEnv, "")
	t.Setenv(n1PairedPopulationManifestEnv, "")
	if manifest := attachN1PairedPopulation(t); manifest != nil {
		t.Fatal("legacy paired component unexpectedly attached a population")
	}
}

func TestN1PairedPopulationManifestRequiresExactMillion(t *testing.T) {
	if err := validateN1PairedPopulationManifest(nil); err == nil {
		t.Fatal("accepted a missing manifest")
	}
	for _, residents := range []int{0, 1, 16, n1PairedPopulationResidents - 1, n1PairedPopulationResidents + 1} {
		m := &QualificationPopulationManifest{Facts: qualificationPopulationFacts{Residents: residents}}
		if err := validateN1PairedPopulationManifest(m); err == nil {
			t.Fatalf("accepted %d residents", residents)
		}
	}
	m := &QualificationPopulationManifest{Facts: qualificationPopulationFacts{Residents: n1PairedPopulationResidents}}
	if err := validateN1PairedPopulationManifest(m); err != nil {
		t.Fatal("rejected the exact million-resident manifest", err)
	}
}

// Opt-in only: the existing tiny exporter is a real native SQLite artifact
// exercise. It proves the actual manifest transport and exact-million refusal
// without adding a default native test or running any paired constructor.
func TestN1PairedPopulationTinyArtifactTransport(t *testing.T) {
	if os.Getenv("PORTICO_PAIRED_POPULATION_FUNCTIONAL") != "1" {
		t.Skip("opt-in paired population transport control; requires the pinned Go1.26.10 artifact exporter")
	}
	dir, pin := canonicalN1ControlArtifact(t)
	manifest, err := readQualificationPopulation(dir, pin)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Facts.Residents >= n1PairedPopulationResidents {
		t.Fatalf("control artifact is not tiny: %d residents", manifest.Facts.Residents)
	}
	if err = validateN1PairedPopulationManifest(&manifest); err == nil {
		t.Fatal("tiny artifact satisfied the exact-million paired requirement")
	}
	if enabled, err := validateN1PairedPopulationSelection(dir, pin); err != nil || !enabled {
		t.Fatal("actual artifact pair rejected", err)
	}
	// Same-exact-test transport registration and consumption; no executor open.
	registered := UseQualificationPopulation(t, dir, pin)
	copied := qualificationCanonicalFixturePath(t)
	if copied == filepath.Join(dir, qualificationPopulationImage) {
		t.Fatal("opened the immutable artifact in place")
	}
	info, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	if registered.DatabaseSHA256 != manifest.DatabaseSHA256 || registered.Facts.Residents != manifest.Facts.Residents || info.Size() != registered.DatabaseBytes {
		t.Fatal("registered population differs from the verified manifest")
	}
}
