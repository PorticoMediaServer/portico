package read

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func newUSSecurityFixture(t *testing.T) (*browseFixture, *USSecurityReader) {
	t.Helper()
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	f := composeReadFixture(t, newForegroundSource(t), USSecurityDefinitions(), gc, true, foregroundFixtureHash(t))
	f.exec(t, `INSERT INTO configuration(key,value) VALUES('id','s')`)
	r, err := NewUSSecurityReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func usTestInstallationHash(installation string) string {
	sum := sha256.Sum256([]byte(installation))
	return hex.EncodeToString(sum[:])
}

func usTestInstallationIncarnation(installation string) string {
	return usTestInstallationHash(installation)[:32]
}

// usTestInput selects a principal-scoped view. The fixture clock is fixed, so
// expiry controls are deterministic rather than real-clock flakes.
func usTestInput(f *browseFixture, installation string) USSecurityInput {
	in := USSecurityInput{View: f.input, Installation: installation}
	in.View.Library, in.View.Kind = 0, 1
	in.View.Limit = 40
	return in
}

func usTestDeviceFence(t *testing.T, f *browseFixture, account, family, device string) {
	t.Helper()
	f.exec(t, `INSERT OR IGNORE INTO identity_security_fences(scope_kind,account_id,member_incarnation,device_id,updated_ms)
SELECT 'device',?,m.incarnation,?,0 FROM direct_memberships AS m WHERE m.account_id=?`, account, device, account)
	f.exec(t, `INSERT OR REPLACE INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation)
SELECT ?, 'device', s.id, s.generation FROM identity_security_fences AS s WHERE s.account_id=? AND s.scope_kind='device' AND s.device_id=?`, family, account, device)
}

// usTestPrepareSession binds the existing fixture account/profile/family to one
// current installation, role and device. It creates a second account's family
// when account 'b' is requested.
func usTestPrepareSession(t *testing.T, f *browseFixture, account, profile, family, token, device, role, installation string) {
	t.Helper()
	f.exec(t, `UPDATE direct_memberships SET role=? WHERE account_id=?`, role, account)
	if role == "owner" {
		f.exec(t, `UPDATE direct_profiles SET is_primary=1 WHERE id=? AND account_id=?`, profile, account)
	}
	f.exec(t, `UPDATE accounts SET profile_id=? WHERE id=?`, profile, account)
	if account == "a" {
		f.exec(t, `UPDATE authorization_session_families SET role=?,device_id=? WHERE id=?`, role, device, family)
	} else {
		f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,'b',?,'bob-device','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`, device, installation)
		f.exec(t, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind)
SELECT ?, 's', ?, ?, 'local', ?, a.epoch, '2026-11-07T00:00:00Z', 1, ?, m.incarnation, p.lifetime, x'00800000000000000000000000000002', 0
FROM accounts AS a JOIN direct_memberships AS m ON m.account_id=a.id JOIN direct_profiles AS p ON p.account_id=a.id AND p.id=? WHERE a.id=?`, family, account, profile, role, device, profile, account)
		f.exec(t, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,'2026-11-07T00:00:00Z')`, token, family)
	}
	f.exec(t, `UPDATE identity_devices SET installation_id=? WHERE id=?`, installation, device)
	f.exec(t, `INSERT OR REPLACE INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, usTestInstallationHash(installation), usTestInstallationIncarnation(installation))
	usTestDeviceFence(t, f, account, family, device)
}

func usTestSelf(t *testing.T) (*browseFixture, *USSecurityReader, USSecurityInput) {
	t.Helper()
	f, r := newUSSecurityFixture(t)
	installation := strings.Repeat("i", 32)
	usTestPrepareSession(t, f, "a", "p", "f", "token", "d", "member", installation)
	return f, r, usTestInput(f, installation)
}

func usTestOwner(t *testing.T) (*browseFixture, *USSecurityReader, USSecurityInput) {
	t.Helper()
	f, r := newUSSecurityFixture(t)
	installation := strings.Repeat("i", 32)
	usTestPrepareSession(t, f, "a", "p", "f", "token", "d", "owner", installation)
	return f, r, usTestInput(f, installation)
}

func usTestMemberB(t *testing.T, f *browseFixture) (USSecurityInput, string) {
	t.Helper()
	installation := strings.Repeat("j", 32)
	usTestPrepareSession(t, f, "b", "q", "f2", "token2", "d2", "member", installation)
	in := usTestInput(f, installation)
	in.View.TokenHash = "token2"
	return in, installation
}

func usTestStatus(t *testing.T, r *USSecurityReader, f *browseFixture, in USSecurityInput) USSecurityStatus {
	t.Helper()
	status, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestUSSecurityRead(t *testing.T) {
	t.Run("status own facts and cross-account isolation", func(t *testing.T) {
		f, r, in := usTestSelf(t)
		status := usTestStatus(t, r, f, in)
		if status.FamilyID != "f" || status.AccountID != "a" || status.ProfileID != "p" || status.Role != "member" ||
			status.Epoch != 1 || status.FamilyGeneration != 1 || status.AuthorityGeneration != 1 ||
			status.PINRequired || status.Remembered {
			t.Fatalf("status facts: %+v", status)
		}
		if status.Profile.ID != "p" || status.Profile.Name != "Alice" || !reflect.DeepEqual(status.Profile.AllowedLibraries, []string{}) {
			t.Fatalf("profile facts: %+v", status.Profile)
		}
		if status.Device.ID != "d" || status.Device.AccountID != "a" || status.Device.Username != "alice" {
			t.Fatalf("device facts: %+v", status.Device)
		}
		// A second principal's token never returns the first account's facts.
		other, _ := usTestMemberB(t, f)
		second := usTestStatus(t, r, f, other)
		if second.AccountID != "b" || second.ProfileID != "q" || second.Device.ID != "d2" || second.Device.Username != "bob" {
			t.Fatalf("second principal: %+v", second)
		}
		// Current source, not a cached image.
		f.exec(t, `UPDATE accounts SET username='alice2' WHERE id='a'`)
		if got := usTestStatus(t, r, f, in); got.Device.Username != "alice2" {
			t.Fatalf("status did not follow current source: %+v", got.Device)
		}
	})

	t.Run("denied and expiry anchors", func(t *testing.T) {
		f, r, in := usTestSelf(t)
		assertDenied := func(name string) {
			t.Helper()
			status, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(status, USSecurityStatus{}) {
				t.Fatalf("%s: got %+v err %v", name, status, err)
			}
		}
		f.exec(t, `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`)
		assertDenied("revoked family")
		f.exec(t, `UPDATE authorization_session_families SET revoked=0 WHERE id='f'`)
		f.exec(t, `UPDATE authorization_family_tokens SET expires_at='2026-11-06T00:00:00Z' WHERE token_hash='token'`)
		assertDenied("token expires at now")
		f.exec(t, `UPDATE authorization_family_tokens SET expires_at='2026-11-07T00:00:00Z' WHERE token_hash='token'`)
		f.exec(t, `UPDATE authorization_session_families SET authorization_horizon='2026-11-06T00:00:00Z' WHERE id='f'`)
		assertDenied("horizon at now")
		f.exec(t, `UPDATE authorization_session_families SET authorization_horizon='2026-11-07T00:00:00Z' WHERE id='f'`)
		f.exec(t, `UPDATE identity_devices SET installation_id='unregistered' WHERE id='d'`)
		assertDenied("unregistered installation")
		f.exec(t, `UPDATE identity_devices SET installation_id=? WHERE id='d'`, in.Installation)
		if status := usTestStatus(t, r, f, in); status.Device.ID != "d" {
			t.Fatalf("restore: %+v", status)
		}
	})

	t.Run("narrow grants credentials reset and moves deny", func(t *testing.T) {
		f, r, in := usTestSelf(t)
		assertDenied := func(name string) {
			t.Helper()
			status, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(status, USSecurityStatus{}) {
				t.Fatalf("%s: got %+v err %v", name, status, err)
			}
		}
		f.exec(t, `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('f','cast_target')`)
		assertDenied("narrow grant")
		f.exec(t, `DELETE FROM authorization_family_grants WHERE family_id='f'`)
		f.exec(t, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('c1','token','a','p','credential','[]','hash1','hint','f','d',0)`)
		assertDenied("credential family")
		f.exec(t, `DELETE FROM api_credentials WHERE id='c1'`)
		f.exec(t, `INSERT INTO reset_file_sessions(family_id,kind) VALUES('f','owner')`)
		assertDenied("reset-file session")
		f.exec(t, `DELETE FROM reset_file_sessions WHERE family_id='f'`)
		f.exec(t, `INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES('a','move','waiting',0)`)
		assertDenied("waiting move")
		f.exec(t, `DELETE FROM identity_move_waiting WHERE account_id='a'`)
		f.exec(t, `UPDATE direct_memberships SET role='admin' WHERE account_id='a'`)
		assertDenied("role mismatch")
		f.exec(t, `UPDATE direct_memberships SET role='member' WHERE account_id='a'`)
		f.exec(t, `DELETE FROM authorization_family_security_fences WHERE family_id='f' AND scope_kind='device'`)
		assertDenied("missing device fence")
		usTestDeviceFence(t, f, "a", "f", "d")
		if status := usTestStatus(t, r, f, in); status.Device.ID != "d" {
			t.Fatalf("restore fence: %+v", status)
		}
	})

	t.Run("pin remember and clear semantics", func(t *testing.T) {
		f, r, in := usTestSelf(t)
		f.exec(t, `UPDATE direct_profiles SET pin_hash=x'00' WHERE id='p'`)
		if _, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
			t.Fatalf("PIN without fence: %v", err)
		}
		f.exec(t, `INSERT INTO identity_security_fences(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,updated_ms)
SELECT 'profile_pin',m.account_id,m.incarnation,p.id,p.lifetime,0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`)
		f.exec(t, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation)
SELECT 'f','profile_pin',s.id,s.generation FROM identity_security_fences AS s WHERE s.account_id='a' AND s.scope_kind='profile_pin'`)
		status := usTestStatus(t, r, f, in)
		if !status.PINRequired {
			t.Fatal("PIN presence not reported")
		}
		f.exec(t, `INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('l1','a','d','2026-11-05T00:00:00Z')`)
		f.exec(t, `UPDATE authorization_session_families SET pin_lineage_id='l1',remember_until='2026-11-20T00:00:00Z',manage_until='2026-11-07T00:00:00Z' WHERE id='f'`)
		if _, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
			t.Fatalf("remember without witnesses: %v", err)
		}
		f.exec(t, `INSERT INTO identity_security_fences(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms)
SELECT 'profile_remember',m.account_id,m.incarnation,p.id,p.lifetime,'',0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`)
		f.exec(t, `INSERT INTO identity_security_fences(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms)
SELECT 'profile_lineage',m.account_id,m.incarnation,p.id,p.lifetime,'l1',0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`)
		for _, kind := range []string{"profile_remember", "profile_lineage"} {
			f.exec(t, `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation)
