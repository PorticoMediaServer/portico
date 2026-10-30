package identity

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// liveSecrets signs a member in on a device, renews once (which stores the
// answer, tokens and all, for a lost-answer replay) and leaves a television
// code approved and uncollected. It returns the member's current session and
// every secret string that is now somewhere in the database in the clear.
func liveSecrets(t *testing.T, s *Service) (Envelope, []string) {
	t.Helper()
	ctx := context.Background()
	phone := onDevice(t, memberPhone, "Member's phone")
	login, err := s.DirectLogin(phone, "member", "Testing2!")
	if err != nil || login.Session == nil {
		t.Fatalf("%+v %v", login, err)
	}
	renewed, err := s.RefreshSession(phone, login.Session.RefreshToken, memberPhone, "renewal-before-backup")
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.StartQuick(onDevice(t, memberTV, "Living room"), QuickStart{RequestID: "00000000-0000-4000-8000-0000000000aa", DeviceName: "Living room", Platform: "tvos", AppVersion: "1.0"}, true)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ReviewQuick(ctx, code.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	approver, err := s.Authenticate(renewed.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideQuick(ctx, approver, preview.RequestID, code.UserCode, "approve"); err != nil {
		t.Fatal(err)
	}
	// The renewal answer holds both current tokens; the code's stored answer
	// holds the secret the television polls with.
	return renewed, []string{renewed.AccessToken, renewed.RefreshToken, code.DeviceCode}
}

func fileHolds(t *testing.T, path string, secret string) bool {
	t.Helper()
	found := false
	for _, name := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			found = true
		}
	}
	return found
}

// A backup copy that has been scrubbed holds no session and none of the
// secrets that could continue one, not even in its free pages; the running
// server's database is not touched.
func TestAScrubbedBackupCopyHoldsNoSessionsOrLiveSecrets(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := memberWithSession(t)
	session, secrets := liveSecrets(t, s)
	copyPath := filepath.Join(t.TempDir(), "portico.db")
	if _, err := s.db.Exec(`VACUUM INTO ?`, copyPath); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if !fileHolds(t, copyPath, secret) {
			t.Fatalf("the fixture is wrong: an unscrubbed copy does not hold the secret %q...", secret[:6])
		}
	}
	if err := scrubCopyAt(ctx, copyPath); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if fileHolds(t, copyPath, secret) {
			t.Fatalf("a scrubbed backup copy still holds a live secret (%q...)", secret[:6])
		}
	}
	if info, err := os.Stat(copyPath + "-wal"); err == nil && info.Size() != 0 {
		t.Fatalf("the scrub left %d bytes of write-ahead log beside the copy", info.Size())
	}
	copyDB, err := dbwork.OpenHandle(copyPath, dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	for what, query := range map[string]string{
		"live sessions":            `SELECT count(*) FROM authorization_session_families WHERE revoked=0`,
		"usable access tokens":     `SELECT count(*) FROM authorization_family_tokens WHERE retired=0`,
		"renewal credentials":      `SELECT count(*) FROM identity_refresh_credentials`,
		"stored renewal answers":   `SELECT count(*) FROM identity_refresh_receipts`,
		"television codes":         `SELECT count(*) FROM quick_connect_v1`,
		"sessions ended otherwise": `SELECT count(*) FROM authorization_session_families WHERE revoked=1 AND revoked_reason<>'restored' AND revoked_at=''`,
	} {
		var n int
		if err = copyDB.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the scrubbed copy holds %d %s: %v", n, what, err)
		}
	}
	// What is not a session is all still there.
	for what, query := range map[string]string{
		"accounts": `SELECT count(*) FROM accounts`, "profiles": `SELECT count(*) FROM direct_profiles`, "devices": `SELECT count(*) FROM identity_devices`,
	} {
		var inCopy, live int
		if err = copyDB.QueryRow(query).Scan(&inCopy); err != nil {
			t.Fatal(err)
		}
		if err = s.db.QueryRow(query).Scan(&live); err != nil || inCopy != live || live == 0 {
			t.Fatalf("%s: %d in the copy, %d on the server: %v", what, inCopy, live, err)
		}
	}
	var hash []byte
	if err = copyDB.QueryRow(`SELECT password_hash FROM accounts WHERE username='member'`).Scan(&hash); err != nil || len(hash) == 0 {
		t.Fatalf("the copy lost the member's password: %v", err)
	}
	// The running server was not touched.
	if _, err = s.Authenticate(session.AccessToken); err != nil {
		t.Fatalf("scrubbing a copy signed someone out of the running server: %v", err)
	}
	// Scrubbing a scrubbed copy again is harmless. A scrub holds the copy
	// exclusively, so nothing else may have it open.
	copyDB.Close()
	if err = scrubCopyAt(ctx, copyPath); err != nil {
		t.Fatalf("a second scrub: %v", err)
	}
	if err = ScrubBackupCopy(ctx, nil); err == nil {
		t.Fatal("a scrub of nothing reported success")
	}
}

