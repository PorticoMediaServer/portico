package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestTopShelfEpisodeUsesSizedShowPoster(t *testing.T) {
	d, owner, _ := tl6SupportFixture(t)
	c := catalogtest.New(t, d.DB)
	library := c.Handle("private-library")
	show := c.Show(library, "Show", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/private/SOURCE-PATH/Episode.mkv")
	c.Drain()
	d.Catalog = catalog.New(d.DB)
	d.Metadata = metadata.New(d.DB, "test-token")
	if err := d.Metadata.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	h := New(d)
	state := editorRequest(t, h, "GET", "/v1/metadata/show/"+show.Public, owner.AccessToken, "", nil)
	if state.Code != http.StatusOK {
		t.Fatal(state.Code, state.Body.String())
	}
	var saved metadata.RepairState
	if err := json.Unmarshal(state.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	body, contentType := editorUpload(t, saved.Revision, editorPNG(t, 800, 1200))
	upload := editorRequest(t, h, "POST", "/v1/metadata/show/"+show.Public+"/art/poster/upload", owner.AccessToken, contentType, body)
	if upload.Code != http.StatusOK {
		t.Fatal(upload.Code, upload.Body.String())
	}
	f, _, err := d.topShelfArtwork(context.Background(), episode.Public, metadata.ArtworkSmall)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || max(cfg.Width, cfg.Height) != 800 {
		t.Fatalf("episode shelf did not use a sized show poster: %+v %v", cfg, err)
	}
	entry := topShelfEntry(catalog.ContentEntry{ID: episode.Public, Kind: "episode"}, "grant")
	if !bytes.Contains([]byte(entry.ImageURL), []byte("&w=400")) {
		t.Fatal(entry.ImageURL)
	}
}
