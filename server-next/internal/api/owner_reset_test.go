package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

func resetFileCode(t *testing.T, s *Server) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.dataDir, ownerResetFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 4 {
		t.Fatal("reset file is incomplete")
	}
	return lines[2]
}

func askOwnerReset(t *testing.T, s *Server) string {
	t.Helper()
	if got := status(t, s, "POST", "/v1/auth/owner-reset", "", OwnerResetStart{}); got != 200 {
		t.Fatalf("asking for reset file: %d", got)
	}
	return resetFileCode(t, s)
}

func TestOwnerResetSessionPreservesTheServer(t *testing.T) {
	s, owner, member := porticoServer(t)
	// The Services worker updates the link's sync state independently of recovery.
	s.link.stop()
	<-s.link.done
	memberProfiles := get[Page[Profile]](t, s, member, "/v1/profiles")
	var memberSession Session
	call(t, s, "POST", "/v1/profiles/"+memberProfiles.Items[0].ID+"/select", member, ProfileSelect{}, &memberSession)
	member = memberSession.AccessToken
	ctx := context.Background()
	before := get[SignInState](t, s, owner, "/v1/admin/sign-in")
	profiles := get[Page[Profile]](t, s, owner, "/v1/profiles")
	main := profiles.Items[0].ID
	if got := status(t, s, "PUT", "/v1/profiles/"+main+"/pin", owner, ProfilePIN{PIN: "1234"}); got != 200 {
		t.Fatalf("setting main PIN: %d", got)
	}
	fake := s.hosted.(*fakeHosted)
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	challenge, identity := porticoProof(t, s)
	var old SignInResponse
	call(t, s, "POST", "/v1/auth/portico/exchange", "", PorticoExchange{Challenge: challenge, Identity: identity, Device: device(8)}, &old)
	s.hosted = &fakeHosted{t: t, fail: true}
	link, linked, err := s.db.Link(ctx)
	if err != nil || !linked {
		t.Fatalf("reading the Portico link before recovery: linked=%t, err=%v", linked, err)
	}
	now := time.Now().Truncate(time.Millisecond)
	s.setClock(func() time.Time { return now })
	code := askOwnerReset(t, s)
	if !auth.IsCode(code, 5) {
		t.Fatal("reset proof must have 20 random symbols")
	}
	var recovered SignInResponse
	if got := from(t, s, "198.51.100.19", "", "POST", "/v1/auth/owner-reset/session", "",
		OwnerResetSessionRequest{Code: code, Device: device(9)}, &recovered); got != 200 {
		t.Fatalf("recovering away from home: %d", got)
	}
	token := recovered.Session.AccessToken
	if recovered.Account.ID != before.Owner.ID || recovered.Session.Viewer.Role != "owner" || recovered.Session.Viewer.ProfileID != main {
		t.Fatal("reset did not enter the existing owner's main profile")
	}
	if recovered.Session.InstallationID != device(9).InstallationID || !recovered.Session.SessionExpiresAt.Equal(now.Add(s.sessionIdle())) {
		t.Fatal("reset did not use the ordinary device/session lifetime")
	}
	for _, oldToken := range []string{owner, old.Session.AccessToken} {
		if got := status(t, s, "GET", "/v1/me", oldToken, nil); got != 401 {
			t.Fatalf("old owner access: %d", got)
		}
	}
	if got, _ := renew(t, s, old.Session.RefreshToken, 8, "old-owner"); got != 401 {
		t.Fatalf("old owner refresh: %d", got)
	}
	if got := status(t, s, "GET", "/v1/me", member, nil); got != 200 {
		t.Fatalf("member session changed: %d", got)
	}
	if after := get[SignInState](t, s, token, "/v1/admin/sign-in"); !reflect.DeepEqual(before, after) {
		t.Fatal("recovery changed sign-in kind, owner, or waiting members")
	}
	if after, linked, err := s.db.Link(ctx); err != nil || !linked || !reflect.DeepEqual(link, after) {
		t.Fatal("recovery changed the Portico link")
	}
	if id, _ := s.db.PorticoAccountID(ctx, 1); id != "pa_1" {
		t.Fatal("owner's Portico Account link changed")
	}
	if creds, _ := s.db.CredentialsByUsername(ctx, "justin"); creds.PasswordHash != "" {
		t.Fatal("recovery created a local password")
	}
	for _, d := range get[Page[Device]](t, s, token, "/v1/devices").Items {
		if d.SignedIn != d.Current {
			t.Fatal("an earlier owner's device still appears signed in")
		}
	}
	noticed := get[Notifications](t, s, token, "/v1/notifications")
	if len(noticed.Items) != 1 || !strings.Contains(noticed.Items[0].Body, "entered with the reset file") {
		t.Fatal("the owner was not told about reset-file entry")
	}
	if len(get[Notifications](t, s, member, "/v1/notifications").Items) != 0 {
		t.Fatal("a member received the owner's security notice")
	}
	audited := false
	for _, entry := range get[Page[ActivityEntry]](t, s, token, "/v1/admin/audit").Items {
		if entry.Action == "owner.reset_file_sign_in" {
			audited = entry.At.Equal(now.Truncate(time.Millisecond)) && strings.Contains(entry.Detail, "Device 9") && strings.Contains(entry.Detail, "198.51.100.19")
			if strings.Contains(entry.Detail, code) {
				t.Fatal("activity exposed the disk proof")
			}
		}
	}
	if !audited {
		t.Fatal("recovery activity lacks the time, device or observed address")
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, ownerResetFile)); !os.IsNotExist(err) {
		t.Fatal("spent reset file remains")
	}
	got, renewed := renew(t, s, recovered.Session.RefreshToken, 9, "recovery")
	if got != 200 || renewed.Viewer.Role != "owner" {
		t.Fatalf("ordinary recovery-session refresh: %d", got)
	}
	token = renewed.AccessToken
	selectPath := "/v1/profiles/" + main + "/select"
	if got := status(t, s, "POST", selectPath, token, ProfileSelect{}); got != 403 {
		t.Fatalf("PIN was bypassed again: %d", got)
	}
	var selected Session
	if got := call(t, s, "POST", selectPath, token, ProfileSelect{PIN: "1234"}, &selected).Code; got != 200 {
		t.Fatalf("original PIN no longer works: %d", got)
	}
	token = selected.AccessToken
	if got := status(t, s, "POST", "/v1/admin/owner", token, OwnerChangeRequest{Previous: "admin",
		NewAccount: &MemberCreate{Username: "another"}}); got != 400 {
		t.Fatalf("change owner without the new Portico approval: %d", got)
	}
	var preview SignInPreview
	call(t, s, "POST", "/v1/admin/sign-in/preview", token, SignInPreviewRequest{To: "local"}, &preview)
	if !preview.PorticoAddressRemoved || preview.SignInAgain != 1 {
		t.Fatal("recovered owner cannot preview a local move")
	}
	if got := status(t, s, "POST", "/v1/admin/sign-in/switch", token,
		SignInSwitchRequest{To: "local", Username: "justin", Password: testPassword, Device: device(9)}); got != 200 {
		t.Fatalf("explicit offline switch after recovery: %d", got)
	}
}

