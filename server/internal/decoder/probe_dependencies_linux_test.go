//go:build linux

package decoder

import (
	"strings"
	"testing"
)

// The availability probe runs only the helper. Resolving dependencies of an
// empty decoder executable made it fail before bubblewrap could even start,
// including on hosts where real decoder sandboxes are available.
func TestConfinementProbeNeedsOnlyItsHelper(t *testing.T) {
	args, err := sandboxArgs(linuxSpec{Probe: true}, "", 0)
	if err != nil {
		t.Fatalf("helper-only probe must not resolve an absent decoder: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "/portico-helper --portico-decoder-inner ") {
		t.Fatal("probe must still run the real inner helper", args)
	}
}

func TestConfinementRealJobStillNeedsItsDecoder(t *testing.T) {
	if _, err := sandboxArgs(linuxSpec{}, "", 0); err == nil {
		t.Fatal("a real job must still reject an absent decoder")
	}
}
