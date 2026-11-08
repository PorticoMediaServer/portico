package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const (
	n1PairedPopulationDirectoryEnv = "PORTICO_PAIRED_POPULATION_DIRECTORY"
	n1PairedPopulationManifestEnv  = "PORTICO_PAIRED_POPULATION_MANIFEST_SHA256"
	n1PairedPopulationResidents    = 1_000_000
)

// validateN1PairedPopulationSelection is pure. It decides whether the optional
// artifact pair is absent (legacy tiny component), malformed, or ready for the
// existing readQualificationPopulation/UseQualificationPopulation transport.
// Empty values are treated as absent, matching ordinary unset CI inputs.
func validateN1PairedPopulationSelection(dir, pin string) (bool, error) {
	if dir == "" && pin == "" {
		return false, nil
	}
	if dir == "" || pin == "" {
		return false, errors.New("paired population requires both directory and manifest SHA256")
	}
	if !filepath.IsAbs(dir) {
		return false, errors.New("paired population directory must be absolute")
	}
	if !qualificationPopulationSHA.MatchString(pin) {
		return false, errors.New("paired population manifest SHA256 must be 64 lowercase hex characters")
	}
	return true, nil
}

// validateN1PairedPopulationManifest is pure and accepts only the actual
// canonical image shape. readQualificationPopulation still owns every other
// provenance, policy, count and file check.
func validateN1PairedPopulationManifest(m *QualificationPopulationManifest) error {
	if m == nil {
		return errors.New("paired population manifest is required")
	}
	if m.Facts.Residents != n1PairedPopulationResidents {
		return fmt.Errorf("paired population requires exactly %d residents: %d", n1PairedPopulationResidents, m.Facts.Residents)
	}
	return nil
}

// attachN1PairedPopulation opts into the existing canonical population for the
// paired N1 component. Both environment values absent means nil (legacy tiny
// fixture). Both present must verify an actual exact-one-million-resident image
// before registering it for exactly this *testing.T. Registration is consumed
// by the parent's next seedInventoryFreshPopulation(..., true) constructor.
func attachN1PairedPopulation(t *testing.T) *QualificationPopulationManifest {
	t.Helper()
	dir := os.Getenv(n1PairedPopulationDirectoryEnv)
	pin := os.Getenv(n1PairedPopulationManifestEnv)
	enabled, err := validateN1PairedPopulationSelection(dir, pin)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		return nil
	}
	verified, err := readQualificationPopulation(dir, pin)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateN1PairedPopulationManifest(&verified); err != nil {
		t.Fatal(err)
	}
	registered := UseQualificationPopulation(t, dir, pin)
	if registered.DatabaseSHA256 != verified.DatabaseSHA256 || registered.Facts.Residents != verified.Facts.Residents {
		t.Fatal("paired population changed between verification and registration")
	}
	manifest := registered
	return &manifest
}
