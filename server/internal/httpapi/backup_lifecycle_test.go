package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBackupSettingsHTTPSecretBoundaryAndEncryptedRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	state := t.TempDir()
	db, e := persistence.Open(filepath.Join(state, "Database", "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1),('member','member',x'00','member',1)`); e != nil {
		t.Fatal(e)
	}
	owner, e := ident.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, e := ident.Issue("member", "member", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	service := backup.New(state, db, ident.ID(), "fixture", persistence.SchemaVersion())
	service.SetCopyScrubber(identity.ScrubBackupCopy)
	service.SetPolicyStore(backup.NewPolicyStore(db, state))
	handler := New(Dependencies{DB: db, Identity: ident, Backups: service})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", member.AccessToken} {
		for _, method := range []string{"GET", "PUT"} {
			w := request(method, "/v1/admin/backups/settings", token, `{}`)
			if token == "" && w.Code != 401 || token != "" && w.Code != 403 {
				t.Fatal("nonowner settings boundary", w.Code, w.Body.String())
			}
		}
	}
	password := "HTTP private fixture phrase!"
	destination := t.TempDir()
	payload, _ := json.Marshal(map[string]any{"backupFolder": destination, "includeArtwork": true, "passphraseProtected": true, "passphrase": password})
	saved := request("PUT", "/v1/admin/backups/settings", owner.AccessToken, string(payload))
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	assertSpecResponse(t, "PUT", "/v1/admin/backups/settings", saved)
	if bytes.Contains(saved.Body.Bytes(), []byte(password)) || !bytes.Contains(saved.Body.Bytes(), []byte(`"passphraseSet":true`)) {
		t.Fatal("secret readback", saved.Body.String())
	}
	read := request("GET", "/v1/admin/backups/settings", owner.AccessToken, "")
	if read.Code != 200 || bytes.Contains(read.Body.Bytes(), []byte(password)) {
		t.Fatal("settings read", read.Code)
	}
	assertSpecResponse(t, "GET", "/v1/admin/backups/settings", read)
	invalid := request("PUT", "/v1/admin/backups/settings", owner.AccessToken, `{"backupFolder":"/"}`)
	if invalid.Code != 400 {
		t.Fatal("root destination accepted", invalid.Code)
	}
	job, e := service.Start(context.Background(), backup.KindManual, "http-encrypted-fixture")
	if e != nil {
		t.Fatal(e)
	}
	j, _ := service.Job(job)
	if e = j.Result(); e != nil {
		t.Fatal(e)
	}
	entries, e := service.List()
	if e != nil || len(entries) != 1 || !entries[0].Encrypted {
		t.Fatal(entries, e)
	}
	if _, e = os.Stat(filepath.Join(destination, entries[0].ID, "archive.pba")); e != nil {
		t.Fatal(e)
	}
	// The owner inspection route uses bytes and has no restore side effects.
	inspectBody, _ := json.Marshal(map[string]any{"source": map[string]string{"backupId": entries[0].ID}})
	for _, token := range []string{"", member.AccessToken} {
		out := request("POST", "/v1/admin/backups/inspect", token, string(inspectBody))
		if token == "" && out.Code != 401 || token != "" && out.Code != 403 {
			t.Fatalf("inspection owner boundary: %d", out.Code)
		}
	}
	inspected := request("POST", "/v1/admin/backups/inspect", owner.AccessToken, string(inspectBody))
	if inspected.Code != 200 || !bytes.Contains(inspected.Body.Bytes(), []byte(`"encrypted":true`)) {
		t.Fatalf("real encrypted inspection: %d", inspected.Code)
	}
	if progress := service.RestoreProgress(); progress != nil && progress.JobID != "" {
		t.Fatal("inspection started a restore")
	}
	// A new portable restore must not destroy a valid pending restore when
	// its protected header has been damaged. Exercise the actual owner route
	// and asynchronous failure result, with the original stage still in place.
	if result, err := service.StageWithPassphrase(context.Background(), backup.Source{BackupID: entries[0].ID}, password); err != nil || !result.Staged {
		t.Fatal("valid pending HTTP restore setup", err)
	}
	staged := filepath.Join(state, backup.RestoreStagedDir)
	beforeStage := backupHTTPRestoreWitness(t, staged)
	beforeLive := backupHTTPRestoreWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))
	damaged, err := os.ReadFile(filepath.Join(destination, entries[0].ID, "archive.pba"))
	if err != nil {
		t.Fatal(err)
	}
	damaged[0] ^= 1
	selected := filepath.Join(t.TempDir(), "damaged.db")
	if err = os.WriteFile(selected, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"operationId": "http-damaged-portable", "source": map[string]string{"path": selected}, "passphrase": password})
	refused := request("POST", "/v1/admin/backups/restore", owner.AccessToken, string(body))
	if refused.Code != 202 {
		t.Fatal("damaged source job not accepted for validation", refused.Code)
	}
	assertSpecResponse(t, "POST", "/v1/admin/backups/restore", refused)
	refusalDeadline := time.Now().Add(20 * time.Second)
	for service.RestoreProgress().Phase != "failed" {
		if service.RestoreProgress().Phase == "staged" || time.Now().After(refusalDeadline) {
			t.Fatal("damaged portable source did not fail")
		}
		time.Sleep(time.Millisecond)
	}
	if progress := service.RestoreProgress(); progress.ErrorCode != "restore_not_portico" {
		t.Fatal("non-SQLite source changed published refusal semantics", progress.ErrorCode)
	}
	if !reflect.DeepEqual(beforeStage, backupHTTPRestoreWitness(t, staged)) {
		t.Fatal("HTTP refusal changed existing staged marker/candidate")
	}
	if !reflect.DeepEqual(beforeLive, backupHTTPRestoreWitness(t, filepath.Join(state, "Database"), filepath.Join(state, "Keys"))) {
		t.Fatal("HTTP refusal changed live DB/Keys")
	}
	if _, err = os.Stat(filepath.Join(staged, ".incoming")); !os.IsNotExist(err) {
		t.Fatal("HTTP refusal left incoming staging", err)
	}
	tempEntries, err := os.ReadDir(filepath.Join(state, "Temp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range tempEntries {
		if strings.HasPrefix(entry.Name(), "backup-") {
			t.Fatal("HTTP refusal left decrypt/snapshot scratch")
		}
	}
	for i, phrase := range []string{"wrong fixture passphrase", password} {
		body, _ := json.Marshal(map[string]any{"operationId": "http-restore-" + string(rune('a'+i)), "source": map[string]string{"backupId": entries[0].ID}, "passphrase": phrase})
		started := request("POST", "/v1/admin/backups/restore", owner.AccessToken, string(body))
		if started.Code != 202 {
			t.Fatal(started.Code, started.Body.String())
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			progress := service.RestoreProgress()
			if progress.Phase == "failed" || progress.Phase == "staged" {
				if i == 0 && progress.ErrorCode != "restore_integrity_failed" {
					t.Fatal("wrong password refusal", progress)
				}
				if i == 1 && progress.Result == nil {
					t.Fatal("valid restore failed", progress)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("restore timeout")
			}
			time.Sleep(10 * time.Millisecond)
		}
	} // A genuine producer failure must survive running disappearing in HTTP status.
	service.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error { return os.ErrPermission })
	failed, e := service.Start(context.Background(), backup.KindManual, "http-failing-fixture")
	if e != nil {
		t.Fatal(e)
	}
	failure, _ := service.Job(failed)
	if e = failure.Result(); e == nil {
		t.Fatal("producer failure accepted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for service.LastBackup() == nil || service.LastBackup().JobID != failed {
		if time.Now().After(deadline) {
			t.Fatal("failure status missing")
		}
		time.Sleep(time.Millisecond)
	}

	status := request("GET", "/v1/admin/backups", owner.AccessToken, "")
	if status.Code != 200 || bytes.Contains(status.Body.Bytes(), []byte(password)) || !bytes.Contains(status.Body.Bytes(), []byte(`"outcome":"failed"`)) {
		t.Fatal("secret status leak", status.Code)
	}
}

func TestBackupMutationsRefuseNonownersAndExpiredSessionsWithoutSideEffects(t *testing.T) {
	f := administrationFixtureNew(t)
	state := t.TempDir()
	service := backup.New(state, f.deps.DB, f.deps.Identity.ID(), "fixture", persistence.SchemaVersion())
	store := backup.NewPolicyStore(f.deps.DB, state)
	service.SetPolicyStore(store)
	f.deps.Backups = service
	f.handler = New(f.deps)
	before, err := store.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"backupFolder": t.TempDir(), "includeArtwork": true, "passphraseProtected": true, "passphrase": "permission fixture private phrase"})
	routes := []struct{ method, path, body string }{
		{"GET", "/v1/admin/backups/settings", ""},
		{"PUT", "/v1/admin/backups/settings", string(body)},
		{"POST", "/v1/admin/backups/inspect", `{"source":{"path":"/unread-fixture"}}`},
		{"POST", "/v1/admin/backups", `{"operationId":"denied-backup-op"}`},
		{"DELETE", "/v1/admin/backups/2026-10-23T030000Z", ""},
		{"POST", "/v1/admin/backups/restore", `{"operationId":"denied-restore-op","source":{"path":"/unread-fixture"},"passphrase":"permission fixture private phrase"}`},
	}
	for _, token := range []string{f.admin, f.member} {
		for _, route := range routes {
			w := f.call(route.method, route.path, token, route.body)
			if w.Code != 403 {
				t.Fatalf("nonowner mutation %s %s: %d", route.method, route.path, w.Code)
			}
		}
	}
	// Revoke the actual owner family using its normal sign-out operation, then
	// replay that credential against every mutation.
	signedOut := f.call("DELETE", "/v1/sessions/current", f.owner, "")
	if signedOut.Code != 204 {
		t.Fatalf("owner logout failed: %d %s", signedOut.Code, signedOut.Body.String())
	}
	for _, route := range routes {
		w := f.call(route.method, route.path, f.owner, route.body)
		if w.Code != 401 {
			t.Fatalf("ended session mutation %s %s: %d", route.method, route.path, w.Code)
		}
	}
	after, err := store.Settings(context.Background())
	if err != nil || after != before {
		t.Fatal("denied writes changed policy", after, err)
	}
	if _, err = os.Stat(filepath.Join(state, "Keys", "backup-passphrase.key")); !os.IsNotExist(err) {
		t.Fatal("denied write minted a key", err)
	}
	if _, running := service.Running(); running || service.RestoreProgress() != nil || service.LastBackup() != nil {
		t.Fatal("denied write started work")
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatal("denied write created files", entries, err)
	}
}

