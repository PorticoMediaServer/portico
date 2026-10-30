package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"
)

func TestManualMetadataHTTPUnicodeBodyLimitsAndStrictInput(t *testing.T) {
	d, owner, member := tl6SupportFixture(t)
	item := tl6FixtureMovie(t, d, "private-library", "manual-item.mkv", "Automatic title", 2000)
	h := New(d)
	request := func(method string, raw []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/items/"+item.Public+"/metadata/manual", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	read := func() catalog.ManualMetadata {
		settleCompactCatalogue(t, d.DB)
		w := request("GET", nil, owner.AccessToken)
		if w.Code != 200 {
			t.Fatal("manual GET failed", w.Code)
		}
		var v catalog.ManualMetadata
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	title := strings.Repeat("😀", 300)
	description := strings.Repeat("😀", 20000)
	for _, escaped := range []bool{false, true} {
		v := read()
		var raw []byte
		if escaped {
			raw = []byte(`{"expectedRevision":"` + v.Revision + `","title":"` + strings.Repeat(`\ud83d\ude00`, 300) + `","description":"` + strings.Repeat(`\ud83d\ude00`, 20000) + `"}`)
		} else {
			raw, _ = json.Marshal(map[string]any{"expectedRevision": v.Revision, "title": title, "description": description})
		}
		if len(raw) <= 64<<10 || len(raw) > manualMetadataRequestBytes {
			t.Fatal("fixture failed to cross old decoder boundary", len(raw))
		}
		w := request("PATCH", raw, owner.AccessToken)
		if w.Code != 200 {
			t.Fatal("valid unicode PATCH rejected", escaped, w.Code)
		}
		saved := read()
		if saved.Title.Value != title || saved.Description.Value != description {
			t.Fatal("unicode text changed")
		}
	}
	before := read()
	prefix := `{"expectedRevision":"` + before.Revision + `",`
	invalid := [][]byte{[]byte(prefix + `"title":{"value":"object"}}`), []byte(prefix + `"extra":"unrecognized","title":"New"}`), []byte(prefix + `"title":"New"} {}`), []byte(prefix + `"title":"broken"`), []byte(`[]`), []byte(`null`), []byte(prefix + `"description":"` + strings.Repeat("A", 20001) + `"}`), append([]byte(prefix+`"title":"`), 255, '"', '}'), []byte(strings.Repeat(" ", manualMetadataRequestBytes+1) + prefix + `"title":"New"}`)}
	for i, raw := range invalid {
		w := request("PATCH", raw, owner.AccessToken)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatal("invalid input status", i, w.Code)
		}
		if read().Revision != before.Revision {
			t.Fatal("invalid input mutated metadata", i)
		}
	}
	for _, token := range []string{"", member.AccessToken} {
		for _, method := range []string{"GET", "PATCH"} {
			if w := request(method, []byte(prefix+`"title":"Denied"}`), token); w.Code != tl6RefusedStatus(token) {
				t.Fatal("non-owner not denied", method, w.Code)
			}
		}
	}
	w := request("PATCH", []byte(`{"expectedRevision":"`+strings.Repeat("0", 64)+`","title":"Stale"}`), owner.AccessToken)
	if w.Code != 409 {
		t.Fatal("stale revision not conflicted", w.Code)
	}
	// Invalid byte/argument cases cannot disclose the raw field content.
	if w = request("PATCH", []byte(prefix+`"unknown_PRIVATE":"PRIVATE"}`), owner.AccessToken); strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("raw invalid argument leaked")
	}
}

func TestManualMetadataHTTPCancelledOwnerRead(t *testing.T) {
	testtier.Media(t, "waits about 30 s of wall clock on a held connection")
	d, owner, _ := tl6SupportFixture(t)
	item := tl6FixtureMovie(t, d, "private-library", "cancelled.mkv", "Automatic", 2000)
	d.DB.SetMaxOpenConns(1)
	held, e := d.DB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/v1/items/"+item.Public+"/metadata/manual", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	w := httptest.NewRecorder()
	New(d).ServeHTTP(w, r)
	if w.Code == 200 || strings.Contains(w.Body.String(), "automaticValue") {
		t.Fatal("cancelled owner read published editable values")
	}
}

