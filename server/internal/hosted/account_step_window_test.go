package hosted

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// The clients read a Portico Account step from packages/contracts; the server
// from claimweb. A setup, an owner move and a member sign-in return must agree.
func TestClientsAndServerShareOneAccountStepWindow(t *testing.T) {
	raw, err := os.ReadFile("../../../packages/contracts/src/account-step.ts")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`export const ACCOUNT_STEP_WINDOW_MS = (\d+) \* 60_000;`).FindSubmatch(raw)
	if match == nil {
		t.Fatal("ACCOUNT_STEP_WINDOW_MS is not written as minutes * 60_000")
	}
	minutes, _ := strconv.Atoi(string(match[1]))
	if time.Duration(minutes)*time.Minute != claimweb.AccountStepLifetime {
		t.Fatalf("clients wait %d minutes, the server %v", minutes, claimweb.AccountStepLifetime)
	}
	if challengeLifetime != claimweb.AccountStepLifetime || claimweb.OwnerStartLifetime != claimweb.AccountStepLifetime || claimweb.SetupStartLifetime != claimweb.AccountStepLifetime {
		t.Fatal("a sign-in challenge, owner start or setup start left the shared window")
	}
}
