package identity

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSetupCodeExpirySurvivesRestart(t *testing.T) {
	_, db, dir := familyFixture(t)
	if _, err := db.Exec(`DELETE FROM accounts`); err != nil {
		t.Fatal(err)
	}
	path := storagepaths.Path(dir, "setup-token")
	oldCode, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	// No expiry: a restart keeps the same resume secret, however old.
	restarted, err := New(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	newCode, err := restarted.BrowserSetupToken(context.Background())
	if err != nil || newCode != string(oldCode) {
		t.Fatalf("restart replaced the setup resume secret: %v", err)
	}
}

func TestP09QuickConnectCommitsOneFamilyAndRecoversExactGrant(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	owner := issueFamily(t, s)
	p, e := s.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	q := QuickStart{RequestID: "11111111-1111-4111-8111-111111111111", DeviceName: "Living Room", Platform: "tvos", AppVersion: "1.0"}
	a, e := s.StartQuick(ctx, q, true)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.StartQuick(ctx, q, true)
	if e != nil || a.DeviceCode != again.DeviceCode || a.UserCode != again.UserCode {
		t.Fatal("create was not idempotent", e)
	}
	preview, e := s.ReviewQuick(ctx, strings.ToLower(strings.ReplaceAll(a.UserCode, "-", " ")))
	if e != nil {
		t.Fatal(e)
	}
	// The phone approves in its own request; the TV collects in its own.
	phone, e := WithIssuingDevice(ctx, DeviceRegistration{InstallationID: Token(), Name: "Phone", Platform: "ios", App: "portico", AppVersion: "1.0"}, "127.0.0.1:1")
	if e != nil {
		t.Fatal(e)
	}
	tvInstallation := Token()
	tv, e := WithIssuingDevice(ctx, DeviceRegistration{InstallationID: tvInstallation, Name: "Living Room", Platform: "tvos", App: "portico", AppVersion: "1.0"}, "127.0.0.1:2")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DecideQuick(phone, p, preview.RequestID, a.UserCode, "approve"); e != nil {
		t.Fatal(e)
	}
	if e = s.DecideQuick(phone, p, preview.RequestID, a.UserCode, "approve"); e != nil {
		t.Fatal("duplicate approval", e)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM authorization_session_families`).Scan(&count)
	if count != 1 {
		t.Fatalf("approval issued a family before the TV collected it: %d", count)
	}
	db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`)
	first, e := s.PollQuick(tv, a.DeviceCode)
	if e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT count(*) FROM authorization_session_families`).Scan(&count)
	if count != 2 {
		t.Fatalf("expected owner plus one issued family, got %d", count)
	}
	var bound, platform string
	if e = db.QueryRow(`SELECT d.installation_id,d.platform FROM identity_device_families f JOIN identity_devices d ON d.id=f.device_id WHERE f.family_id=?`, first.SessionFamilyID).Scan(&bound, &platform); e != nil || bound != tvInstallation || platform != "tvos" {
		t.Fatalf("TV session bound to %q (%s), not the TV: %v", bound, platform, e)
	}
	// The TV is signed in as whoever approved the code: here the owner.
	if first.Viewer.Role != p.Role || first.Viewer.AccountID != p.AccountID {
		t.Fatalf("TV signed in as %s (%s), not as the approver %s (%s)", first.Viewer.AccountID, first.Viewer.Role, p.AccountID, p.Role)
	}
	if first.InstallationID == "" {
		t.Fatal("Quick Connect grant omitted its refresh binding")
	}
	if _, err := s.Authenticate(first.AccessToken); err != nil {
		t.Fatalf("the TV's token was rejected: %v", err)
	}
	// The owner's television does what the owner can.
	if _, err := s.DirectMembers(ctx, first.AccessToken); err != nil {
		t.Fatalf("the owner's TV could not read the accounts the owner can: %v", err)
	}
	if a.UserCode[0] < '2' || a.UserCode[0] > '9' {
		t.Fatal("server code must start with a digit")
	}
	db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`)
	second, e := s.PollQuick(tv, a.DeviceCode)
	if e != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("lost response recovery changed family", e)
	}
	if refreshed, refreshErr := s.RefreshSession(ctx, first.RefreshToken, first.InstallationID, Token()); refreshErr != nil || refreshed.InstallationID != first.InstallationID {
		t.Fatal("Quick Connect grant could not refresh from its response", refreshErr)
	}
	if e = s.CancelQuick(ctx, a.DeviceCode); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(first.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("cancelled family remained active")
	}
	if _, e = s.Authenticate(owner.AccessToken); e != nil {
		t.Fatal("unrelated owner family was revoked", e)
	}
}

