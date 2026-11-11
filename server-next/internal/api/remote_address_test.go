package api

import (
	"strings"
	"testing"
)

// B-44, first part: a source at a link-local address, where a cloud machine keeps its own
// sign-in, is refused with a plain sentence and nothing is saved.
func TestRemoteSourceAtALinkLocalAddressIsRefused(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	for _, address := range []string{"http://169.254.169.254/latest/", "http://metadata.google.internal/"} {
		add := RemoteSourceCreate{Kind: "webdav", Name: "Cloud", WebDAV: &WebDAVSettings{Address: address}}
		if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "source_address_refused") {
			t.Errorf("%s answered %d %s", address, w.Code, w.Body)
		}
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a refused source was saved: %+v", list.Items)
	}
}
