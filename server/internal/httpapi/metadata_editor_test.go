package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"strings"
	"testing"
)

func editorHTTPFixture(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	h, owner, member, _ := editorHTTPFixtureDB(t)
	return h, owner, member
}

// editorHTTPFixtureDB is editorHTTPFixture with the database attached, so
// tests that change catalogue rows mid-test (an artwork upload selects and
// projects the item's poster, which queues its own publication) can settle
// the projector between the upload and the read that expects it.
func editorHTTPFixtureDB(t *testing.T) (http.Handler, string, string, *sql.DB) {
	t.Helper()
	d, owner, member := tl6SupportFixture(t)
	tl6FixtureMovie(t, d, "private-library", "Alpha.mkv", "Alpha", 2020)
	tl6FixtureMovie(t, d, "private-library", "Beta.mkv", "Beta", 2021)
	service := metadata.New(d.DB, "test-token")
	if e := service.SetArtworkDirectory(t.TempDir()); e != nil {
		t.Fatal(e)
	}
	d.Metadata = service
	return New(d), owner.AccessToken, member.AccessToken, d.DB
}

func editorItemPublic(t *testing.T, db *sql.DB, title string) string {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE title=?`, title).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return catalogtest.New(t, db).Public(id)
}

func editorRequest(t *testing.T, h http.Handler, method, path, token, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// editorRequestWithHeader is editorRequest with extra request headers, for the
// conditional-request tests.
func editorRequestWithHeader(t *testing.T, h http.Handler, method, path, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(nil))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func editorState(t *testing.T, h http.Handler, token, id string) metadata.RepairState {
	t.Helper()
	w := editorRequest(t, h, "GET", "/v1/metadata/item/"+id, token, "", nil)
	if w.Code != 200 {
		t.Fatalf("read editor state: %d %s", w.Code, w.Body.String())
	}
	tl6AssertSpecResponse(t, "GET", "/v1/metadata/{kind}/{id}", w)
	var state metadata.RepairState
	if e := json.Unmarshal(w.Body.Bytes(), &state); e != nil {
		t.Fatal(e)
	}
	return state
}

func TestMetadataEditorRoutesEndToEnd(t *testing.T) {
	h, owner, member, db := editorHTTPFixtureDB(t)
	editOne, editTwo := editorItemPublic(t, db, "Alpha"), editorItemPublic(t, db, "Beta")
	state := editorState(t, h, owner, editOne)
	if len(state.Schema) == 0 || len(state.ArtworkRoles) == 0 {
		t.Fatal("editor read publishes no registry")
	}
	other := editorState(t, h, owner, editTwo)
	rating := "PG-13"
	edit := metadata.BulkEdit{
		OperationID: "http-bulk",
		Targets: []metadata.BulkTarget{
			{Kind: "item", ID: editOne, ExpectedRevision: state.Revision},
			{Kind: "item", ID: editTwo, ExpectedRevision: strings.Repeat("0", 64)},
		},
		Fields: map[string]metadata.RepairFieldEdit{"contentRating": {Value: &rating}},
	}
	raw, _ := json.Marshal(edit)
	w := editorRequest(t, h, "POST", "/v1/metadata/bulk", owner, "application/json", raw)
	if w.Code != 200 {
		t.Fatalf("bulk: %d %s", w.Code, w.Body.String())
	}
	var receipt metadata.BulkReceipt
	if e := json.Unmarshal(w.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	if receipt.Updated != 1 || receipt.Failed != 1 || receipt.Results[1].Code != "metadata_conflict" {
		t.Fatalf("bulk receipt %+v", receipt)
	}
	// A partial failure is still a 200 with a per-target report, so a client never
	// has to guess which half of a batch landed.
	if editorState(t, h, owner, editOne).Snapshot.Fields["contentRating"].Value != rating {
		t.Fatal("bulk edit did not reach the item")
	}
	if editorState(t, h, owner, editTwo).Snapshot.Fields["contentRating"].Value != "" {
		t.Fatal("stale target was edited anyway")
	}
	_ = other
	for _, token := range []string{"", member} {
		if got := editorRequest(t, h, "POST", "/v1/metadata/bulk", token, "application/json", raw); got.Code != tl6RefusedStatus(token) {
			t.Fatal("non-owner bulk", got.Code)
		}
	}

	// Upload, then withdraw, over the real multipart route.
	state = editorState(t, h, owner, editOne)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 24, 36))
	w = editorRequest(t, h, "POST", "/v1/metadata/item/"+editOne+"/art/poster/upload", owner, contentType, body)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &state); e != nil {
		t.Fatal(e)
	}
	if len(state.Snapshot.Artwork) != 1 || !state.Snapshot.Artwork[0].Locked {
		t.Fatalf("upload not selected: %+v", state.Snapshot.Artwork)
	}
	candidate := state.Snapshot.Artwork[0].CandidateID
	bad, contentType := editorUpload(t, state.Revision, []byte("still not an image"))
	if got := editorRequest(t, h, "POST", "/v1/metadata/item/"+editOne+"/art/poster/upload", owner, contentType, bad); got.Code != 415 {
		t.Fatalf("non-image upload: %d %s", got.Code, got.Body.String())
	}
	for _, token := range []string{"", member} {
		body, contentType := editorUpload(t, state.Revision, editorPNG(t, 24, 36))
		if got := editorRequest(t, h, "POST", "/v1/metadata/item/"+editOne+"/art/poster/upload", token, contentType, body); got.Code != tl6RefusedStatus(token) {
			t.Fatal("non-owner upload", got.Code)
		}
	}
	path := "/v1/metadata/item/" + editOne + "/art/poster/upload/" + candidate + "?expectedRevision=" + state.Revision
	w = editorRequest(t, h, "DELETE", path, owner, "", nil)
	if w.Code != 200 {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &state); e != nil {
		t.Fatal(e)
	}
	if len(state.Snapshot.Artwork) != 0 {
		t.Fatalf("withdrawn upload retained: %+v", state.Snapshot.Artwork)
	}
	if got := editorRequest(t, h, "DELETE", path, member, "", nil); got.Code != 403 {
		t.Fatal("non-owner withdraw", got.Code)
	}
}

func editorPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{G: 220, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func editorUpload(t *testing.T, revision string, image []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("expectedRevision", revision); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "poster.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(image); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), form.FormDataContentType()
}
