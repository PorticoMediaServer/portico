package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"unicode/utf8"
)

// These pin the boundaries of the accepting transaction behind
// TransferDirectOwnershipToNewAccount: who may call it, that the sign-in policy's
// minimum length is enforced inside it, and that a server which is not on local
// sign-in refuses before writing. Setup uses the existing directFixture and real
// producers (CreateDirectProfile, SelectDirectProfile, CreateDirectMember,
// UpdateDirectMember, ChangeSignInPolicy); no SQL bypasses the guard under test.

func TestNewLocalOwnerRefusesSecondaryProfileSession(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	snapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, "Second", "blue")
	if err != nil {
		t.Fatal(err)
	}
	second := ""
	for _, profile := range snapshot.Profiles {
		if profile.ID != owner.Account.PrimaryProfileID {
			second = profile.ID
		}
	}
	if second == "" {
		t.Fatal("fixture did not create a second profile")
	}
	selection, err := s.SelectDirectProfile(ctx, owner.AccountToken, second, DirectSelection{})
	if err != nil || selection.Session.AccessToken == "" {
		t.Fatal("secondary-profile session was not issued", err)
	}
	accounts, profiles := rowCount(t, db, `SELECT count(*) FROM accounts`), rowCount(t, db, `SELECT count(*) FROM direct_profiles`)
	if err = s.TransferDirectOwnershipToNewAccount(ctx, selection.Session.AccessToken,
		LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: "New-password2!"},
		"Testing1!", owner.Account.Revision); err == nil {
		t.Fatal("a secondary-profile session created and transferred ownership")
	}
	if got := rowCount(t, db, `SELECT count(*) FROM accounts`); got != accounts {
		t.Fatal("refused transfer created an account", accounts, got)
	}
	if got := rowCount(t, db, `SELECT count(*) FROM direct_profiles`); got != profiles {
		t.Fatal("refused transfer created a profile", profiles, got)
	}
	principal, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierOwner {
		t.Fatal("refused transfer changed the owner", principal.Role, err)
	}
}

func TestNewLocalOwnerRefusesDisabledMemberSession(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{
		Username: "member", Name: "Member", Password: "Member-password1!", AllowedLibraries: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.DirectLogin(ctx, "member", "Member-password1!")
	if err != nil || login.Session == nil {
		t.Fatal("member could not sign in", err)
	}
	if err = s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{
		Username: "member", Name: "Member", AllowedLibraries: []string{}, ExpectedRevision: member.Revision, Disabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	accounts := rowCount(t, db, `SELECT count(*) FROM accounts`)
	if err = s.TransferDirectOwnershipToNewAccount(ctx, login.Session.AccessToken,
		LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: "New-password2!"},
		"Member-password1!", login.Account.Revision); err == nil {
		t.Fatal("a disabled member created and transferred ownership")
	}
	if got := rowCount(t, db, `SELECT count(*) FROM accounts`); got != accounts {
		t.Fatal("refused transfer created an account", accounts, got)
	}
	principal, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierOwner {
		t.Fatal("refused transfer changed the owner", principal.Role, err)
	}
}

func TestNewLocalOwnerHonoursSignInPolicyMinimum(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	policy, err := s.ReadSignInPolicy(ctx, owner.AccountToken)
	if err != nil {
		t.Fatal(err)
	}
	values := policy.Values
	values.PasswordMinLength = 16
	if _, err = s.ChangeSignInPolicy(ctx, owner.AccountToken, SignInPolicyPatch{ExpectedRevision: policy.Revision, Values: values}); err != nil {
		t.Fatal(err)
	}
	// Twelve characters pass the cheap field check and the default minimum of
	// eight, so only the accepting transaction's policy read can refuse them.
	const offered = "Newpass1234!"
	if !directPassword(offered) || utf8.RuneCountInString(offered) >= 16 {
		t.Fatal("fixture password does not isolate the policy check")
	}
	accounts := rowCount(t, db, `SELECT count(*) FROM accounts`)
	err = s.TransferDirectOwnershipToNewAccount(ctx, owner.AccountToken,
		LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: offered},
		"Testing1!", owner.Account.Revision)
	if !errors.Is(err, ErrDirectInput) {
		t.Fatalf("policy minimum was not enforced in the accepting transaction: %v", err)
	}
	if got := rowCount(t, db, `SELECT count(*) FROM accounts`); got != accounts {
		t.Fatal("policy refusal created an account", accounts, got)
	}
	principal, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierOwner {
		t.Fatal("policy refusal changed the owner", principal.Role, err)
	}
}

func TestNewLocalOwnerRefusesWhenSignInKindIsNotLocal(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	// The baseline fence refuses a bare kind flip while password hashes exist, so
	// use the established one-kind setup other Portico tests already rely on.
	selectPorticoFixture(t, s)
	accounts := rowCount(t, db, `SELECT count(*) FROM accounts`)
	err := s.TransferDirectOwnershipToNewAccount(ctx, owner.AccountToken,
		LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: "New-password2!"},
		"Testing1!", owner.Account.Revision)
	if !errors.Is(err, ErrSignInKindPortico) {
		t.Fatalf("non-local kind was not refused up front: %v", err)
	}
	if got := rowCount(t, db, `SELECT count(*) FROM accounts`); got != accounts {
		t.Fatal("kind refusal created an account", accounts, got)
	}
}

func rowCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
