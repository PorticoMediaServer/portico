//go:build devtrust && !release

package identity

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func devE2EService(t *testing.T, dir string) *Service {
	t.Helper()
	state := filepath.Join(t.TempDir(), dir)
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	// Setup records its remote access choices in the same transaction (O-169).
	stubSetupRemote(s)
	return s
}

// On a throwaway e2e state directory a devtrust build seeds the test owner and
// signs it in without a password; on any other state it does neither.
func TestDevE2EOwnerSeedsAndSignsInOnlyOnE2EState(t *testing.T) {
	ctx := context.Background()
	s := devE2EService(t, "e2e-state")
	if err := s.SeedDevE2EOwner(ctx); err != nil || s.SetupRequired() {
		t.Fatalf("seed: %v", err)
	}
	if err := s.SeedDevE2EOwner(ctx); err != nil {
		t.Fatalf("a second seed on an initialized server: %v", err)
	}
	name, ok := s.DevE2EUsername()
	if !ok || name != "e2e-owner" {
		t.Fatalf("offered owner: %q %v", name, ok)
	}
	// The test sign-in arrives the way an app's does, from a device it has never used.
	installation := Token()
	deviceCtx, err := WithIssuingDevice(ctx, registration(installation), "203.0.113.7:51314")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.DevE2ESignIn(deviceCtx, name)
	if err != nil || out.Session == nil || out.Session.AccessToken == "" {
		t.Fatalf("sign-in: %+v %v", out, err)
	}
	var devices int
	if err = s.db.QueryRow(`SELECT count(*) FROM identity_devices WHERE installation_id=?`, installation).Scan(&devices); err != nil || devices != 1 {
		t.Fatalf("the test sign-in registered %d devices: %v", devices, err)
	}
	// ...but it is no person's device: no "New device signed in" for anyone.
	for _, audience := range []string{"profile", "account-admin"} {
		if got := noticeCount(t, s.db, "security.new-device", audience); got != 0 {
			t.Fatalf("a test sign-in raised %d %s new-device notices", got, audience)
		}
	}
	if _, err = s.DevE2ESignIn(ctx, "someone-else"); err != ErrUnauthorized {
		t.Fatalf("another username: %v", err)
	}

	plain := devE2EService(t, "state")
	if err = plain.SeedDevE2EOwner(ctx); err != nil || !plain.SetupRequired() {
		t.Fatalf("seeded an ordinary state: %v", err)
	}
	if _, ok = plain.DevE2EUsername(); ok {
		t.Fatal("an ordinary state offers a development owner")
	}
	if _, err = plain.DevE2ESignIn(ctx, "e2e-owner"); err != ErrUnauthorized {
		t.Fatalf("password-free sign-in on an ordinary state: %v", err)
	}
}
