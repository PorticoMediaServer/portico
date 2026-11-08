package writer

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// The experiment flag is off by default and is the ONLY permitted difference from
// the adopted phase 1a policy.
func TestCanonicalPassiveDeferFlagIsOffByDefaultAndOnlyChangesItself(t *testing.T) {
	t.Setenv("PORTICO_GV_PASSIVE_DEFER_MS", "")
	if canonicalN1Maintenance().policy != governor.Phase1aWALPolicy() {
		t.Fatal("default maintenance policy must be exactly the adopted policy")
	}
	t.Setenv("PORTICO_GV_PASSIVE_DEFER_MS", "250")
	p := canonicalN1Maintenance().policy
	if p.PassiveDeferMax != 250*time.Millisecond {
		t.Fatal("flag not applied", p)
	}
	p.PassiveDeferMax = 0
	if p != governor.Phase1aWALPolicy() {
		t.Fatal("the flag changed something besides itself")
	}
	for _, bad := range []string{"-1", "5001", "x"} {
		t.Setenv("PORTICO_GV_PASSIVE_DEFER_MS", bad)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%q must be refused", bad)
				}
			}()
			canonicalPassiveDefer()
		}()
	}
}
