package api

import (
	"strings"
	"testing"
)

func TestBackupPassphraseIsOwnerOnlyAndWriteOnly(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	req := BackupPassphrase{Passphrase: t.Name() + " value"}
	if code := status(t, s, "PUT", "/v1/admin/backups/passphrase", admin, req); code != 403 {
		t.Fatal(code)
	}
	if code := status(t, s, "PUT", "/v1/admin/backups/passphrase", owner, req); code != 204 {
		t.Fatal(code)
	}
	var page Backups
	w := call(t, s, "GET", "/v1/admin/backups", owner, nil, &page)
	if w.Code != 200 || !page.Protected || page.Readable || strings.Contains(w.Body.String(), req.Passphrase) {
		t.Fatal("protection status or write-only input is wrong", w.Code, page.Protected, page.Readable, s.backups.Protected(), strings.Contains(w.Body.String(), req.Passphrase))
	}
	if code := status(t, s, "PUT", "/v1/admin/backups/passphrase", owner, BackupPassphrase{}); code != 204 {
		t.Fatal(code)
	}
	call(t, s, "GET", "/v1/admin/backups", owner, nil, &page)
	if page.Protected || !page.Readable {
		t.Fatal("turning protection off did not apply")
	}
}
