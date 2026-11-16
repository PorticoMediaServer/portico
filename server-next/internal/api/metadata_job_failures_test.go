package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
)

// Failures beyond the old 20-title note remain readable after restart, and retrying those
// titles leaves the already saved titles alone, even when saving changes the view's filter.
func TestBulkMetadataFailuresArePagedAndRetryOnlyThoseTitles(t *testing.T) {
	s, owner, _ := seededWith(t, 300, 1)
	ctx := context.Background()
	library := get[Libraries](t, s, owner, "/v1/libraries").Items[0].ID
	films := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=200").Items
	if len(films) < 26 {
		t.Fatal("fixture needs 26 films")
	}
	var genres []string
	for n := range 64 {
		genres = append(genres, fmt.Sprintf("Genre %02d", n))
	}
	year := 2001
	for n, film := range films[:26] {
		doc, err := s.db.MetadataDocument(ctx, film.ID)
		if err != nil {
			t.Fatal(err)
		}
		edit := db.MetadataEdit{Year: &year}
		if n < 25 {
			edit.Genres = &genres
		}
		if _, err := s.db.EditMetadata(ctx, film.ID, doc.Revision, edit); err != nil {
			t.Fatal(err)
		}
	}
	var job Job
	start := JobStart{Kind: jobEditItems, Target: JobTarget{Filter: "library=" + library + "&kind=movie&year=2001"},
		Params: JSON(`{"year":2002,"add":{"genres":["One more"]}}`)}
	call(t, s, "POST", "/v1/jobs", owner, start, &job)
	job = waitForJob(t, s, owner, job.ID)
	if job.State != db.JobFailed || job.Error != selectionFailureSummary {
		t.Fatalf("partial edit: %+v", job)
	}
	var failed []string
	for cursor := ""; ; {
		page := get[Page[JobItemFailure]](t, s, owner, "/v1/jobs/"+job.ID+"/failures?limit=7&cursor="+cursor)
		for _, f := range page.Items {
			if f.Error.Code != "metadata_list_full" || f.Title == "" {
				t.Fatalf("failure: %+v", f)
			}
			failed = append(failed, f.ItemID)
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if len(failed) != 25 {
		t.Fatalf("%d failures, want all 25", len(failed))
	}
	if saved := get[Item](t, s, owner, "/v1/items/"+films[25].ID); saved.Year != 2002 {
		t.Fatalf("the successful title was not saved: %+v", saved)
	}
	historyPath := "/v1/admin/items/" + films[25].ID + "/metadata/history"
	before := get[Page[MetadataHistoryEntry]](t, s, owner, historyPath)
	database := filepath.Join(s.dataDir, "main.db")
	stopServer(s)
	closeDB(s.db)
	s = openTestServer(t, database)
	owner = onMain(t, s, signIn(t, s, 7)).AccessToken
	if kept := get[Page[JobItemFailure]](t, s, owner, "/v1/jobs/"+job.ID+"/failures?limit=200"); len(kept.Items) != 25 {
		t.Fatalf("restart lost failures: %+v", kept)
	}
	start.Target = JobTarget{Items: failed}
	start.Params = JSON(`{"year":2002,"add":{"genres":["One more"]},"remove":{"genres":["Genre 00"]}}`)
	call(t, s, "POST", "/v1/jobs", owner, start, &job)
	if job = waitForJob(t, s, owner, job.ID); job.State != db.JobDone {
		t.Fatalf("failed-only retry: %+v", job)
	}
	for _, id := range failed {
		if item := get[Item](t, s, owner, "/v1/items/"+id); item.Year != 2002 {
			t.Fatalf("retry did not save %s", id)
		}
	}
	if after := get[Page[MetadataHistoryEntry]](t, s, owner, historyPath); len(after.Items) != len(before.Items) || after.Items[0].ID != before.Items[0].ID {
		t.Fatal("retry saved the already successful title again")
	}
}

type jobFailureProvider struct {
	metadata.Provider
	id             string
	refuse, outage atomic.Bool
	timeout        atomic.Bool
	failed, others atomic.Int64
}

func (p *jobFailureProvider) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	if ref.ID == p.id {
		p.failed.Add(1)
	} else {
		p.others.Add(1)
	}
	if p.timeout.Load() {
		return metadata.Record{}, fmt.Errorf("/private/timed-out-provider: %w", context.DeadlineExceeded)
	}
	if p.outage.Load() {
		return metadata.Record{}, metadata.StatusError{Provider: "tmdb", Status: 503, Path: "/private/provider-response"}
	}
	if p.refuse.Load() && ref.ID == p.id {
		return metadata.Record{}, metadata.StatusError{Provider: "tmdb", Status: 422, Path: "/private/provider-response"}
	}
	return p.Provider.Details(ctx, ref, locale)
}

// A provider's refusal of one title is durable and does not stop other titles. An outage
// stops immediately and does not create pretend failures for the titles never attempted.
func TestRefreshSelectionFailuresAndOutages(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat (1995).mkv")
	items := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=movie").Items
	doc, err := sh.s.db.MetadataDocument(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var providerID string
	for _, id := range doc.ExternalIDs {
		if id.Provider == "tmdb" {
			providerID = id.ID
		}
	}
	if providerID == "" {
		t.Fatal("fixture has no provider id")
	}
	french := "fr"
	sh.patch(items[0].ID, fmt.Sprint(doc.Revision), MetadataPatch{MetadataLanguage: &french}, nil, 200)
	p := &jobFailureProvider{Provider: tmdb.New("test-token", sh.tmdb.URL), id: providerID}
	p.refuse.Store(true)
	sh.s.UseProvider(p)
	start := JobStart{Kind: jobRefreshItems, Target: JobTarget{Filter: "library=" + sh.library + "&kind=movie"}}
	job := sh.startJob(start)
	if job.State != db.JobFailed || job.Error != selectionFailureSummary || p.others.Load() == 0 {
		t.Fatalf("partial refresh: %+v, other titles asked %d", job, p.others.Load())
	}
	path := "/v1/jobs/" + job.ID + "/failures"
	page := get[Page[JobItemFailure]](t, sh.s, sh.owner, path)
	if len(page.Items) != 1 || page.Items[0].ItemID != items[0].ID || page.Items[0].Error.Code != "provider_rejected" ||
		strings.Contains(page.Items[0].Error.Message, "private") || strings.Contains(job.Error, "422") {
		t.Fatalf("unsafe or incorrect result: %+v", page)
	}
	before := p.others.Load()
	p.refuse.Store(false)
	start.Target = JobTarget{Items: []string{page.Items[0].ItemID}}
	if retried := sh.startJob(start); retried.State != db.JobDone || p.others.Load() != before {
		t.Fatalf("failed-only refresh retry: %+v, other title was asked again", retried)
	}
	current := get[MetadataDocument](t, sh.s, sh.owner, "/v1/admin/items/"+items[0].ID+"/metadata")
	if language := current.Fields["metadataLanguage"]; language.Value != "fr" || language.Locked {
		t.Fatalf("refresh retry changed or locked metadataLanguage: %+v", language)
	}
	p.outage.Store(true)
	before = p.failed.Load() + p.others.Load()
	start.Target = JobTarget{Items: []string{items[0].ID, items[1].ID}}
	job = sh.startJob(start)
	if job.State != db.JobFailed || strings.Contains(job.Error, "private") || strings.Contains(job.Error, "503") {
		t.Fatalf("outage summary: %+v", job)
	}
	if asked := p.failed.Load() + p.others.Load() - before; asked != 1 {
		t.Fatalf("an outage asked %d titles, want one", asked)
	}
	if failures := get[Page[JobItemFailure]](t, sh.s, sh.owner, "/v1/jobs/"+job.ID+"/failures"); len(failures.Items) != 0 {
		t.Fatalf("outage invented per-title failures: %+v", failures)
	}
	p.outage.Store(false)
	p.timeout.Store(true)
	if timedOut := sh.startJob(start); timedOut.State != db.JobFailed || strings.Contains(timedOut.Error, "private") ||
		timedOut.Error != "The metadata provider did not answer. Titles not reached are as they were; try again later." {
		t.Fatalf("provider timeout leaked its details: %+v", timedOut)
	}

}

type childJobFailureProvider struct {
	metadata.Provider
	refuse atomic.Bool
	reads  atomic.Int64
}

func (p *childJobFailureProvider) Details(_ context.Context, ref metadata.Ref, _ metadata.Locale) (metadata.Record, error) {
	p.reads.Add(1)
	return metadata.Record{Ref: ref, Title: "Refreshed show"}, nil
}

func (p *childJobFailureProvider) Children(context.Context, metadata.Ref, int, metadata.Locale) ([]metadata.Record, error) {
	if p.refuse.Load() {
		return nil, metadata.StatusError{Provider: "tmdb", Status: 422, Path: "/private/children"}
	}
	return []metadata.Record{{Index: 1, Title: "Refreshed episode"}}, nil
}

// A show's root may be saved before a child request fails. Resuming must retry that failed
// title rather than mistake the root's refreshed timestamp for a completed title.
func TestRefreshJobRetriesAChildFailureAfterRootWasSaved(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	ctx := context.Background()
	lib, err := sh.s.db.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{filepath.Join(t.TempDir(), "shows")})
	if err != nil {
		t.Fatal(err)
	}
	show, season := db.ItemKey{Kind: db.KeyFolder, Key: "show"}, db.ItemKey{Kind: db.KeyName, Key: "season"}
	one := 1
	_, err = sh.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Items: []db.ImportItem{
		{Kind: db.KindShow, Title: "Original show", Keys: []db.ItemKey{show}, ExternalIDs: []db.ExternalID{{Provider: "tmdb", ID: "1396"}}},
		{Kind: db.KindSeason, Title: "Season 1", Keys: []db.ItemKey{season}, Parent: &show, IndexNumber: &one},
		{Kind: db.KindEpisode, Title: "Original episode", Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "episode"}},
			Parent: &season, IndexNumber: &one, ParentIndex: &one},
	}})
	if err != nil {
		t.Fatal(err)
	}
	items := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+lib.PublicID+"&kind=show").Items

	p := &childJobFailureProvider{Provider: tmdb.New("test-token", sh.tmdb.URL)}
	p.refuse.Store(true)
	sh.s.UseProvider(p)
	job := sh.startJob(JobStart{Kind: jobRefreshItems, Target: JobTarget{Items: []string{items[0].ID}}})
	if job.State != db.JobFailed || job.Error != selectionFailureSummary {
		t.Fatalf("child failure: %+v", job)
	}
	stored, err := sh.s.db.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := sh.s.db.RefreshTarget(ctx, items[0].ID)
	if err != nil || root.RefreshedAt < stored.CreatedAt {
		t.Fatalf("root was not saved before its child failed: %+v %v", root, err)
	}
	var req jobs.Request
	if err := json.Unmarshal([]byte(stored.Payload), &req); err != nil {
		t.Fatal(err)
	}
	req.UserID = stored.OwnerID
	p.refuse.Store(false)
	if err := sh.s.refreshItems(ctx, jobs.Work{Job: stored, Request: req, Progress: func(int64, int64) {}}); err != nil {
		t.Fatal(err)
	}
	if p.reads.Load() != 2 {
		t.Fatalf("resumed refresh asked about the root %d times, want 2", p.reads.Load())
	}
	episodes := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+lib.PublicID+"&kind=episode").Items
	if len(episodes) != 1 || episodes[0].Title != "Refreshed episode" {
		t.Fatalf("child was not refreshed: %+v", episodes)
	}
	if done := get[Job](t, sh.s, sh.owner, "/v1/jobs/"+job.ID); done.State != db.JobDone || done.Error != "" {
		t.Fatalf("successful replay kept the failure: %+v", done)
	}
}
