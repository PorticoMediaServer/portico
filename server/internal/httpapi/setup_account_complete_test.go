package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A server with no Portico Services connection (local sign-in, or a harness) refuses
// Portico Account completion before anything is admitted or any member push is woken.
func TestAccountSetupCompletionWithoutPorticoServicesRefusesCleanly(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db)})
	body := []byte(`{"requestId":"44444444-4444-4444-8444-444444444444","setupToken":"` + identity.Token() + `","porticoIdentity":{"keyId":"k","payload":"p","signature":"s"}}`)
	r := httptest.NewRequest("POST", "http://127.0.0.1:32500/v1/setup/account/complete", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://127.0.0.1:32500")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("completion without Portico Services: %d %s", w.Code, w.Body.String())
	}
}
