package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

func TestSonicTypedOwnerProducerUsesRealAuthorityCASAndSafeProjection(t *testing.T) {
	for _, role := range []string{"owner", "member"} {
		t.Run(role, func(t *testing.T) {
			d, session, _ := hostedAdministrationFixture(t, role)
			c := catalogtest.New(t, d.DB)
			c.Library("sonic-api", "Music", "music", "/music")
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			service, err := mediaanalysis.New(mediaanalysis.Options{DB: d.DB, Directory: root,
				Open: func(context.Context, string, string) (mediaanalysis.Input, error) {
					t.Error("status opened a recording")
					return nil, mediaanalysis.ErrUnsupported
				},
				Decoder: func(mediaanalysis.Input) mediaanalysis.Decode { t.Error("status invoked decoder"); return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			d.Analysis = service
			r := httptest.NewRequest("GET", "/sonic-producer", nil)
			r.SetPathValue("id", "sonic-api")
			r.Header.Set("Authorization", "Bearer "+session.AccessToken)
			status, err := d.sonicStatusRequest(r)
			if role != "owner" {
				if !errors.Is(err, identity.ErrForbidden) {
					t.Fatal("member saw Sonic owner facts", err)
				}
				if _, err = d.sonicSettingsRequest(r, SonicSettingsChange{Enabled: false, ExpectedRevision: 1}); !errors.Is(err, identity.ErrForbidden) {
					t.Fatal("member changed Sonic", err)
				}
				return
			}
			if err != nil || status.LibraryID != "sonic-api" || !status.Enabled || status.Revision != 1 || status.State != "unavailable" || status.Done != 0 {
				t.Fatal(status, err)
			}
			data, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"windows", "configuration", "/music", "Work", "Runtime"} {
				if strings.Contains(string(data), private) {
					t.Fatal("raw private domain projection", string(data))
				}
			}
			status, err = d.sonicSettingsRequest(r, SonicSettingsChange{Enabled: false, ExpectedRevision: 1})
			if err != nil || status.Enabled || status.Revision != 2 || status.State != "disabled" {
				t.Fatal(status, err)
			}
			if _, err = d.sonicSettingsRequest(r, SonicSettingsChange{Enabled: true, ExpectedRevision: 1}); !errors.Is(err, catalog.ErrLibraryConfigurationConflict) {
				t.Fatal("stale CAS admitted", err)
			}
			if _, err = d.sonicSettingsRequest(r, SonicSettingsChange{Enabled: true, ExpectedRevision: 0}); !errors.Is(err, errStrictJSON) {
				t.Fatal("missing revision admitted", err)
			}
		})
	}
}
