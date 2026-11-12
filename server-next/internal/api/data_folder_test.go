//go:build !windows

package api

import (
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/datafolder"
)

// While other accounts on the computer can open the data folder, the Dashboard says so with
// the folder and the server's account, and its one action is Fix permissions. The owner's fix
// makes the folder private, the alert goes by itself, the activity record has it, and a second
// fix answers the same. Nobody else may read or fix it.
func TestFixPermissionsMakesTheDataFolderPrivateAndTheAlertGoes(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	if err := os.Chmod(s.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alert := func() *DashboardAlert {
		t.Helper()
		for _, a := range get[Dashboard](t, s, owner, "/v1/admin/dashboard").Alerts {
			if a.Code == "data_folder_readable" {
				return &a
			}
		}
		return nil
	}
	a := alert()
	if a == nil || !a.Major || a.Action.Kind != "fix-permissions" || a.Params["path"] != s.dataDir || a.Params["owner"] == "" || a.Params["reason"] != "" {
		t.Fatalf("the alert: %+v", a)
	}
	if p := get[DataFolderPermissions](t, s, owner, "/v1/admin/data-folder/permissions"); !p.ReadableByOthers || !p.Fixable || !p.Checked || p.Path != s.dataDir || p.Owner == "" {
		t.Fatalf("the read before the fix: %+v", p)
	}

	member := memberToken(t, s, owner)
	for _, token := range []string{"", member} {
		if code := status(t, s, "POST", "/v1/admin/data-folder/permissions/fix", token, nil); code != 401 && code != 403 {
			t.Errorf("someone other than the owner fixing was answered %d", code)
		}
		if code := status(t, s, "GET", "/v1/admin/data-folder/permissions", token, nil); code != 401 && code != 403 {
			t.Errorf("someone other than the owner reading was answered %d", code)
		}
	}
	if info, _ := os.Stat(s.dataDir); info.Mode().Perm() != 0o755 {
		t.Fatalf("a refused fix changed the folder: %o", info.Mode().Perm())
	}

	for range 2 {
		var p DataFolderPermissions
		if w := call(t, s, "POST", "/v1/admin/data-folder/permissions/fix", owner, nil, &p); w.Code != 200 || p.ReadableByOthers || !p.Checked {
			t.Fatalf("the fix answered %d: %s", w.Code, w.Body)
		}
	}
	if info, _ := os.Stat(s.dataDir); info.Mode().Perm() != 0o700 {
		t.Fatalf("after the fix the folder is %o", info.Mode().Perm())
	}
	if a := alert(); a != nil {
		t.Fatalf("the alert stayed: %+v", a)
	}
	fixes := 0
	for _, entry := range get[Page[ActivityEntry]](t, s, owner, "/v1/admin/audit").Items {
		if entry.Action == "data_folder.private" {
			fixes++
		}
	}
	if fixes != 1 {
		t.Fatalf("the activity record has %d fixes; want the one that changed something", fixes)
	}
}

// On a drive that cannot keep who may open a folder the alert stays, because others can open
// it, but offers no Fix permissions and says why; a fix sent all the same is refused with that
// reason and recorded nowhere. The folder's owner is named, not the server's account.
func TestADataFolderThatCannotBeFixedKeepsItsAlertAndSaysWhy(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	lookDataFolder = func(string) (datafolder.State, error) {
		return datafolder.State{ReadableByOthers: true, Owner: "media", Why: datafolder.ErrNotKept}, nil
	}
	makeFolderPrivate = func(string) error { return datafolder.ErrNotKept }
	t.Cleanup(func() { lookDataFolder, makeFolderPrivate = datafolder.Look, datafolder.MakePrivate })
	var alert *DashboardAlert
	for _, a := range get[Dashboard](t, s, owner, "/v1/admin/dashboard").Alerts {
		if a.Code == "data_folder_readable" {
			alert = &a
		}
	}
	if alert == nil || alert.Action.Kind != "none" || alert.Params["reason"] != "not_kept_here" || alert.Params["message"] == "" || alert.Params["owner"] != "media" {
		t.Fatalf("the alert: %+v", alert)
	}
	p := get[DataFolderPermissions](t, s, owner, "/v1/admin/data-folder/permissions")
	if !p.ReadableByOthers || p.Fixable || p.Reason != "not_kept_here" || p.Message == "" || p.Owner != "media" {
		t.Fatalf("the read: %+v", p)
	}
	w := call(t, s, "POST", "/v1/admin/data-folder/permissions/fix", owner, nil, nil)
	if w.Code != 409 || refusalCode(w) != "not_kept_here" {
		t.Fatalf("the fix answered %d: %s", w.Code, w.Body)
	}
	for _, entry := range get[Page[ActivityEntry]](t, s, owner, "/v1/admin/audit").Items {
		if entry.Action == "data_folder.private" {
			t.Fatalf("a fix that was not made was recorded: %+v", entry)
		}
	}
}
