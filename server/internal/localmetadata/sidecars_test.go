package localmetadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

var errMissing = errors.New("missing")

func sidecarPNG(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	if e := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 8, 8))); e != nil {
		t.Fatal(e)
	}
	return raw.Bytes()
}

// fakeSidecarCalls counts reads per path.
func TestReadSidecarsShowSeasonAndRootGuard(t *testing.T) {
	png := sidecarPNG(t)
	present := map[string]bool{
		"/root/Show/poster.jpg":                          true,
		"/root/Show/fanart.jpg":                          true,
		"/root/Show/logo.png":                            true,
		"/root/Show/Season 1/season01.jpg":               true,
		"/root/Show/Season 1/season-specials-poster.jpg": true,
		"/root/Show2/poster.jpg":                         true,
		"/root/logo.png":                                 true,
	}
	calls := map[string]int{}
	service, e := New(t.TempDir(), "", nil)
	if e != nil {
		t.Fatal(e)
	}
	service.readSmall = func(_ context.Context, _ string, path string, _ int64) ([]byte, error) {
		calls[path]++
		if present[path] {
			return png, nil
		}
		return nil, errMissing
	}
	got := service.ReadSidecars(context.Background(), "lib", "tv", "/root", "/root/Show/Season 1/ep.mkv", 1)
	for _, key := range []string{"show/poster", "show/backdrop", "show/logo", "season/1/poster"} {
		if got[key] == "" {
			t.Fatalf("missing %s: %v", key, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("unexpected keys: %v", got)
	}
	// Nothing above the library root is ever read.
	for path := range calls {
		if !strings.HasPrefix(path, "/root/") {
			t.Fatalf("read above root: %s", path)
		}
	}
	// A second scan shares the cached reads.
	before := len(calls)
	service.ReadSidecars(context.Background(), "lib", "tv", "/root", "/root/Show/Season 1/other.mkv", 1)
	if len(calls) != before+2 {
		t.Fatalf("cache missed: %d vs %d reads", len(calls), before)
	}
	// Movies read logos from their own folder only.
	movie := service.ReadSidecars(context.Background(), "lib", "movie", "/root", "/root/Film/film.mkv", -1)
	if len(movie) != 0 {
		t.Fatalf("movie without logo: %v", movie)
	}
	// A show filed directly under the root never inherits the root's files.
	flat := service.ReadSidecars(context.Background(), "lib", "tv", "/root", "/root/Show2/ep.mkv", -1)
	if flat["show/poster"] == "" || flat["show/logo"] != "" {
		t.Fatalf("root files leaked into a show: %v", flat)
	}
}

// An image inside a season folder belongs to that season. It is never the
// show's, whichever episode happens to be scanned last.
func TestSeasonFolderImagesAreNotTheShows(t *testing.T) {
	images := map[string][]byte{}
	sized := func(n int) []byte {
		var raw bytes.Buffer
		if e := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, n, n))); e != nil {
			t.Fatal(e)
		}
		return raw.Bytes()
	}
	service, e := New(t.TempDir(), "", nil)
	if e != nil {
		t.Fatal(e)
	}
	service.readSmall = func(_ context.Context, _ string, path string, _ int64) ([]byte, error) {
		if raw, ok := images[path]; ok {
			return raw, nil
		}
		return nil, errMissing
	}
	read := func(path string, season int) map[string]string {
		service.sidecars = nil
		return service.ReadSidecars(context.Background(), "lib", "tv", "/root", path, season)
	}

	images["/root/Show/Season 1/poster.jpg"] = sized(8)
	images["/root/Show/Season 1/fanart.jpg"] = sized(9)
	images["/root/Show/Season 1/logo.png"] = sized(10)
	got := read("/root/Show/Season 1/ep.mkv", 1)
	if got["show/poster"] != "" || got["show/backdrop"] != "" || got["show/logo"] != "" {
		t.Fatalf("season folder images were given to the show: %v", got)
	}
	season := got["season/1/poster"]
	if season == "" {
		t.Fatalf("season folder poster was not the season's: %v", got)
	}

	images["/root/Show/poster.jpg"] = sized(11)
	got = read("/root/Show/Season 1/ep.mkv", 1)
	if got["show/poster"] == "" || got["show/poster"] == season || got["season/1/poster"] != season {
		t.Fatalf("show and season posters were not kept apart: %v", got)
	}

	// The season-poster names in the show folder, including seasonNN-poster.
	delete(images, "/root/Show/Season 1/poster.jpg")
	images["/root/Show/season01-poster.jpg"] = sized(12)
	got = read("/root/Show/Season 1/ep.mkv", 1)
	if got["season/1/poster"] == "" || got["season/1/poster"] == got["show/poster"] {
		t.Fatalf("season01-poster.jpg not recognised: %v", got)
	}
	images["/root/Show/Specials/folder.jpg"] = sized(13)
	got = read("/root/Show/Specials/special.mkv", 0)
	if got["season/0/poster"] == "" || got["show/poster"] != storedKey(t, service, images["/root/Show/poster.jpg"]) {
		t.Fatalf("specials folder image misattributed: %v", got)
	}

	// A show whose episodes sit directly in its folder still uses that folder.
	images["/root/Flat/poster.jpg"] = sized(14)
	if got = read("/root/Flat/Flat - S01E01.mkv", 1); got["show/poster"] == "" || got["season/1/poster"] != "" {
		t.Fatalf("flat show folder poster lost: %v", got)
	}
}

func storedKey(t *testing.T, service *Service, raw []byte) string {
	t.Helper()
	key, e := service.store(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	return key
}

// When the scan has already listed a directory, a sidecar it did not see there
// is not asked for; a directory it has no listing of is read as before.
func TestSidecarsNotListedByTheScanAreNotRead(t *testing.T) {
	png := sidecarPNG(t)
	service, e := New(t.TempDir(), "", nil)
	if e != nil {
		t.Fatal(e)
	}
	calls := map[string]int{}
	service.readSmall = func(_ context.Context, _ string, path string, _ int64) ([]byte, error) {
		calls[path]++
		if path == "/root/Show/poster.jpg" {
			return png, nil
		}
		return nil, errMissing
	}
	listed := WithListing(context.Background(), func(path string) (present, known bool) {
		switch {
		case strings.HasPrefix(path, "/root/Show/Season 1/"):
			return false, true
		case strings.HasPrefix(path, "/root/Show/"):
			return path == "/root/Show/poster.jpg", true
		}
		return false, false
	})
	got := service.ReadSidecars(listed, "lib", "tv", "/root", "/root/Show/Season 1/ep.mkv", 1)
	if got["show/poster"] == "" || len(calls) != 1 || calls["/root/Show/poster.jpg"] != 1 {
		t.Fatalf("listed directories: %v after reads %v", got, calls)
	}
	// No listing for this show: every candidate is still asked for.
	calls = map[string]int{}
	service.ReadSidecars(listed, "lib", "tv", "/root", "/root/Other/Season 1/ep.mkv", 1)
	if len(calls) < 10 {
		t.Fatalf("an unlisted directory was not read: %v", calls)
	}
}
