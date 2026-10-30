package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestReadingOwnerMetadataUsesExistingRouteAndCurrentLibraryAuthority(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	read := c.Library("read", "Read", "book", "/read")
	hidden := c.Library("hidden-read", "Hidden", "comic", "/hidden")
	a := c.Entity(compactcatalog.Entity{Library: read, Kind: compactcatalog.Ebook, Key: "ebook:one", Title: "One"}, nil)
	b := c.Entity(compactcatalog.Entity{Library: hidden, Kind: compactcatalog.Comic, Key: "comic:two", Title: "Two"}, nil)
	c.Drain()
	f.deps.Metadata = metadata.New(f.deps.DB, "test-token")
	f.handler = New(f.deps)
	get := func(token, id string) metadata.RepairState {
		t.Helper()
		w := f.call("GET", "/v1/metadata/item/"+id, token, "")
		if w.Code != http.StatusOK {
			t.Fatal("metadata read", w.Code, w.Body.String())
		}
		var out metadata.RepairState
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := get(f.owner, a.Public)
	second := get(f.owner, b.Public)
	for _, token := range []string{"", f.member} {
		w := f.call("GET", "/v1/metadata/item/"+a.Public, token, "")
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("nonadministrator read", w.Code)
		}
	}
	value := "A & B"
	body, _ := json.Marshal(metadata.BulkEdit{OperationID: "read-owner-http", Targets: []metadata.BulkTarget{{Kind: "item", ID: a.Public, ExpectedRevision: first.Revision}, {Kind: "item", ID: b.Public, ExpectedRevision: second.Revision}}, Fields: map[string]metadata.RepairFieldEdit{"author": {Value: &value}}})
	w := f.call("POST", "/v1/metadata/bulk", f.owner, string(body))
	if w.Code != 200 {
		t.Fatal("owner bulk", w.Code, w.Body.String())
	}
	if get(f.owner, a.Public).Snapshot.Fields["author"].Value != value || get(f.owner, b.Public).Snapshot.Fields["author"].Value != value {
		t.Fatal("bulk did not publish exact scalar values")
	}
	if _, err := dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassSecurityFence, `UPDATE direct_memberships SET allowed_libraries='["read"]',revision=revision+1 WHERE account_id='admin'`); err != nil {
		t.Fatal(err)
	}
	admin, err := f.deps.Identity.Issue("admin", "admin-profile", "local", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical administrators bypass membership library lists. An explicit
	// selected-profile restriction is the real narrower content boundary.
	get(admin.AccessToken, b.Public)
	if _, err = dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassSecurityFence, `UPDATE direct_profiles SET allowed_libraries='["read"]',revision=revision+1 WHERE id='admin-profile'`); err != nil {
		t.Fatal(err)
	}
	admin, err = f.deps.Identity.Issue("admin", "admin-profile", "local", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	allowed := get(admin.AccessToken, a.Public)
	w = f.call("GET", "/v1/metadata/item/"+b.Public, admin.AccessToken, "")
	if w.Code != 404 {
		t.Fatal("hidden library metadata disclosed", w.Code)
	}
	other := "Scoped owner edit"
	second = get(f.owner, b.Public)
	body, _ = json.Marshal(metadata.BulkEdit{OperationID: "read-scoped-http", Targets: []metadata.BulkTarget{{Kind: "item", ID: a.Public, ExpectedRevision: allowed.Revision}, {Kind: "item", ID: b.Public, ExpectedRevision: second.Revision}}, Fields: map[string]metadata.RepairFieldEdit{"author": {Value: &other}}})
	w = f.call("POST", "/v1/metadata/bulk", admin.AccessToken, string(body))
	if w.Code != 200 {
		t.Fatal("scoped bulk", w.Code, w.Body.String())
	}
	var receipt metadata.BulkReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Updated != 1 || receipt.Failed != 1 {
		t.Fatal("per-target authority not preserved", receipt, err)
	}
	if get(f.owner, b.Public).Snapshot.Fields["author"].Value != value {
		t.Fatal("hidden target changed")
	}
	// A mixed receipt can replay without retrying its denied target.
	w = f.call("POST", "/v1/metadata/bulk", admin.AccessToken, string(body))
	if w.Code != 200 {
		t.Fatal("mixed receipt replay", w.Code)
	}
	p, err := f.deps.Identity.Authenticate(admin.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := f.deps.Metadata.RepairState(context.Background(), metadata.RepairTarget{Kind: "item", ID: a.Public}, func(tx *sql.Tx) error {
		if err := f.deps.manualMetadataAuthorize(context.Background(), tx, p); err != nil {
			return err
		}
		return f.deps.readingMetadataTargetAuthorize(context.Background(), tx, p, metadata.RepairTarget{Kind: "item", ID: a.Public})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassSecurityFence, `UPDATE direct_profiles SET allowed_libraries='["hidden-read"]',revision=revision+1 WHERE id='admin-profile'`); err != nil {
		t.Fatal(err)
	}
	if err = f.deps.readingMetadataResponseAuthorize(context.Background(), p, captured); err == nil {
		t.Fatal("late library narrowing retained a read response")
	}
	admin, err = f.deps.Identity.Issue("admin", "admin-profile", "local", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	w = f.call("POST", "/v1/metadata/bulk", admin.AccessToken, string(body))
	if w.Code != 404 {
		t.Fatal("revoked successful target replayed", w.Code)
	}
}

// MOB-055: a Reading selection's owner edit starts from the shared bulk preview,
// which admits ebook and comic targets with their own Reading schema.
func TestReadingBulkMetadataPreviewAdmitsReadingTargets(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	read := c.Library("read", "Read", "book", "/read")
	a := c.Entity(compactcatalog.Entity{Library: read, Kind: compactcatalog.Ebook, Key: "ebook:one", Title: "One"}, nil)
	b := c.Entity(compactcatalog.Entity{Library: read, Kind: compactcatalog.Ebook, Key: "ebook:two", Title: "Two"}, nil)
	c.Drain()
	f.deps.Metadata = metadata.New(f.deps.DB, "test-token")
	// The production server installs the memory-only preparation scheduler.
	// Without it this isolated fixture returns capacity refusal before Reading
	// target admission; no rate-limit bucket is involved.
	store := installBulkPreviewTestStore(t, f.deps.Catalog)
	releaseWorker := blockBulkPreviewWorker(t, store)
	f.handler = New(f.deps)
	body, _ := json.Marshal(map[string]any{"targets": []map[string]string{
		{"kind": "item", "contentKind": "ebook", "id": a.Public, "libraryId": "read"},
		{"kind": "item", "contentKind": "ebook", "id": b.Public, "libraryId": "read"},
	}})
	w := f.call("POST", "/v1/metadata/bulk/preview", f.owner, string(body))
	if w.Code != 202 {
		t.Fatalf("cold Reading preview did not enter preparation: %d %s", w.Code, w.Body.String())
	}
	releaseWorker()
	w = bulkPreviewHTTPReady(t, f.handler, f.owner, w)
	var preview metadata.BulkMetadataPreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.ContentKind != "ebook" || preview.Total != 2 {
		t.Fatalf("Reading bulk preview %d %s", w.Code, w.Body.String())
	}
	fields := map[string]bool{}
	for _, spec := range preview.Schema {
		fields[spec.Field] = true
	}
	if !fields["author"] || !fields["series"] || !fields["publicationType"] {
		t.Fatalf("Reading bulk schema %+v", preview.Schema)
	}
	if w = f.call("POST", "/v1/metadata/bulk/preview", f.member, string(body)); w.Code != 401 && w.Code != 403 {
		t.Fatal("member previewed owner metadata", w.Code)
	}
}
