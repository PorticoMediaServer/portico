package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestOperationsOwnerBoundaryAndIndependentPanels(t *testing.T) {
	d, owner, member := operationsFixture(t)
	mux := http.NewServeMux()
	d.operationsRoutes(mux)
	hostedOwner, e := operationsHostedOwner(d)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"memory", "database", "build"} {
		path := "/v1/admin/operations/" + name
		for _, token := range []string{"", member.AccessToken, hostedOwner.AccessToken} {
			if w := supportRequest(mux, token, path); w.Code != refusedStatus(token) {
				t.Fatal("authority", w.Code)
			}
		}
		w := supportRequest(mux, owner.AccessToken, path)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > 8192 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct {
			Scope struct {
				ServerID string `json:"serverId"`
				Fence    string `json:"viewerFence"`
			}
			Panel operations.Panel
		}
		if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil || out.Scope.ServerID != d.Identity.ID() || len(out.Scope.Fence) != 64 || out.Panel.Name != name {
			t.Fatal("scope/panel", e, out)
		}
		for _, secret := range []string{"PRIVATE", "TOKEN-secret", owner.AccessToken, "SOURCE-PATH", "private-library"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("leak", secret)
			}
		}
	}
	for _, path := range []string{"/v1/admin/operations/disk", "/v1/admin/operations/memory?path=private"} {
		if w := supportRequest(mux, owner.AccessToken, path); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestOperationsLateRevocationClosedErrorAndCancellation(t *testing.T) {
	d, owner, _ := operationsFixture(t)
	principal, e := d.Identity.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/admin/operations/{panel}", d.operationsHandler(func(ctx context.Context, db *sql.DB, name string) (operations.Panel, error) {
		p, e := operations.Read(ctx, db, name)
		if err := d.Identity.Logout(principal); err != nil {
			t.Fatal(err)
		}
		return p, e
	}))
	if w := supportRequest(mux, owner.AccessToken, "/v1/admin/operations/memory"); w.Code != 401 || strings.Contains(w.Body.String(), "heapObjectsBytes") {
		t.Fatal(w.Code, w.Body.String())
	}
	owner, e = d.Identity.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	mux = http.NewServeMux()
	mux.HandleFunc("GET /v1/admin/operations/{panel}", d.operationsHandler(func(context.Context, *sql.DB, string) (operations.Panel, error) {
		return operations.Panel{}, errors.New("PRIVATE/token=SECRET")
	}))
	if w := supportRequest(mux, owner.AccessToken, "/v1/admin/operations/database"); w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Code, w.Body.String())
	}
	d.DB.SetMaxOpenConns(1)
	held, e := d.DB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "/v1/admin/operations/memory", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	w := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(w, r)
	if time.Since(start) > time.Second || w.Code != 503 {
		t.Fatal("authorization wait escaped deadline", w.Code)
	}
}

func TestOperationsCompletionAuthDeadlineIsTransient(t *testing.T) {
	d, owner, _ := operationsFixture(t)
	d.DB.SetMaxOpenConns(1)
	var held *sql.Conn
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/admin/operations/{panel}", d.operationsHandler(func(ctx context.Context, db *sql.DB, name string) (operations.Panel, error) {
		panel, e := operations.Read(ctx, db, name)
		if e != nil {
			t.Fatal(e)
		}
		held, e = db.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return panel, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "/v1/admin/operations/memory", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if held != nil {
		held.Close()
	}
	if w.Code != 503 || strings.Contains(w.Body.String(), "heapObjectsBytes") {
		t.Fatal("temporary auth wait must withhold data without declaring revocation", w.Code, w.Body.String())
	}
	// The same credential still works after the pool is available.
	mux = http.NewServeMux()
	d.operationsRoutes(mux)
	if w = supportRequest(mux, owner.AccessToken, "/v1/admin/operations/memory"); w.Code != 200 {
		t.Fatal("owner incorrectly revoked", w.Code)
	}
}

func operationsFixture(t *testing.T) (Dependencies, identity.Envelope, identity.Envelope) {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','PRIVATE-OWNER',x'00','profile',1);INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','PRIVATE-MEMBER',x'00','member-profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('private-library','PRIVATE-TITLE','movie','/private/SOURCE-PATH');INSERT INTO jobs(id,library_id,status,error,created_at)VALUES('private-job','private-library','failed','TOKEN-secret','2026-09-05T00:00:00Z');`)
	if e != nil {
		t.Fatal(e)
	}
	owner, e := id.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, e := id.Issue("member", "member-profile", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	return Dependencies{DB: db, Identity: id}, owner, member
}

// A synthetic retired-authority credential is used only to prove refusal.
func operationsHostedOwner(d Dependencies) (identity.Envelope, error) {
	return identity.Envelope{AccessToken: "retired-authority-credential"}, nil
}
