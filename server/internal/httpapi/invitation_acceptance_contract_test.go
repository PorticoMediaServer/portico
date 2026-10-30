package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The contract/client test invokes this test and reads its ephemeral output.
// Every document comes from the acceptance store and real sign-in producer,
// marshalled by the same writeAuth path as the installed invitation handler.
// No credential-bearing fixture is checked into the repository.
func TestInvitationAcceptanceProducerContract(t *testing.T) {
	responses := map[string]json.RawMessage{}
	for _, state := range []string{"local", "signed-in"} {
		t.Run(state, func(t *testing.T) {
			f := administrationFixtureNew(t)
			ctx := context.Background()
			privateNote := "private invitation contract note"
			invite, err := f.deps.Access.Access.Invite(ctx, nil, identity.Principal{Viewer: identity.Viewer{AccountID: "owner", Role: "owner"}}, access.InvitationRequest{OperationID: "contract-" + state, Notes: &privateNote})
			if err != nil {
				t.Fatal(err)
			}
			var w *httptest.ResponseRecorder
			if state == "local" {
				body, err := json.Marshal(map[string]string{"code": invite.Code, "username": "contract_local", "password": "Contract-password1!"})
				if err != nil {
					t.Fatal(err)
				}
				w = f.call("POST", "/v1/access/invitations/accept", "", string(body))
			} else {
				who := identity.PorticoIdentity{AccountID: "acc_contract_person", Username: "contract_person", DisplayName: "Contract profile"}
				if _, err = f.deps.DB.Exec(`DELETE FROM account_portico_links;UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico',revision=revision+1`); err != nil {
					t.Fatal(err)
				}
				result, err := f.deps.Access.Access.AcceptPortico(ctx, access.PorticoAcceptance{Code: invite.Code, AccountID: who.AccountID, Username: who.Username, DisplayName: who.DisplayName}, func(ctx context.Context, tx *sql.Tx) (identity.DirectSignIn, error) {
					return f.deps.Identity.PorticoSignInTx(ctx, tx, who)
				})
				if err != nil {
					t.Fatal(err)
				}
				if result.Session == nil || result.Challenge != nil {
					t.Fatal("signed-in producer state not reached")
				}
				w = httptest.NewRecorder()
				writeAuth(w, httptest.NewRequest("POST", "/v1/access/invitations/accept", nil), 201, porticoAcceptanceBody(result))
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("acceptance credentials can be cached")
				}
			}
			if w.Code != 201 {
				t.Fatalf("acceptance status: %d", w.Code)
			}
			assertSpecResponse(t, "POST", "/v1/access/invitations/accept", w)
			if strings.Contains(w.Body.String(), "notes") || strings.Contains(w.Body.String(), privateNote) || strings.Contains(w.Body.String(), "email") {
				t.Fatal("recipient response disclosed private data")
			}
			var wire map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			if want := map[string]string{"local": "accepted", "signed-in": "signed-in", "challenge": "challenge"}[state]; wire["variant"] != want {
				t.Fatalf("acceptance variant %v, want %q", wire["variant"], want)
			}
			if state == "challenge" {
				if wire["profiles"] != nil || wire["account"].(map[string]any)["allowedLibraries"] != nil {
					t.Fatal("intermediate producer no longer has zero-snapshot nulls")
				}
			}
			var accepted string
			if err := f.deps.DB.QueryRow(`SELECT state FROM access_invitations WHERE id=?`, invite.ID).Scan(&accepted); err != nil || accepted != "accepted" {
				t.Fatalf("acceptance was not committed: %s %v", accepted, err)
			}
			responses[state] = append(json.RawMessage(nil), w.Body.Bytes()...)
		})
	}
	if t.Failed() {
		return
	}
	if path := os.Getenv("PORTICO_TEST_INVITATION_RESPONSES"); path != "" {
		raw, err := json.Marshal(responses)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
