package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"testing"
	"time"
)

func mutationHTTP(t *testing.T, f preferenceFixture, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if body == nil {
		raw = nil
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func TestUserMutationPreferenceReceiptOverHTTP(t *testing.T) {
	f := preferenceHTTP(t)
	key := fmt.Sprintf("p251_%013d_%s", time.Now().UnixMilli(), strings.Repeat("a", 32))
	body := map[string]any{"scope": "profile-server", "deviceClass": "tablet", "expectedRevision": 1, "idempotencyKey": key, "values": map[string]any{"reading.theme": "sepia"}}
	for i := 0; i < 2; i++ {
		w := mutationHTTP(t, f, "PATCH", "/v1/preferences", key, body)
		if w.Code != 200 {
			t.Fatalf("attempt%d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := mutationHTTP(t, f, "GET", "/v1/user-mutations/"+key, "", nil)
	if w.Code != 200 {
		t.Fatalf("lookup %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "values") || strings.Contains(w.Body.String(), "sepia") || strings.Contains(w.Body.String(), "family") || strings.Contains(w.Body.String(), "digest") {
		t.Fatal("receipt exposed state instead of compact facts", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"operation":"patch_preferences"`) {
		t.Fatal(w.Body.String())
	}
	body["values"] = map[string]any{"reading.theme": "dark"}
	w = mutationHTTP(t, f, "PATCH", "/v1/preferences", key, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatalf("changed replay %d %s", w.Code, w.Body.String())
	}
}
func TestUserMutationExpiredKeysNeverReachSettings(t *testing.T) {
	f := preferenceHTTP(t)
	key := fmt.Sprintf("p251_%013d_%s", time.Now().Add(-31*24*time.Hour).UnixMilli(), strings.Repeat("b", 32))
	body := map[string]any{"scope": "profile-server", "deviceClass": "tablet", "expectedRevision": 1, "idempotencyKey": key, "values": map[string]any{"reading.theme": "sepia"}}
	w := mutationHTTP(t, f, "PATCH", "/v1/preferences", key, body)
	if w.Code != 410 || !strings.Contains(w.Body.String(), "receipt_expired") {
		t.Fatalf("expired mutation %d %s", w.Code, w.Body.String())
	}
}
func TestUserMutationRegistryPublishesExactCohort(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range registry.Definitions() {
		if !userMutationRoutes[d.ID] {
			continue
		}
		seen[d.ID] = true
		found := false
		for _, h := range d.Headers {
			found = found || h == "Idempotency-Key"
		}
		if !found {
			t.Errorf("%s missing Idempotency-Key", d.ID)
		}
	}
	for id := range userMutationRoutes {
		if !seen[id] {
			t.Errorf("unregistered protocol route %s", id)
		}
	}
}

func TestUserMutationReservedBodyKeyRequiresMatchingHeader(t *testing.T) {
	f := preferenceHTTP(t)
	key := fmt.Sprintf("p251_%013d_%s", time.Now().UnixMilli(), strings.Repeat("c", 32))
	body := map[string]any{"scope": "profile-server", "deviceClass": "tablet", "expectedRevision": 1, "idempotencyKey": key, "values": map[string]any{"reading.theme": "sepia"}}
	for _, header := range []string{"", "legacy-key"} {
		w := mutationHTTP(t, f, "PATCH", "/v1/preferences", header, body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
			t.Fatalf("header %q: %d %s", header, w.Code, w.Body.String())
		}
	}
}

func userMutationReceiptRestrictionCheck(t *testing.T) {
	f := preferenceHTTP(t)
	p, err := f.ident.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := catalogtest.New(t, f.db)
	library := fixtures.Library("private-receipt", "Private", "movie", "/private-receipt")
	hidden := fixtures.Movie(library, "/private-receipt/hidden.mkv", "Private receipt title", 2020)
	fixtures.Drain()
	key := fmt.Sprintf("p251_%013d_%s", time.Now().UnixMilli(), strings.Repeat("d", 32))
	err = dbwork.WithWriteTx(dbwork.WithDurableUser(context.Background()), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		family, e := f.ident.SessionFamilyTx(context.Background(), tx, p)
		if e != nil {
			return e
		}
		b := usermutation.Binding{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, FamilyID: family.ID}
		envelope := usermutation.Envelope{Key: key, Operation: "put_items_id_personal_state", Digest: strings.Repeat("a", 64)}
		if _, e = usermutation.Check(context.Background(), tx, b, envelope, time.Now().UnixMilli()); e != nil {
			return e
		}
		return usermutation.Save(context.Background(), tx, b, envelope, usermutation.Result{Status: "applied", TargetID: hidden.Public, Revision: 1}, time.Now().UnixMilli())
	})
	if err != nil {
		t.Fatal(err)
	}
	w := mutationHTTP(t, f, "GET", "/v1/user-mutations/"+key, "", nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), hidden.Public) {
		t.Fatalf("hidden receipt: %d %s", w.Code, w.Body.String())
	}
	// The same account in another family cannot read the old family's result.
	other, err := f.ident.Issue(p.AccountID, p.ProfileID, p.Authority, p.Role, p.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	otherFixture := f
	otherFixture.token = other.AccessToken
	w = mutationHTTP(t, otherFixture, "GET", "/v1/user-mutations/"+key, "", nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), hidden.Public) {
		t.Fatalf("foreign family: %d %s", w.Code, w.Body.String())
	}
	if _, err = dbwork.ExecWrite(context.Background(), f.db, dbwork.ClassSecurityFence, `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash); err != nil {
		t.Fatal(err)
	}
	w = mutationHTTP(t, f, "GET", "/v1/user-mutations/"+key, "", nil)
	if w.Code != 401 || strings.Contains(w.Body.String(), hidden.Public) {
		t.Fatalf("revoked family: %d %s", w.Code, w.Body.String())
	}
}
func TestUserMutationReceiptChecksCurrentVisibilityAndFamily(t *testing.T) {
	userMutationReceiptRestrictionCheck(t)
}
