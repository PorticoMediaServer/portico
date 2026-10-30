package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// The policy is applied by the function that creates a session, so it holds
// for every caller of it, including ones no HTTP route test knows about.
func TestIssuanceAppliesTheRemoteSignInPolicy(t *testing.T) {
	s, db, _ := familyFixture(t)
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('guest','guest',X'00','guest-profile',1)`); err != nil {
		t.Fatal(err)
	}
	issue := func(ctx context.Context, account, profile, role string) error {
		_, err := s.IssueWithDevice(ctx, account, profile, "local", role, 1)
		return err
	}
	families := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM authorization_session_families`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, c := range []struct {
		name          string
		admission     SignInAdmission
		owner, member bool
	}{
		{"allow, outside", SignInAdmission{Remote: true, Policy: RemoteSignInAllow}, true, true},
		{"owner only, outside", SignInAdmission{Remote: true, Policy: RemoteSignInOwnerOnly}, true, false},
		{"off, outside", SignInAdmission{Remote: true, Policy: RemoteSignInOff}, false, false},
		{"off, at home", SignInAdmission{Remote: false, Policy: RemoteSignInOff}, true, true},
		{"a value this build does not know, outside", SignInAdmission{Remote: true, Policy: "friends"}, false, false},
	} {
		ctx := WithSignInAdmission(context.Background(), c.admission)
		before := families()
		// The owner's first session after a password is an account session; it
		// is still the owner signing in.
		for _, role := range []string{"owner", "account"} {
			err := issue(ctx, "account", "profile", role)
			if c.owner && err != nil || !c.owner && !errors.Is(err, ErrRemoteSignIn) {
				t.Fatalf("%s: owner as %s: %v", c.name, role, err)
			}
		}
		err := issue(ctx, "guest", "guest-profile", "member")
		if c.member && err != nil || !c.member && !errors.Is(err, ErrRemoteSignIn) {
			t.Fatalf("%s: member: %v", c.name, err)
		}
		want := before
		if c.owner {
			want += 2
		}
		if c.member {
			want++
		}
		if got := families(); got != want {
			t.Fatalf("%s: %d sessions exist, want %d: a refusal must create nothing", c.name, got, want)
		}
		// A sign-in the device already holds is continued whatever the policy.
		if err = issue(continuedSignIn(ctx), "guest", "guest-profile", "member"); err != nil {
			t.Fatalf("%s: continuing an existing sign-in: %v", c.name, err)
		}
	}
	// The refusal is not an authentication failure: clients end a sign-in on those.
	if errors.Is(ErrRemoteSignIn, ErrUnauthorized) {
		t.Fatal("the remote sign-in refusal reads as a failed credential")
	}
}

func TestRemoteSignInClosedOnlyWhenNobodyIsAdmitted(t *testing.T) {
	for _, c := range []struct {
		admission SignInAdmission
		closed    bool
	}{
		{SignInAdmission{Remote: true, Policy: RemoteSignInOff}, true},
		{SignInAdmission{Remote: true, Policy: "friends"}, true},
		{SignInAdmission{Remote: true, Policy: RemoteSignInOwnerOnly}, false},
		{SignInAdmission{Remote: true, Policy: RemoteSignInAllow}, false},
		{SignInAdmission{Remote: false, Policy: RemoteSignInOff}, false},
	} {
		if got := RemoteSignInClosed(WithSignInAdmission(context.Background(), c.admission)); got != c.closed {
			t.Fatalf("%+v: closed=%v, want %v", c.admission, got, c.closed)
		}
	}
	if RemoteSignInClosed(context.Background()) {
		t.Fatal("a caller with no admission attached was treated as remote")
	}
}

// The account rule runs before a session is created and before a renewal
// rotates anything.
func TestAccountRuleRefusesIssuanceAndRenewalWithoutEndingTheSession(t *testing.T) {
	s, db, _ := familyFixture(t)
	closed := errors.New("outside the hours")
	open := true
	ctx := WithAccountAdmitter(context.Background(), func(_ context.Context, tx *sql.Tx, account string) error {
		if tx == nil || account != "account" {
			t.Fatalf("rule called with tx=%v account=%q", tx != nil, account)
		}
		if open {
			return nil
		}
		return closed
	})
	registration := DeviceRegistration{InstallationID: "installation-rule-00000000000000000000000001", Name: "Phone", Platform: "ios", App: "portico"}
	ctx, err := WithIssuingDevice(ctx, registration, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.IssueWithDevice(ctx, "account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	open = false
	if _, err = s.IssueWithDevice(ctx, "account", "profile", "local", "owner", 1); !errors.Is(err, closed) {
		t.Fatalf("issuance while closed: %v", err)
	}
	if _, err = s.RefreshSession(ctx, session.RefreshToken, registration.InstallationID, "renewal-0001"); !errors.Is(err, closed) {
		t.Fatalf("renewal while closed: %v", err)
	}
	var revoked, generation int
	if err = db.QueryRow(`SELECT revoked,current_generation FROM authorization_session_families WHERE id=?`, session.SessionFamilyID).Scan(&revoked, &generation); err != nil || revoked != 0 || generation != 1 {
		t.Fatalf("a refused renewal changed the session: revoked=%d generation=%d %v", revoked, generation, err)
	}
	if _, err = s.AuthenticateContext(context.Background(), session.AccessToken); err != nil {
		t.Fatalf("the session stopped working: %v", err)
	}
	open = true
	renewed, err := s.RefreshSession(ctx, session.RefreshToken, registration.InstallationID, "renewal-0002")
	if err != nil || renewed.AccessToken == "" {
		t.Fatalf("the same credential once open again: %v", err)
	}
}
