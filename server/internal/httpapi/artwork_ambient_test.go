package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestArtworkAmbientExplicitSizeKeepsNormalWidthPriority(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  metadata.ArtworkSize
	}{
		{"size=ambient-v1", metadata.ArtworkAmbient},
		{"size=ambient-v1&w=1", metadata.ArtworkSmall},
		{"size=ambient-v1&w=64", metadata.ArtworkSmall},
		{"size=ambient-v1&w=800", metadata.ArtworkSmall},
		{"size=ambient-v1&w=900", metadata.ArtworkLarge},
		{"size=ambient-v1&w=bad", -1},
		{"size=ambient-v1&w=0", -1},
		{"size=ambient-v1&w=-1", -1},
		{"w=32", metadata.ArtworkSmall},
		{"size=thumbnail", metadata.ArtworkSmall},
		{"size=full", metadata.ArtworkLarge},
		{"", metadata.ArtworkLarge},
	} {
		if got := artworkSize(httptest.NewRequest("GET", "/?"+tc.query, nil)); got != tc.want {
			t.Fatalf("%s: %d != %d", tc.query, got, tc.want)
		}
	}
}

func TestArtworkAmbientHTTPAuthorityVersionsValidatorsAndRevocation(t *testing.T) {
	h, owner, member, db := editorHTTPFixtureDB(t)
	item := editorItemPublic(t, db, "Alpha")
	state := editorState(t, h, owner, item)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 1200, 1800))
	upload := editorRequest(t, h, "POST", "/v1/metadata/item/"+item+"/art/poster/upload", owner, contentType, body)
	if upload.Code != http.StatusOK {
		t.Fatal(upload.Code, upload.Body.String())
	}
	var saved metadata.RepairState
	if err := json.Unmarshal(upload.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	settleCompactCatalogue(t, db)
	fullPath := "/v1/items/" + item + "/art/poster"
	full := editorRequest(t, h, "GET", fullPath, owner, "", nil)
	if full.Code != 200 {
		t.Fatal(full.Code, full.Body.String())
	}
	version := selectedArtworkSource(t, db, item, "poster")
	itemPath := fullPath + "?v=" + version + "&size=ambient-v1"
	metaPath := "/v1/metadata/item/" + item + "/art/poster?v=" + version + "&size=ambient-v1"
	// Real HTTP requests prove bytes and conditional handling on the mounted
	// production handler, without touching the shared development runtime.
	server := httptest.NewServer(h)
	defer server.Close()
	request := func(path, token, tag string) (int, http.Header, []byte) {
		t.Helper()
		r, err := http.NewRequest("GET", server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if tag != "" {
			r.Header.Set("If-None-Match", tag)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, raw
	}
	var tag string
	var ambientBytes []byte
	for _, path := range []string{itemPath, metaPath} {
		code, header, raw := request(path, owner, full.Header().Get("ETag"))
		if code != 200 {
			t.Fatal(path, code, string(raw))
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "png" || cfg.Width != 42 || cfg.Height != 64 || len(raw) > 20000 {
			t.Fatal(cfg, format, len(raw), err)
		}
		// The ambient effect has no descriptor token, so it revalidates.
		if header.Get("Content-Type") != "image/png" || header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Cache-Control") != "private, no-cache" {
			t.Fatal(header)
		}
		if tag != "" && (tag != header.Get("ETag") || !bytes.Equal(ambientBytes, raw)) {
			t.Fatal("route aliases served different representations")
		}
		tag, ambientBytes = header.Get("ETag"), raw
		if tag == "" || tag == full.Header().Get("ETag") {
			t.Fatal("ambient reused foreground validator")
		}
		code, headers304, body304 := request(path, owner, tag)
		if code != 304 || len(body304) != 0 || headers304.Get("Cache-Control") != header.Get("Cache-Control") {
			t.Fatal(code, headers304, string(body304))
		}
		if code, _, raw = request(path, "", tag); code != 401 {
			t.Fatal("anonymous conditional request", code, string(raw))
		}
		if code, _, raw = request(path, member, tag); code == 200 || code == 304 {
			t.Fatal("private library disclosed", code)
		}
	}
	for _, query := range []string{"?w=400", "?w=800", "?w=1920", "?w=64", "?w=800&size=ambient-v1"} {
		code, header, raw := request(fullPath+query, owner, tag)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if code != 200 || err != nil || max(cfg.Width, cfg.Height) < 400 || header.Get("ETag") == tag {
			t.Fatal("foreground policy changed", query, code, cfg, err)
		}
	}
	code, header, raw := request(fullPath+"?size=ambient-v1", owner, "")
	if code != 200 || header.Get("Cache-Control") != "private, no-cache" || !bytes.Equal(raw, ambientBytes) {
		t.Fatal(code, header)
	}
	// Grant then revoke the same viewer while both response and access caches
	// are warm. Neither endpoint may answer 304 or serve art after revocation.
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries='["private-library"]',revision=revision+1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{itemPath, metaPath} {
		if code, _, raw := request(path, member, tag); code != 304 {
			t.Fatal("allowed member denied", code, string(raw))
		}
	}
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries='[]',revision=revision+1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{itemPath, metaPath} {
		for _, conditional := range []string{"", tag} {
			if code, header, raw := request(path, member, conditional); code == 200 || code == 304 || bytes.Equal(raw, ambientBytes) || header.Get("ETag") == tag {
				t.Fatal("revoked member obtained image/validator", path, code)
			}
		}
	}
	body, contentType = editorUpload(t, saved.Revision, editorPNG(t, 30, 40))
	if response := editorRequest(t, h, "POST", "/v1/metadata/item/"+item+"/art/poster/upload", owner, contentType, body); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	settleCompactCatalogue(t, db)
	for _, path := range []string{itemPath, metaPath} {
		code, header, raw := request(path, owner, tag)
		if code == 200 || code == 304 || header.Get("Cache-Control") != "no-store" || !bytes.Contains(raw, []byte("artwork_gone")) {
			t.Fatal("stale URL served", code, header, string(raw))
		}
	}
	code, newHeader, newRaw := request(fullPath+"?size=ambient-v1", owner, tag)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(newRaw))
	if code != 200 || err != nil || cfg.Width != 30 || cfg.Height != 40 || newHeader.Get("ETag") == tag {
		t.Fatal("small replacement enlarged or stale", code, cfg, err)
	}
}

func TestArtworkAmbientMissingPhysicalSourceCannotReturn304(t *testing.T) {
	d, owner, _ := tl6SupportFixture(t)
	item := tl6FixtureMovie(t, d, "private-library", "ambient-item.mkv", "Ambient", 2020)
	service := metadata.New(d.DB, "test-token")
	cache := t.TempDir()
	if err := service.SetArtworkDirectory(cache); err != nil {
		t.Fatal(err)
	}
	d.Metadata = service
	h := New(d)
	state := editorState(t, h, owner.AccessToken, item.Public)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 128, 192))
	if response := editorRequest(t, h, "POST", "/v1/metadata/item/"+item.Public+"/art/poster/upload", owner.AccessToken, contentType, body); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	settleCompactCatalogue(t, d.DB)
	full := editorRequest(t, h, "GET", "/v1/items/"+item.Public+"/art/poster", owner.AccessToken, "", nil)
	version := selectedArtworkSource(t, d.DB, item.Public, "poster")
	path := "/v1/items/" + item.Public + "/art/poster?v=" + version + "&size=ambient-v1"
	first := editorRequest(t, h, "GET", path, owner.AccessToken, "", nil)
	if first.Code != 200 || full.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	// The ambient file was made at arrival. A missing file is pending: it is
	// never rebuilt on request and never answered from a client validator.
	if err := os.Remove(filepath.Join(cache, strings.Trim(first.Header().Get("ETag"), `"`)+".img")); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{path, "/v1/metadata/item/" + item.Public + "/art/poster?v=" + version + "&size=ambient-v1"} {
		response := editorRequestWithHeader(t, h, "GET", route, owner.AccessToken, map[string]string{"If-None-Match": first.Header().Get("ETag")})
		if response.Code == 200 || response.Code == 304 || response.Header().Get("Retry-After") != "5" || response.Header().Get("Cache-Control") != "no-store" || !bytes.Contains(response.Body.Bytes(), []byte("artwork_pending")) {
			t.Fatal("missing exact source extended cache", route, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestArtworkAmbientContentRestrictionPrecedesWarmConditionalCache(t *testing.T) {
	h, owner, _, db := editorHTTPFixtureDB(t)
	item := editorItemPublic(t, db, "Alpha")
	state := editorState(t, h, owner, item)
	body, contentType := editorUpload(t, state.Revision, editorPNG(t, 128, 192))
	if response := editorRequest(t, h, "POST", "/v1/metadata/item/"+item+"/art/poster/upload", owner, contentType, body); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	settleCompactCatalogue(t, db)
	paths := []string{"/v1/items/" + item + "/art/poster?size=ambient-v1", "/v1/metadata/item/" + item + "/art/poster?size=ambient-v1"}
	first := editorRequest(t, h, "GET", paths[0], owner, "", nil)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	// The fixture is unrated. An owner choosing a restricted profile is held
	// to the same content visibility as viewers, including on warm 304 paths.
	if _, err := db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('profile',13,0)`); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		response := editorRequestWithHeader(t, h, "GET", path, owner, map[string]string{"If-None-Match": first.Header().Get("ETag")})
		if response.Code == 200 || response.Code == 304 || bytes.Equal(response.Body.Bytes(), first.Body.Bytes()) || response.Header().Get("ETag") == first.Header().Get("ETag") {
			t.Fatal("content-restricted profile obtained ambient art", path, response.Code, response.Body.String())
		}
	}
}
