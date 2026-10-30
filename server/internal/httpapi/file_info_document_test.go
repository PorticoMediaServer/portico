package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestFileInfoDetailOpeningAndPreparingBeforeConditional(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	path := "/v1/items/" + f.base.item.Public + "/detail?related=none"
	ready := f.request("GET", path, nil)
	if ready.Code != 200 {
		t.Fatalf("Detail %d %s", ready.Code, ready.Body.String())
	}
	detail := fileInfoBody[struct {
		FileInfoOpening string                  `json:"fileInfoOpening"`
		Scope           catalog.DetailScope     `json:"scope"`
		Revision        catalog.ContentRevision `json:"revision"`
	}](t, ready)
	if !fileInfoTextToken(detail.FileInfoOpening, fileInfoOpeningText) {
		t.Fatal("ready Detail omitted opening")
	}
	page := f.lane(t, fileInfoLanePath(f.base.item.Public, "/file-info", detail.FileInfoOpening), nil)
	if page.Code != 200 {
		t.Fatalf("lane %d %s", page.Code, page.Body.String())
	}
	lane := fileInfoBody[catalog.FileInfoFilesPage](t, page)
	if lane.Detail.Scope != detail.Scope || lane.Detail.Revision != detail.Revision {
		t.Fatal("lane did not echo original Detail", lane.Detail, detail)
	}
	tag := ready.Header().Get("ETag")
	if tag == "" {
		t.Fatal("ready Detail omitted ETag")
	}
	if again := f.request("GET", path, map[string]string{"If-None-Match": tag}); again.Code != 304 {
		t.Fatalf("ready conditional %d %s", again.Code, again.Body.String())
	}
	f.base.c.Exec(`UPDATE catalog_entities SET info_source_blockers=1 WHERE id=?`, f.base.item.ID)
	pending := f.request("GET", path, map[string]string{"If-None-Match": tag})
	if pending.Code != 202 || pending.Header().Get("ETag") != "" {
		t.Fatalf("304 beat preparation: %d %s", pending.Code, pending.Body.String())
	}
	prep := fileInfoBody[map[string]any](t, pending)
	if _, exists := prep["detail"]; exists {
		t.Fatal("opening preparation invented original Detail")
	}
	location := pending.Header().Get("Location")
	if !strings.HasPrefix(location, "/v1/file-info-queries/open.") {
		t.Fatal(location)
	}
	f.base.c.Exec(`UPDATE catalog_entities SET info_source_blockers=0 WHERE id=?`, f.base.item.ID)
	resumed := f.lane(t, location, nil)
	if resumed.Code != http.StatusOK || !strings.Contains(resumed.Body.String(), `"fileInfoOpening"`) {
		t.Fatalf("opening poll did not resume original Detail %d %s", resumed.Code, resumed.Body.String())
	}
}
