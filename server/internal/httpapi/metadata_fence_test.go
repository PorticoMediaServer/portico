package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// J-2 (walk register, 2 Nov): the web opens a title's editor from its detail page only while
// the editing document carries the detail's viewer fence. Both are the one item fence, on
// the read and on every edit's answer, so Edit metadata from a detail page opens.
func TestTitleDetailAndMetadataEditorShareOneViewerFence(t *testing.T) {
	h, owner, _, db := editorHTTPFixtureDB(t)
	item := editorItemPublic(t, db, "Alpha")
	w := editorRequest(t, h, "GET", "/v1/items/"+item+"/detail", owner, "", nil)
	if w.Code != 200 {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	var detail struct {
		Scope struct {
			ViewerFence string `json:"viewerFence"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Scope.ViewerFence) != 64 {
		t.Fatalf("detail fence %q: %v", detail.Scope.ViewerFence, err)
	}
	state := editorState(t, h, owner, item)
	if state.ViewerFence != detail.Scope.ViewerFence {
		t.Fatalf("editor fence %q, detail fence %q", state.ViewerFence, detail.Scope.ViewerFence)
	}
	rating := "PG-13"
	raw, _ := json.Marshal(metadata.RepairCommand{Action: "edit", ExpectedRevision: state.Revision, Fields: map[string]metadata.RepairFieldEdit{"contentRating": {Value: &rating}}})
	w = editorRequest(t, h, "POST", "/v1/metadata/item/"+item, owner, "application/json", raw)
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	var saved metadata.RepairState
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ViewerFence != detail.Scope.ViewerFence {
		t.Fatalf("edit answer fence %q, detail fence %q", saved.ViewerFence, detail.Scope.ViewerFence)
	}
}
