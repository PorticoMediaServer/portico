package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
)

func TestLibrarySonicControlsRequireOwnerAndReadyRuntime(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, err := f.deps.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('sonic-music','Music','music',?)`, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ready := true
	f.deps.Administration = administration.New(f.deps.DB)
	f.deps.Administration.SonicRuntimeAvailable = func() bool { return ready }
	f.handler = New(f.deps)
	for _, tc := range []struct {
		token     string
		available bool
		want      bool
	}{{f.owner, true, true}, {f.admin, true, false}, {f.owner, false, false}} {
		ready = tc.available
		w := f.call("GET", "/v1/admin/libraries/sonic-music/settings", tc.token, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var doc administration.LibraryDocument
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, id := range doc.EligibleControls {
			found = found || id == "libraries.sonicAnalysis"
		}
		if found != tc.want {
			t.Fatal("owner/runtime eligibility mismatch", tc.available, found, tc.want)
		}
	}
	if w := f.call("GET", "/v1/admin/libraries/sonic-music/settings", f.member, ""); w.Code != 403 {
		t.Fatal("member reached Library controls", w.Code)
	}
}
