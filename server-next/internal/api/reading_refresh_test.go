package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
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
	if done := waitForJob(t, b.s, token, job.ID); done.State != db.JobFailed || done.Error != selectionFailureSummary {
		t.Fatalf("unreadable selection was not reported: %+v", done)
	}
	if detail, err := b.s.db.Item(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, id); err != nil || detail.Title != "Revised title" {
		t.Fatalf("missing source lost the old metadata: %q, %v", detail.Title, err)
	}
}

// One unreadable local title must not stop the next title, and taking up the same job
// after the file is restored must clear that title's earlier failure.
func TestLocalRefreshJobKeepsAndClearsTitleFailures(t *testing.T) {
	b := newBookShelf(t)
	token, ctx := b.owner.AccessToken, context.Background()
	book, comic := b.find(db.KindBook, "Nineteen"), b.find(db.KindIssue, "Saga 001")
	if err := os.RemoveAll(filepath.Join(b.books, "Orwell")); err != nil {
		t.Fatal(err)
	}
	zipFile(t, filepath.Join(b.comics, "Saga", "Saga 001.cbz"), [2]string{"001.png", pngPicture(t, 40, 60)},
		[2]string{"ComicInfo.xml", `<ComicInfo><Title>Still refreshed</Title></ComicInfo>`})
	var job Job
	if w := call(t, b.s, "POST", "/v1/jobs", token, JobStart{Kind: jobRefreshItems, Target: JobTarget{Items: []string{book, comic}}}, &job); w.Code != 202 {
		t.Fatalf("start local refresh: %d %s", w.Code, w.Body)
	}
	job = waitForJob(t, b.s, token, job.ID)
	if job.State != db.JobFailed || job.Error != selectionFailureSummary || job.Progress == nil || job.Progress.Done != 2 {
		t.Fatalf("partial local refresh: %+v", job)
	}
	path := "/v1/jobs/" + job.ID + "/failures"
	failed := get[Page[JobItemFailure]](t, b.s, token, path)
	if len(failed.Items) != 1 || failed.Items[0].ItemID != book || failed.Items[0].Error.Code != "metadata_file_unavailable" ||
		failed.Items[0].Error.Message != "This title's metadata could not be read from its files. Check that the files are available and try again." {
		t.Fatalf("local title failure: %+v", failed)
	}
	if it := get[Item](t, b.s, token, "/v1/items/"+comic); it.Title != "Still refreshed" {
		t.Fatalf("unreadable book stopped the comic refresh: %+v", it)
	}
	stored, err := b.s.db.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := b.s.db.RefreshTarget(ctx, comic)
	if err != nil {
		t.Fatal(err)
	}
	epubFile(t, filepath.Join(b.books, "Orwell", "Nineteen.epub"), chapterOne)
	if err := os.WriteFile(filepath.Join(b.books, "Orwell", "metadata.opf"), []byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/">
		<metadata><dc:title>Available again</dc:title></metadata></package>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var req jobs.Request
	if err := json.Unmarshal([]byte(stored.Payload), &req); err != nil {
		t.Fatal(err)
	}
	req.UserID = stored.OwnerID
	if err := b.s.refreshItems(ctx, jobs.Work{Job: stored, Request: req, Progress: func(int64, int64) {}}); err != nil {
		t.Fatal(err)
	}
	if page := get[Page[JobItemFailure]](t, b.s, token, path); len(page.Items) != 0 {
		t.Fatalf("successful local reread kept its failure: %+v", page)
	}
	if it := get[Item](t, b.s, token, "/v1/items/"+book); it.Title != "Available again" {
		t.Fatalf("restored book was not reread: %+v", it)
	}
	if after, _, err := b.s.db.RefreshTarget(ctx, comic); err != nil || after.RefreshedAt != before.RefreshedAt {
		t.Fatalf("resume reread the already successful comic: %+v %v", after, err)
	}
	if done := get[Job](t, b.s, token, "/v1/jobs/"+job.ID); done.State != db.JobDone || done.Error != "" {
		t.Fatalf("successful local replay kept a failed summary: %+v", done)
	}
}
