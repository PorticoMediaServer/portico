package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
)

func TestInvitationShortCapabilityRoutesKeepAdmissionAndOneUse(t *testing.T) {
	f := administrationFixtureNew(t)
	created := f.call("POST", "/v1/admin/access/invitations", f.owner, `{"operationId":"short-http-invite","role":"member","allowedLibraries":[],"expiresInHours":1}`)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var invitation access.Invitation
	if err := json.Unmarshal(created.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/access/invitations/" + invitation.ID + "/short-code"
	body := fmt.Sprintf(`{"expectedRevision":%d,"operationId":"short-http-issue"}`, invitation.Revision)
	for _, token := range []string{"", f.member} {
		w := f.call("POST", path, token, body)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("unprivileged short issuance: %d %s", w.Code, w.Body.String())
		}
	}
	issued := f.call("POST", path, f.owner, body)
	if issued.Code != 200 {
		t.Fatalf("issue %d %s", issued.Code, issued.Body.String())
	}
	var code access.InvitationShortResponse
	if err := json.Unmarshal(issued.Body.Bytes(), &code); err != nil || code.ShortCode == "" {
		t.Fatal("short response missing", err)
	}
	replay := f.call("POST", path, f.owner, body)
	if replay.Code != 200 || strings.Contains(replay.Body.String(), `"shortCode"`) {
		t.Fatalf("replay leaked %d %s", replay.Code, replay.Body.String())
	}
	preview := f.call("POST", "/v1/access/invitations/preview", "", fmt.Sprintf(`{"shortCode":%q}`, code.ShortCode))
	if preview.Code != 200 || strings.Contains(preview.Body.String(), invitation.Code) {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	accepted := f.call("POST", "/v1/access/invitations/accept", "", fmt.Sprintf(`{"shortCode":%q,"username":"short_guest","password":"Correct-Horse-9"}`, code.ShortCode))
	if accepted.Code != 201 {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	after := f.call("POST", "/v1/access/invitations/preview", "", fmt.Sprintf(`{"code":%q}`, invitation.Code))
	if after.Code != 404 {
		t.Fatalf("long code survived short consumption: %d", after.Code)
	}
}
func TestInvitationAcceptanceRejectsMixedCapabilityPresence(t *testing.T) {
	long := strings.Repeat("a", 43)
	for _, raw := range []string{
		`{"code":"` + long + `","shortCode":"","username":"guest","password":"Correct-Horse-9"}`,
		`{"code":"` + long + `","shortCode":null,"username":"guest","password":"Correct-Horse-9"}`,
		`{"code":null,"shortCode":"0123456789ABCDEF","username":"guest","password":"Correct-Horse-9"}`,
		`{"code":"","shortCode":"0123456789ABCDEF","porticoIdentity":{}}`,
		`{"shortCode":"0123456789ABCDEF","porticoIdentity":null}`,
		`{"shortCode":"0123456789ABCDEF","porticoIdentity":{},"password":""}`,
	} {
		var q InvitationAcceptRequest
		if json.Unmarshal([]byte(raw), &q) == nil {
			t.Fatalf("mixed capability or auth form accepted: %s", raw)
		}
	}
}
