package api

import "github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"

// Libraries and the storage they read from.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		dav := remotetest.NewDAV(th.T, map[string]string{"Films/film.mkv": "x"})
		settings := &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}
		return []retryCase{
			{route: "POST /v1/admin/libraries", body: LibraryCreate{Name: "Retry Films", Kind: "movies", Folders: []string{th.T.TempDir()}},
				other: LibraryCreate{Name: "Other", Kind: "movies", Folders: []string{th.T.TempDir()}},
				count: func() int { return th.named("/v1/admin/libraries", "name", "Retry Films") }},
			{route: "POST /v1/admin/remote-sources", body: RemoteSourceCreate{Kind: "webdav", Name: "NAS", WebDAV: settings},
				other: RemoteSourceCreate{Kind: "webdav", Name: "Other", WebDAV: settings},
				count: func() int { return th.named("/v1/admin/remote-sources", "name", "NAS") }},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/admin/libraries/{id}/scan":      "a second scan request while one waits or runs joins it and queues nothing new",
		"POST /v1/admin/remote-sources/{id}/test": "tests the saved connection and replaces its status",
		"POST /v1/admin/items/deletion":           "a read with a body: it says what deleting a selection would take and changes nothing",
		"POST /v1/admin/items/delete":             "a repeat is refused whole with 400, because its first title is gone, and deletes nothing more",
	})
}