func TestManualMetadataHTTPAuthorizationCancellationAndFreshRevocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, stage := range []string{"initial", "final"} {
		for _, ending := range []string{"cancel", "deadline"} {
			t.Run(stage+"/"+ending, func(t *testing.T) {
				d, owner, _ := tl6SupportFixture(t)
				item := tl6FixtureMovie(t, d, "private-library", "cancelled-"+stage+ending+".mkv", "Automatic", 2000)
				d.DB.SetMaxOpenConns(1)
				var held *sql.Conn
				var e error
				if stage == "initial" {
					held, e = d.DB.Conn(context.Background())
					if e != nil {
						t.Fatal(e)
					}
				}
				defer func() {
					if held != nil {
						held.Close()
					}
				}()
				snapshotDone := make(chan struct{})
				h := d.manualMetadataHandler("GET", func(ctx context.Context, server, fence, item string, authorize func(*sql.Tx) error) (catalog.ManualMetadata, error) {
					v, e := d.Catalog.ManualMetadata(ctx, server, fence, item, authorize)
					if e != nil {
						return v, e
					}
					held, e = d.DB.Conn(ctx)
					close(snapshotDone)
					return v, e
				})
				ctx, cancel := context.WithCancel(context.Background())
				if ending == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
				}
				defer cancel()
				r := httptest.NewRequest("GET", "/v1/items/"+item.Public+"/metadata/manual", nil).WithContext(ctx)
				r.SetPathValue("id", item.Public)
				r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				waits := d.DB.Stats().WaitCount
				go func() { h.ServeHTTP(w, r); close(done) }()
				if stage == "final" {
					select {
					case <-snapshotDone:
					case <-time.After(time.Second):
						t.Fatal("real snapshot did not finish")
					}
				}
				until := time.Now().Add(time.Second)
				for d.DB.Stats().WaitCount == waits && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if d.DB.Stats().WaitCount == waits {
					t.Fatal("authorization never waited on real pool")
				}
				if ending == "cancel" {
					cancel()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("metadata authorization ignored cancelled context")
				}
				if w.Code == 200 || strings.Contains(w.Body.String(), "automaticValue") {
					t.Fatal("cancelled authorization disclosed metadata")
				}
			})
		}
	}
	t.Run("fresh-revocation", func(t *testing.T) {
		d, owner, _ := tl6SupportFixture(t)
		item := tl6FixtureMovie(t, d, "private-library", "revoked.mkv", "Automatic", 2000)
		h := d.manualMetadataHandler("GET", func(ctx context.Context, server, fence, item string, authorize func(*sql.Tx) error) (catalog.ManualMetadata, error) {
			v, e := d.Catalog.ManualMetadata(ctx, server, fence, item, authorize)
			if e != nil {
				return v, e
			}
			_, e = d.DB.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, identity.Digest(owner.AccessToken))
			return v, e
		})
		r := httptest.NewRequest("GET", "/v1/items/"+item.Public+"/metadata/manual", nil)
		r.SetPathValue("id", item.Public)
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 || strings.Contains(w.Body.String(), "automaticValue") {
			t.Fatal("revoked owner received snapshot", w.Code)
		}
	})
}

func TestManualMetadataTransactionAuthorizationPredicates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	changes := map[string]string{
		"revoked": `UPDATE authorization_session_families SET revoked=1`, "expired": `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z'`,
		"session-role": `UPDATE authorization_session_families SET role='member'`, "session-authority": `UPDATE authorization_session_families SET authority='hosted'`,
		"session-profile": `UPDATE authorization_session_families SET profile_id='different'`, "session-account": `UPDATE authorization_session_families SET account_id='different'`,
		"session-epoch": `UPDATE authorization_session_families SET epoch=2`, "account-profile": `UPDATE accounts SET profile_id='different'`, "account-epoch": `UPDATE accounts SET epoch=2`,
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			d, owner, _ := tl6SupportFixture(t)
			p, e := d.Identity.Authenticate(owner.AccessToken)
			if e != nil {
				t.Fatal(e)
			}
			if name == "session-authority" {
				if _, e = d.DB.Exec(change); e == nil {
					t.Fatal("retired authority stored")
				}
				p.Authority = "hosted"
				tx, e := d.DB.Begin()
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = manualMetadataAuthorize(context.Background(), tx, p); !errors.Is(e, identity.ErrForbidden) {
					t.Fatalf("retired authority admitted: %v", e)
				}
				return
			}
			if _, e = d.DB.Exec(change); e != nil {
				t.Fatal(e)
			}
			tx, e := d.DB.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if e = manualMetadataAuthorize(context.Background(), tx, p); !errors.Is(e, identity.ErrUnauthorized) {
				t.Fatal("changed authority predicate accepted", name, e)
			}
		})
	}
}
