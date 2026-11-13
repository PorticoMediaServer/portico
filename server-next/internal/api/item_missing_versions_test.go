package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/compat"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func TestItemKeepsMissingVersionFactsWithoutOfferingMissingPlayback(t *testing.T) {
	files := []string{"Heat (1995)/Heat (1995) {edition-First}.mkv", "Heat (1995)/Heat (1995) {edition-Other}.mkv"}
	sh := newShelf(t, files...)
	var page ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("wanted one film: %+v", page.Items)
	}
	id := page.Items[0].ID
	var before Item
	sh.do("GET", "/v1/items/"+id, nil, &before, 200)
	if len(before.Versions) != 2 {
		t.Fatalf("wanted two versions: %+v", before.Versions)
	}
	old := map[string]Media{}
	for _, v := range before.Versions {
		old[v.ID] = v
	}
	var recorded ItemFiles
	sh.do("GET", "/v1/items/"+id+"/files", nil, &recorded, 200)
	missing := map[string]bool{}
	claims, err := sh.s.tokens.CheckAccess(sh.member, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	who := db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}
	for removed, file := range files {
		if err := os.Remove(filepath.Join(sh.root, file)); err != nil {
			t.Fatal(err)
		}
		for _, v := range recorded.Versions {
			for _, part := range v.Files {
				if part.Path == filepath.Join(sh.root, file) {
					missing[v.ID] = true
				}
			}
		}
		libraries, err := sh.s.db.AllLibraries(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (&scan.Scanner{DB: sh.s.db, Prober: stillProber{}}).Scan(context.Background(), libraries[0]); err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{sh.owner, sh.member} {
			var item Item
			response := call(t, sh.s, "GET", "/v1/items/"+id, token, nil, &item)
			if response.Code != 200 || len(item.Versions) != 2 {
				t.Fatalf("missing versions vanished: %d %s", response.Code, response.Body)
			}
			available := 0
			for _, version := range item.Versions {
				if version.Available {
					available++
				}
				was := old[version.ID]
				if version.ID != was.ID || version.Container != was.Container || version.Size != was.Size || version.DurationMs != was.DurationMs || version.Available == missing[version.ID] {
					t.Fatalf("missing version facts/state changed: %+v, was %+v", version, was)
				}
				if len(version.Parts) != 1 || len(version.Parts[0].Streams) != 1 || version.Parts[0].Streams[0].Codec != "h264" {
					t.Fatalf("missing version lost its recorded streams: %+v", version)
				}
			}
			if item.Available == nil || *item.Available != (removed == 0) || available != 1-removed || strings.Contains(response.Body.String(), sh.root) || strings.Contains(response.Body.String(), `"path"`) {
				t.Fatalf("wrong missing state or a server path in ordinary item: %s", response.Body)
			}
		}
		playable, err := sh.s.db.PlaybackItem(context.Background(), who, id)
		if err != nil || len(playable.Versions) != 1-removed {
			t.Fatalf("playback offered a missing version: %+v, %v", playable.Versions, err)
		}
	}
}

func TestCompatMediaSourcesSkipMissingVersions(t *testing.T) {
	s := newTestServer(t)
	out := map[string]any{}
	item := Item{ID: "film", Versions: []Media{{ID: "missing", Available: false, Parts: []Part{}}, {ID: "playable", Available: true, Parts: []Part{}}}}
	s.compatSources(httptest.NewRequest("GET", "/", nil), item, out)
	sources := out["MediaSources"].([]map[string]any)
	if len(sources) != 1 || sources[0]["Id"] != compat.ID("playable") {
		t.Fatalf("missing version offered as a media source: %+v", sources)
	}
}
