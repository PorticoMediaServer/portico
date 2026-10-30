package httpapi

import (
	"bytes"
	"image"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// NEW-21: when an artwork URL carries both size= and w=, w= wins. Every
// artwork route serves through artworkWidth, so the helper is pinned for each
// route's URL shape; the items route is additionally covered over HTTP below
// (the metadata route already is, in TestArtworkImmutableWidthsAndReplacedVersion).
func TestArtworkWidthPrefersExplicitWidth(t *testing.T) {
	paths := []string{
		"/v1/items/edit-one/art/poster",
		"/v1/metadata/movie/edit-one/art/poster",
		"/v1/topshelf/art/edit-one",
	}
	cases := []struct {
		query string
		want  metadata.ArtworkSize
	}{
		{"size=thumbnail&w=800", metadata.ArtworkSmall},
		{"size=thumbnail&w=200", metadata.ArtworkSmall},
		{"size=thumbnail&w=10000", metadata.ArtworkLarge},
		{"size=thumbnail", metadata.ArtworkSmall},
		{"size=full", metadata.ArtworkLarge},
		{"", metadata.ArtworkLarge},
		{"w=800", metadata.ArtworkSmall},
		{"w=1920", metadata.ArtworkLarge},
		{"w=1921", metadata.ArtworkLarge},
		{"w=abc", -1},
		{"w=0", -1},
		{"w=-5", -1},
	}
	for _, path := range paths {
		for _, tc := range cases {
			r := httptest.NewRequest("GET", path+"?"+tc.query, nil)
			if got := artworkSize(r); got != tc.want {
				t.Fatalf("%s?%s: size=%d, want %d", path, tc.query, got, tc.want)
			}
		}
	}
}

// NEW-21 over HTTP: the items artwork route gives w= priority over size=.
func TestItemsArtworkWidthBeatsSize(t *testing.T) {
	h, owner, _, db := editorHTTPFixtureDB(t)
	editOne := editorItemPublic(t, db, "Alpha")
	state := editorState(t, h, owner, editOne)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 1600, 2400))
	if w := editorRequest(t, h, "POST", "/v1/metadata/item/"+editOne+"/art/poster/upload", owner, contentType, body); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	// The upload projects the poster onto the item: settle its publication.
	settleCompactCatalogue(t, db)
	for _, tc := range []struct {
		query, name string
		bucket      int
	}{
		{"?w=800&size=thumbnail", "explicit 800", 800},
		{"?w=1000&size=thumbnail", "explicit 1000 beats thumbnail", 1920},
	} {
		response := editorRequest(t, h, "GET", "/v1/items/"+editOne+"/art/poster"+tc.query, owner, "", nil)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", tc.name, response.Code, response.Body.String())
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
		if err != nil || max(cfg.Width, cfg.Height) != tc.bucket {
			t.Fatalf("%s: cfg=%+v err=%v", tc.name, cfg, err)
		}
	}
}

// WEB-MENU-03 (server part): the metadata editor's candidate preview honors
// the requested size instead of always serving one fixed rendition.
func TestCandidatePreviewHonorsSize(t *testing.T) {
	h, owner, _, db := editorHTTPFixtureDB(t)
	editOne := editorItemPublic(t, db, "Alpha")
	state := editorState(t, h, owner, editOne)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 1200, 1800))
	if w := editorRequest(t, h, "POST", "/v1/metadata/item/"+editOne+"/art/poster/upload", owner, contentType, body); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	// The upload projects the poster onto the item: settle its publication.
	settleCompactCatalogue(t, db)
	state = editorState(t, h, owner, editOne)
	var candidate string
	for _, c := range state.Artwork.Candidates {
		if c.Provider == "upload" {
			candidate = c.ID
		}
	}
	if candidate == "" {
		t.Fatalf("no upload candidate: %+v", state.Artwork.Candidates)
	}
	for _, tc := range []struct {
		query, name string
		limit       int
	}{
		{"?candidate=" + candidate + "&size=thumbnail", "thumbnail", 800},
		{"?candidate=" + candidate + "&w=800", "explicit 800", 800},
	} {
		response := editorRequest(t, h, "GET", "/v1/metadata/item/"+editOne+"/art/poster"+tc.query, owner, "", nil)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", tc.name, response.Code, response.Body.String())
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
		if err != nil || cfg.Width > tc.limit || cfg.Height > tc.limit {
			t.Fatalf("%s: cfg=%+v err=%v", tc.name, cfg, err)
		}
	}
	if response := editorRequest(t, h, "GET", "/v1/metadata/item/"+editOne+"/art/poster?candidate="+candidate+"&w=abc", owner, "", nil); response.Code != 400 {
		t.Fatalf("invalid width answered %d, want 400: %s", response.Code, response.Body.String())
	}
}
