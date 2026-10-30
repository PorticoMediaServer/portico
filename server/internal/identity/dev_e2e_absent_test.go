//go:build !devtrust || release

package identity

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A build without the devtrust tag, and every release build, has no
// password-free development owner, even on a state directory named for e2e.
func TestDevE2EOwnerAbsentOutsideDevtrust(t *testing.T) {
	if devE2EUsername != nil || devE2ESeed != nil || devE2ESignIn != nil {
		t.Fatal("development e2e hooks are set in a non-devtrust build")
	}
	state := filepath.Join(t.TempDir(), "e2e-state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SeedDevE2EOwner(context.Background()); err != nil || !s.SetupRequired() {
		t.Fatalf("seeded an owner: %v", err)
	}
	if _, ok := s.DevE2EUsername(); ok {
		t.Fatal("offers a development owner")
	}
	if _, err = s.DevE2ESignIn(context.Background(), "e2e-owner"); err != ErrUnauthorized {
		t.Fatalf("password-free sign-in: %v", err)
	}
}
