package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestRefreshMetadataRereadsLocalTitlesAndSelections(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	id := b.find(db.KindBook, "Nineteen")
	place := `{"format":"epub","progression":0.4,"cfi":"epubcfi(/6/2!/4/2/1:0)","fingerprint":"0123456789abcdef"}`
	status(t, b.s, "PUT", "/v1/items/"+id+"/state", token, map[string]any{"position": place, "favorite": true})
	if err := os.WriteFile(filepath.Join(b.books, "Orwell", "metadata.opf"), []byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/">
		<metadata><dc:title>Revised title</dc:title><dc:creator>New Author</dc:creator><dc:description>New description</dc:description></metadata></package>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var answer MetadataRefreshed
	if w := call(t, b.s, "POST", "/v1/admin/items/"+id+"/metadata/refresh", token, nil, &answer); w.Code != 200 || answer.Matched {
		t.Fatalf("file metadata refresh: %d %s", w.Code, w.Body)
	}
	item := get[Item](t, b.s, token, "/v1/items/"+id)
	if item.Title != "Revised title" || item.Summary != "New description" || item.State.Position != place || !item.State.Favorite {
		t.Fatalf("refresh did not reread metadata or kept no personal state: %+v", item)
	}

	comic := b.find(db.KindIssue, "Saga 001")
	zipFile(t, filepath.Join(b.comics, "Saga", "Saga 001.cbz"), [2]string{"001.png", pngPicture(t, 40, 60)},
		[2]string{"ComicInfo.xml", `<ComicInfo><Title>A new story</Title><Writer>A new writer</Writer><Summary>New comic description</Summary></ComicInfo>`})
	var job Job
	if w := call(t, b.s, "POST", "/v1/jobs", token, JobStart{Kind: "refresh_items", Target: JobTarget{Items: []string{comic}}}, &job); w.Code != 202 {
		t.Fatalf("selection refresh: %d %s", w.Code, w.Body)
	}
	if done := waitForJob(t, b.s, token, job.ID); done.State != db.JobDone || done.Progress == nil || done.Progress.Done != 1 {
		t.Fatalf("selection refresh ended: %+v", done)
	}
	if item := get[Item](t, b.s, token, "/v1/items/"+comic); item.Title != "A new story" || item.Summary != "New comic description" {
		t.Fatalf("selection did not reread local metadata: %+v", item)
	}

	// A lost source must not turn Refresh into a successful no-op or erase previous facts.
	if err := os.RemoveAll(filepath.Join(b.books, "Orwell")); err != nil {
		t.Fatal(err)
	}
	if w := call(t, b.s, "POST", "/v1/admin/items/"+id+"/metadata/refresh", token, nil, nil); w.Code != 502 {
		t.Fatalf("missing source: %d %s", w.Code, w.Body)
	}
	if w := call(t, b.s, "POST", "/v1/jobs", token, JobStart{Kind: "refresh_items", Target: JobTarget{Items: []string{id}}}, &job); w.Code != 202 {
		t.Fatalf("selection refresh of missing source: %d %s", w.Code, w.Body)
	}
	if done := waitForJob(t, b.s, token, job.ID); done.State != db.JobFailed || !strings.Contains(done.Error, "could not be read from its files") {
		t.Fatalf("unreadable selection was not reported: %+v", done)
	}
	if detail, err := b.s.db.Item(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, id); err != nil || detail.Title != "Revised title" {
		t.Fatalf("missing source lost the old metadata: %q, %v", detail.Title, err)
	}
}
