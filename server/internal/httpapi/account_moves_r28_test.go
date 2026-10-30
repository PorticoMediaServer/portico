package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

func TestAccountMoveOwnerAuthorizationPrecedesBodyAndRevision(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, path := range []string{
		"/v1/admin/sign-in/move/preview", "/v1/admin/sign-in/move/confirm",
		"/v1/admin/sign-in/move/unknown/cancel", "/v1/admin/sign-in/move/unknown/finish",
		"/v1/admin/sign-in/move/codes",
	} {
		for _, token := range []string{f.admin, f.member} {
			for _, body := range []string{`{}`, `{`, `{"expectedRevision":0}`} {
				if w := f.call("POST", path, token, body); w.Code != 403 {
					t.Fatalf("non-owner reached body/revision processing on %s: status=%d", path, w.Code)
				}
			}
		}
	}
	var count int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM identity_moves`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused request changed moves: count=%d error=%v", count, err)
	}
}

func TestAccountMovePublicHTTPCodeAndOwnerReadOnly(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	if _, e := f.deps.DB.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) SELECT id,'acc_'||id FROM accounts`); e != nil {
		t.Fatal(e)
	}
	c, e := identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: identity.Token(), Name: "Browser", Platform: "web", App: "portico"}, "192.0.2.1")
	if e != nil {
		t.Fatal(e)
	}
	owner, e := f.deps.Identity.IssueWithDevice(c, "owner", "owner-profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	before := f.call("GET", "/v1/system", "", "")
	if before.Code != 200 || !strings.Contains(before.Body.String(), `"move":null`) {
		t.Fatalf("probe %d %s", before.Code, before.Body)
	}
	p := f.call("POST", "/v1/admin/sign-in/move/preview", owner.AccessToken, `{"to":"local","startOver":false}`)
	var v identity.AccountMove
	if e = json.Unmarshal(p.Body.Bytes(), &v); e != nil || p.Code != 200 {
		t.Fatalf("preview %d %s %v", p.Code, p.Body, e)
	}
	q, _ := json.Marshal(identity.MoveConfirmRequest{MoveID: v.ID, ExpectedRevision: v.Revision, Username: "owner", Password: "New-owner1!"})
	moved := f.call("POST", "/v1/admin/sign-in/move/confirm", owner.AccessToken, string(q))
	var result moveConfirmation
	if e = json.Unmarshal(moved.Body.Bytes(), &result); e != nil || moved.Code != 200 || result.SignIn == nil {
		t.Fatalf("move %d %s %v", moved.Code, moved.Body, e)
	}
	probe := f.call("GET", "/v1/system", "", "")
	if probe.Code != 200 || !strings.Contains(probe.Body.String(), `"move":"to_local"`) {
		t.Fatalf("moving probe %d %s", probe.Code, probe.Body)
	}
	codeReply := f.call("POST", "/v1/admin/sign-in/move/codes", result.SignIn.Session.AccessToken, `{"accountIds":["member"]}`)
	var codes identity.MoveCodes
	if e = json.Unmarshal(codeReply.Body.Bytes(), &codes); e != nil || codeReply.Code != 200 || len(codes.Items) != 1 {
		t.Fatalf("codes %d %s %v", codeReply.Code, codeReply.Body, e)
	}
	if codes.Items[0].ServerID != f.deps.Identity.ID() {
		t.Fatal("Local move link lost canonical identity")
	}
	code := codes.Items[0].Code
	preview := f.call("POST", "/v1/auth/move/code", "", `{"code":"`+code+`"}`)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"username":"member"`) || strings.Contains(preview.Body.String(), code) {
		t.Fatalf("code preview %d %s", preview.Code, preview.Body)
	}
	if w := f.call("GET", "/v1/admin/sign-in/move", result.SignIn.Session.AccessToken, ""); w.Code != 200 || strings.Contains(w.Body.String(), code) {
		t.Fatalf("readonly %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/direct/devices/anything/approval", "/v1/admin/access/devices/anything/trust", "/v1/admin/access-tokens"} {
		method := "POST"
		if strings.HasSuffix(path, "/trust") {
			method = "PUT"
		}
		if strings.HasSuffix(path, "access-tokens") {
			method = "GET"
		}
		w := f.call(method, path, result.SignIn.Session.AccessToken, "")
		if w.Code != 404 {
			t.Fatalf("retired route %s: %d %s", path, w.Code, w.Body)
		}
	}
	table := f.call("GET", "/v1/admin/access/members?status=waiting", result.SignIn.Session.AccessToken, "")
	if table.Code != 200 || !strings.Contains(table.Body.String(), `"hasMoveCode":true`) {
		t.Fatalf("waiting code source %d %s", table.Code, table.Body)
	}
	red := f.call("POST", "/v1/auth/move/local", "", `{"code":"`+code+`","username":"member","password":"New-member1!"}`)
	if red.Code != 200 {
		t.Fatalf("redeem %d %s", red.Code, red.Body)
	}
	if replay := f.call("POST", "/v1/auth/move/local", "", `{"code":"`+code+`","username":"member","password":"New-member1!"}`); replay.Code != 410 {
		t.Fatalf("code replay %d %s", replay.Code, replay.Body)
	}
}

func TestMoveCollisionPublishesIdentityFactsAndOwnerReadHasNoSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := httptest.NewRecorder()
	failure(w, &identity.MoveCollisionError{Username: "sam_k", ServerName: "Portico demo"})
	var wire struct {
		Error struct {
			Code    string               `json:"code"`
			Current MoveCollisionDetails `json:"current"`
		}
	}
	if e := json.Unmarshal(w.Body.Bytes(), &wire); e != nil || w.Code != 409 || wire.Error.Code != "move_collision" || wire.Error.Current.ExistingUsername != "sam_k" || wire.Error.Current.ServerName != "Portico demo" {
		t.Fatalf("structured collision %d %s %v", w.Code, w.Body, e)
	}
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`UPDATE accounts SET display_name='Current Owner',email='verified@example.test' WHERE id='owner'`); e != nil {
		t.Fatal(e)
	}
	owner := f.call("GET", "/v1/admin/sign-in", f.owner, "")
	if owner.Code != 200 || !strings.Contains(owner.Body.String(), `"username":"owner"`) || !strings.Contains(owner.Body.String(), `"email":"verified@example.test"`) {
		t.Fatalf("owner facts %d %s", owner.Code, owner.Body)
	}
	if w := f.call("GET", "/v1/admin/sign-in", f.admin, ""); w.Code != 403 {
		t.Fatalf("admin read owner identity %d %s", w.Code, w.Body)
	}
	var settings identity.SignInSettings
	if e := json.Unmarshal(owner.Body.Bytes(), &settings); e != nil || settings.WebPath != "/settings?scope=server&section=signin" {
		t.Fatalf("canonical web route %q %v", settings.WebPath, e)
	}
	assertSpecResponse(t, "GET", "/v1/admin/sign-in", owner)
}