// The first start after a restore signs everyone out, once. Accounts,
// passwords and devices stay, so people simply sign in again.
func TestARestoreSignsEveryoneOutOnce(t *testing.T) {
	ctx := context.Background()
	s, owner, _, _ := memberWithSession(t)
	session, _ := liveSecrets(t, s)
	if acted, err := s.SignEveryoneOutAfterRestore(ctx, ""); err != nil || acted {
		t.Fatalf("no restore: acted=%v %v", acted, err)
	}
	if _, err := s.Authenticate(session.AccessToken); err != nil {
		t.Fatal(err)
	}
	acted, err := s.SignEveryoneOutAfterRestore(ctx, "2026-10-13T10:00:00Z")
	if err != nil || !acted {
		t.Fatalf("the first start after a restore: acted=%v %v", acted, err)
	}
	for name, token := range map[string]string{"the member": session.AccessToken, "the owner": owner.Session.AccessToken} {
		if _, err = s.Authenticate(token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s is still signed in after a restore: %v", name, err)
		}
	}
	if _, err = s.RefreshSession(onDevice(t, memberPhone, "Member's phone"), session.RefreshToken, memberPhone, "renewal-after-restore"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a session from before the restore renewed: %v", err)
	}
	var live, restored int
	if err = s.db.QueryRow(`SELECT (SELECT count(*) FROM authorization_session_families WHERE revoked=0),(SELECT count(*) FROM authorization_session_families WHERE revoked_reason='restored')`).Scan(&live, &restored); err != nil || live != 0 || restored == 0 {
		t.Fatalf("after a restore: %d live sessions, %d ended as restored: %v", live, restored, err)
	}
	// People sign in again with what they had: the password, on the same device.
	again, err := s.DirectLogin(onDevice(t, memberPhone, "Member's phone"), "member", "Testing2!")
	if err != nil || again.Session == nil {
		t.Fatalf("signing in again after a restore: %+v %v", again, err)
	}
	// A later start of the same restored state does nothing more.
	if acted, err = s.SignEveryoneOutAfterRestore(ctx, "2026-10-13T10:00:00Z"); err != nil || acted {
		t.Fatalf("a second start after the same restore: acted=%v %v", acted, err)
	}
	if _, err = s.Authenticate(again.Session.AccessToken); err != nil {
		t.Fatalf("a sign-in made after the restore was ended by a later start: %v", err)
	}
	// Another restore acts again.
	if acted, err = s.SignEveryoneOutAfterRestore(ctx, "2026-10-14T09:30:00Z"); err != nil || !acted {
		t.Fatalf("a later restore: acted=%v %v", acted, err)
	}
	if _, err = s.Authenticate(again.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a later restore left a session alive: %v", err)
	}
}

// scrubCopyAt scrubs the copy at path the way the backup service does: on a
// private candidate handle, closed afterwards so its log folds into the file.
func scrubCopyAt(ctx context.Context, path string) error {
	copy, err := dbwork.OpenCandidate(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return err
	}
	if err = ScrubBackupCopy(ctx, copy); err != nil {
		copy.Close()
		return err
	}
	return copy.Close()
}
