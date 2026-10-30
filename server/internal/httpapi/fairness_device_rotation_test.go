package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestVerifiedDeviceFairnessSurvivesRefreshProfileAndGrant(t *testing.T) {
	f := newV1Fixture(t, 1)
	gate := newAdmission()
	remember := func(token string) (identity.Principal, string) {
		t.Helper()
		p, err := f.id.AuthenticateContext(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		if p.DeviceID == "" {
			t.Fatal("verified device metadata missing")
		}
		gate.rememberCredential(token, p)
		key, known := gate.clients.lookup(token, time.Now())
		if !known {
			t.Fatal("verified credential was not remembered")
		}
		return p, key
	}
	first, want := remember(f.owner.AccessToken)
	if first.DeviceID != f.owner.DeviceID {
		t.Fatal("authenticated device did not match issued binding")
	}
	session, err := f.deps.Playback.CreateContext(context.Background(), first, f.items[0], "automatic", "device-accounting-grant")
	if err != nil {
		t.Fatal(err)
	}
	grant := strings.TrimPrefix(session.StreamURL, "/v1/media/")
	successor, err := f.id.RefreshSession(context.Background(), f.owner.RefreshToken, f.owner.InstallationID, identity.Token())
	if err != nil {
		t.Fatal(err)
	}
	current, refreshedKey := remember(successor.AccessToken)
	if current.Hash == first.Hash || refreshedKey != want || current.DeviceID != first.DeviceID {
		t.Fatal("credential rotation minted another device share")
	}
	_, grantPrincipal, _, err := f.deps.Playback.ResolveGrantContext(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if grantPrincipal.DeviceID != first.DeviceID || grantPrincipal.Hash != current.Hash {
		t.Fatal("grant continuation did not inherit verified current-family device")
	}
	gate.rememberCredential(grant, grantPrincipal)
	if key, known := gate.clients.lookup(grant, time.Now()); !known || key != want {
		t.Fatal("media grant minted a share after token rotation")
	}
	if _, err = f.db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES('second-profile',?,'Second',0,1)`, first.AccountID); err != nil {
		t.Fatal(err)
	}
	ctx, err := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: f.owner.InstallationID, Name: "Same device", Platform: "test", App: "portico"}, "198.51.100.42")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := f.id.IssueWithDevice(ctx, first.AccountID, "second-profile", "local", "member", first.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	selected, selectedKey := remember(profile.AccessToken)
	if selected.ProfileID == first.ProfileID || selected.DeviceID != first.DeviceID || selectedKey != want {
		t.Fatal("profile family minted another device share")
	}
	other := f.device("other-accounting-device")
	_, otherKey := remember(other.AccessToken)
	if otherKey == want {
		t.Fatal("distinct verified device shares collided")
	}
	if err = f.id.SignOutDevice(context.Background(), successor.AccessToken, first.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.Authenticate(successor.AccessToken); err == nil {
		t.Fatal("remembered device bypassed live sign-out")
	}
	if _, _, _, err = f.deps.Playback.ResolveGrantContext(context.Background(), grant); err == nil {
		t.Fatal("remembered grant bypassed live device sign-out")
	}
	if _, err = f.id.Authenticate(other.AccessToken); err != nil {
		t.Fatal("signing out one device affected another", err)
	}
}
