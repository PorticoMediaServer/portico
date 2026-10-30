package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

func TestSonicMountedOwnerCASStrictBodiesAndMemberRefusal(t *testing.T) {
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
					t.Error("owner status opened recording")
					return nil, mediaanalysis.ErrUnsupported
				},
				Decoder: func(mediaanalysis.Input) mediaanalysis.Decode { t.Error("owner status decoded recording"); return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			d.Analysis = service
			h := New(d)
			call := func(method, path, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+session.AccessToken)
				if body != "" {
					r.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			path := "/v1/admin/libraries/sonic-api/sonic-analysis"
			if role != "owner" {
				for _, request := range []struct{ method, path, body string }{{"GET", path, ""}, {"PUT", path, `{"enabled":false,"expectedRevision":1}`}, {"POST", path + "/retry", `{}`}} {
					if w := call(request.method, request.path, request.body); w.Code != 403 {
						t.Fatal("member admitted", request.method, w.Code, w.Body.String())
					}
				}
				return
			}
			if w := call("GET", path, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, body := range []string{`{}`, `{"enabled":null,"expectedRevision":1}`, `{"enabled":"false","expectedRevision":1}`, `{"enabled":false,"expectedRevision":null}`, `{"enabled":false,"expectedRevision":0}`, `{"enabled":false,"expectedRevision":1,"path":"/model"}`, `{"enabled":false,"enabled":true,"expectedRevision":1}`, `{"Enabled":false,"expectedRevision":1}`} {
				if w := call("PUT", path, body); w.Code < 400 {
					t.Fatal("invalid mutation admitted", body, w.Code, w.Body.String())
				}
			}
			w := call("PUT", path, `{"enabled":false,"expectedRevision":1}`)
			var status mediaanalysis.SonicStatusView
			if err = json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 200 || status.Enabled || status.Revision != 2 || status.State != "disabled" || status.Available || !status.DefaultEnabled {
				t.Fatal("explicit Off not retained", w.Code, w.Body.String(), err)
			}
			if w = call("PUT", path, `{"enabled":true,"expectedRevision":1}`); w.Code != http.StatusConflict {
				t.Fatal("stale write admitted", w.Code, w.Body.String())
			}
			for _, body := range []string{`{"path":"/model"}`, `null`, `{"model":"other"}`} {
				if w = call("POST", path+"/retry", body); w.Code < 400 {
					t.Fatal("retry accepted client model/path", w.Code, w.Body.String())
				}
			}
			if w = call("POST", path+"/retry", `{}`); w.Code != 200 {
				t.Fatal("unavailable retry did not report safe status", w.Code, w.Body.String())
			}
			if w = call("POST", path+"/retry", ""); w.Code != 200 {
				t.Fatal("empty retry rejected", w.Code, w.Body.String())
			}
		})
	}
}
