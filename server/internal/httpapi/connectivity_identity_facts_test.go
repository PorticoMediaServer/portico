package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestConnectivityOwnerFactsUseLocalProtectedIdentityAndBoundListener(t *testing.T) {
	f := administrationFixtureNew(t)
	f.deps.LocalHTTPURL = "http://localhost:41823"
	f.handler = New(f.deps)
	read := func(token string) map[string]any {
		t.Helper()
		w := f.call("GET", "/v1/admin/connectivity/status", token, "")
		if w.Code != http.StatusOK {
			t.Fatal("status", w.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	current, err := f.deps.Console.Settings(context.Background(), operations.AllowServerScope)
	if err != nil || current.Effective.OwnAddressEnabled {
		t.Fatal("own-address default", err)
	}
	before := dbwork.ChangingCommits()
	out := read(f.owner)
	if out["identityCode"] != "JJWY-C8J8-J26F" || out["localUrl"] != "http://localhost:41823" {
		t.Fatal("missing actual protected/listener facts")
	}
	if dbwork.ChangingCommits() != before {
		t.Fatal("status read wrote state")
	}
	// Historical configuration IDs are never a derivation authority.
	if _, err := dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassMaintenance, `UPDATE configuration SET value='historical-not-an-identity' WHERE key='id'`); err != nil {
		t.Fatal(err)
	}
	if read(f.owner)["identityCode"] != "JJWY-C8J8-J26F" {
		t.Fatal("historical config changed identity code")
	}
	admin := read(f.admin)
	if _, exists := admin["identityCode"]; exists {
		t.Fatal("owner code disclosed to administrator")
	}
	if _, exists := admin["localUrl"]; exists {
		t.Fatal("owner listener fact disclosed to administrator")
	}
	public := f.call("GET", "/v1/networking/connection", "", "")
	var conn map[string]any
	if err := json.Unmarshal(public.Body.Bytes(), &conn); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"identityCode", "localUrl"} {
		if _, exists := conn[key]; exists {
			t.Fatal("owner fact entered public connection", key)
		}
	}
}

func TestConnectivityOwnerFactsOmitUnpublishedIdentityAndUnboundURL(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, err := dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassMaintenance, `DELETE FROM networking_claim_identity`); err != nil {
		t.Fatal(err)
	}
	w := f.call("GET", "/v1/admin/connectivity/status", f.owner, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"identityCode", "localUrl"} {
		if _, exists := out[key]; exists {
			t.Fatal("invented fact", key)
		}
	}
}

func TestConnectivityIdentityCodeOmitsCanonicalPorticoKind(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	if _, err := dbwork.ExecWrite(ctx, f.deps.DB, dbwork.ClassMaintenance, `UPDATE accounts SET password_hash=X''; UPDATE identity_state SET sign_in_kind='portico' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	read, err := dbwork.BeginSnapshot(ctx, f.deps.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	if code, err := connectivityIdentityCodeTx(ctx, read.Tx()); err != nil || code != "" {
		t.Fatal("Portico identity code disclosed", err)
	}
}
