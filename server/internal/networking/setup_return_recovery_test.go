package networking

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

)

// The recovery trace uses the real installed claim and its acknowledgement. Setup finishes
// at claim install (O-163): no certificate, HTTPS route or Hosted resolver is involved, so
// a server still without its certificate ("no certificate yet") installs exactly as one
// with it. Capture/identity callbacks are controlled fixtures; signed capture and atomic
// owner admission have separate identity tests.
func TestSetupReturnRecoveryUsesOnlyCurrentUnexpiredInstalledCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"current", "no certificate yet", "expired", "not acknowledged", "wrong note operation", "wrong note generation", "guard refused", "already installed"} {
		t.Run(mode, func(t *testing.T) {
			f, h, ctx, _ := managedHTTPSFixture(t)
			if _, err := f.db.Exec(`UPDATE networking_claim_intents SET purpose='setup',current_owner_account_id='' WHERE stage='installed'`); err != nil {
				t.Fatal(err)
			}
			v, err := f.store.InstalledIntent(ctx)
			if err != nil {
				t.Fatal(err)
			}
			note := webClaimResume{Expected: ClaimExpected{OperationID: v.OperationID, LocalGeneration: v.LocalGeneration}, Owner: "captured-setup-owner"}
			if mode == "wrong note operation" {
				note.Expected.OperationID = "different-operation"
			}
			if mode == "wrong note generation" {
				note.Expected.LocalGeneration++
			}
			raw, _ := json.Marshal(note)
			now := time.Now().UnixMilli()
			expiry := now + 60000
			if mode == "expired" {
				expiry = now - 1
			}
			if _, err = f.db.Exec(`INSERT INTO networking_web_claims(operation_id,request,watch) VALUES(?,'{}',?)`, v.OperationID, string(raw)); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`INSERT INTO identity_setup_completions(operation_id,setup_request_id,setup_revision,setup_proof,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms) VALUES(?,'setup-request',1,'captured-proof','prepared',?,X'01',zeroblob(64),X'01',zeroblob(64),'state',?,'installation','challenge',?,?)`, v.OperationID, strings.Repeat("h", 43), strings.Repeat("c", 43), now-60000, expiry); err != nil {
				t.Fatal(err)
			}
			if mode == "not acknowledged" {
				if _, err = f.db.Exec(`UPDATE networking_claim_intents SET installation_acknowledged=0`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "no certificate yet" {
				if _, err = f.db.Exec(`UPDATE networking_remote_state SET status='{}'`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "already installed" {
				if _, err = f.db.Exec(`UPDATE identity_setup_completions SET phase='installed',transaction_id='bound-transaction'`); err != nil {
					t.Fatal(err)
				}
			}
			h.transport.(*HTTPTransport).client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
				t.Fatal("setup installation called Hosted", r.URL.Path)
				return nil, nil
			})
			guard := func(context.Context, *sql.Tx) error {
				if mode == "guard refused" {
					return ErrClaimOwnerRequired
				}
				return nil
			}
			marks, calls := 0, 0
			h.UseSetupReturnResolver(func(ctx context.Context, owner, operation string) error {
				calls++
				if owner != note.Owner || operation != v.OperationID {
					t.Fatal("capture substituted")
				}
				return h.InstallSetupReturn(ctx, guard, func(ctx context.Context, tx *sql.Tx) error {
					marks++
					_, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase='installed' WHERE operation_id=? AND phase='prepared'`, operation)
					return err
				})
			})
			h.resumeSetupReturnBinding(context.Background(), func(owner string) ClaimRequestGuard {
				if owner != note.Owner {
					t.Fatal("owner substituted")
				}
				return guard
			})
			var phase string
			if err = f.db.QueryRow(`SELECT phase FROM identity_setup_completions`).Scan(&phase); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "current", "no certificate yet":
				if calls != 1 || marks != 1 || phase != "installed" {
					t.Fatal("installed capture not marked installed", mode, calls, marks, phase)
				}
			case "guard refused":
				if marks != 0 || phase != "prepared" {
					t.Fatal("refused capture installed", calls, marks, phase)
				}
			case "already installed":
				if marks != 0 {
					t.Fatal("an installed capture was marked again", calls, marks)
				}
			default:
				if calls != 0 || marks != 0 || phase != "prepared" {
					t.Fatal("retired capture reached installation", mode, calls, marks, phase)
				}
			}
			var after int64
			if err = f.db.QueryRow(`SELECT expires_ms FROM identity_setup_completions`).Scan(&after); err != nil || after != expiry {
				t.Fatal("expiry changed", err)
			}
		})
	}
}
