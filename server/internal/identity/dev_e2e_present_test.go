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
	out, err := s.DevE2ESignIn(ctx, name)
	if err != nil || out.Session == nil || out.Session.AccessToken == "" {
		t.Fatalf("sign-in: %+v %v", out, err)
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
