package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestTypedDeviceLifecycleAndBinding(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	h := New(d)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/v1/devices", "", ""); w.Code != 401 {
		t.Fatalf("anonymous devices: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/v1/direct/devices", owner.AccessToken, ""); w.Code != 404 {
		t.Fatalf("retired device alias: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/v1/devices?installationId=a&installationId=b", owner.AccessToken, ""); w.Code != 400 {
		t.Fatalf("ambiguous current device: %d %s", w.Code, w.Body.String())
	}
	listed := request("GET", "/v1/devices?installationId="+owner.InstallationID, owner.AccessToken, "")
	if listed.Code != 200 {
		t.Fatalf("list devices: %d %s", listed.Code, listed.Body.String())
	}
	var devices DevicesDocument
	if err := json.Unmarshal(listed.Body.Bytes(), &devices); err != nil || len(devices.Items) != 1 || devices.Items[0].ID != owner.DeviceID || !devices.Items[0].Current {
		t.Fatalf("current device: %+v, %v", devices, err)
	}
	newInstallation := strings.Repeat("A", 32)
	postBody := `{"installationId":"` + newInstallation + `","name":"TV","platform":"tvOS","app":"Portico","appVersion":"1"}`
	created := request("POST", "/v1/devices", owner.AccessToken, postBody)
	if created.Code != 201 {
		t.Fatalf("register device: %d %s", created.Code, created.Body.String())
	}
	var tv identity.Device
	if err := json.Unmarshal(created.Body.Bytes(), &tv); err != nil || tv.ID == "" || tv.InstallationID != newInstallation {
		t.Fatalf("registered device: %+v, %v", tv, err)
	}
	if w := request("PATCH", "/v1/devices/"+owner.DeviceID, owner.AccessToken, `{"name":"Living Room"}`); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"Living Room"`)) {
		t.Fatalf("edit device: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/v1/devices/"+tv.ID+"/approval", owner.AccessToken, `{"approved":true}`); w.Code != 404 {
		t.Fatalf("retired approval route: %d %s", w.Code, w.Body.String())
	}
	bindBody := `{"installationId":"` + owner.InstallationID + `","sessionFamilyId":"` + owner.SessionFamilyID + `"}`
	if w := request("POST", "/v1/devices/"+tv.ID+"/sessions/bind", owner.AccessToken, bindBody); w.Code != 404 {
		t.Fatalf("mismatched device path: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/v1/devices/"+owner.DeviceID+"/sessions/bind", owner.AccessToken, bindBody); w.Code != 204 {
		t.Fatalf("verify family binding: %d %s", w.Code, w.Body.String())
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if err := contract.Record("../../testdata/contracts/post-device.json", "PostDeviceResponse", "POST", "/v1/devices", []byte(postBody), created, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if w := request("DELETE", "/v1/devices/"+owner.DeviceID+"/sessions", owner.AccessToken, ""); w.Code != 204 {
		t.Fatalf("sign out device: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/v1/devices", owner.AccessToken, ""); w.Code != 401 {
		t.Fatalf("signed-out device retained session: %d %s", w.Code, w.Body.String())
	}
}

func TestTypedDeviceForgetRevokesItsSessions(t *testing.T) {
	d, old := logoutHTTPFixture(t)
	newer, err := d.Identity.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	h := New(d)
	if w := logoutHTTPRequest(h, "DELETE", "/v1/devices/"+old.DeviceID, newer.AccessToken); w.Code != 204 {
		t.Fatalf("forget device: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/devices", old.AccessToken); w.Code != 401 {
		t.Fatalf("forgotten device retained session: %d %s", w.Code, w.Body.String())
	}
	if w := logoutHTTPRequest(h, "GET", "/v1/devices", newer.AccessToken); w.Code != 200 || strings.Contains(w.Body.String(), old.InstallationID) {
		t.Fatalf("remaining device list: %d %s", w.Code, w.Body.String())
	}
}
