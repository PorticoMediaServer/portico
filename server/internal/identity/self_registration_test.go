package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSelfRegistrationIsClosedUntilTheOwnerOpensIt(t *testing.T) {
	ctx := context.Background()
	s, _, _ := directFixture(t)
	// An unset policy, and any value the owner did not choose, read as off. A
	// policy nobody set must never open a server to the network it sits on.
	if s.SelfRegistration() != SelfRegistrationOff {
		t.Fatal("an unset policy did not read as off")
	}
	if _, e := s.db.Exec(`INSERT INTO configuration VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, selfRegistrationKey, "yes-please"); e != nil {
		t.Fatal(e)
	}
	if s.SelfRegistration() != SelfRegistrationOff {
		t.Fatal("a corrupt policy value did not read as off")
	}
	if _, e := s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!"}, true); !errors.Is(e, ErrRegistrationClosed) {
		t.Fatalf("registration succeeded while closed: %v", e)
	}
	if e := s.SetSelfRegistration("sometimes"); !errors.Is(e, ErrDirectInput) {
		t.Fatal("an unknown mode was stored")
	}
}

func TestOpenRegistrationCreatesAMemberWithNoLibraries(t *testing.T) {
	ctx := context.Background()
	s, db, _ := directFixture(t)
	if e := s.SetSelfRegistration(SelfRegistrationOpen); e != nil {
		t.Fatal(e)
	}
	out, e := s.Register(ctx, SelfRegistrationRequest{Username: "Guest", Password: "Testing1!"}, true)
	if e != nil {
		t.Fatal(e)
	}
	// Reaching the server is not evidence of what its owner wants shared.
	if out.Account.Role != "member" {
		t.Fatalf("self-registration produced role %q", out.Account.Role)
	}
	if len(out.Account.AllowedLibraries) != 0 {
		t.Fatalf("a new account arrived with libraries: %v", out.Account.AllowedLibraries)
	}
	if out.Account.Username != "guest" {
		t.Fatalf("username was not folded to lower case: %q", out.Account.Username)
	}
	if out.AccountToken == "" || out.Session == nil {
		t.Fatal("registration did not sign the new account in")
	}
	// There is still exactly one owner.
	var owners int
	if e = db.QueryRow(`SELECT count(*) FROM direct_memberships WHERE role='owner'`).Scan(&owners); e != nil {
		t.Fatal(e)
	}
	if owners != 1 {
		t.Fatalf("owner count %d", owners)
	}
	if _, e = s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!"}, true); !errors.Is(e, ErrRegistrationTaken) {
		t.Fatalf("a duplicate username was accepted: %v", e)
	}
	for name, request := range map[string]SelfRegistrationRequest{
		"weak password":   {Username: "second", Password: "short"},
		"empty username":  {Username: "  ", Password: "Testing1!"},
		"spaced username": {Username: "two words", Password: "Testing1!"},
		"path username":   {Username: "a/b", Password: "Testing1!"},
	} {
		if _, e = s.Register(ctx, request, true); !errors.Is(e, ErrDirectInput) {
			t.Errorf("%s: want ErrDirectInput, got %v", name, e)
		}
	}
	// The direct-account policy has a length and byte bound, not composition
	// requirements. Self-registration must use that same policy.
	if _, e = s.Register(ctx, SelfRegistrationRequest{Username: "lowercase", Password: "alllowercase123"}, true); e != nil {
		t.Fatalf("a valid password without uppercase or symbols was refused: %v", e)
	}
}

func TestInviteOnlyRegistrationRefusesWithoutARedeemedInvitation(t *testing.T) {
	ctx := context.Background()
	s, _, _ := directFixture(t)
	if e := s.SetSelfRegistration(SelfRegistrationInvite); e != nil {
		t.Fatal(e)
	}
	// Invitations are owned by the administration surface. While no redeemer is
	// installed, invite-only must refuse everything rather than degrading to open.
	if _, e := s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!", InvitationCode: "anything"}, true); !errors.Is(e, ErrRegistrationInvite) {
		t.Fatalf("invite-only accepted a code with no redeemer: %v", e)
	}
	refusal := errors.New("no such invitation")
	spent := 0
	s.RedeemInvitation = func(_ context.Context, tx *sql.Tx, code, account string) ([]string, error) {
		if tx == nil || account == "" {
			t.Fatalf("the redeemer was called outside the registration: tx=%v account=%q", tx != nil, account)
		}
		if code != "good" {
			return nil, refusal
		}
		spent++
		return []string{"library-one"}, nil
	}
	// A name that is already taken is refused before the invitation is looked at.
	if _, e := s.Register(ctx, SelfRegistrationRequest{Username: "owner", Password: "Testing1!", InvitationCode: "good"}, true); !errors.Is(e, ErrRegistrationTaken) || spent != 0 {
		t.Fatalf("a taken username: %v, invitation spent %d times", e, spent)
	}
	if _, e := s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!"}, true); !errors.Is(e, ErrRegistrationInvite) {
		t.Fatal("invite-only accepted an empty code")
	}
	if _, e := s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!", InvitationCode: "bad"}, true); !errors.Is(e, refusal) {
		t.Fatal("the redeemer's refusal was not surfaced")
	}
	out, e := s.Register(ctx, SelfRegistrationRequest{Username: "guest", Password: "Testing1!", InvitationCode: "good"}, true)
	if e != nil {
		t.Fatal(e)
	}
	// The invitation, not the request, decides the libraries.
	if out.Account.Role != "member" || len(out.Account.AllowedLibraries) != 1 || out.Account.AllowedLibraries[0] != "library-one" {
		t.Fatalf("invited account %+v", out.Account)
	}
}

func TestCapabilitiesDescribeTheServerAndNotAnAccount(t *testing.T) {
	ctx := context.Background()
	s, db, _ := familyFixture(t)
	if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
		t.Fatal(e)
	}
	secret, e := s.BrowserSetupToken(ctx)
	if e != nil {
		t.Fatal(e)
	}
	// Complete the real Local setup. A hand-seeded account is not a completed
	// onboarding fixture and must not make the producer invent readiness.
	if _, e = s.SetupWithOptions(ctx, SetupOptions{RequestID: "66666666-6666-4666-8666-666666666666", SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Fixture"}, true); e != nil {
		t.Fatal(e)
	}
	out, e := s.Capabilities(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if out.ServerID != s.ID() || out.SetupRequired {
		t.Fatalf("capabilities %+v", out)
	}
	if out.SelfRegistration != SelfRegistrationOff {
		t.Fatalf("self registration reported as %q", out.SelfRegistration)
	}
	for _, method := range out.Methods {
		if method == "self-registration" {
			t.Fatal("a closed server advertised self-registration")
		}
	}
	if e = s.SetSelfRegistration(SelfRegistrationOpen); e != nil {
		t.Fatal(e)
	}
	if out, e = s.Capabilities(ctx); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, method := range out.Methods {
		found = found || method == "self-registration"
	}
	if !found {
		t.Fatal("an open server did not advertise self-registration")
	}
}
