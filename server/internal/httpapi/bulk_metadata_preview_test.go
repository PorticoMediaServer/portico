package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// These exercise New(d)'s real registered contract after root's one-line
// registration and apigen integration. No alternate test-only route is installed.
func TestBulkMetadataPreviewHTTPMatchesOwnerGateAndRealValues(t *testing.T) {
	f := newBulkPreviewHTTPFixture(t)
	handler, owner, member, db := f.h, f.owner, f.member, f.d.DB
	a, b := editorItemPublic(t, db, "Alpha"), editorItemPublic(t, db, "Beta")
	body, _ := json.Marshal(catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: a, LibraryID: "private-library"}, {Kind: "item", ContentKind: "movie", ID: b, LibraryID: "private-library"}}})
	denied := editorRequest(t, handler, "POST", "/v1/metadata/bulk/preview", member, "application/json", body)
	if denied.Code != http.StatusForbidden || errorCode(t, denied) != "not_permitted" || strings.Contains(denied.Body.String(), a) {
		t.Fatalf("member preview disclosed metadata: %d %s", denied.Code, denied.Body.String())
	}
	response := editorRequest(t, handler, "POST", "/v1/metadata/bulk/preview", owner, "application/json", body)
	response = bulkPreviewHTTPReady(t, handler, owner, response)
	if response.Code != 200 {
		t.Fatalf("preview: %d %s", response.Code, response.Body.String())
	}
	var preview metadata.BulkMetadataPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Total != 2 || len(preview.Targets) != 2 || preview.Fields["year"].DistinctCount != 2 || preview.Fields["year"].State != "mixed" {
		t.Fatal(preview)
	}
	// Both crossing public paths must coexist in the real mux: the older
	// single-target preview still reaches its declared, scope-checked handler.
	single := editorRequest(t, handler, "GET", "/v1/metadata/item/"+a+"/preview", owner, "", nil)
	if single.Code != http.StatusOK {
		t.Fatal(single.Code, single.Body.String())
	}
	tl6AssertSpecResponse(t, "POST", "/v1/metadata/bulk/preview", response)
}

func TestBulkMetadataPreviewValidationUsesDeclaredClientErrors(t *testing.T) {
	d, owner, member := tl6SupportFixture(t)
	movie := tl6FixtureMovie(t, d, "private-library", "Preview.mkv", "Preview movie", 2020)
	fixtures := catalogtest.New(t, d.DB)
	library := fixtures.Library("preview-music", "Preview music", "music", t.TempDir())
	artist := fixtures.Artist(library, "Preview artist")
	album := fixtures.Album(artist, "Preview album", 2020)
	song := fixtures.Song(album, 1, "preview.flac", "Preview song")
	fixtures.Drain()
	d.Metadata = metadata.New(d.DB, "")
	installBulkPreviewTestStore(t, d.Catalog)
	handler := New(d)
	mixed, err := json.Marshal(catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{
		{Kind: "item", ContentKind: "movie", ID: movie.Public, LibraryID: "private-library"},
		{Kind: "item", ContentKind: "song", ID: song.Public, LibraryID: "preview-music"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"targets":[]}`, string(mixed)} {
		response := editorRequest(t, handler, "POST", "/v1/metadata/bulk/preview", owner.AccessToken, "application/json", []byte(body))
		response = bulkPreviewHTTPReady(t, handler, owner.AccessToken, response)
		if response.Code != http.StatusBadRequest || errorCode(t, response) != "invalid_request" {
			t.Fatalf("invalid selection: %d %s", response.Code, response.Body.String())
		}
		if err := apispec.ValidateErrorEnvelope(response.Code, response.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
		denied := editorRequest(t, handler, "POST", "/v1/metadata/bulk/preview", member.AccessToken, "application/json", []byte(body))
		if denied.Code != http.StatusForbidden || errorCode(t, denied) != "not_permitted" {
			t.Fatalf("invalid input bypassed owner gate: %d %s", denied.Code, denied.Body.String())
		}
	}
}

func TestBulkMetadataPreviewMissingDependencyDoesNotTurnMalformedInputIntoServerError(t *testing.T) {
	d, owner, member := tl6SupportFixture(t)
	handler := New(d) // The hostile-input fixture also omits the metadata service.
	for _, caller := range []struct {
		token  string
		status int
		code   string
	}{
		{owner.AccessToken, http.StatusBadRequest, "invalid_request"},
		{member.AccessToken, http.StatusForbidden, "not_permitted"},
		{"", http.StatusUnauthorized, "unauthorized"},
	} {
		response := editorRequest(t, handler, "POST", "/v1/metadata/bulk/preview", caller.token, "application/json", []byte(`{}`))
		if response.Code != caller.status || errorCode(t, response) != caller.code {
			t.Fatalf("missing dependency: %d %s", response.Code, response.Body.String())
		}
		if err := apispec.ValidateErrorEnvelope(response.Code, response.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
}
