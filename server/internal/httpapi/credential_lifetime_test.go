package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// A restore signs everyone out and a backup holds no sign-in, but an API key
// is not a sign-in: what the owner connected keeps working across both. A key
// that was already revoked stays revoked.
func TestAKeyOutlivesABackupAndARestoreAndSessionsDoNot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := integrationFixture(t)
	ctx := context.Background()
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-restore-1","name":"Download manager","scopes":["read"]}`)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-restore-1","name":"Member script","scopes":["read"]}`)
	gone := issueCredential(t, f, "/v1/me/access-tokens", f.owner, `{"operationId":"token-restore-2","name":"Old script","scopes":["read"]}`)
	if w := f.call("DELETE", "/v1/me/access-tokens/"+gone.Credential.ID, f.owner, ""); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}

	app := newApp(t, f, "restore-app")

	// Use the actual backup publisher and scrubber, not a hand-made SQL copy.
	sourceRoot := t.TempDir()
	source := backup.New(sourceRoot, f.deps.DB, "srv-test", "test", persistence.SchemaVersion())
	source.SetCopyScrubber(identity.ScrubBackupCopy)
	id, err := source.Create(ctx, backup.KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(sourceRoot, backup.BackupsDir, id)
	copyPath := filepath.Join(folder, filepath.FromSlash(backup.DatabaseName))
	raw, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range map[string]string{"personal token": token.Secret, "revoked token": gone.Secret, "owner session": f.owner, "administrator session": f.admin, "member session": f.member} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("the backup copy holds the %s's secret", name)
		}
	}
	copyDB, err := dbwork.OpenHandle(copyPath, dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	// Retain the upstream recovery and all-personal-token storage checks too.
	recovered, err := access.New(copyDB).RevealCredential(ctx, func(context.Context, *sql.Tx) error { return nil }, key.Credential.ID, identity.CredentialKindKey)
	if err != nil || recovered.Secret != key.Secret {
		t.Fatalf("backup lost owner key recovery: %v", err)
	}
	var recoverableTokens int
	if err = copyDB.QueryRow(`SELECT count(*) FROM api_credentials WHERE kind='token' AND secret<>''`).Scan(&recoverableTokens); err != nil || recoverableTokens != 0 {
		t.Fatalf("backup changed personal token storage: %d %v", recoverableTokens, err)
	}
	for name, credential := range map[string]struct{ id, secret string }{
		"API key":      {key.Credential.ID, key.Secret},
		"app password": {app.AppPassword.ID, app.Secret},
	} {
		var secret string
		if err = copyDB.QueryRow(`SELECT secret FROM api_credentials WHERE id=?`, credential.id).Scan(&secret); err != nil || secret != credential.secret {
			t.Fatalf("backup did not preserve the recoverable %s: %v", name, err)
		}
	}
	var personalSecret string
	if err = copyDB.QueryRow(`SELECT secret FROM api_credentials WHERE id=?`, token.Credential.ID).Scan(&personalSecret); err != nil || personalSecret != "" {
		t.Fatalf("personal token must remain digest-only: %v", err)
	}
	for what, check := range map[string]struct {
		query string
		want  int
	}{
		"sessions that are not a live key or token": {`SELECT count(*) FROM authorization_session_families WHERE revoked=0 AND id NOT IN(SELECT family_id FROM api_credentials WHERE revoked_ms=0)`, 0},
		"usable tokens of anything else":            {`SELECT count(*) FROM authorization_family_tokens WHERE retired=0 AND family_id NOT IN(SELECT family_id FROM api_credentials WHERE revoked_ms=0)`, 0},
		"live keys and tokens":                      {`SELECT count(*) FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id WHERE f.revoked=0 AND t.retired=0 AND f.id IN(SELECT family_id FROM api_credentials WHERE revoked_ms=0)`, 3},
		"revoked keys brought back":                 {`SELECT count(*) FROM authorization_session_families WHERE revoked=0 AND id IN(SELECT family_id FROM api_credentials WHERE revoked_ms<>0)`, 0},
	} {
		var n int
		if err = copyDB.QueryRow(check.query).Scan(&n); err != nil || n != check.want {
			t.Fatalf("the backup copy holds %d %s, want %d: %v", n, what, check.want, err)
		}
	}

	// Taking the backup must not sign anyone out on the source server.
	for name, session := range map[string]string{"owner": f.owner, "administrator": f.admin, "member": f.member} {
		if w := f.call("GET", "/v1/me", session, ""); w.Code != 200 {
			t.Fatalf("backup ended %s session: %d", name, w.Code)
		}
	}
	// Restore into a separate data folder, then reopen only the restored DB.
	targetRoot := t.TempDir()
	targetPath := storagepaths.Path(targetRoot, backup.DatabaseName)
	targetDB, err := persistence.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer targetDB.Close()
	target := backup.New(targetRoot, targetDB, "srv-test", "test", persistence.SchemaVersion())
	if _, err = target.Stage(ctx, backup.Source{Path: folder}); err != nil {
		t.Fatal(err)
	}
	if err = targetDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err = backup.ApplyStaged(ctx, targetRoot, nil); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := persistence.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoredDB.Close() })
	ident, err := identity.New(restoredDB, targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	acted, err := ident.SignEveryoneOutAfterRestore(ctx, "2026-01-01T00:00:00Z")
	if err != nil || !acted {
		t.Fatalf("sign everyone out: %v %v", acted, err)
	}
	control, err := hosted.New(restoredDB, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), tl6ZeroHostedRootID)
	if err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{DB: restoredDB, Identity: ident, Catalog: catalog.New(restoredDB), Hosted: control, Console: operations.New(restoredDB), Access: AccessArea{Access: access.New(restoredDB)}}
	deps.credentials = newCredentialGate()
	deps.self = &selfHandler{}
	restored := administrationFixture{deps: deps, handler: New(deps)}
	t.Cleanup(func() {
		if err := FlushCredentialUse(ctx, restored.handler); err != nil {
			t.Error(err)
		}
	})
	dbwork.BumpAuthority()
	for name, session := range map[string]string{"the owner": f.owner, "the administrator": f.admin, "the member": f.member} {
		if w := restored.call("GET", "/v1/me", session, ""); w.Code != 401 {
			t.Fatalf("%s is still signed in after a restore: %d %s", name, w.Code, w.Body)
		}
	}
	if w := restored.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("the key stopped working at a restore: %d %s", w.Code, w.Body)
	}
	if w := restored.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("the member's token stopped working at a restore: %d %s", w.Code, w.Body)
	}
	if w := restored.call("GET", "/v1/me", gone.Secret, ""); w.Code != 401 {
		t.Fatalf("a restore brought a revoked token back: %d %s", w.Code, w.Body)
	}
	// App passwords still exchange after restore, and owner recovery reads the
	// preserved value repeatedly. Neither capability uses the original DB.
	appLogin(t, restored, app)
	owner, err := ident.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	for path, secret := range map[string]string{
		"/v1/admin/api-keys/" + key.Credential.ID + "/secret":              key.Secret,
		"/v1/integrations/app-passwords/" + app.AppPassword.ID + "/secret": app.Secret,
	} {
		for range 2 {
			w := restored.call("GET", path, owner.AccessToken, "")
			var recovered access.CredentialSecret
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &recovered) != nil || recovered.Secret != secret {
				t.Fatalf("restored credential could not be revealed: %s status=%d", path, w.Code)
			}
		}
	}
}

// Deleting a member through the server ends their keys and tokens with their
// sessions: nothing of theirs is left signed in, and nobody else's is touched.
func TestDeletingAMemberEndsTheirKeysWithTheirSessions(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-delete-1","name":"Member script","scopes":["read"]}`)
	other := issueCredential(t, f, "/v1/me/access-tokens", f.admin, `{"operationId":"token-delete-2","name":"Admin script","scopes":["read"]}`)
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("before: %d %s", w.Code, w.Body)
	}
	var family string
	if err := f.deps.DB.QueryRow(`SELECT family_id FROM api_credentials WHERE id=?`, token.Credential.ID).Scan(&family); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithWriteTx(ctx, f.deps.DB, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return f.deps.Identity.EraseMemberTx(ctx, tx, "member")
	}); err != nil {
		t.Fatal(err)
	}
	dbwork.BumpAuthority()
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("a deleted member's token works: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me", f.member, ""); w.Code != 401 {
		t.Fatalf("a deleted member is still signed in: %d %s", w.Code, w.Body)
	}
	var live, stored int
	if err := f.deps.DB.QueryRow(`SELECT (SELECT count(*) FROM authorization_session_families WHERE revoked=0 AND (id=? OR account_id='member')),(SELECT count(*) FROM api_credentials WHERE account_id='member')`, family).Scan(&live, &stored); err != nil || live != 0 || stored != 0 {
		t.Fatalf("after deleting the member: %d live sessions of theirs, %d stored tokens: %v", live, stored, err)
	}
	if w := f.call("GET", "/v1/me", other.Secret, ""); w.Code != 200 {
		t.Fatalf("deleting one member ended another's token: %d %s", w.Code, w.Body)
	}
}