func TestBackupRejectsMalformedSecretAndSourceBodiesBeforeStartingWork(t *testing.T) {
	f := administrationFixtureNew(t)
	state := t.TempDir()
	service := backup.New(state, f.deps.DB, f.deps.Identity.ID(), "fixture", persistence.SchemaVersion())
	store := backup.NewPolicyStore(f.deps.DB, state)
	service.SetPolicyStore(store)
	f.deps.Backups = service
	f.handler = New(f.deps)
	for _, test := range []struct{ path, body string }{
		{"/v1/admin/backups/settings", `{"passphraseProtected":true,"passphrase":42}`},
		{"/v1/admin/backups/settings", `{"passphraseProtected":true,"passphrase":"short"}`},
		{"/v1/admin/backups/settings", `{"includeArtwork":true,"unknownSecret":"private fixture"}`},
		{"/v1/admin/backups/restore", `{"operationId":"malformed-restore","source":{"path":"/unread-fixture","passphrase":"private fixture"}}`},
		{"/v1/admin/backups/restore", `{"operationId":"malformed-restore","source":{"path":42},"passphrase":"private fixture"}`},
	} {
		method := "POST"
		if strings.HasSuffix(test.path, "/settings") {
			method = "PUT"
		}
		w := f.call(method, test.path, f.owner, test.body)
		if w.Code != 400 {
			t.Fatalf("malformed request %s accepted: %d %s", test.path, w.Code, w.Body.String())
		}
	}
	if _, running := service.Running(); running || service.RestoreProgress() != nil {
		t.Fatal("malformed request started work")
	}
	settings, err := store.Settings(context.Background())
	if err != nil || settings.IncludeArtwork || settings.PassphraseProtected || settings.PassphraseSet {
		t.Fatal("malformed request persisted policy", settings, err)
	}
	if _, err = os.Stat(filepath.Join(state, "Keys", "backup-passphrase.key")); !os.IsNotExist(err) {
		t.Fatal("malformed body minted a key", err)
	}
}

func backupHTTPRestoreWitness(t *testing.T, roots ...string) map[string]struct {
	Mode fs.FileMode
	Hash [32]byte
} {
	t.Helper()
	out := make(map[string]struct {
		Mode fs.FileMode
		Hash [32]byte
	})
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := struct {
				Mode fs.FileMode
				Hash [32]byte
			}{Mode: info.Mode()}
			if info.Mode().IsRegular() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				hash := sha256.New()
				_, copyErr := io.Copy(hash, file)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				copy(value.Hash[:], hash.Sum(nil))
			}
			out[path] = value
			return nil
		})
		if err != nil {
			t.Fatal("HTTP fixture witness unavailable", err)
		}
	}
	return out
}
