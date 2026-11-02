package identity_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSetupCodeAdmitsOnceAtItsOriginWhileUnconfigured(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(s)
	first, err := s.RotateSetupCode(ctx)
	if err != nil || first == "" {
		t.Fatal("rotate", err)
	}
	second, err := s.RotateSetupCode(ctx)
	if err != nil || second == first {
		t.Fatal("each start makes a new code", err)
	}
	const peer, origin = "203.0.113.9", "http://media.example.com:32500"
	if _, err = s.AdmitSetupCode(ctx, first); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("the earlier code stopped working", err)
	}
	next, err := s.AdmitSetupCode(ctx, strings.ToLower(second))
	if err != nil || next == "" || next == second {
		t.Fatal("admit", next, err)
	}
	if _, err = s.AdmitSetupCode(ctx, second); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("a used code works once", err)
	}
	// A correct code admits nothing by itself: the setup token it issues is bound to the
	// peer and origin, and only all three together admit.
	token, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SetupTokenAdmitted(ctx, token, peer, origin) {
		t.Fatal("a token admitted before it was bound")
	}
	if err = s.AdmitSetupToken(ctx, token, peer, origin); err != nil {
		t.Fatal("bind", err)
	}
	if !s.SetupTokenAdmitted(ctx, token, peer, origin) {
		t.Fatal("admitted")
	}
	for name, try := range map[string][3]string{
		"no token (a neighbour behind the same address)": {"", peer, origin},
		"another token":  {"another-setup-token", peer, origin},
		"another origin": {token, peer, "http://evil.example:32500"},
		"another peer":   {token, "198.51.100.1", origin},
	} {
		if s.SetupTokenAdmitted(ctx, try[0], try[1], try[2]) {
			t.Fatal("admitted with", name)
		}
	}
	if _, err = s.AdmitSetupCode(ctx, next); err != nil {
		t.Fatal("the replacement code works", err)
	}
	// Set up: nothing is admitted, nothing more is bound and no code is made.
	if _, err = s.Setup(token, "owner", "A-long-local-password", "Den"); err != nil {
		t.Fatal("local setup", err)
	}
	if s.SetupTokenAdmitted(ctx, token, peer, origin) {
		t.Fatal("an admission outlived setup")
	}
	if err = s.AdmitSetupToken(ctx, token, peer, origin); !errors.Is(err, identity.ErrConflict) {
		t.Fatal("a set-up server binds no admission", err)
	}
	if code, err := s.RotateSetupCode(ctx); err != nil || code != "" {
		t.Fatal("a set-up server makes no setup code", code, err)
	}
}