// A device linked with a code is signed in as whoever approved it: it can do what
// that person's own session can, and no more. A member's television manages the
// member's profiles as the member does; a session that is itself viewer-only
// links a viewer-only television.
func TestQuickConnectSignsTheDeviceInAsTheApprover(t *testing.T) {
	s, db, _ := familyFixture(t)
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member-account','member',X'00','member-profile',1)`); err != nil {
		t.Fatal(err)
	}
	member, err := s.Issue("member-account", "member-profile", "local", TierMember, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(member.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	link := func(request string) Envelope {
		t.Helper()
		a, err := s.StartQuick(ctx, QuickStart{RequestID: request, DeviceName: "TV", Platform: "tvos", AppVersion: "1"}, true)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := s.ReviewQuick(ctx, a.UserCode)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.DecideQuick(ctx, p, preview.RequestID, a.UserCode, "approve"); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`); err != nil {
			t.Fatal(err)
		}
		grant, err := s.PollQuick(ctx, a.DeviceCode)
		if err != nil {
			t.Fatal(err)
		}
		return grant
	}
	grant := link("33333333-3333-4333-8333-333333333333")
	if grant.Viewer.AccountID != "member-account" || grant.Viewer.Role != TierMember {
		t.Fatalf("television signed in as %s (%s), not as the member who approved it", grant.Viewer.AccountID, grant.Viewer.Role)
	}
	if _, err = s.DirectMe(ctx, grant.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateDirectProfile(ctx, grant.AccessToken, "Second", ""); err != nil {
		t.Fatalf("the member's television could not do what the member's own session can: %v", err)
	}

}
func TestP09QuickConnectReplayCannotRestoreRotatedAuthority(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	owner := issueFamily(t, s)
	p, _ := s.Authenticate(owner.AccessToken)
	a, e := s.StartQuick(ctx, QuickStart{"22222222-2222-4222-8222-222222222222", "TV", "tvos", "1.0"}, true)
	if e != nil {
		t.Fatal(e)
	}
	v, _ := s.ReviewQuick(ctx, a.UserCode)
	if e = s.DecideQuick(ctx, p, v.RequestID, a.UserCode, "approve"); e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`)
	grant, e := s.PollQuick(ctx, a.DeviceCode)
	if e != nil {
		t.Fatal(e)
	}
	renewed := renewFamily(t, s, grant.AccessToken, "p09-rotation")
	db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`)
	if _, e = s.PollQuick(ctx, a.DeviceCode); e == nil {
		t.Fatal("retired setup grant replayed")
	}
	if _, e = s.Authenticate(renewed.AccessToken); e != nil {
		t.Fatal("current family was lost", e)
	}
}

func TestP09RecoveryPasswordAndHumanCodePolicy(t *testing.T) {
	// One rule for every local password: 8+ characters (bcrypt caps input at 72 bytes).
	for _, password := range []string{"short9!", strings.Repeat("a", 73), "abcdefg\xff"} {
		if directPassword(password) {
			t.Fatalf("password %q accepted", password)
		}
	}
	for _, password := range []string{"abcdefgh", "password", "orchard riverside candle marigold"} {
		if !directPassword(password) {
			t.Fatalf("password %q rejected", password)
		}
	}
	for i := 0; i < 100; i++ {
		code, e := quickCode()
		if e != nil || len(code) != 8 {
			t.Fatal(e)
		}
		if _, e = normalizedQuickCode(code); e != nil {
			t.Fatal(e)
		}
	}
}

// Setup from outside the LAN is allowed (no setup code), and the recovery owner
// it creates stays reachable from there so the person setting up is never locked out.

func TestPruneSetupExpiresApprovedCodeThatWasNeverCollected(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	owner := issueFamily(t, s)
	p, e := s.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.StartQuick(ctx, QuickStart{RequestID: "33333333-3333-4333-8333-333333333333", DeviceName: "Den", Platform: "tvos", AppVersion: "1.0"}, true)
	if e != nil {
		t.Fatal(e)
	}
	preview, e := s.ReviewQuick(ctx, a.UserCode)
	if e != nil {
		t.Fatal(e)
	}
	phone, e := WithIssuingDevice(ctx, DeviceRegistration{InstallationID: Token(), Name: "Phone", Platform: "ios", App: "portico", AppVersion: "1.0"}, "127.0.0.1:1")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DecideQuick(phone, p, preview.RequestID, a.UserCode, "approve"); e != nil {
		t.Fatal(e)
	}
	stale := time.Now().Add(-48 * time.Hour).Unix()
	if _, e = db.Exec(`UPDATE quick_connect_v1 SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), preview.RequestID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO setup_rate_limits_v1(subject_hash,kind,window_start,attempts) VALUES('stale','quick',?,1)`, stale); e != nil {
		t.Fatal(e)
	}
	if e = s.PruneSetup(ctx); e != nil {
		t.Fatalf("clean-up failed on an approved code that was never collected: %v", e)
	}
	var status string
	var cipher []byte
	var userHash sql.NullString
	if e = db.QueryRow(`SELECT status,grant_cipher,user_hash FROM quick_connect_v1 WHERE id=?`, preview.RequestID).Scan(&status, &cipher, &userHash); e != nil {
		t.Fatal(e)
	}
	if status != "expired" || cipher != nil || userHash.Valid {
		t.Fatalf("uncollected approval was left as %q (approval kept=%v, code kept=%v)", status, cipher != nil, userHash.Valid)
	}
	var limits int
	if e = db.QueryRow(`SELECT count(*) FROM setup_rate_limits_v1 WHERE subject_hash='stale'`).Scan(&limits); e != nil || limits != 0 {
		t.Fatalf("clean-up stopped before removing old rate-limit rows: %d %v", limits, e)
	}
	// The approver's own session is untouched, and the expired code can no longer be collected.
	if _, e = s.Authenticate(owner.AccessToken); e != nil {
		t.Fatalf("expiring an uncollected code revoked the approver: %v", e)
	}
	// A later run removes the expired row once it is a day old.
	if _, e = db.Exec(`UPDATE quick_connect_v1 SET expires_at=? WHERE id=?`, stale, preview.RequestID); e != nil {
		t.Fatal(e)
	}
	if e = s.PruneSetup(ctx); e != nil {
		t.Fatal(e)
	}
	var left int
	if e = db.QueryRow(`SELECT count(*) FROM quick_connect_v1 WHERE id=?`, preview.RequestID).Scan(&left); e != nil || left != 0 {
		t.Fatalf("expired code was never deleted: %d %v", left, e)
	}
}