// Error bodies differ only by the ordinary tracing id. Timing classes are enforced by
// the shared database path and constant-time digest comparison, not wall-clock assertions.
func TestOwnerResetSessionDoesNotRevealPendingCodes(t *testing.T) {
	s, _, _ := porticoServer(t)
	now := time.Now().Truncate(time.Millisecond)
	s.setClock(func() time.Time { return now })
	refuse := func(code string) errorBody {
		t.Helper()
		var out errorBody
		w := call(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: device(9)}, &out)
		if w.Code != 401 {
			t.Fatalf("invalid proof answered %d", w.Code)
		}
		out.Error.RequestID = ""
		return out
	}
	absent := refuse("wrong")
	code := askOwnerReset(t, s)
	wrong := refuse("wrong")
	now = now.Add(ownerResetLife)
	expired := refuse(code)
	code = askOwnerReset(t, s)
	if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: device(9)}); got != 200 {
		t.Fatalf("valid proof: %d", got)
	}
	spent := refuse(code)
	for _, got := range []errorBody{wrong, expired, spent} {
		if !reflect.DeepEqual(absent, got) {
			t.Fatal("refusal reveals whether a reset was pending")
		}
	}
}

func TestOwnerResetFiveGuessesAcrossNetworksAndRoutes(t *testing.T) {
	s, _, _ := porticoServer(t)
	now := time.Now().Truncate(time.Millisecond)
	s.setClock(func() time.Time { return now })
	code := askOwnerReset(t, s)
	for n := range 5 {
		peer := fmt.Sprintf("198.51.100.%d", n+1)
		path := "/v1/auth/owner-reset/session"
		var body any = OwnerResetSessionRequest{Code: "wrong", Device: device(9)}
		if n%2 == 1 {
			path, body = "/v1/auth/owner-reset/confirm", OwnerResetFinish{Code: "wrong", NewPassword: testPassword}
		}
		if got := from(t, s, peer, "", "POST", path, "", body, nil); got != 401 {
			t.Fatalf("wrong try %d: %d", n+1, got)
		}
	}
	if got := from(t, s, "198.51.100.99", "", "POST", "/v1/auth/owner-reset/session", "",
		OwnerResetSessionRequest{Code: code, Device: device(9)}, nil); got != 401 {
		t.Fatalf("cancelled code from a sixth network: %d", got)
	}
	if askOwnerReset(t, s) != code {
		t.Fatal("cancelled code replaced before cooldown")
	}
	now = now.Add(time.Minute)
	fresh := askOwnerReset(t, s)
	if fresh == code {
		t.Fatal("cancelled proof not replaced after cooldown")
	}
	if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: fresh, Device: device(9)}); got != 200 {
		t.Fatalf("new disk proof after cancellation: %d", got)
	}
}

