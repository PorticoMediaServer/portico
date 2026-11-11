package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
// home networks that do not cover the whole internet. Then a device at home (Tailscale and a
// proxy the owner listed included), with no sign-in, browses the libraries and plays an
// original by range, and nothing is recorded. A device away, a proxy the owner did not list,
// and a web page that re-points its own name at the server are refused, with the same 404 as
// when DLNA is off.
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
	on, off := true, false
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &on}); got != 400 {
		t.Errorf("turning DLNA on before listing the home networks answered %d, want 400", got)
	}
	setHome := func(home ...string) {
		t.Helper()
		networks := NetworkSettingsEdit{HomeNetworks: &home, TrustedProxies: []string{proxy}}
		if got := status(t, s, "PUT", "/v1/admin/settings/network", owner, networks); got != 200 {
			t.Fatalf("saving the home networks answered %d", got)
		}
	}
	setHome("192.168.1.0/24", "100.64.0.0/10")
	var settings RemoteAccessSettings
	if w := call(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &on}, &settings); w.Code != 200 || settings.Warning != "" {
		t.Fatalf("turning DLNA on answered %d with warning %q", w.Code, settings.Warning)
	}

	if w := dlnaCall(s, home, "GET", "/dlna/device.xml", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "MediaServer:1") {
		t.Fatalf("the device description answered %d", w.Code)
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
	// The owner adds their own domain on the proxy: it is answered, and still nothing else is.
	names := []string{"Media.Example.com"}
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNANames: &names}); got != 200 {
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
	if call(t, s, "GET", "/v1/admin/settings/remote-access", owner, nil, &settings); settings.Warning != "" {
		t.Errorf("the warning stayed after the range was removed: %q", settings.Warning)
	}
	status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &off})
	if w := dlnaCall(s, home, "GET", media, "", nil); w.Code != 404 {
		t.Errorf("with DLNA turned off a device at home was answered %d", w.Code)
	}
}