SELECT 'f',?,s.id,s.generation FROM identity_security_fences AS s WHERE s.account_id='a' AND s.scope_kind=?`, kind, kind)
		}
		status = usTestStatus(t, r, f, in)
		if !status.PINRequired || !status.Remembered {
			t.Fatalf("remembered status: %+v", status)
		}
		f.exec(t, `UPDATE authorization_session_families SET manage_until='2026-11-06T00:00:00Z' WHERE id='f'`)
		if _, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
			t.Fatalf("expired manage bound: %v", err)
		}
		f.exec(t, `UPDATE authorization_session_families SET manage_until='2026-11-07T00:00:00Z' WHERE id='f'`)
		// Clearing the PIN preserves the sign-in: the same family remains current
		// and the residual remember timestamp is not a gate.
		f.exec(t, `UPDATE direct_profiles SET pin_hash=NULL WHERE id='p'`)
		status = usTestStatus(t, r, f, in)
		if status.PINRequired || !status.Remembered {
			t.Fatalf("PIN clear semantics: %+v", status)
		}
	})

	t.Run("owner reads refuse a PIN-protected main profile", func(t *testing.T) {
		f, r, in := usTestOwner(t)
		f.exec(t, `UPDATE direct_profiles SET pin_hash=x'00' WHERE id='p'`)
		if _, _, err := r.status(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
			t.Fatalf("owner PIN: %v", err)
		}
	})

	t.Run("profiles are own-account bounded", func(t *testing.T) {
		f, r, in := usTestSelf(t)
		f.exec(t, `INSERT INTO direct_profiles(id,account_id,name,position) VALUES('p2','a','Second',1)`)
		f.exec(t, `INSERT INTO direct_profiles(id,account_id,name,position,deleted) VALUES('p3','a','Gone',2,1)`)
		page, _, err := r.profiles(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.AccountID != "a" || len(page.Items) != 2 || page.Items[0].ID != "p" || page.Items[1].ID != "p2" || !page.Exhausted {
			t.Fatalf("profiles: %+v", page)
		}
		for _, profile := range page.Items {
			if profile.ID == "q" {
				t.Fatal("another account's profile escaped")
			}
		}
		for n := 2; n <= 8; n++ {
			f.exec(t, `INSERT INTO direct_profiles(id,account_id,name,position) VALUES(?, 'a', ?, ?)`, fmt.Sprintf("x%d", n), fmt.Sprintf("P%d", n), n)
		}
		// p, p2, x2..x8 = 9 live profiles: refuse the over-cap cohort.
		if page, _, err := r.profiles(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, USSecurityProfilesPage{}) {
			t.Fatalf("profile overflow: %+v %v", page, err)
		}
	})

	t.Run("owner member point list cursor and tier", func(t *testing.T) {
		f, r, in := usTestOwner(t)
		in.View.Limit = 1
		first, _, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Items) != 1 || first.Items[0].AccountID != "a" || first.Next == nil || first.Exhausted {
			t.Fatalf("first member page: %+v", first)
		}
		in.After = first.Next
		second, _, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Items) != 1 || second.Items[0].AccountID != "b" || second.Next != nil || !second.Exhausted {
			t.Fatalf("second member page: %+v", second)
		}
		member, _, err := r.member(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: in.View, Installation: in.Installation, Account: "b"})
		if err != nil || member.AccountID != "b" || member.Username != "bob" || member.Role != "member" ||
			!reflect.DeepEqual(member.AllowedLibraries, []string{"other"}) {
			t.Fatalf("member b: %+v %v", member, err)
		}
		missing, _, err := r.member(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: in.View, Installation: in.Installation, Account: "missing"})
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(missing, USSecurityMember{}) {
			t.Fatalf("missing member: %+v %v", missing, err)
		}
		// A cursor is bound to the issuing authority witness, not just its text.
		in.After = first.Next
		f.exec(t, `UPDATE identity_state SET authority_generation=2`)
		f.exec(t, `UPDATE authorization_session_families SET authority_generation=2 WHERE id='f'`)
		stale, _, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(stale, USSecurityMembersPage{}) {
			t.Fatalf("cursor across authority change: %+v %v", stale, err)
		}
		f.exec(t, `UPDATE identity_state SET authority_generation=1`)
		f.exec(t, `UPDATE authorization_session_families SET authority_generation=1 WHERE id='f'`)
		// A member token is denied both administration reads.
		other, _ := usTestMemberB(t, f)
		if page, _, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, other); !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, USSecurityMembersPage{}) {
			t.Fatalf("member list tier: %+v %v", page, err)
		}
		if member, _, err := r.member(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: other.View, Installation: other.Installation, Account: "a"}); !errors.Is(err, ErrDenied) || member.AccountID != "" {
			t.Fatalf("member point tier: %+v %v", member, err)
		}
	})

	t.Run("devices are owner-only bounded and account-scoped", func(t *testing.T) {
		f, r, in := usTestOwner(t)
		page, _, err := r.devices(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: in.View, Installation: in.Installation, Account: "a"})
		if err != nil || page.AccountID != "a" || len(page.Items) != 1 || page.Items[0].ID != "d" {
			t.Fatalf("own devices: %+v %v", page, err)
		}
		for _, item := range page.Items {
			if item.ID == "d2" {
				t.Fatal("foreign device escaped")
			}
		}
		other, _ := usTestMemberB(t, f)
		denied, _, err := r.devices(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: other.View, Installation: other.Installation, Account: "a"})
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(denied, USSecurityDevicesPage{}) {
			t.Fatalf("member device list: %+v %v", denied, err)
		}
		ownerB, _, err := r.devices(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: in.View, Installation: in.Installation, Account: "b"})
		if err != nil || len(ownerB.Items) != 1 || ownerB.Items[0].ID != "d2" {
			t.Fatalf("owner b devices: %+v %v", ownerB, err)
		}
		for n := 0; n < 64; n++ {
			f.exec(t, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?, 'a', ?, 'extra', 'test', 'test', '1', '2026-11-05T00:00:00Z', '2026-11-05T00:00:00Z')`, fmt.Sprintf("e%d", n), fmt.Sprintf("install-%d", n))
		}
		if page, _, err := r.devices(t.Context(), foregroundRequest(), time.Second, f.output, USSecurityInput{View: in.View, Installation: in.Installation, Account: "a"}); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, USSecurityDevicesPage{}) {
			t.Fatalf("device overflow: %+v %v", page, err)
		}
	})

	t.Run("unbacked classes refuse by name", func(t *testing.T) {
		_, r, _ := usTestSelf(t)
		if _, _, err := r.Invitations(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}); !errors.Is(err, ErrUSSecurityInvitationsUnbacked) {
			t.Fatal("invitation refusal", err)
		}
		if _, _, err := r.Setup(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}); !errors.Is(err, ErrUSSecuritySetupUnbacked) {
			t.Fatal("setup refusal", err)
		}
		if _, _, err := r.Hosted(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}); !errors.Is(err, ErrUSSecurityHostedUnbacked) {
			t.Fatal("Hosted refusal", err)
		}
	})
}

