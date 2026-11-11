package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// Scanning rule 12: the owner sets a source's download speed (none by default) beside its
// other limits, and sees it; a negative one is refused.
func TestRemoteSourceDownloadLimit(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat.mkv": "x"})
	var src RemoteSource
	if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken,
		RemoteSourceCreate{Kind: "webdav", Name: "NAS", WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &src); w.Code != 200 ||
		src.Limits.DownloadBytesPerSecond != 0 {
		t.Fatalf("adding answered %d %s", w.Code, w.Body)
	}
	limits := RemoteLimits{Connections: 2, PerSecond: 2, DownloadBytesPerSecond: 5_000_000}
	if w := call(t, s, "PATCH", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken, RemoteSourceEdit{Limits: &limits}, &src); w.Code != 200 || src.Limits != limits {
		t.Fatalf("setting the download speed answered %d %s", w.Code, w.Body)
	}
	limits.DownloadBytesPerSecond = -1
	if code := status(t, s, "PATCH", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken, RemoteSourceEdit{Limits: &limits}); code != 400 {
		t.Fatalf("a negative download speed answered %d", code)
	}
}
