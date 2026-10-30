package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The account switcher's routes take the installation id in the body, never
// in the address, and the device list shows an installation id only for the
// caller's own device.
func TestInstallationIDsStayOutOfAddressesAndOffOtherDevices(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	passwordAccount(t, f, "switcher", "Testing1!")
	phone := "installation-switcher-phone-00000000001"
	tablet := "installation-switcher-tablet-0000000001"
	signIn := func(installation, name string) identity.DirectSignIn {
		t.Helper()
		w := f.raw("POST", "/v1/direct/sign-in", "", map[string]string{"X-Portico-Installation-Id": installation, "X-Portico-Device-Name": name, "X-Portico-Device-Platform": "ios", "X-Portico-App": "portico"}, map[string]string{"username": "switcher", "password": "Testing1!"})
		var out identity.DirectSignIn
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || out.Session == nil {
			t.Fatalf("sign-in on %s: %d %s", name, w.Code, w.Body.String())
		}
		return out
	}
	onPhone := signIn(phone, "Phone")
	onTablet := signIn(tablet, "Tablet")

	if w := f.raw("POST", "/v1/direct/remembered-accounts", onPhone.Session.AccessToken, nil, map[string]any{"installationId": phone, "automaticSignIn": false}); w.Code != 200 {
		t.Fatalf("remember: %d %s", w.Code, w.Body.String())
	}
	// The old address-carried forms are gone.
	if w := f.raw("GET", "/v1/auth/remembered-accounts?installationId="+phone, "", nil, nil); w.Code != 404 && w.Code != 405 {
		t.Fatalf("the list still answers with the installation id in its address: %d %s", w.Code, w.Body.String())
	}
	w := f.raw("POST", "/v1/auth/remembered-accounts", "", nil, map[string]string{"installationId": phone})
	var listed RememberedAccountsDocument
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != 200 || len(listed.Items) != 1 || listed.Items[0].Username != "switcher" {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	// Another installation sees nothing of it, and a short guess is refused.
	w = f.raw("POST", "/v1/auth/remembered-accounts", "", nil, map[string]string{"installationId": tablet})
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != 200 || len(listed.Items) != 0 {
		t.Fatalf("another installation's list: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("POST", "/v1/auth/remembered-accounts", "", nil, map[string]string{"installationId": "short"}); w.Code != 400 {
		t.Fatalf("a short installation id: %d %s", w.Code, w.Body.String())
	}
	// Forgetting needs a session, and takes the id in the body.
	path := "/v1/direct/remembered-accounts/switcher"
	if w = f.raw("DELETE", path, "", nil, map[string]string{"installationId": phone}); w.Code != 401 {
		t.Fatalf("forget with no session: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("DELETE", path+"?installationId="+phone, onPhone.Session.AccessToken, nil, nil); w.Code == 204 {
		t.Fatalf("forget still takes the installation id from its address: %d", w.Code)
	}
	if w = f.raw("DELETE", path, onPhone.Session.AccessToken, nil, map[string]string{"installationId": phone}); w.Code != 204 {
		t.Fatalf("forget: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/auth/remembered-accounts", "", nil, map[string]string{"installationId": phone})
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Items) != 0 {
		t.Fatalf("after forgetting: %d %s", w.Code, w.Body.String())
	}

	// The device list: the caller's own device is marked from its session, and
	// only that one carries an installation id.
	w = f.raw("GET", "/v1/devices", onTablet.Session.AccessToken, nil, nil)
	var devices DevicesDocument
	if err := json.Unmarshal(w.Body.Bytes(), &devices); err != nil || w.Code != 200 || len(devices.Items) < 2 {
		t.Fatalf("devices: %d %s", w.Code, w.Body.String())
	}
	current := 0
	for _, d := range devices.Items {
		switch {
		case d.Current:
			current++
			if d.Name != "Tablet" || d.InstallationID != tablet {
				t.Fatalf("the caller's own device: %+v", d)
			}
		case d.InstallationID != "":
			t.Fatalf("another device's installation id was shown to this one: %+v", d)
		}
	}
	if current != 1 {
		t.Fatalf("%d devices marked as the caller's", current)
	}
	if strings.Contains(w.Body.String(), phone) {
		t.Fatal("the phone's installation id appears in the tablet's device list")
	}
}
