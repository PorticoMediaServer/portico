package hosted

import (
	"context"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

const pendingSetupID = "33333333-3333-4333-8333-333333333333"

func pendingClaimFixture(t *testing.T) (*claimFixture, string) {
	secret := identity.Token()
	f := newClaimFixture(t, func(f *claimFixture) {
		if _, e := f.db.Exec(`DELETE FROM accounts;DELETE FROM onboarding_state_v1`); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(storagepaths.Path(f.state, "setup-token"), []byte(secret), 0600); e != nil {
			t.Fatal(e)
		}
		out, e := f.id.SetupWithOptions(context.Background(), identity.SetupOptions{RequestID: pendingSetupID, SetupToken: secret, Name: "Account server"}, true)
		if e != nil || out.Pending == nil {
			t.Fatal("pending setup", e)
		}
		f.setupOwner = &networking.LocalOwner{SetupOperationID: pendingSetupID, Epoch: out.Pending.Revision}
		f.id.SetupClaimReadyTx = networking.SetupClaimInstalledTx
	})
	return f, secret
}
func TestAccountSetupActualClaimAndVerifiedIdentityCreatePasswordlessOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, secret := pendingClaimFixture(t)
	ctx := context.Background()
	var count int
	if e := f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); e != nil || count != 0 {
		t.Fatal("claim fabricated local owner", e)
	}
	wrong, e := verify(f, freshClaims(f, "unrelated-account"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.id.CompleteAccountSetup(ctx, pendingSetupID, secret, wrong); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("wrong account claimed owner", e)
	}
	claims := freshClaims(f, "account")
	claims.Username = "verified_username"
	claims.DisplayName = "Private display name"
	who, e := verify(f, claims)
	if e != nil {
		t.Fatal(e)
	}
	out, e := f.id.CompleteAccountSetup(ctx, pendingSetupID, secret, who)
	if e != nil || out.Session == nil {
		t.Fatal("actual pending claim completion", e)
	}
	if out.Account.Username != "verified_username" || out.Account.HostedAccountID != "account" || out.Account.Role != identity.TierOwner || out.ResetFileOwnerSession {
		t.Fatal("owner identity/authority fabricated")
	}
	var hash []byte
	var profile string
	if e = f.db.QueryRow(`SELECT password_hash,profile_id FROM accounts WHERE id=?`, out.Account.ID).Scan(&hash, &profile); e != nil || len(hash) != 0 || profile == "" {
		t.Fatal("local password fallback", e)
	}
	if _, e = f.id.DirectLogin(ctx, "verified_username", "Testing1!"); !errors.Is(e, identity.ErrSignInKindPortico) {
		t.Fatal("Portico owner accepts password", e)
	}
	if _, e = f.id.Authenticate(out.Session.AccessToken); e != nil {
		t.Fatal(e)
	}
	resumed, e := f.id.ResumeSetup(ctx, pendingSetupID, secret, false)
	if e != nil || resumed.Pending != nil || resumed.AccessToken != out.Session.AccessToken {
		t.Fatal("completed setup not resumable", e)
	}
	if _, e = f.id.CompleteAccountSetup(ctx, pendingSetupID, secret, who); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("setup completion replay", e)
	}
}
func TestAccountSetupCompletionRechecksRevokedSignerAndTerminalClaim(t *testing.T) {
	for _, kind := range []string{"signer", "claim"} {
		t.Run(kind, func(t *testing.T) {
			f, secret := pendingClaimFixture(t)
			who, e := verify(f, freshClaims(f, "account"))
			if e != nil {
				t.Fatal(e)
			}
			if kind == "signer" {
				_, e = f.db.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES(?)`, who.SignerKeyID)
			} else {
				_, e = f.db.Exec(`UPDATE networking_claim_intents SET stage='cancel_pending' WHERE operation_id=?`, f.installed.OperationID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.id.CompleteAccountSetup(context.Background(), pendingSetupID, secret, who); e == nil {
				t.Fatal("revoked authority completed setup")
			}
			var n int
			if e = f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&n); e != nil || n != 0 {
				t.Fatal("failed setup created owner", e)
			}
		})
	}
}
