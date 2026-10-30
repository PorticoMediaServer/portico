package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestMetadataAdministratorEditsAndLiveDemotion(t *testing.T) {
	d, _, member := tl6SupportFixture(t)
	installBulkPreviewTestStore(t, d.Catalog)
	movie := tl6FixtureMovie(t, d, "private-library", "Admin.mkv", "Admin title", 2020)
	if _, err := d.DB.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	admin, err := d.Identity.Issue("member", "member-profile", "local", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	d.Metadata = metadata.New(d.DB, "test-token")
	h := New(d)
	raw, _ := json.Marshal(catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: movie.Public, LibraryID: "private-library"}}})
	response := editorRequest(t, h, "POST", "/v1/metadata/bulk/preview", admin.AccessToken, "application/json", raw)
	response = bulkPreviewHTTPReady(t, h, admin.AccessToken, response)
	if response.Code != 200 {
		t.Fatalf("admin preview %d %s", response.Code, response.Body.String())
	}
	var preview metadata.BulkMetadataPreview
	if err = json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	year := "2024"
	body, _ := json.Marshal(metadata.BulkEdit{OperationID: "admin-edit", Targets: []metadata.BulkTarget{{Kind: "item", ID: movie.Public, ExpectedRevision: preview.Targets[0].ExpectedRevision}}, Fields: map[string]metadata.RepairFieldEdit{"year": {Value: &year}}})
	edited := editorRequest(t, h, "POST", "/v1/metadata/bulk", admin.AccessToken, "application/json", body)
	if edited.Code != 200 {
		t.Fatalf("admin bulk %d %s", edited.Code, edited.Body.String())
	}
	var receipt metadata.BulkReceipt
	if err = json.Unmarshal(edited.Body.Bytes(), &receipt); err != nil || receipt.Updated != 1 {
		t.Fatalf("admin outcome %+v %v", receipt, err)
	}
	// The same account's narrower member session may never borrow its new tier.
	denied := editorRequest(t, h, "POST", "/v1/metadata/bulk/preview", member.AccessToken, "application/json", raw)
	if denied.Code < 400 || denied.Code >= 500 {
		t.Fatalf("member session borrowed tier: %d %s", denied.Code, denied.Body.String())
	}
	// File information is opening-bound now: a bare read refuses rather than
	// serving an eager page to an administrator or a member.
	files := editorRequest(t, h, "GET", "/v1/items/"+movie.Public+"/file-info", admin.AccessToken, "", nil)
	if files.Code != 400 {
		t.Fatalf("bare lazy info %d %s", files.Code, files.Body.String())
	}
	principal, err := d.Identity.Authenticate(admin.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithWriteTx(context.Background(), d.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE direct_memberships SET role='member' WHERE account_id='member'`); err != nil {
			return err
		}
		return d.manualMetadataAuthorize(context.Background(), tx, principal)
	})
	if !errors.Is(err, identity.ErrForbidden) && !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("live demotion not fenced: %v", err)
	}
}
