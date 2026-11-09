// Package testtier sorts slow tests out of the default suite.
//
// The default tier is what every lane runs on every change: it must finish in
// about five minutes on the G12. Tests that
// need real FFmpeg media, a real Live TV or Library Channel pipeline, wait on
// the wall clock for more than two seconds, or project a large fixture (tens of
// thousands of titles) per test belong to the media tier,
// which the integrator runs once per merge batch (scripts/runner-test.sh
// --tier all, or --full). Scale proofs keep PORTICO_PERFORMANCE_TIER.
//
// PORTICO_TEST_TIER: unset or "default" skips media tests; "media" or "all"
// runs them. Any other value fails the test, so a typo never silently skips.
package testtier

import (
	"os"
	"testing"
)

// Env is the variable that selects the tier.
const Env = "PORTICO_TEST_TIER"

// MediaEnabled reports whether media-tier tests run in this process.
func MediaEnabled() (bool, error) {
	switch v := os.Getenv(Env); v {
	case "", "default":
		return false, nil
	case "media", "all":
		return true, nil
	default:
		return false, &unknownTier{v}
	}
}

type unknownTier struct{ value string }

func (e *unknownTier) Error() string {
	return Env + "=" + e.value + " is not a tier (default, media, all)"
}

// Media skips t unless the media tier is selected. why says what makes the
// test slow (real FFmpeg, a live pipeline, a wall-clock window); it is printed
// in the skip message so a reader knows where the test went.
func Media(t testing.TB, why string) {
	t.Helper()
	on, err := MediaEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Skipf("media tier (%s): set %s=media or all, or run scripts/runner-test.sh --tier all", why, Env)
	}
}
