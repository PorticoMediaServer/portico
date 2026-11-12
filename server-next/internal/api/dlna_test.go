package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// dlnaCall sends a request as a device at peer would.
func dlnaCall(s *Server, peer, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = peer + ":40000"
	r.Host = "192.168.1.10:32500" // the address the server announced, as players use it
	for k, v := range header {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func browse(s *Server, peer, id, flag string) *httptest.ResponseRecorder {
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` +
		`<u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><ObjectID>` + id + `</ObjectID><BrowseFlag>` + flag +
		`</BrowseFlag><Filter>*</Filter><StartingIndex>0</StartingIndex><RequestedCount>50</RequestedCount><SortCriteria></SortCriteria>` +
		`</u:Browse></s:Body></s:Envelope>`
	return dlnaCall(s, peer, "POST", "/dlna/control/cds", body, map[string]string{"SOAPACTION": `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`})
}

// DLNA is off until the owner turns it on, and can be turned on only once the owner has listed
// home networks; a list that takes in the whole internet is warned about, not refused. Then a
// device at home (on Tailscale, or behind a proxy the owner listed that passes its address),
// with no sign-in, browses the libraries and plays an original by range, and nothing is
// recorded. A listed proxy that passes on no address is served and warned about. A device away,
// this machine's loopback, a proxy the owner did not list, and a web page that re-points its own
// name at the server are refused, with the same 404 as when DLNA is off.
func TestDLNA(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	ctx := context.Background()
	folder := t.TempDir()
	original := []byte(strings.Repeat("an original film, served as it is. ", 1000))
	if err := os.WriteFile(filepath.Join(folder, "heat.mkv"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "heat.mkv"}, Role: db.FileMedia, Size: int64(len(original)), Container: "matroska",
			Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 1920, Height: 1080}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "heat"}}, Title: "Heat",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "heat.mkv"}}}}}}},
	}); err != nil {
		t.Fatal(err)
	}
	const home, away, proxy = "192.168.1.20", "198.51.100.9", "192.168.1.5"
	if w := dlnaCall(s, home, "GET", "/dlna/device.xml", "", nil); w.Code != 404 {
		t.Fatalf("with DLNA off a device at home was answered %d", w.Code)
	}
	// The DLNA switch and names are fields of the remote access settings, saved whole.
	var settings RemoteAccessSettings
	saveDLNA := func(change func(*RemoteAccessSettings)) int {
		t.Helper()
		var v RemoteAccessSettings
		call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &v)
		change(&v)
		settings = RemoteAccessSettings{}
		return call(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSave{RemoteAccessSettings: v}, &settings).Code
	}
	if got := saveDLNA(func(v *RemoteAccessSettings) { v.DLNA = true }); got != 400 {
		t.Errorf("turning DLNA on before listing the home networks answered %d, want 400", got)
	}
	setHome := func(home ...string) {
		t.Helper()
		networks := NetworkSettingsEdit{HomeNetworks: &home, TrustedProxies: []string{proxy}}
		if got := status(t, s, "PUT", "/v1/admin/settings/network", owner, networks); got != 200 {
			t.Fatalf("saving the home networks answered %d", got)
		}
	}
	setHome("192.168.1.0/24", "100.64.0.0/10", "127.0.0.0/8")
	if got := saveDLNA(func(v *RemoteAccessSettings) { v.DLNA = true }); got != 200 || settings.Warning != "" || !settings.DLNA {
		t.Fatalf("turning DLNA on answered %d with warning %q", got, settings.Warning)
	}

	// TVs list the server by the name the owner gave it in Settings › General.
	rename := newRequest("PUT", "/v1/admin/settings/general", owner, json.RawMessage(`{"name":"Den"}`))
	rename.Header.Set("If-Match", `"0"`)
	if w := serve(s, rename); w.Code != 200 {
		t.Fatalf("renaming the server answered %d", w.Code)
	}
	if w := dlnaCall(s, home, "GET", "/dlna/device.xml", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "MediaServer:1") ||
		!strings.Contains(w.Body.String(), "<friendlyName>Den</friendlyName>") {
		t.Fatalf("the device description answered %d: %s", w.Code, w.Body)
	}
	root := browse(s, home, "0", "BrowseDirectChildren").Body.String()
	if !strings.Contains(root, "Films") {
		t.Fatalf("the root did not list the film library: %s", root)
	}
	films := browse(s, home, lib.PublicID, "BrowseDirectChildren").Body.String()
	start := strings.Index(films, "/dlna/media/")
	if !strings.Contains(films, "Heat") || start < 0 {
		t.Fatalf("the library did not list the film with its file: %s", films)
	}
	media := films[start : strings.Index(films[start:], "&lt;/res")+start]
	w := dlnaCall(s, home, "GET", media, "", map[string]string{"Range": "bytes=3-12"})
	got, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusPartialContent || string(got) != string(original[3:13]) {
		t.Errorf("playing a range of the original answered %d %q", w.Code, got)
	}
	if len(s.playback.All()) != 0 {
		t.Error("a DLNA play started a playback session")
	}

	name, _ := os.Hostname()
	short, _, _ := strings.Cut(strings.ToLower(name), ".")
	for _, c := range []struct {
		what, peer string
		header     map[string]string
		want       int
	}{
		{"a device on Tailscale, listed as home", "100.101.102.103", nil, 200},
		{"the server's own name", home, map[string]string{"Host": short + ":32500"}, 200},
		{"the server's .local name", home, map[string]string{"Host": short + ".local:32500"}, 200},
		{"the server's MagicDNS name", home, map[string]string{"Host": short + ".tail1234.ts.net"}, 200},
		{"a name the router made", home, map[string]string{"Host": "nas.fritz.box:32500"}, 200},
		{"the Fritz!Box router's own name", home, map[string]string{"Host": "fritz.box"}, 200},
		{"a bought name under the public .box domain", home, map[string]string{"Host": "nas.evil.box"}, 404},
		{"a name of several parts under a home ending", home, map[string]string{"Host": "nas.home.lan"}, 200},
		{"the owner's own domain, not yet added", home, map[string]string{"Host": "media.example.com"}, 404},
		{"the owner's proxy passing a home address", proxy, map[string]string{"X-Forwarded-For": home}, 200},
		{"the owner's proxy passing no address (warned, not refused)", proxy, nil, 200},
		{"the owner's proxy passing no address, with a re-pointed name", proxy, map[string]string{"Host": "evil.example"}, 404},
		{"this machine's loopback, although listed as home", "127.0.0.1", nil, 404},
		{"a device away from home", away, nil, 404},
		{"the owner's proxy passing an address away", proxy, map[string]string{"X-Forwarded-For": away}, 404},
		{"a proxy the owner did not list", home, map[string]string{"X-Forwarded-For": home}, 404},
		{"a web page that re-pointed its own name", home, map[string]string{"Host": "evil.example:32500"}, 404},
		{"a look-alike name", home, map[string]string{"Host": short + ".evil.example"}, 404},
	} {
		if w := dlnaCall(s, c.peer, "GET", media, "", c.header); w.Code != c.want {
			t.Errorf("%s was answered %d, want %d", c.what, w.Code, c.want)
		}
	}
	// The proxy that passed on no address is warned about from then on.
	call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &settings)
	if !strings.Contains(settings.Warning, "proxy at "+proxy+" ") || !strings.Contains(settings.Warning, "anyone who can reach that proxy") {
		t.Errorf("after a trusted proxy passed on no address the settings warned %q", settings.Warning)
	}
	var network NetworkSettings
	call(t, s, "GET", "/v1/admin/settings/network", owner, nil, &network)
	if !slices.ContainsFunc(network.Warnings, func(w Warning) bool { return w.Code == "proxy_without_header" && strings.Contains(w.Message, proxy) }) {
		t.Errorf("the network settings did not name the proxy: %+v", network.Warnings)
	}
	// The owner adds their own domain on the proxy: it is answered, and still nothing else is.
	if got := saveDLNA(func(v *RemoteAccessSettings) { v.DLNANames = []string{"Media.Example.com"} }); got != 200 {
		t.Fatalf("adding a name answered %d", got)
	}
	if w := dlnaCall(s, home, "GET", media, "", map[string]string{"Host": "media.example.com:443"}); w.Code != 200 {
		t.Errorf("a name the owner added was answered %d", w.Code)
	}
	if w := dlnaCall(s, home, "GET", media, "", map[string]string{"Host": "evil.example.com"}); w.Code != 404 {
		t.Errorf("another name under the owner's domain was answered %d", w.Code)
	}
	// The owner lists the whole internet as home: warned, not stopped. Anyone can then play.
	setHome("192.168.1.0/24", "0.0.0.0/0")
	call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &settings)
	if !strings.Contains(settings.Warning, "anyone on the internet") {
		t.Errorf("with the whole internet listed as home the settings warned %q", settings.Warning)
	}
	if w := dlnaCall(s, away, "GET", media, "", nil); w.Code != 200 {
		t.Errorf("with the whole internet listed as home DLNA answered %d, want 200: the owner chose it", w.Code)
	}
	if w := dlnaCall(s, away, "GET", media, "", map[string]string{"Host": "evil.example"}); w.Code != 404 {
		t.Errorf("a re-pointed name was answered %d even so", w.Code)
	}
	setHome("192.168.1.0/24")
	settings = RemoteAccessSettings{}
	if call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &settings); strings.Contains(settings.Warning, "anyone on the internet") {
		t.Errorf("the warning stayed after the range was removed: %q", settings.Warning)
	}
	saveDLNA(func(v *RemoteAccessSettings) { v.DLNA = false })
	// Opening the library to every device at home is for the owner signed in: a key may save
	// the rest of the remote access settings, never the DLNA switch.
	var key KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Tool", Scopes: []string{"admin"}}, &key)
	var current RemoteAccessSettings
	call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &current)
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", key.Secret, RemoteAccessSave{RemoteAccessSettings: current}); got != 200 {
		t.Errorf("a key saving the remote access settings unchanged answered %d", got)
	}
	current.DLNA = true
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", key.Secret, RemoteAccessSave{RemoteAccessSettings: current}); got != 403 {
		t.Errorf("a key turning DLNA on answered %d, want 403", got)
	}
	if w := dlnaCall(s, home, "GET", media, "", nil); w.Code != 404 {
		t.Errorf("with DLNA turned off a device at home was answered %d", w.Code)
	}
}

// DLNA decides by its own home-only rule whatever the remote access settings say about
// connections from outside: a TV behind a listed proxy that passes on no visitor's address is
// answered and the owner warned, under Remote access Off, Secure connections Required and
// Enhanced alike, while an unlisted proxy and a device away from home are still refused.
func TestDLNABehindAListedProxyUnderEveryRemoteAccessSetting(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	const home, away, proxy = "192.168.1.20", "198.51.100.9", "192.168.1.5"
	homeList := []string{"192.168.1.0/24"}
	if got := status(t, s, "PUT", "/v1/admin/settings/network", owner, NetworkSettingsEdit{HomeNetworks: &homeList, TrustedProxies: []string{proxy}}); got != 200 {
		t.Fatalf("saving the home networks answered %d", got)
	}
	var v RemoteAccessSettings
	call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &v)
	v.DLNA = true
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSave{RemoteAccessSettings: v}); got != 200 {
		t.Fatalf("turning DLNA on answered %d", got)
	}
	const device = "/dlna/device.xml"
	for _, c := range []struct {
		what string
		set  func(*RemoteAccessSettings)
	}{
		{"Remote access Off", func(v *RemoteAccessSettings) { v.RemoteAccess = "off" }},
		{"Secure connections Required", func(v *RemoteAccessSettings) { v.RemoteSecurity, v.SecureConnections = "standard", "required" }},
		{"Enhanced in force", func(v *RemoteAccessSettings) { v.AllowedAddresses = []string{"media.example.com"} }},
	} {
		// Put in force as a save would, without the owner's own connection being judged.
		next := v
		c.set(&next)
		s.access.set(next, true)
		// The setting does refuse an ordinary request that arrives the same way.
		if w := dlnaCall(s, proxy, "GET", "/v1/system", "", nil); w.Code != 403 {
			t.Fatalf("%s: an ordinary request through the proxy with no address answered %d, want 403", c.what, w.Code)
		}
		if w := dlnaCall(s, proxy, "GET", device, "", nil); w.Code != 200 {
			t.Errorf("%s: a TV behind the listed proxy that passes on no address was answered %d, want 200", c.what, w.Code)
		}
		if warning := s.dlnaWarning(true); !strings.Contains(warning, "proxy at "+proxy+" ") {
			t.Errorf("%s: the owner was not warned about the proxy: %q", c.what, warning)
		}
		if w := dlnaCall(s, home, "GET", device, "", map[string]string{"X-Forwarded-For": home}); w.Code != 404 {
			t.Errorf("%s: a proxy the owner did not list was answered %d, want 404", c.what, w.Code)
		}
		if w := dlnaCall(s, away, "GET", device, "", nil); w.Code != 404 {
			t.Errorf("%s: a device away from home was answered %d, want 404", c.what, w.Code)
		}
	}
}