// TestUSSecurityDTOParity holds the local read values to the current public
// response types. It is a name/type check only: the read code never imports
// those adapters, and their SQL is not reused. The shapes are mirrored here
// rather than imported because the identity and access packages register legacy
// SQL scalars from init(); importing them into the read test binary changes the
// package-wide pragma_function_list that plan capture hashes into every
// golden's environment.functionsSha256.
func TestUSSecurityDTOParity(t *testing.T) {
	type publicProfile struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Primary          bool     `json:"primary"`
		Position         int      `json:"position"`
		Art              string   `json:"art"`
		Revision         int64    `json:"revision"`
		PINRevision      int64    `json:"pinRevision"`
		PINRequired      bool     `json:"pinRequired"`
		AllowedLibraries []string `json:"allowedLibraries"`
	}
	type publicDevice struct {
		ID         string `json:"id"`
		AccountID  string `json:"accountId"`
		Username   string `json:"username"`
		ProfileID  string `json:"profileId,omitempty"`
		Name       string `json:"name"`
		Platform   string `json:"platform"`
		App        string `json:"app"`
		AppVersion string `json:"appVersion"`
		FirstSeen  string `json:"firstSeenAt"`
		LastSeen   string `json:"lastSeenAt"`
		Revision   int64  `json:"revision"`
	}
	type publicMember struct {
		AccountID        string   `json:"accountId"`
		Username         string   `json:"username"`
		Email            string   `json:"email,omitempty"`
		DisplayName      string   `json:"displayName"`
		ProfileID        string   `json:"profileId"`
		Role             string   `json:"role"`
		Disabled         bool     `json:"disabled"`
		DisabledAt       string   `json:"disabledAt,omitempty"`
		DisabledReason   string   `json:"disabledReason,omitempty"`
		Revision         int64    `json:"revision"`
		AllowedLibraries []string `json:"allowedLibraries"`
		Notes            *string  `json:"notes,omitempty"`
		JoinedVia        string   `json:"joinedVia,omitempty"`
		JoinedAt         string   `json:"joinedAt,omitempty"`
	}

	assertSubset := func(t *testing.T, local any, external reflect.Type, extras ...string) {
		t.Helper()
		localType := reflect.TypeOf(local)
		allowed := map[string]bool{}
		for _, name := range extras {
			allowed[name] = true
		}
		for n := 0; n < localType.NumField(); n++ {
			field := localType.Field(n)
			if allowed[field.Name] {
				continue
			}
			other, ok := external.FieldByName(field.Name)
			if !ok || other.Type != field.Type {
				t.Fatalf("%s.%s has no matching %s field", localType.Name(), field.Name, external.Name())
			}
		}
	}
	assertSubset(t, USSecurityProfile{}, reflect.TypeOf(publicProfile{}))
	assertSubset(t, USSecurityDevice{}, reflect.TypeOf(publicDevice{}), "RememberAccount")
	assertSubset(t, USSecurityMember{}, reflect.TypeOf(publicMember{}))

	// Secret-bearing or computed product fields never belong in this surface.
	device := reflect.TypeOf(USSecurityDevice{})
	for _, forbidden := range []string{"InstallationID", "IP", "Current", "Sessions"} {
		if _, ok := device.FieldByName(forbidden); ok {
			t.Fatalf("secret/computed device field %s present", forbidden)
		}
	}
}

func TestUSSecurityFormattedValueHasNoSecret(t *testing.T) {
	f, r, in := usTestSelf(t)
	status := usTestStatus(t, r, f, in)
	formatted := fmt.Sprintf("%+v", status)
	if strings.Contains(formatted, in.Installation) || strings.Contains(formatted, "token") {
		t.Fatalf("secret material in formatted status")
	}
}