func TestOwnerResetRefusesWrongMethodAndMemberAuthority(t *testing.T) {
	t.Run("local code cannot issue a session", func(t *testing.T) {
		s := newTestServer(t)
		onMainProfile(t, s)
		code := askOwnerReset(t, s)
		if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: device(9)}); got != 401 {
			t.Fatalf("local proof bypassed password reset: %d", got)
		}
	})
	t.Run("Portico code cannot make a password", func(t *testing.T) {
		s, owner, _ := porticoServer(t)
		code := askOwnerReset(t, s)
		if got := status(t, s, "POST", "/v1/auth/owner-reset/confirm", "", OwnerResetFinish{Code: code, NewPassword: testPassword}); got != 401 {
			t.Fatalf("Portico owner received a local password: %d", got)
		}
		if got := status(t, s, "GET", "/v1/me", owner, nil); got != 200 {
			t.Fatal("refused method revoked owner")
		}
		if creds, _ := s.db.CredentialsByUsername(context.Background(), "justin"); creds.PasswordHash != "" {
			t.Fatal("password created")
		}
	})
	t.Run("member token and member code prove no owner", func(t *testing.T) {
		s, owner, member := porticoServer(t)
		claims, _ := s.tokens.CheckAccess(member, s.now())
		code := auth.NewCode(5)
		err := s.db.CreateCode(context.Background(), db.Code{Kind: "password_reset", Hash: ownerResetHash(code), UserID: claims.UserID,
			ExpiresAt: s.now().Add(time.Minute)}, s.now())
		if err != nil {
			t.Fatal(err)
		}
		askOwnerReset(t, s)
		for _, proof := range []string{"", code} {
			if got := status(t, s, "POST", "/v1/auth/owner-reset/session", member, OwnerResetSessionRequest{Code: proof, Device: device(9)}); got != 401 {
				t.Fatalf("member authority opened owner: %d", got)
			}
		}
		if got := status(t, s, "GET", "/v1/me", owner, nil); got != 200 {
			t.Fatal("member attempts changed owner")
		}
	})
}

