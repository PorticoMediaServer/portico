package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// BE-member item 2: every session-issuing path must produce a
// device-bound session that renews. One test per path: issue, then
// POST /v1/auth/refresh, then a subtitle read, all 200.

// qcInstallHeaders is the installation claim a first-party client sends on
// credential-issuing requests.
func qcInstallHeaders(installation string) map[string]string {
	return map[string]string{
		"X-Portico-Installation-Id": installation,
		"X-Portico-Device-Name":     "Renewal TV",
		"X-Portico-Device-Platform": "tvos",
		"X-Portico-App":             "portico",
		"X-Portico-App-Version":     "1.0",
	}
}

// qcRefresh renews over HTTP and requires 200.
func qcRefresh(t *testing.T, f *tl6V1Fixture, refreshToken, installation string) identity.Envelope {
	t.Helper()
	var out identity.Envelope
	f.callAs("", "POST", "/v1/auth/refresh", nil, map[string]string{
		"refreshToken": refreshToken, "installationId": installation, "requestId": identity.Token(),
	}, 200, &out)
	if out.AccessToken == "" || out.RefreshToken == "" || out.RefreshToken == refreshToken {
		t.Fatalf("refresh did not rotate credentials: %+v", out)
	}
	return out
}

// qcSubtitleRead starts a playback session and reads its subtitle plan, both
// over HTTP, requiring 200. The plan authenticates the bearer, so a session
// without a device binding fails here with 401.
func qcSubtitleRead(t *testing.T, f *tl6V1Fixture, accessToken, key string) {
	t.Helper()
	var started struct {
		ID string `json:"id"`
	}
	f.callAs(accessToken, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": (key + "-0000000000000000")[:max(16, len(key))]}, map[string]string{"itemId": f.items[0]}, 201, &started)
	var plan subtitles.PlaybackPlan
	f.callAs(accessToken, "GET", "/v1/items/"+f.items[0]+"/playback/"+started.ID+"/subtitles", nil, nil, 200, &plan)
	if plan.SessionID != started.ID {
		t.Fatalf("subtitle plan for the wrong session: %+v", plan)
	}
}

// qcTOTP computes the current six-digit code for a test TOTP secret, mirroring
// the server's own algorithm.
func qcTOTP(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(time.Now().Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// Quick Connect: the TV starts with its installation claim, the owner
// approves, and the TV collects WITHOUT any installation headers (an older
// client, or a poll that lost them). The grant must still bind the TV's
// installation from the start request, so the TV can renew with it.
func TestQCFamilyQuickConnectRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	tvInstallation := identity.Token()
	start := f.raw("POST", "/v1/quick-connect", "", qcInstallHeaders(tvInstallation), map[string]string{
		"requestId": "11111111-1111-4111-8111-111111111111", "deviceName": "Renewal TV", "platform": "tvos", "appVersion": "1.0",
	})
	if start.Code != 201 {
		t.Fatalf("start %d %s", start.Code, start.Body.String())
	}
	var auth identity.QuickAuthorization
	if err := json.Unmarshal(start.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	var preview identity.QuickPreview
	f.callAs(f.owner.AccessToken, "POST", "/v1/quick-connect/review", nil, map[string]string{"userCode": auth.UserCode}, 200, &preview)
	f.callAs(f.owner.AccessToken, "POST", "/v1/quick-connect/decision", nil, map[string]string{
		"requestId": preview.RequestID, "userCode": auth.UserCode, "decision": "approve",
	}, 204, nil)
	if _, err := f.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`); err != nil {
		t.Fatal(err)
	}
	var grant identity.Envelope
	f.callAs("", "POST", "/v1/quick-connect/token", nil, map[string]string{"deviceCode": auth.DeviceCode}, 200, &grant)
	if grant.InstallationID != tvInstallation {
		t.Fatalf("Quick Connect grant bound to %q, not the TV %q", grant.InstallationID, tvInstallation)
	}
	refreshed := qcRefresh(t, f, grant.RefreshToken, tvInstallation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00001")
}

// Direct sign-in: the session binds the installation the sign-in carried,
// renews with it, and reads subtitles.
func TestQCFamilyDirectSignInRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	installation := identity.Token()
	var signed identity.DirectSignIn
	f.callAs("", "POST", "/v1/direct/sign-in", qcInstallHeaders(installation), map[string]string{
		"username": "owner", "password": "long-test-password",
	}, 200, &signed)
	if signed.Session == nil {
		t.Fatal("sign-in returned no session")
	}
	refreshed := qcRefresh(t, f, signed.Session.RefreshToken, installation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00002")
}

// Portico sign-in: a Portico Account member signs in through its Hosted
// identity and gets the same device-bound, renewable session.
func TestQCFamilyPorticoSignInRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	// Portico admission starts from a completed password-free Portico server.
	// Linking an account on a Local server must remain a constraint failure.
	if _, err := f.db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, f.owner.Viewer.AccountID, "hosted-renewal-1"); err != nil {
		t.Fatal(err)
	}
	installation := identity.Token()
	ctx, err := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{
		InstallationID: installation, Name: "Renewal TV", Platform: "tvos", App: "portico", AppVersion: "1.0",
	}, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.id.PorticoSignIn(ctx, identity.PorticoIdentity{AccountID: "hosted-renewal-1", Username: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if signed.Session == nil {
		t.Fatal("Portico sign-in returned no session")
	}
	refreshed := qcRefresh(t, f, signed.Session.RefreshToken, installation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00003")
}

// Two-factor completion: the challenge the password step raised completes
// into the same device-bound, renewable session.
func TestQCFamilyTwoFactorSignInRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	installation := identity.Token()
	headers := qcInstallHeaders(installation)
	var enrol identity.TwoFactorEnrolment
	f.callAs(f.owner.AccessToken, "POST", "/v1/direct/two-factor/enrol", nil, map[string]string{"password": "long-test-password"}, 201, &enrol)
	f.callAs(f.owner.AccessToken, "POST", "/v1/direct/two-factor/verify", nil, map[string]string{
		"password": "long-test-password", "code": qcTOTP(t, enrol.Secret),
	}, 200, nil)
	var challenged identity.DirectSignIn
	f.callAs("", "POST", "/v1/direct/sign-in", headers, map[string]string{
		"username": "owner", "password": "long-test-password",
	}, 200, &challenged)
	if challenged.Challenge == nil {
		t.Fatal("a confirmed factor did not raise a challenge")
	}
	// Enrolment consumed the current TOTP step; rewinding is how the test
	// expresses the minutes-later completion without sleeping.
	if _, err := f.db.Exec(`UPDATE identity_account_factors SET last_step=0`); err != nil {
		t.Fatal(err)
	}
	var completed identity.DirectSignIn
	f.callAs("", "POST", "/v1/auth/two-factor/challenge", headers, map[string]string{
		"token": challenged.Challenge.Token, "code": qcTOTP(t, enrol.Secret),
	}, 200, &completed)
	if completed.Session == nil {
		t.Fatal("completing the challenge produced no session")
	}
	refreshed := qcRefresh(t, f, completed.Session.RefreshToken, installation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00004")
}

// Self-registration: a new member registers, renews, and reads subtitles in
// a library the owner shares with it.
func TestQCFamilyRegisterRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	if err := f.id.SetSelfRegistration(identity.SelfRegistrationOpen); err != nil {
		t.Fatal(err)
	}
	installation := identity.Token()
	var registered identity.DirectSignIn
	f.callAs("", "POST", "/v1/auth/register", qcInstallHeaders(installation), map[string]string{
		"username": "guest1", "password": "Guest-Password1!",
	}, 201, &registered)
	if registered.Session == nil {
		t.Fatal("registration returned no session")
	}
	if _, err := f.db.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, `["`+f.library+`"]`, registered.Session.Viewer.AccountID); err != nil {
		t.Fatal(err)
	}
	refreshed := qcRefresh(t, f, registered.Session.RefreshToken, installation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00005")
}

// Setup: first-run ownership creates the owner session bound to the setup
// device, which renews and reads subtitles.
func TestQCFamilySetupRenews(t *testing.T) {
	f, token := qcSetupFixture(t)
	installation := identity.Token()
	var owner identity.Envelope
	f.callAs("", "POST", "/v1/setup", qcInstallHeaders(installation), map[string]string{
		"authMode": "local", "setupToken": token, "username": "owner", "password": "long-test-password", "name": "Test",
	}, 201, &owner)
	if owner.RefreshToken == "" {
		t.Fatal("setup returned no refresh credential")
	}
	refreshed := qcRefresh(t, f, owner.RefreshToken, installation)
	qcSubtitleRead(t, f, refreshed.AccessToken, "qc-family-renewal-00006")
}

// qcSetupFixture is newV1Fixture without the setup step, so the test itself
// can own first-run setup over HTTP.
func qcSetupFixture(t *testing.T) (*tl6V1Fixture, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	cat := catalog.New(db)
	f := &tl6V1Fixture{t: t, db: db, id: id, cat: cat, root: root, v1: &playbackv1.Service{}}
	player := playback.New(db)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := subtitles.New(subtitles.Options{DB: db, Directory: filepath.Join(realRoot, "subtitles"), Storage: storage.New(binary), HelperBinary: binary,
		Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { subs.Close() })
	player.ConfigureSubtitleDelivery(subs)
	f.deps = Dependencies{DB: db, Identity: id, Catalog: cat, Playback: player, Subtitles: subs, PlaybackV1: f.v1}
	f.handler = New(f.deps)
	f.deps.v1 = f.v1
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	f.library = lib.ID
	path := filepath.Join(root, "Film 00.mp4")
	if err = os.WriteFile(path, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	item := c.Movie(c.Handle(lib.ID), path, "Film 00", 2000)
	c.Drain()
	f.items = []string{item.Public}
	secret, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	return f, string(secret)
}

// Device authorization (Cast pairing): redeeming a pairing code issues the
// receiver an ordinary bearer bound to the receiver's own device record. The
// receiver renews with POST /v1/cast/reconnect and reads subtitles with the
// renewed bearer, all 200.
func TestQCFamilyCastRedeemRenews(t *testing.T) {
	f, _, _, _ := tl6SubtitleFixture(t)
	var bootstrap social.CastBootstrapResponse
	f.callAs(f.owner.AccessToken, "POST", "/v1/cast/bootstrap", nil, social.CastBootstrapRequest{
		ProtocolVersion: social.Protocol, DisplayName: "Renewal TV",
	}, 201, &bootstrap)
	var redeemed social.CastReceiverSession
	f.callAs("", "POST", "/v1/cast/redeem", nil, social.CastRedeemRequest{
		ProtocolVersion: social.Protocol, Code: bootstrap.Bootstrap.Code, ExpectedBootstrapID: bootstrap.Bootstrap.ID, DeviceID: "cast-renewal-1", DisplayName: "Renewal TV",
	}, 200, &redeemed)
	if redeemed.Session.AccessToken == "" || redeemed.DeviceToken == "" {
		t.Fatal("redeem issued no session")
	}
	var family, installation, name string
	if err := f.db.QueryRow(`SELECT f.family_id,d.installation_id,d.name FROM identity_device_families f JOIN identity_devices d ON d.id=f.device_id JOIN authorization_family_tokens t ON t.family_id=f.family_id WHERE t.token_hash=?`, identity.Digest(redeemed.Session.AccessToken)).Scan(&family, &installation, &name); err != nil {
		t.Fatalf("redeemed session has no device binding: %v", err)
	}
	if installation == "" || name != "Renewal TV" {
		t.Fatalf("redeemed session bound to %q (%q), not the receiver", installation, name)
	}
	var renewed social.CastReceiverSession
	f.callAs("", "POST", "/v1/cast/reconnect", nil, social.CastReconnectRequest{
		ProtocolVersion: social.Protocol, DeviceToken: redeemed.DeviceToken,
	}, 200, &renewed)
	if renewed.Session.AccessToken == "" || renewed.Session.AccessToken == redeemed.Session.AccessToken {
		t.Fatal("reconnect did not rotate the bearer")
	}
	// Renewal keeps the receiver's one device record: no phantom per reconnect.
	var renewedInstallation string
	if err := f.db.QueryRow(`SELECT d.installation_id FROM identity_device_families f JOIN identity_devices d ON d.id=f.device_id JOIN authorization_family_tokens t ON t.family_id=f.family_id WHERE t.token_hash=?`, identity.Digest(renewed.Session.AccessToken)).Scan(&renewedInstallation); err != nil {
		t.Fatalf("renewed session has no device binding: %v", err)
	}
	if renewedInstallation != installation {
		t.Fatalf("reconnect moved the receiver from %q to %q", installation, renewedInstallation)
	}
	qcSubtitleRead(t, f, renewed.Session.AccessToken, "qc-family-renewal-00007")
}
