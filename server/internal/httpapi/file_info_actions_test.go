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

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func (f *fileInfoWindowsFixture) actionRequest(method, path string, body any) *httptest.ResponseRecorder {
	f.base.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.base.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.base.token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *fileInfoWindowsFixture) withAdministration() {
	f.base.d.Administration = administration.New(f.base.db)
	f.d.Administration = f.base.d.Administration
	f.h = New(f.base.d)
}

func (f *fileInfoWindowsFixture) addSecondFile(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "second.mkv")
	_, token := f.base.c.File(f.base.item.ID, path, 5400)
	f.base.c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'http' FROM catalog_assets WHERE token=?`, token)
	f.base.c.Drain()
	f.mint(t)
	files := f.lane(t, fileInfoLanePath(f.base.item.Public, "/file-info", f.opening), nil)
	if files.Code != http.StatusOK {
		t.Fatalf("files %d %s", files.Code, files.Body.String())
	}
	page := fileInfoBody[catalog.FileInfoFilesPage](t, files)
	for _, file := range page.Files {
		if file.ID == token {
			return token, file.Occurrence
		}
	}
	t.Fatalf("second file %s not in files page", token)
	return "", ""
}

func fileInfoActionBody(opening, occurrence string) map[string]any {
	return map[string]any{"fileInfo": map[string]any{"opening": opening, "occurrence": occurrence}}
}

func TestFileInfoVersionDeletePreviewBindsExactFileAndKeepsOldBodies(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	f.withAdministration()
	second, occurrence := f.addSecondFile(t)
	path := "/v1/admin/items/" + f.base.item.Public + "/files/" + second + "/delete/preview"

	w := f.actionRequest("POST", path, fileInfoActionBody(f.opening, occurrence))
	if w.Code != http.StatusOK {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	var preview administration.VersionDeletePreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.AssetID != second || preview.Confirmation == "" || len(preview.PreviewKey) != 64 {
		t.Fatalf("preview %+v", preview)
	}
	if preview.TotalFiles != 1 || len(preview.Targets) != 1 || len(preview.Targets[0].Files) != 1 || preview.Targets[0].Files[0].AssetID != second {
		t.Fatalf("preview target %+v", preview)
	}

	// The old bodyless preview remains valid; FileInfo is optional.
	if w = f.actionRequest("POST", path, nil); w.Code != http.StatusOK {
		t.Fatalf("bodyless preview %d %s", w.Code, w.Body.String())
	}

	// A valid occurrence for another file of the same title cannot stand in for
	// the file named by the route.
	other := "/v1/admin/items/" + f.base.item.Public + "/files/" + f.base.item.Token + "/delete/preview"
	if w = f.actionRequest("POST", other, fileInfoActionBody(f.opening, occurrence)); w.Code != http.StatusBadRequest || errorCode(t, w) != "invalid_request" {
		t.Fatalf("mismatched file: %d %s", w.Code, w.Body.String())
	}
}

func TestFileInfoSeparateRouteUsesExactSelectionAndRejectsParentPath(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	other := f.base.c.Movie(f.base.c.Handle("movies"), filepath.Join(t.TempDir(), "other.mkv"), "Other", 2020)
	f.base.c.Drain()
	second, occurrence := f.addSecondFile(t)
	route := "/v1/admin/items/" + f.base.item.Public + "/file-info/separate"

	// The same member named through another title's path is not a valid action.
	if w := f.actionRequest("POST", "/v1/admin/items/"+other.Public+"/file-info/separate", fileInfoActionBody(f.opening, occurrence)); w.Code != http.StatusBadRequest || errorCode(t, w) != "invalid_request" {
		t.Fatalf("parent/path mismatch: %d %s", w.Code, w.Body.String())
	}

	w := f.actionRequest("POST", route, fileInfoActionBody(f.opening, occurrence))
	if w.Code != http.StatusOK {
		t.Fatalf("separate %d %s", w.Code, w.Body.String())
	}
	var out fileInfoSeparateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ItemID == "" || out.ItemID == f.base.item.Public {
		t.Fatalf("separate item %q", out.ItemID)
	}
	var links int
	if err := f.base.db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, f.base.item.ID, second).Scan(&links); err != nil || links != 0 {
		t.Fatalf("old title kept the selected link: %d %v", links, err)
	}
	if err := f.base.db.QueryRow(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.public_id=pid_blob(?) AND l.asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, out.ItemID, second).Scan(&links); err != nil || links != 1 {
		t.Fatalf("new title link: %d %v", links, err)
	}
	// The other file stays with the original title.
	if err := f.base.db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, f.base.item.ID, f.base.item.Token).Scan(&links); err != nil || links != 1 {
		t.Fatalf("original file: %d %v", links, err)
	}
}

func TestFileInfoMergeRequiresTheExactSourceMember(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	into := f.base.c.Movie(f.base.c.Handle("movies"), filepath.Join(t.TempDir(), "into.mkv"), "Into", 2021)
	f.base.c.Drain()
	f.mint(t)
	files := f.lane(t, fileInfoLanePath(f.base.item.Public, "/file-info", f.opening), nil)
	if files.Code != http.StatusOK {
		t.Fatalf("files %d %s", files.Code, files.Body.String())
	}
	page := fileInfoBody[catalog.FileInfoFilesPage](t, files)
	if len(page.Files) != 1 {
		t.Fatalf("files %+v", page.Files)
	}
	occurrence := page.Files[0].Occurrence

	// A proof for the target title is not a proof for the source member.
	body := map[string]any{"fromId": into.Public, "intoId": f.base.item.Public, "fileInfo": map[string]any{"opening": f.opening, "occurrence": occurrence}}
	if w := f.actionRequest("POST", "/v1/admin/titles/merge", body); w.Code != http.StatusBadRequest || errorCode(t, w) != "invalid_request" {
		t.Fatalf("substituted merge proof: %d %s", w.Code, w.Body.String())
	}

	body = map[string]any{"fromId": f.base.item.Public, "intoId": into.Public, "fileInfo": map[string]any{"opening": f.opening, "occurrence": occurrence}}
	w := f.actionRequest("POST", "/v1/admin/titles/merge", body)
	if w.Code != http.StatusOK {
		t.Fatalf("merge %d %s", w.Code, w.Body.String())
	}
	var out mergeTitlesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ItemID != into.Public {
		t.Fatalf("merge item %q", out.ItemID)
	}
}

func TestFileInfoPreviewRejectsAReusedAssetProof(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	f.withAdministration()
	second, occurrence := f.addSecondFile(t)
	path := "/v1/admin/items/" + f.base.item.Public + "/files/" + second + "/delete/preview"

	var asset int64
	if err := f.base.db.QueryRow(`SELECT id FROM catalog_assets WHERE token=?`, second).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	// Move the exact asset to another title, as a rescan or association would.
	f.base.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, f.base.item.ID, asset)
	})
	other := f.base.c.Movie(f.base.c.Handle("movies"), filepath.Join(t.TempDir(), "other.mkv"), "Other", 2020)
	f.base.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, other.ID, asset, compactcatalog.Link{})
	})
	f.base.c.Drain()

	w := f.actionRequest("POST", path, fileInfoActionBody(f.opening, occurrence))
	if w.Code != http.StatusConflict || errorCode(t, w) != "stale_continuation" {
		t.Fatalf("reused asset: %d %s", w.Code, w.Body.String())
	}
}