func TestOwnerResetInvalidDeviceDoesNotSpendTheCode(t *testing.T) {
	s, _, _ := porticoServer(t)
	code := askOwnerReset(t, s)
	for _, bad := range []DeviceInfo{{}, {InstallationID: "short", Name: "Phone"}, {InstallationID: device(9).InstallationID},
		{InstallationID: device(9).InstallationID, Name: "Phone", Platform: strings.Repeat("x", 65)}} {
		if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: bad}); got != 400 {
			t.Fatalf("invalid device accepted: %d", got)
		}
	}
	if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: device(9)}); got != 200 {
		t.Fatalf("invalid devices spent the code: %d", got)
	}
}

func TestOwnerResetSessionRollsBackAndRacesOnce(t *testing.T) {
	s, owner, _ := porticoServer(t)
	now := s.now()
	code := askOwnerReset(t, s)
	claims, _ := s.tokens.CheckAccess(owner, now)
	session, _, err := s.newSession(newRequest("POST", "/v1/auth/owner-reset/session", "", nil), device(9), now)
	if err != nil {
		t.Fatal(err)
	}
	session.ID = claims.SessionID // fail session creation after spending/revoking
	if _, err := s.db.OwnerResetSession(context.Background(), ownerResetHash(code), "198.51.100.19", session, nil, now); err == nil {
		t.Fatal("duplicate session id did not fail")
	}
	if got := status(t, s, "GET", "/v1/me", owner, nil); got != 200 {
		t.Fatal("failed recovery revoked the owner")
	}
	if _, err := s.db.PeekCode(context.Background(), "owner_reset", ownerResetHash(code), now); err != nil {
		t.Fatal("failed recovery spent the code")
	}
	if len(get[Notifications](t, s, owner, "/v1/notifications").Items) != 0 {
		t.Fatal("failed recovery sent a notice")
	}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- serve(s, newRequest("POST", "/v1/auth/owner-reset/session", "", OwnerResetSessionRequest{Code: code, Device: device(9)})).Code
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for got := range results {
		if got == 200 {
			wins++
		} else if got != 401 {
			t.Fatalf("racing recovery answered %d", got)
		}
	}
	if wins != 1 {
		t.Fatalf("one proof created %d sessions", wins)
	}
}

func TestOwnerResetProxyLimitsAndObservedAddress(t *testing.T) {
	s, owner, _ := porticoServer(t)
	const proxy = "10.9.9.9"
	if got := status(t, s, "PUT", "/v1/admin/settings/network", owner,
		NetworkSettingsEdit{HomeNetworks: &[]string{"127.0.0.0/8"}, TrustedProxies: []string{proxy}}); got != 200 {
		t.Fatal("network save failed")
	}
	askOwnerReset(t, s)
	wrong := OwnerResetSessionRequest{Code: "wrong", Device: device(9)}
	for n := range 5 {
		if got := from(t, s, proxy, "198.51.100.1", "POST", "/v1/auth/owner-reset/session", "", wrong, nil); got != 401 {
			t.Fatalf("wrong guess %d: %d", n, got)
		}
	}
	if got := from(t, s, proxy, "198.51.100.1", "POST", "/v1/auth/owner-reset/session", "", wrong, nil); got != 429 {
		t.Fatalf("trusted forwarded network not slowed: %d", got)
	}
	if got := from(t, s, proxy, "198.51.100.2", "POST", "/v1/auth/owner-reset/session", "", wrong, nil); got != 401 {
		t.Fatalf("proxy blocked another visitor: %d", got)
	}
	now := s.now().Add(time.Minute)
	s.setClock(func() time.Time { return now })
	code := askOwnerReset(t, s)
	var recovered SignInResponse
	if got := from(t, s, proxy, "198.51.100.3", "POST", "/v1/auth/owner-reset/session", "",
		OwnerResetSessionRequest{Code: code, Device: device(9)}, &recovered); got != 200 {
		t.Fatalf("recovery behind trusted proxy: %d", got)
	}
	found := false
	for _, entry := range get[Page[ActivityEntry]](t, s, recovered.Session.AccessToken, "/v1/admin/audit").Items {
		if entry.Action == "owner.reset_file_sign_in" {
			found = strings.Contains(entry.Detail, "198.51.100.3") && !strings.Contains(entry.Detail, proxy)
		}
	}
	if !found {
		t.Fatal("activity did not record the trusted forwarded address")
	}
}

