package hosted

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

func TestClaimAcknowledgmentImmediatelyAdmitsOwner(t *testing.T) {
	f := newClaimFixture(t, func(f *claimFixture) {
		// Run the same first pass as the control loop before a claim exists;
		// it finds no credential and settles on its six-hour safety sleep.
		if err := f.runner.Do(context.Background(), func(ctx context.Context) error {
			_, err := f.service.currentControlStep(ctx)
			if !errors.Is(err, networking.ErrStale) {
				t.Fatal("unclaimed control step", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	// No post-install control step, status readback, topology change or wake has run.
	if !f.installed.InstallationAcknowledged {
		t.Fatal("claim not acknowledged")
	}
	who, err := verify(f, freshClaims(f, "account"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := f.id.PorticoSignIn(context.Background(), who)
	if err != nil || in.Session == nil {
		t.Fatalf("acknowledged owner refused: %+v %v", in, err)
	}
	if in.Session.Viewer.AccountID != f.owner.Viewer.AccountID || in.Session.Viewer.Role != identity.TierOwner {
		t.Fatal("claim admitted the wrong owner", in.Session.Viewer)
	}
	if _, err = f.id.Authenticate(in.Session.AccessToken); err != nil {
		t.Fatal("owner session unusable", err)
	}
}

func TestClaimInstallationPreservesExistingAccountLinks(t *testing.T) {
	for _, alreadyLinked := range []string{"owner", "another member"} {
		t.Run(alreadyLinked, func(t *testing.T) {
			f := newClaimFixture(t, func(f *claimFixture) {
				if alreadyLinked == "owner" {
					if _, err := f.db.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,'previous-account')`, f.owner.Viewer.AccountID); err != nil {
						t.Fatal(err)
					}
				} else {
					addLinkedMember(t, f, "other-local", "account")
				}
			})
			var account string
			query, key, want := `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, f.owner.Viewer.AccountID, "previous-account"
			if alreadyLinked != "owner" {
				query, key, want = `SELECT account_id FROM account_portico_links WHERE hosted_account_id=?`, "account", "other-local"
			}
			if err := f.db.QueryRow(query, key).Scan(&account); err != nil || account != want {
				t.Fatal("installation reassigned existing identity", account, err)
			}
		})
	}
}
