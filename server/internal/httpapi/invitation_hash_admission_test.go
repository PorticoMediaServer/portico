package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

func TestDeadInvitationAcceptanceResponsesStayIndistinguishable(t *testing.T) {
	f := administrationFixtureNew(t)
	var baseline string
	for _, state := range []string{"unknown", "expired", "revoked", "used"} {
		w := f.call("POST", "/v1/admin/access/invitations", f.owner, fmt.Sprintf(`{"operationId":"hash-http-%s","role":"member","allowedLibraries":[],"expiresInHours":24}`, state))
		var invite access.Invitation
		if err := json.Unmarshal(w.Body.Bytes(), &invite); err != nil || w.Code != 201 {
			t.Fatalf("invite: %d %s %v", w.Code, w.Body, err)
		}
		code := invite.Code
		switch state {
		case "unknown":
			code = strings.Repeat("x", 43)
		case "expired":
			if _, err := f.deps.DB.Exec(`UPDATE access_invitations SET expires_ms=? WHERE id=?`, time.Now().Add(-time.Hour).UnixMilli(), invite.ID); err != nil {
				t.Fatal(err)
			}
		case "revoked":
			if w := f.call("POST", "/v1/admin/access/invitations/"+invite.ID+"/revoke", f.owner, fmt.Sprintf(`{"expectedRevision":%d,"operationId":"hash-http-revoke"}`, invite.Revision)); w.Code != 200 {
				t.Fatalf("revoke: %d %s", w.Code, w.Body)
			}
		case "used":
			if w := f.call("POST", "/v1/access/invitations/accept", "", fmt.Sprintf(`{"code":%q,"username":"firstguest","password":"Correct-Horse-9"}`, code)); w.Code != 201 {
				t.Fatalf("valid accept: %d %s", w.Code, w.Body)
			}
			var hash []byte
			if err := f.deps.DB.QueryRow(`SELECT password_hash FROM accounts WHERE username='firstguest'`).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if cost, err := bcrypt.Cost(hash); err != nil || cost != identity.PasswordCost || bcrypt.CompareHashAndPassword(hash, []byte("Correct-Horse-9")) != nil {
				t.Fatalf("accepted account has no usable fixed-cost password: cost=%d err=%v", cost, err)
			}
		}
		w = f.call("POST", "/v1/access/invitations/accept", "", fmt.Sprintf(`{"code":%q,"username":"nextguest","password":"Correct-Horse-9"}`, code))
		var response struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 410 || response.Error["code"] != "invitation_expired" {
			t.Fatalf("%s: %d %s %v", state, w.Code, w.Body, err)
		}
		delete(response.Error, "requestId")
		encoded, _ := json.Marshal(response)
		if baseline == "" {
			baseline = string(encoded)
		}
		if string(encoded) != baseline || w.Header().Get("Retry-After") != "" {
			t.Errorf("%s reveals code state: %s headers=%v", state, encoded, w.Header())
		}
	}
	// Keep the existing weak-password precedence even when the code is spent.
	if w := f.call("POST", "/v1/access/invitations/accept", "", fmt.Sprintf(`{"code":%q,"username":"nextguest","password":"weak"}`, strings.Repeat("x", 43))); w.Code != 400 {
		t.Fatalf("password precedence: %d %s", w.Code, w.Body)
	}
}

func TestInvitationAcceptanceDeclaresSharedHasherBusy(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := registry.MetadataForPattern("POST /v1/access/invitations/accept")
	if !ok || m.Access != apikit.Public {
		t.Fatal("acceptance must remain public", m)
	}
	for _, code := range m.Errors {
		if code == "authentication_busy" {
			return
		}
	}
	t.Fatal("invitation acceptance omits the shared hashing saturation error")
}
