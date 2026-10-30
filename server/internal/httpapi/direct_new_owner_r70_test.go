package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// ACC-LEAF-16: the request boundaries of POST /v1/direct/ownership for the
// newAccount variant, exercised through the real router and a real local owner
// session. The three refusals must leave the database untouched; the valid body
// must reach the existing accepting service path and produce the create+transfer
// in one request, with no separate member-create step.

type newOwnerHTTP struct {
	server   *httptest.Server
	db       *sql.DB
	token    string
	revision int64
}

func newOwnerHTTPFixture(t *testing.T) newOwnerHTTP {
	t.Helper()
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {db.Close()})
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("Testing1!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',?,'profile',1)`, hash); err != nil {
		t.Fatal(err)
	}
	login, err := ident.DirectLogin(context.Background(), "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)}))
	t.Cleanup(server.Close)
	return newOwnerHTTP{server: server, db: db, token: login.AccountToken, revision: login.Account.Revision}
}

func (f newOwnerHTTP) post(t *testing.T, body any) int {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/direct/ownership", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func (f newOwnerHTTP) accounts(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func newAccountBody() map[string]any {
	return map[string]any{"username": "harbourview", "name": "Harbour View", "password": "New-password2!"}
}

func TestNewOwnerHTTPRefusesMixedTargets(t *testing.T) {
	f := newOwnerHTTPFixture(t)
	before := f.accounts(t)
	for _, body := range []map[string]any{
		{"accountId": "member", "newAccount": newAccountBody(), "currentPassword": "Testing1!", "expectedRevision": f.revision},
		{"porticoIdentity": map[string]any{}, "newAccount": newAccountBody(), "currentPassword": "Testing1!", "expectedRevision": f.revision},
	} {
		if status := f.post(t, body); status != http.StatusBadRequest {
			t.Fatalf("mixed target accepted with status %d", status)
		}
	}
	if after := f.accounts(t); after != before {
		t.Fatal("a mixed target created an account", before, after)
	}
}

func TestNewOwnerHTTPRefusesMissingCurrentPassword(t *testing.T) {
	f := newOwnerHTTPFixture(t)
	before := f.accounts(t)
	for _, body := range []map[string]any{
		{"newAccount": newAccountBody(), "expectedRevision": f.revision},
		{"newAccount": newAccountBody(), "currentPassword": "", "expectedRevision": f.revision},
	} {
		if status := f.post(t, body); status != http.StatusBadRequest {
			t.Fatalf("missing current proof accepted with status %d", status)
		}
	}
	if after := f.accounts(t); after != before {
		t.Fatal("a refused proof created an account", before, after)
	}
}

func TestNewOwnerHTTPAcceptsOneCreateAndTransfer(t *testing.T) {
	f := newOwnerHTTPFixture(t)
	before := f.accounts(t)
	if status := f.post(t, map[string]any{"newAccount": newAccountBody(), "currentPassword": "Testing1!", "expectedRevision": f.revision}); status != http.StatusNoContent {
		t.Fatalf("valid create answered %d", status)
	}
	if after := f.accounts(t); after != before+1 {
		t.Fatal("a create did not add exactly one account", before, after)
	}
	var role string
	if err := f.db.QueryRow(`SELECT m.role FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.username='harbourview'`).Scan(&role); err != nil || role != identity.TierOwner {
		t.Fatal("new account is not the owner", role, err)
	}
	if err := f.db.QueryRow(`SELECT m.role FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id='account'`).Scan(&role); err != nil || role != identity.TierMember {
		t.Fatal("former owner was not demoted", role, err)
	}
	var memberships int
	if err := f.db.QueryRow(`SELECT count(*) FROM direct_memberships`).Scan(&memberships); err != nil || memberships != 2 {
		t.Fatal("the create+transfer did not produce exactly the two memberships", memberships, err)
	}
}
