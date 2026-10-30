package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// TestCrossLibraryHardlinkDeleteKeepsTheOtherLibrary proves that two library
// roots may reach one inode, that each library still gets its own reading item,
// and that deleting one item with its own path does not damage the other.
func TestCrossLibraryHardlinkDeleteKeepsTheOtherLibrary(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	h := New(r.d)
	token := r.f.owner.AccessToken

	bRoot := t.TempDir()
	bPath := filepath.Join(bRoot, "Hardlink alias.pdf")
	if err := os.Link(r.path, bPath); err != nil {
		t.Skipf("filesystem does not support hard links: %v", err)
	}
	original, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.Stat(bPath)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(r.path) == bRoot || !os.SameFile(original, alias) {
		t.Fatal("the two library roots do not reach one inode")
	}

	bLibrary, err := r.f.cat.Create("Hardlink alias reading", "book", bRoot)
	if err != nil {
		t.Fatal(err)
	}
	setCrossLibrarySettings(t, r.f, r.library, "cross-library-hardlink-a", true)
	setCrossLibrarySettings(t, r.f, bLibrary.ID, "cross-library-hardlink-b", false)
	readingScan(t, r.f, bLibrary.ID)

	var aItems, bItems int
	if err = r.f.db.QueryRow(`SELECT count(*) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=?`, r.library).Scan(&aItems); err != nil {
		t.Fatal(err)
	}
	if err = r.f.db.QueryRow(`SELECT count(*) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=?`, bLibrary.ID).Scan(&bItems); err != nil {
		t.Fatal(err)
	}
	if aItems != 1 || bItems != 1 {
		t.Fatalf("hardlinked libraries produced %d and %d reading items, want 1 and 1", aItems, bItems)
	}
	var bItem, bSource string
	if err = r.f.db.QueryRow(`SELECT pid(e.public_id),a.token FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_assets a ON a.id=p.asset_id WHERE cl.library_id=?`, bLibrary.ID).Scan(&bItem, &bSource); err != nil {
		t.Fatal(err)
	}
	if bItem == "" || bItem == r.item || bSource == "" {
		t.Fatalf("second library item %q source %q overlaps the first item %q", bItem, bSource, r.item)
	}

	previewResponse := readingHTTP(t, h, "POST", "/v1/items/"+r.item+"/delete/preview", token, "", nil)
	var previewEnvelope struct {
		Result administration.DeletePreview `json:"result"`
	}
	if previewResponse.Code != 200 || json.Unmarshal(previewResponse.Body.Bytes(), &previewEnvelope) != nil {
		t.Fatalf("delete preview: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	preview := previewEnvelope.Result
	if len(preview.Targets) != 1 || preview.Targets[0].ItemID != r.item || preview.Targets[0].LibraryID != r.library || !preview.Targets[0].AllowMediaDeletion || preview.Confirmation == "" || len(preview.Blocked) != 0 {
		t.Fatalf("delete preview did not bind the first library only: %+v", preview)
	}

	deleteResponse := readingHTTP(t, h, "POST", "/v1/items/"+r.item+"/delete", token, "", administration.DeleteRequest{
		DeleteFiles:      true,
		Confirmation:     preview.Confirmation,
		ExpectedRevision: preview.Revision,
		OperationID:      "cross-library-hardlink-delete",
	})
	var deletedEnvelope struct {
		Result administration.DeleteResult `json:"result"`
	}
	if deleteResponse.Code != 200 || json.Unmarshal(deleteResponse.Body.Bytes(), &deletedEnvelope) != nil {
		t.Fatalf("delete: %d %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	deleted := deletedEnvelope.Result
	if deleted.Removed != 1 || deleted.Failed != 0 || deleted.FilesRetained || len(deleted.Receipts) != 1 {
		t.Fatalf("delete result %+v", deleted)
	}
	if receipt := deleted.Receipts[0]; receipt.ItemID != r.item || !receipt.Removed || receipt.Code != "" {
		t.Fatalf("delete receipt %+v", receipt)
	}
	// Sharing one asset record is allowed; the requirement is that the second
	// library's path still names the original inode after the first title goes.
	remaining, err := os.Stat(bPath)
	if err != nil {
		t.Fatalf("second library path disappeared: %v", err)
	}
	if !os.SameFile(original, remaining) {
		t.Fatal("second library path no longer names the shared inode")
	}

	if err = compactcatalog.Drain(context.Background(), r.f.db); err != nil {
		t.Fatal(err)
	}
	var aAfter int
	if err = r.f.db.QueryRow(`SELECT count(*) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=?`, r.library).Scan(&aAfter); err != nil {
		t.Fatal(err)
	}
	if aAfter != 0 {
		t.Fatalf("first library still has %d reading items after delete", aAfter)
	}

	browseResponse := readingHTTP(t, h, "POST", "/v1/libraries/"+bLibrary.ID+"/browse", token, "", map[string]any{"pivot": "books", "limit": 10})
	var page catalog.BrowseResult
	if browseResponse.Code != 200 || json.Unmarshal(browseResponse.Body.Bytes(), &page) != nil {
		t.Fatalf("second library browse: %d %s", browseResponse.Code, browseResponse.Body.String())
	}
	if len(page.Entries) != 1 || page.Entries[0].ID != bItem || page.Entries[0].Removed {
		t.Fatalf("second library browse lost the shared-inode item: %+v", page.Entries)
	}

	detailResponse := readingHTTP(t, h, "GET", "/v1/items/"+bItem+"/detail", token, "", nil)
	if detailResponse.Code != 200 || !strings.Contains(detailResponse.Body.String(), bItem) {
		t.Fatalf("second library detail: %d %s", detailResponse.Code, detailResponse.Body.String())
	}

	sessionResponse := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{ItemID: bItem, SourceID: bSource})
	var manifest ReadingSessionResponse
	if sessionResponse.Code != 200 || json.Unmarshal(sessionResponse.Body.Bytes(), &manifest) != nil {
		t.Fatalf("second library session: %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	if manifest.ItemID != bItem || manifest.SourceID != bSource || manifest.SessionID == "" || manifest.Size <= 0 {
		t.Fatalf("second library session manifest %+v", manifest)
	}
	bytesRequest := httptest.NewRequest("GET", "/v1/reading/sessions/"+manifest.SessionID+"/source", nil)
	bytesRequest.Header.Set("Authorization", "Bearer "+token)
	bytesRequest.Header.Set("If-Match", `"`+manifest.Revision+`"`)
	bytesRequest.Header.Set("Range", "bytes=0-4")
	bytesResponse := httptest.NewRecorder()
	h.ServeHTTP(bytesResponse, bytesRequest)
	if bytesResponse.Code != 206 || bytesResponse.Body.String() != "%PDF-" || !strings.HasPrefix(bytesResponse.Header().Get("Content-Range"), "bytes 0-4/") {
		t.Fatalf("second library source range: %d %q %q", bytesResponse.Code, bytesResponse.Body.String(), bytesResponse.Header().Get("Content-Range"))
	}
}

// setCrossLibrarySettings keeps monitoring off for this explicit-scan test and
// grants the real owner deletion policy only to the library that is deleted.
func setCrossLibrarySettings(t *testing.T, f *v1Fixture, library, operation string, allowDeletion bool) {
	t.Helper()
	settingsPath := "/v1/admin/libraries/" + library + "/settings"
	response := readingHTTP(t, f.handler, "GET", settingsPath, f.owner.AccessToken, "", nil)
	var document administration.LibraryDocument
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &document) != nil {
		t.Fatalf("library settings read: %d %s", response.Code, response.Body.String())
	}
	document.Settings.MonitorChanges = false
	document.Settings.AllowMediaDeletion = allowDeletion
	response = readingHTTP(t, f.handler, "PUT", settingsPath, f.owner.AccessToken, "", administration.Change[administration.LibrarySettings]{
		ExpectedRevision: document.Revision,
		OperationID:      operation,
		Settings:         document.Settings,
	})
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &document) != nil || document.Settings.MonitorChanges || document.Settings.AllowMediaDeletion != allowDeletion {
		t.Fatalf("library settings write: %d %s", response.Code, response.Body.String())
	}
}
