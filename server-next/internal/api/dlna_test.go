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
	for k, v := range header {
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
// the home networks. Then a device there, with no sign-in, browses the video libraries and plays
// an original by range, and nothing is recorded; a device anywhere else, a request through a
// proxy, and this machine's loopback are refused; turning it off refuses at once.
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
	const home, away = "192.168.1.20", "198.51.100.9"
	if w := dlnaCall(s, home, "GET", "/dlna/device.xml", "", nil); w.Code != 404 {
		t.Fatalf("with DLNA off a device at home was answered %d", w.Code)
	}
	on, off := true, false
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &on}); got != 400 {
		t.Errorf("turning DLNA on before listing the home networks answered %d, want 400", got)
	}
	networks := NetworkSettingsEdit{HomeNetworks: &[]string{"192.168.1.0/24", "127.0.0.0/8"}, TrustedProxies: []string{}}
	if got := status(t, s, "PUT", "/v1/admin/settings/network", owner, networks); got != 200 {
		t.Fatalf("saving the home networks answered %d", got)
	}
	if got := status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &on}); got != 200 {
		t.Fatalf("turning DLNA on answered %d", got)
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

	for _, c := range []struct {
		what, peer string
		header     map[string]string
	}{
		{"a device away from home", away, nil},
		{"a request through a proxy", home, map[string]string{"X-Forwarded-For": home}},
		{"this machine's loopback, although listed", "127.0.0.1", nil},
	} {
		if w := dlnaCall(s, c.peer, "GET", media, "", c.header); w.Code != 403 {
			t.Errorf("%s was answered %d, want 403", c.what, w.Code)
		}
	}
	status(t, s, "PUT", "/v1/admin/settings/remote-access", owner, RemoteAccessSettingsEdit{DLNA: &off})
	if w := dlnaCall(s, home, "GET", media, "", nil); w.Code != 404 {
		t.Errorf("with DLNA turned off a device at home was answered %d", w.Code)
	}
}
