package api

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyCacheWaitsForOpenDownloadFiles(t *testing.T) {
	rig := newDownloadRig(t)
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	if code != 201 {
		t.Fatal(code, dl)
	}
	rig.waitReady(rig.owner, dl.ID)
	item := get[DownloadedItem](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID+"/items/"+rig.film)
	grant, name, _ := strings.Cut(strings.TrimPrefix(item.Files[0].URL, "/v1/media/"), "/")
	file, err := rig.s.downloads.Open(context.Background(), grant, name, true)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var subtitleURL string
	for _, sub := range item.Subtitles {
		if sub.Source != "sidecar" && sub.URL != "" {
			subtitleURL = sub.URL
			break
		}
	}
	if subtitleURL == "" {
		t.Fatal("the prepared copy has no extracted subtitle")
	}
	grant, name, _ = strings.Cut(strings.TrimPrefix(subtitleURL, "/v1/media/"), "/")
	subtitle, err := rig.s.downloads.Open(context.Background(), grant, name, true)
	if err != nil {
		t.Fatal(err)
	}
	defer subtitle.Close()
	var preview StorageCacheReport
	if w := call(t, rig.s, "POST", "/v1/admin/storage/cache/empty", rig.owner, nil, &preview); w.Code != 200 || preview.Bytes != 0 {
		t.Fatal(w.Code, w.Body)
	}
	if raw, err := io.ReadAll(file); err != nil || len(raw) == 0 {
		t.Fatal("Empty cache interrupted the download", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if preview = get[StorageCacheReport](t, rig.s, rig.owner, "/v1/admin/storage/cache"); preview.Bytes != 0 {
		t.Fatal("the open subtitle lost its copy", preview)
	}
	if raw, err := io.ReadAll(subtitle); err != nil || len(raw) == 0 {
		t.Fatal("Empty cache interrupted subtitle transfer", err)
	}
	if err := subtitle.Close(); err != nil {
		t.Fatal(err)
	}
	if preview = get[StorageCacheReport](t, rig.s, rig.owner, "/v1/admin/storage/cache"); preview.Bytes != 0 {
		t.Fatal("the download request still needs its closed copy", preview)
	}
	if w := call(t, rig.s, "DELETE", "/v1/downloads/"+dl.ID, rig.owner, nil, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	if preview = get[StorageCacheReport](t, rig.s, rig.owner, "/v1/admin/storage/cache"); preview.Bytes == 0 {
		t.Fatal("an unreferenced closed copy could not be cleared")
	}
	if w := call(t, rig.s, "POST", "/v1/admin/storage/cache/empty", rig.owner, nil, &preview); w.Code != 200 || preview.Bytes == 0 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(rig.folder, "film.mkv")); err != nil {
		t.Fatal("cache removal touched the original", err)
	}
}

func TestEmptyCacheKeepsAReadyUnfetchedDownload(t *testing.T) {
	rig := newDownloadRig(t)
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	if code != 201 {
		t.Fatal(code, dl)
	}
	rig.waitReady(rig.owner, dl.ID)
	// No media or subtitle bytes have been fetched; the request still needs them.
	var cleared StorageCacheReport
	if w := call(t, rig.s, "POST", "/v1/admin/storage/cache/empty", rig.owner, nil, &cleared); w.Code != 200 || cleared.Bytes != 0 {
		t.Fatal("Empty cache took a person's prepared download", w.Code, w.Body)
	}
	item := get[DownloadedItem](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID+"/items/"+rig.film)
	if len(item.Files) == 0 {
		t.Fatal("the unfetched copy expired", item)
	}
	grant, name, _ := strings.Cut(strings.TrimPrefix(item.Files[0].URL, "/v1/media/"), "/")
	file, err := rig.s.downloads.Open(context.Background(), grant, name, true)
	if err != nil {
		t.Fatal("the first fetch no longer works", err)
	}
	defer file.Close()
	if raw, err := io.ReadAll(file); err != nil || len(raw) == 0 {
		t.Fatal("prepared bytes lost", err)
	}
}
