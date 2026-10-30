package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func supportFixture(t *testing.T) (Dependencies, identity.Envelope, identity.Envelope) {
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
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','PRIVATE-OWNER',x'00','profile',1),('member','PRIVATE-MEMBER',x'00','member-profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('private-library','PRIVATE-TITLE','movie','/private/SOURCE-PATH');INSERT INTO jobs(id,library_id,status,error,created_at)VALUES('private-job','private-library','failed','TOKEN-secret','2026-09-05T00:00:00Z');`); e != nil {
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
	control, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	return Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control}, owner, member
}
func supportRequest(h http.Handler, token, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestSupportHTTPExactOwnerNoStoreBoundedReport(t *testing.T) {
	d, owner, member := supportFixture(t)
	h := New(d)
	for _, token := range []string{"", member.AccessToken} {
		if w := supportRequest(h, token, "/v1/admin/support-report"); w.Code != refusedStatus(token) || strings.Contains(w.Body.String(), "schemaVersion") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := supportRequest(h, owner.AccessToken, "/v1/admin/support-report")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > diagnostics.MaxReportBytes {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Scope struct {
			ServerID    string `json:"serverId"`
			ViewerFence string `json:"viewerFence"`
		} `json:"scope"`
		Report diagnostics.Report `json:"report"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil || result.Scope.ServerID != d.Identity.ID() || len(result.Scope.ViewerFence) != 64 {
		t.Fatal(e)
	}
	for _, secret := range []string{"PRIVATE", "SOURCE-PATH", "TOKEN-secret", owner.AccessToken, "private-library", "private-job"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret leaked", secret)
		}
	}
	for _, path := range []string{"/v1/admin/support-report?include=logs", "/v1/admin/support-report?x=%zz"} {
		if w = supportRequest(h, owner.AccessToken, path); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestSupportHTTPRechecksOwnerAfterSnapshotAndRedactsReadFailure(t *testing.T) {
	d, owner, _ := supportFixture(t)
	p, e := d.Identity.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	h := d.supportHandler(func(ctx context.Context, db *sql.DB, hosted bool) (diagnostics.Report, error) {
		report, e := diagnostics.Read(ctx, db, hosted)
		if e != nil {
			t.Fatal(e)
		}
		if e = d.Identity.Logout(p); e != nil {
			t.Fatal(e)
		}
		return report, nil
	})
	if w := supportRequest(h, owner.AccessToken, "/v1/admin/support-report"); w.Code != 401 || strings.Contains(w.Body.String(), "schemaVersion") {
		t.Fatal("late revoked owner received report", w.Code, w.Body.String())
	}
	fresh, e := d.Identity.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	h = d.supportHandler(func(context.Context, *sql.DB, bool) (diagnostics.Report, error) {
		return diagnostics.Report{}, &privateSupportError{}
	})
	w := supportRequest(h, fresh.AccessToken, "/v1/admin/support-report")
	if w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Code, w.Body.String())
	}
}

type privateSupportError struct{}

func (*privateSupportError) Error() string { return "/PRIVATE/token=PRIVATE database error" }

// These cases exercise a real exhausted sql.DB pool, not an authorization stub.
func TestSupportHTTPAuthorizationContextAndReleasedSlots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, stage := range []string{"initial", "final"} {
		for _, ending := range []string{"cancel", "deadline"} {
			t.Run(stage+"/"+ending, func(t *testing.T) {
				d, owner, _ := supportFixture(t)
				d.DB.SetMaxOpenConns(1)
				var held *sql.Conn
				var err error
				if stage == "initial" {
					held, err = d.DB.Conn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
				}
				defer func() {
					if held != nil {
						held.Close()
					}
				}()
				var reads atomic.Int32
				snapshotReturned := make(chan struct{})
				nextEntered := make(chan struct{}, 2)
				nextRelease := make(chan struct{})
				nextDone := make(chan struct{}, 2)
				released := false
				defer func() {
					if !released {
						close(nextRelease)
					}
				}()
				h := d.supportHandler(func(ctx context.Context, db *sql.DB, configured bool) (diagnostics.Report, error) {
					n := reads.Add(1)
					if stage == "final" && n == 1 {
						report, e := diagnostics.Read(ctx, db, configured)
						if e != nil {
							return report, e
						}
						held, e = db.Conn(ctx)
						close(snapshotReturned)
						return report, e
					}
					nextEntered <- struct{}{}
					select {
					case <-nextRelease:
					case <-ctx.Done():
						return diagnostics.Report{}, ctx.Err()
					}
					return diagnostics.Read(ctx, db, configured)
				})
				ctx, cancel := context.WithCancel(context.Background())
				if ending == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
				}
				defer cancel()
				r := httptest.NewRequest("GET", "/v1/admin/support-report", nil).WithContext(ctx)
				r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				waits := d.DB.Stats().WaitCount
				go func() { h.ServeHTTP(w, r); close(done) }()
				if stage == "final" {
					select {
					case <-snapshotReturned:
					case <-time.After(time.Second):
						t.Fatal("snapshot did not complete")
					}
				}
				until := time.Now().Add(time.Second)
				for d.DB.Stats().WaitCount == waits && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if d.DB.Stats().WaitCount == waits {
					t.Fatal("authorization did not enter the real database pool wait")
				}
				if ending == "cancel" {
					cancel()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("authorization ignored context while database remained held")
				}
				if w.Code == 200 || strings.Contains(w.Body.String(), "schemaVersion") {
					t.Fatal("cancelled authorization published report")
				}
				if stage == "initial" && reads.Load() != 0 {
					t.Fatal("snapshot ran before initial authorization")
				}
				held.Close()
				held = nil
				// Both slots must be available again, and a third request must get 429.
				for i := 0; i < 2; i++ {
					go func() { supportRequest(h, owner.AccessToken, "/v1/admin/support-report"); nextDone <- struct{}{} }()
				}
				for i := 0; i < 2; i++ {
					select {
					case <-nextEntered:
					case <-time.After(time.Second):
						t.Fatal("cancelled handler retained a report slot")
					}
				}
				if result := supportRequest(h, owner.AccessToken, "/v1/admin/support-report"); result.Code != 429 || result.Header().Get("Retry-After") != "1" {
					t.Fatal("report admission was not bounded", result.Code)
				}
				close(nextRelease)
				released = true
				for i := 0; i < 2; i++ {
					select {
					case <-nextDone:
					case <-time.After(time.Second):
						t.Fatal("released reports did not complete")
					}
				}
			})
		}
	}
}
