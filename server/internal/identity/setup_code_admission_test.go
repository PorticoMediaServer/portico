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
	if _, err = s.AdmitSetupCode(ctx, first, peer, origin); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("the earlier code stopped working", err)
	}
	if s.SetupPeerAdmitted(ctx, peer, origin) {
		t.Fatal("a wrong code admitted the peer")
	}
	next, err := s.AdmitSetupCode(ctx, strings.ToLower(second), peer, origin)
	if err != nil || next == "" || next == second {
		t.Fatal("admit", next, err)
	}
	if !s.SetupPeerAdmitted(ctx, peer, origin) {
		t.Fatal("admitted")
	}
	if s.SetupPeerAdmitted(ctx, peer, "http://evil.example:32500") || s.SetupPeerAdmitted(ctx, "198.51.100.1", origin) {
		t.Fatal("the admission is for this peer at this origin only")
	}
	if _, err = s.AdmitSetupCode(ctx, second, "198.51.100.1", origin); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("a used code works once", err)
	}
	if _, err = s.AdmitSetupCode(ctx, next, "198.51.100.1", origin); err != nil {
		t.Fatal("the replacement code works", err)
	}
	// Set up: nothing is admitted and no code is made.
	token, err := s.BrowserSetupToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Setup(token, "owner", "A-long-local-password", "Den"); err != nil {
		t.Fatal("local setup", err)
	}
	if s.SetupPeerAdmitted(ctx, peer, origin) {
		t.Fatal("an admission outlived setup")
	}
	if code, err := s.RotateSetupCode(ctx); err != nil || code != "" {
		t.Fatal("a set-up server makes no setup code", code, err)
	}
}
