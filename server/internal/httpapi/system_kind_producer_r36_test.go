package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// These are actual served public documents: Portico before setup, then a
// genuinely completed Local setup. No caller kind, claim hook or cast is used.
func TestSystemKindProducerUsesCanonicalSetupAuthority(t *testing.T) {
	responses := map[string]json.RawMessage{}
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	read := func(h http.Handler, name string, want identity.SignInKind, setup bool) {
		t.Helper()
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest("GET", "/v1/system", nil))
		var got SystemInfo
		if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil || out.Code != http.StatusOK || got.SignIn != want || got.SetupRequired != setup || got.ID == "" || got.Move != nil {
			t.Fatalf("%s: %d %+v %v", name, out.Code, got, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		// O-159 adds the negotiation facts: apiVersion, apiMajors and capabilities.
		if len(fields) != 13 {
			t.Fatalf("unexpected public fields: %v", fields)
		}
		for _, key := range []string{"apiVersion", "apiMajors", "capabilities"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("negotiation field %s missing", key)
			}
		}
		for _, key := range []string{"accessToken", "refreshToken", "owner", "account", "email", "returnRegistrationId"} {
			if _, ok := fields[key]; ok {
				t.Fatalf("private field %s", key)
			}
		}
		assertSpecResponse(t, "GET", "/v1/system", out)
		responses[name] = append(json.RawMessage(nil), out.Body.Bytes()...)
	}
	read(New(Dependencies{DB: db, Identity: id}), "portico-before-setup", identity.SignInPortico, true)
	d, _ := completedLocalFoundationFixture(t)
	read(New(d), "local-after-setup", identity.SignInLocal, false)
	if path := os.Getenv("PORTICO_TEST_SYSTEM_KIND_RESPONSES"); path != "" {
		raw, err := json.Marshal(responses)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
