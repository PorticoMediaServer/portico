package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestNewLocalOwnerCreationAndTransferAreOneTransaction(t *testing.T) {
	s, db, owner := directFixture(t)
	ctx := context.Background()
	input := LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: "New-password2!"}
	refusal := errors.New("owner hook refused")
	calls := 0
	s.OnOwnerChangedTx = func(ctx context.Context, tx *sql.Tx) error {
		calls++
		var role string
		if err := tx.QueryRowContext(ctx, `SELECT m.role FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.username='newowner'`).Scan(&role); err != nil {
			return err
		}
		if role != TierOwner {
			t.Fatalf("hook before ownership transfer: %s", role)
		}
		return refusal
	}
	if err := s.TransferDirectOwnershipToNewAccount(ctx, owner.AccountToken, input, "Testing1!", owner.Account.Revision); !errors.Is(err, refusal) {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM accounts WHERE username='newowner'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("new account escaped rollback", count, err)
	}
	principal, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierOwner {
		t.Fatal("failed transfer changed original owner", principal.Role, err)
	}
	refusal = nil
	if err = s.TransferDirectOwnershipToNewAccount(ctx, owner.AccountToken, input, "Testing1!", owner.Account.Revision); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("missing accepting hook", calls)
	}
	principal, err = s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierMember {
		t.Fatal("former owner was not demoted on existing session", principal.Role, err)
	}
	login, err := s.DirectLogin(ctx, input.Username, input.Password)
	if err != nil || login.Session == nil || login.Account.Role != TierOwner || login.Account.HostedAccountID != "" {
		t.Fatal("new local owner cannot sign in", err)
	}
	if len(login.Profiles) != 1 || login.Profiles[0].Name != input.Name {
		t.Fatal("normal primary profile was not created")
	}
}

func TestNewLocalOwnerRefusalsCreateNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, scenario := range []string{"empty-current", "wrong-current", "stale-owner", "existing-name", "invalid-new-password", "member"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, owner := directFixture(t)
			ctx := context.Background()
			input := LocalOwnerCreate{Username: "newowner", Name: "New owner", Password: "New-password2!"}
			password, revision, bearer := "Testing1!", owner.Account.Revision, owner.AccountToken
			switch scenario {
			case "empty-current":
				password = ""
			case "wrong-current":
				password = "Wrong-password1!"
			case "stale-owner":
				revision++
			case "existing-name":
				input.Username = owner.Account.Username
			case "invalid-new-password":
				input.Password = "short"
			case "member":
				_, err := s.CreateDirectMember(ctx, bearer, DirectMemberCreate{Username: "member", Name: "Member", Password: "Member-password1!", AllowedLibraries: []string{}})
				if err != nil {
					t.Fatal(err)
				}
				login, err := s.DirectLogin(ctx, "member", "Member-password1!")
				if err != nil {
					t.Fatal(err)
				}
				bearer, password, revision = login.AccountToken, "Member-password1!", login.Account.Revision
			}
			var before int
			if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := s.TransferDirectOwnershipToNewAccount(ctx, bearer, input, password, revision); err == nil {
				t.Fatal("refused ownership accepted")
			}
			var after int
			if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&after); err != nil || after != before {
				t.Fatal("refusal created an account", before, after, err)
			}
			principal, err := s.Authenticate(owner.Session.AccessToken)
			if err != nil || principal.Role != TierOwner {
				t.Fatal("refusal changed original owner", principal.Role, err)
			}
		})
	}
}
