package identity

import (
	"context"
	"errors"
	"testing"
)

func registration(installation string) DeviceRegistration {
	return DeviceRegistration{InstallationID: installation, Name: "Living room TV", Platform: "tvos", App: "portico-tv", AppVersion: "1.0.0"}
}

func TestDeviceRegistrationRecordsThePeerNotTheClaim(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	installation := Token()
	device, e := s.RegisterDevice(ctx, login.AccountToken, registration(installation), "203.0.113.7:51314")
	if e != nil {
		t.Fatal(e)
	}
	if device.IP != "203.0.113.7" {
		t.Fatalf("peer address %q: the port must not be stored", device.IP)
	}
	// A nonsense peer is recorded as nothing rather than as itself: this column is
	// shown to people, so it holds an address or it holds nothing.
	second, e := s.RegisterDevice(ctx, login.AccountToken, registration(Token()), "not-an-address")
	if e != nil {
		t.Fatal(e)
	}
	if second.IP != "" {
		t.Fatalf("unparseable peer stored as %q", second.IP)
	}
	// Re-registering the same installation refreshes the record rather than
	// creating a second one, or an app update would look like a new device.
	refreshed, e := s.RegisterDevice(ctx, login.AccountToken, DeviceRegistration{InstallationID: installation, Name: "Renamed by client", Platform: "tvos", App: "portico-tv", AppVersion: "1.1.0"}, "203.0.113.9:1")
	if e != nil {
		t.Fatal(e)
	}
	if refreshed.ID != device.ID || refreshed.AppVersion != "1.1.0" {
		t.Fatalf("re-registration produced %+v", refreshed)
	}
	// The name a person gave the device is theirs; a later sign-in must not
	// overwrite it with whatever the client now calls itself.
	if refreshed.Name != "Living room TV" {
		t.Fatalf("re-registration renamed the device to %q", refreshed.Name)
	}
	for name, invalid := range map[string]DeviceRegistration{
		"no installation": {Name: "x"},
		"short id":        {InstallationID: "abc"},
		"control name":    {InstallationID: installation, Name: "bad\nname"},
	} {
		if _, e = s.RegisterDevice(ctx, login.AccountToken, invalid, ""); !errors.Is(e, ErrDeviceInput) {
			t.Errorf("%s: want ErrDeviceInput, got %v", name, e)
		}
	}
}

func TestPerDeviceSignOutRevokesOnlyThatDevice(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	if login.Session == nil {
		t.Fatal("first device has no session")
	}
	second := Token()
	secondCtx, e := WithIssuingDevice(ctx, registration(second), "")
	if e != nil {
		t.Fatal(e)
	}
	secondLogin, e := s.DirectLoginFrom(secondCtx, "owner", "Testing1!", true)
	if e != nil || secondLogin.Session == nil {
		t.Fatal("second device has no session", e)
	}
	if e = s.SignOutDevice(ctx, secondLogin.AccountToken, login.DeviceID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(login.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("the signed-out device kept its session: %v", e)
	}
	var signoutReason string
	if e = db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, login.Session.SessionFamilyID).Scan(&signoutReason); e != nil || signoutReason != string(RevokedExplicitSignout) {
		t.Fatalf("device sign-out reason %q: %v", signoutReason, e)
	}
	if _, e = s.Authenticate(secondLogin.Session.AccessToken); e != nil {
		t.Fatalf("signing out one device ended another: %v", e)
	}
	// The record survives, so the person keeps seeing the device and an owner
	// watching approvals does not see a familiar TV arrive as a stranger.
	devices, e := s.Devices(ctx, secondLogin.AccountToken, second)
	if e != nil {
		t.Fatal(e)
	}
	if len(devices) != 2 {
		t.Fatalf("device list lost a record: %d", len(devices))
	}
	for _, device := range devices {
		if device.ID == login.DeviceID {
			if device.Sessions != 0 || device.Current {
				t.Fatalf("signed-out device: %+v", device)
			}
		} else if device.Sessions != 1 || !device.Current {
			t.Fatalf("other device lost its session: %+v", device)
		}
	}
}