func TestOwnerResetFileDestinationIsPrivate(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := porticoServer(t)
			file := filepath.Join(s.dataDir, ownerResetFile)
			if kind == "directory" {
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(filepath.Join(s.dataDir, "other"), file); err != nil {
					t.Fatal(err)
				}
			}
			w := call(t, s, "POST", "/v1/auth/owner-reset", "", OwnerResetStart{}, nil)
			if w.Code != 503 || strings.Contains(w.Body.String(), s.dataDir) {
				t.Fatal("unsafe reset destination accepted or path exposed")
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			code := askOwnerReset(t, s)
			if got := status(t, s, "POST", "/v1/auth/owner-reset/session", "",
				OwnerResetSessionRequest{Code: code, Device: device(9)}); got != 200 {
				t.Fatalf("a failed write left an inaccessible pending proof: %d", got)
			}
		})
	}
}

// The old file must not acquire the next owner's authority, even if the old owner
// becomes an administrator rather than being deleted.
func TestOwnerResetCodeDoesNotFollowAnOwnershipChange(t *testing.T) {
	s, owner, _, _ := twoMembers(t)
	code := askOwnerReset(t, s)
	var target string
	for _, member := range get[Page[Member]](t, s, owner, "/v1/admin/members").Items {
		if member.Username == "max" {
			target = member.ID
		}
	}
	if target == "" {
		t.Fatal("no member to make owner")
	}
	if got := status(t, s, "POST", "/v1/admin/owner", owner,
		OwnerChangeRequest{Password: testPassword, AccountID: target, Previous: "admin"}); got != 204 {
		t.Fatalf("changing owner: %d", got)
	}
	if got := status(t, s, "POST", "/v1/auth/owner-reset/confirm", "", OwnerResetFinish{Code: code, NewPassword: "a different password"}); got != 401 {
		t.Fatalf("previous owner's file reset the next owner: %d", got)
	}
}

func TestOwnerResetKeepsRecoveryCodesButEndsPendingGrants(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	claims, _ := s.tokens.CheckAccess(owner.AccessToken, s.now())
	for _, kind := range []string{"recovery", "two_factor", "tv_poll", "cast"} {
		err := s.db.CreateCode(context.Background(), db.Code{Kind: kind, Hash: auth.Hash(kind), UserID: claims.UserID,
			ExpiresAt: s.now().Add(time.Hour)}, s.now())
		if err != nil {
			t.Fatal(err)
		}
	}
	code := askOwnerReset(t, s)
	if got := status(t, s, "POST", "/v1/auth/owner-reset/confirm", "", OwnerResetFinish{Code: code, NewPassword: testPassword}); got != 204 {
		t.Fatalf("local password reset: %d", got)
	}
	for _, kind := range []string{"recovery", "two_factor", "tv_poll", "cast"} {
		_, err := s.db.PeekCode(context.Background(), kind, auth.Hash(kind), s.now())
		if (err == nil) != (kind == "recovery") {
			t.Fatalf("reset preserved the wrong grant: %s", kind)
		}
	}
}

func TestOwnerResetUntrustedForwardingCannotEvadeWait(t *testing.T) {
	s, _, _ := porticoServer(t)
	askOwnerReset(t, s)
	for n := range 6 {
		got := from(t, s, "198.51.100.1", fmt.Sprintf("203.0.113.%d", n+1), "POST", "/v1/auth/owner-reset/session", "",
			OwnerResetSessionRequest{Code: "wrong", Device: device(9)}, nil)
		want := 401
		if n == 5 {
			want = 429
		}
		if got != want {
			t.Fatalf("untrusted forwarded address changed network limits: %d, want %d", got, want)
		}
	}
}
