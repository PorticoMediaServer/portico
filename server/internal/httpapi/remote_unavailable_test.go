package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Optional storage services are absent in small server compositions and the
// hostile-route fixture. Their absence is not an internal error and must not
// disclose subsystem availability to a caller without current owner authority.
func TestRemoteSourceContractsKeepOwnerChecksBeforeUnavailableSubsystem(t *testing.T) {
	for _, mountPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "mounts absent", true: "remote service absent"}[mountPresent], func(t *testing.T) {
			d, owner := logoutHTTPFixture(t)
			if mountPresent {
				helper, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				d.Mounts, err = mounts.New(d.DB, filepath.Join(t.TempDir(), "managed"), "", helper, storage.New(helper))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.DB.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member-unavailable','member-unavailable',X'00','member-profile',1)`); err != nil {
				t.Fatal(err)
			}
			member, err := d.Identity.Issue("member-unavailable", "member-profile", "local", "member", 1)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := FoundationRegistry(d)
			if err != nil {
				t.Fatal(err)
			}
			change := remotesources.SourceReadSettingsChange{OperationID: "proof-read-profile", ExpectedGeneration: "1", Values: remotesources.SourceReadValues{ScanDepth: "basic", Connections: 4, RequestsPerSecond: 4}}
			for _, route := range []struct {
				method, path string
				body         any
			}{
				{"GET", "/v1/storage/sources/missing/settings/read-profile", nil},
				{"PUT", "/v1/storage/sources/missing/settings/read-profile", change},
				{"POST", "/v1/storage/sources/webdav/test", remotesources.DAVConnectionInput{Name: "Archive", Root: "https://nas.example/media/", Username: "", Password: ""}},
			} {
				for _, caller := range []struct {
					token  string
					status int
				}{{"", 401}, {member.AccessToken, 403}, {owner.AccessToken, 503}} {
					var body []byte
					if route.body != nil {
						body, err = json.Marshal(route.body)
						if err != nil {
							t.Fatal(err)
						}
					}
					r := httptest.NewRequest(route.method, route.path, bytes.NewReader(body))
					if route.body != nil {
						r.Header.Set("Content-Type", "application/json")
					}
					if caller.token != "" {
						r.Header.Set("Authorization", "Bearer "+caller.token)
					}
					w := httptest.NewRecorder()
					registry.ServeHTTP(w, r)
					if w.Code != caller.status || caller.status == 503 && !strings.Contains(w.Body.String(), `"code":"administration_unavailable"`) {
						t.Fatal(route.method, route.path, "incorrect owner/unavailable classification", w.Code, w.Body.String())
					}
				}
			}
		})
	}
}