func TestSignOutEverywhereNeedsThePasswordAndClearsEveryFamily(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	installation := Token()
	if _, e := s.RegisterDevice(ctx, login.AccountToken, registration(installation), ""); e != nil {
		t.Fatal(e)
	}
	session, e := s.SelectDirectProfile(ctx, login.AccountToken, login.Account.PrimaryProfileID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RememberBrowserAccount(ctx, login.AccountToken, installation, true); e != nil {
		t.Fatal(e)
	}
	if e = s.SignOutEverywhere(ctx, login.AccountToken); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(session.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("a viewing session survived sign-out-everywhere")
	}
	// The epoch bump sweeps the account session too, and the remembered account
	// list must not survive: it is the thing that makes signing back in silent.
	if _, e = s.DirectMe(ctx, login.AccountToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("the account session survived sign-out-everywhere")
	}
	remembered, e := s.BrowserAccounts(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	if len(remembered) != 0 {
		t.Fatalf("remembered accounts survived: %+v", remembered)
	}
}

func TestRememberedAccountsHoldDescriptorsOnly(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	installation := Token()
	// Remembering requires a device record: the list is keyed by an installation
	// the server has actually seen.
	if _, e := s.RememberBrowserAccount(ctx, login.AccountToken, installation, false); !errors.Is(e, ErrDeviceUnknown) {
		t.Fatalf("remembered an account on an unknown installation: %v", e)
	}
	device, e := s.RegisterDevice(ctx, login.AccountToken, registration(installation), "")
	if e != nil {
		t.Fatal(e)
	}
	list, e := s.RememberBrowserAccount(ctx, login.AccountToken, installation, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(list) != 1 || list[0].AccountID != login.Account.ID || !list[0].AutomaticSignIn {
		t.Fatalf("remembered %+v", list)
	}
	// The unauthenticated read is what the account switcher uses. It must return
	// descriptors and nothing a caller could sign in with.
	public, e := s.BrowserAccounts(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	if len(public) != 1 || public[0].Username != "owner" {
		t.Fatalf("public list %+v", public)
	}
	// Turning remembering off on the device forgets the entry, so the switch is a
	// real withdrawal rather than a flag the list ignores.
	if _, e = s.EditDevice(ctx, login.AccountToken, device.ID, DeviceEdit{RememberAccount: pointer(false)}); e != nil {
		t.Fatal(e)
	}
	if public, e = s.BrowserAccounts(ctx, installation); e != nil || len(public) != 0 {
		t.Fatalf("entry survived rememberAccount=false: %+v %v", public, e)
	}
	if _, e = s.RememberBrowserAccount(ctx, login.AccountToken, installation, false); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("remembering was accepted after it was turned off: %v", e)
	}
	// An unknown installation reads as an empty list, never an error that would
	// tell a prober which installation ids exist.
	if other, err := s.BrowserAccounts(ctx, Token()); err != nil || len(other) != 0 {
		t.Fatalf("unknown installation: %+v %v", other, err)
	}
	if _, err := s.BrowserAccounts(ctx, "short"); !errors.Is(err, ErrBrowserAccountInput) {
		t.Fatal("a malformed installation id was not refused")
	}
}

func TestDeviceRenameAndLastProfileAreBounded(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	installation := Token()
	device, e := s.RegisterDevice(ctx, login.AccountToken, registration(installation), "")
	if e != nil {
		t.Fatal(e)
	}
	renamed, e := s.EditDevice(ctx, login.AccountToken, device.ID, DeviceEdit{Name: pointer("Kitchen"), LastProfileID: pointer(login.Account.PrimaryProfileID)})
	if e != nil {
		t.Fatal(e)
	}
	if renamed.Name != "Kitchen" || renamed.LastProfileID != login.Account.PrimaryProfileID {
		t.Fatalf("edited %+v", renamed)
	}
	for name, edit := range map[string]DeviceEdit{
		"empty name":      {Name: pointer("  ")},
		"control name":    {Name: pointer("bad\nname")},
		"unknown profile": {LastProfileID: pointer(Token())},
		"nothing":         {},
	} {
		if _, e = s.EditDevice(ctx, login.AccountToken, device.ID, edit); !errors.Is(e, ErrDeviceInput) {
			t.Errorf("%s: want ErrDeviceInput, got %v", name, e)
		}
	}
	if _, e = s.EditDevice(ctx, login.AccountToken, Token(), DeviceEdit{Name: pointer("Ghost")}); !errors.Is(e, ErrDeviceUnknown) {
		t.Fatalf("editing an unknown device: %v", e)
	}
}

func TestProfileSwitchEndsTheOutgoingProfilesPlaybackOnThatDeviceOnly(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	snapshot, e := s.CreateDirectProfile(ctx, login.AccountToken, "Child", "mint")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, snapshot)
	here, there := Token(), Token()
	if _, e = s.RegisterDevice(ctx, login.AccountToken, registration(here), ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RegisterDevice(ctx, login.AccountToken, registration(there), ""); e != nil {
		t.Fatal(e)
	}
	hereCtx, e := WithIssuingDevice(ctx, registration(here), "")
	if e != nil {
		t.Fatal(e)
	}
	hereLogin, e := s.DirectLoginFrom(hereCtx, "owner", "Testing1!", true)
	if e != nil {
		t.Fatal(e)
	}
	thereCtx, e := WithIssuingDevice(ctx, registration(there), "")
	if e != nil {
		t.Fatal(e)
	}
	thereLogin, e := s.DirectLoginFrom(thereCtx, "owner", "Testing1!", true)
	if e != nil {
		t.Fatal(e)
	}
	outgoing, e := s.SelectDirectProfile(ctx, hereLogin.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	elsewhere, e := s.SelectDirectProfile(ctx, thereLogin.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	// Switching profiles on this device ends this device's occurrence. It is never
	// handed to the incoming profile: a handover would carry one profile's
	// position and restrictions into another's session.
	if e = s.SwitchProfileFence(ctx, login.AccountToken, here, child.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(outgoing.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("the outgoing profile kept its session on this device: %v", e)
	}
	// The same profile on another device is untouched: a switch is local to the
	// screen someone is standing in front of.
	if _, e = s.Authenticate(elsewhere.Session.AccessToken); e != nil {
		t.Fatalf("a switch on one device ended the same profile elsewhere: %v", e)
	}
	if e = s.SwitchProfileFence(ctx, login.AccountToken, Token(), ""); !errors.Is(e, ErrDeviceUnknown) {
		t.Fatalf("fencing an unknown device: %v", e)
	}
}

func TestTopShelfTokenIsBoundAndIsNotASession(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	installation := Token()
	device, e := s.RegisterDevice(ctx, login.AccountToken, registration(installation), "")
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.SelectDirectProfile(ctx, login.AccountToken, login.Account.PrimaryProfileID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Authenticate(session.Session.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.IssueTopShelfToken(ctx, p, installation, 0)
	if e != nil {
		t.Fatal(e)
	}
	resolved, e := s.TopShelfGrant(ctx, grant.Token)
	if e != nil {
		t.Fatal(e)
	}
	if resolved.Viewer.ProfileID != p.ProfileID || resolved.Viewer.AccountID != p.AccountID {
		t.Fatalf("resolved %+v", resolved.Viewer)
	}
	// A feed token must not be usable as a session anywhere else.
	if _, e = s.Authenticate(grant.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("a Top Shelf token authenticated as a viewer session")
	}
	if _, e = s.DirectMe(ctx, grant.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("a Top Shelf token authenticated as an account session")
	}
	// Denying the device takes the feed away without a separate sweep.
	if e = s.ForgetDevice(ctx, login.AccountToken, device.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TopShelfGrant(ctx, grant.Token); !errors.Is(e, ErrTopShelfToken) {
		t.Fatalf("a denied device kept its feed: %v", e)
	}
}

// A25: a sign-in that names no installation gets a device record of its own,
// and under owner approval each one waits. However often such a client tries,
// an account keeps only the newest few waiting records; an approved one, or
// one that already holds a sign-in, is never removed by this.
