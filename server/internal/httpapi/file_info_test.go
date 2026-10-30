package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetailFileInfoLazyAndPrivate(t *testing.T) {
	h, owner, member, db := editorHTTPFixtureDB(t)
	id := editorItemPublic(t, db, "Alpha")
	for _, token := range []string{owner, member} {
		page := editorRequest(t, h, "GET", "/v1/items/"+id+"/detail", token, "", nil)
		// This fixture member cannot read the private library. An authorized member
		// is covered by the catalogue test; this asserts refusal at the HTTP boundary.
		if token == member {
			if page.Code == 200 {
				t.Fatal("private page leaked")
			}
			continue
		}
		if page.Code != 200 {
			t.Fatalf("detail %d %s", page.Code, page.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(page.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["files"]; ok {
			t.Fatal("files remain on page")
		}
		if strings.Contains(string(raw["item"]), `"sources"`) {
			t.Fatal("technical sources remain on page")
		}
		if _, ok := raw["languages"]; !ok {
			t.Fatal("missing language summary")
		}
	}
	// File information is now a set of opening-bound lanes. A bare read has no
	// accepted Detail to bind to and refuses rather than serving an eager page.
	for _, token := range []string{owner, member} {
		response := editorRequest(t, h, "GET", "/v1/items/"+id+"/file-info", token, "", nil)
		want, code := 400, "invalid_request"
		if token == member {
			want, code = 404, "not_found"
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != want || body.Error.Code != code || strings.Contains(response.Body.String(), "Alpha.mkv") || strings.Contains(response.Body.String(), id) {
			t.Fatalf("bare file info: want %d/%s, got %d %s", want, code, response.Code, response.Body.String())
		}
	}
}

func TestMetadataMalformedRequestsAreClientErrors(t *testing.T) {
	h, owner, _ := editorHTTPFixture(t)
	for _, path := range []string{"/v1/metadata/bulk", "/v1/metadata/bulk/preview"} {
		for _, body := range []string{`{`, `{}`, `null`, `{"targets":"bad"}`, `{"targets":[]}`, `{"fields":{"year":{"value":"bad"}}}`} {
			response := editorRequest(t, h, "POST", path, owner, "application/json", []byte(body))
			if response.Code < 400 || response.Code >= 500 {
				t.Errorf("%s %s: %d %s", path, body, response.Code, response.Body.String())
			}
		}
	}
}

func TestBulkMetadataPreviewHostileQueryAndAbsentService(t *testing.T) {
	h, owner, _ := hostileFixture(t)
	for _, query := range hostileQueries {
		response := editorRequest(t, h, "POST", "/v1/metadata/bulk/preview"+query, owner, "application/json", []byte(`{"targets":[{"kind":"item","contentKind":"movie","id":"absent","libraryId":"private-library"}]}`))
		want := 400
		if query == "" {
			want = 503 // The fixture deliberately has no metadata subsystem.
		}
		if response.Code != want {
			t.Errorf("query %q: %d %s", query, response.Code, response.Body.String())
		}
	}
}
