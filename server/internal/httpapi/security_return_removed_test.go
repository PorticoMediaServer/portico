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

// The server-account Security return is gone: a Portico Account owner changes
// credentials on Hosted's own account page. Through the real router, none of its
// four routes exists any more, and a fresh install has no table for it.
func TestServerAccountSecurityReturnRoutesAreGone(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)}))
	defer server.Close()
	for _, step := range []string{"prepare", "confirm", "check", "cancel"} {
		response, err := server.Client().Post(server.URL+"/v1/direct/security-return/"+step, "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("POST /v1/direct/security-return/%s answered %d, want 404", step, response.StatusCode)
		}
	}
	var tables int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name LIKE 'identity_security_return%'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("a fresh install still has the Security return's objects: %d, %v", tables, err)
	}
}
