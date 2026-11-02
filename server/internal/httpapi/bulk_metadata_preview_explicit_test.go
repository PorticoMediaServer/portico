package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// Walk J-12 (2 Nov): titles picked one by one are previewed in the request itself, never as a retained
// preparation whose whole-view basis any write anywhere retires. The preparation worker is held throughout,
// so a ready answer can only have come from the request; the owner check still applies.
func TestExplicitBulkMetadataPreviewAnswersInTheRequest(t *testing.T) {
	f := newBulkPreviewHTTPFixture(t)
	release := blockBulkPreviewWorker(t, f.store)
	defer release()
	alpha, beta := editorItemPublic(t, f.d.DB, "Alpha"), editorItemPublic(t, f.d.DB, "Beta")
	raw, err := json.Marshal(catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{
		{Kind: "item", ContentKind: "movie", ID: alpha, LibraryID: "private-library"},
		{Kind: "item", ContentKind: "movie", ID: beta, LibraryID: "private-library"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
	if w.Code != 200 || w.Header().Get("Location") != "" {
		t.Fatalf("explicit picks were not previewed in the request: %d %s", w.Code, clip(w.Body.String()))
	}
	var preview metadata.BulkMetadataPreview
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || preview.Total != 2 || len(preview.Targets) != 2 || len(preview.ViewerFence) != 64 {
		t.Fatalf("explicit preview %+v: %v", preview, err)
	}
	for _, target := range preview.Targets {
		if target.ExpectedRevision == "" || !strings.Contains(alpha+" "+beta, target.ID) {
			t.Fatalf("each picked target carries its own revision for the edit: %+v", target)
		}
	}
	if member := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.member, "application/json", raw); member.Code != 401 && member.Code != 403 {
		t.Fatalf("a member previewed owner metadata: %d", member.Code)
	}
}
