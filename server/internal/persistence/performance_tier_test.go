package persistence

import (
	"os"
	"testing"
)

// scaleTier reports whether the release or deep performance tier is running
// (PORTICO_PERFORMANCE_TIER=release|deep). Scale and capacity proofs that take
// more than a few seconds run there; the default suite keeps a cheap guard of
// the same property (a query plan, a statement count or a small-N variant).
func scaleTier() bool {
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
		return true
	}
	return false
}

// requireScaleTier skips a scale proof outside the release and deep tiers.
func requireScaleTier(t *testing.T) {
	t.Helper()
	if !scaleTier() {
		t.Skip("scale proof: runs with PORTICO_PERFORMANCE_TIER=release or deep")
	}
}
