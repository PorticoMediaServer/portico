package lyrics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func bulkFixture(t *testing.T) (*catalogtest.Catalog, *Bulk, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	music := c.Library("library", "Music", "music", "/synthetic")
	c.Library("other", "Other", "music", "/other")
	artist := c.Artist(music, "Fixture Artist")
	album := c.Album(artist, "Fixture Album", 2024)
	names := catalogtest.Names{
		"song-a": c.Song(album, 1, "/synthetic/a.flac", "Alpha"),
		"song-b": c.Song(album, 2, "/synthetic/b.flac", "Beta"),
	}
	c.Drain()
	// These worker tests explicitly choose provider acquisition; new libraries
	// otherwise keep the approved files-only default.
	c.Exec(`INSERT INTO library_preferences(library_id,lyrics_source) VALUES('library','lrclib')`)
	c.Exec(`UPDATE configuration SET value='true' WHERE key='lyrics_lrclib_enabled'`)
	// A configured provider with a stubbed client: no network, no origin policy.
	service := &Service{DB: c.DB, Provider: &LRCLIB{}}
	return c, &Bulk{Service: service, Batch: 8}, names
}

func TestBulkLyricFetchEnqueuesOnlyWithAnEnabledProvider(t *testing.T) {
	c, bulk, names := bulkFixture(t)
	db := c.DB
	ctx := context.Background()
	queue := func(operation, library string) (string, error) {
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		id, e := bulk.QueueTx(ctx, tx, operation, library)
		if e != nil {
			return "", e
		}
		return id, tx.Commit()
	}
	if _, e := db.Exec(`UPDATE configuration SET value='false' WHERE key='lyrics_lrclib_enabled'`); e != nil {
		t.Fatal(e)
	}
	if _, e := queue("job-off", "library"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("disabled provider admitted work", e)
	}
	if _, e := db.Exec(`UPDATE configuration SET value='true' WHERE key='lyrics_lrclib_enabled'`); e != nil {
		t.Fatal(e)
	}
	if _, e := queue("job-1", "missing-library"); !errors.Is(e, ErrInput) {
		t.Fatal("unknown library admitted", e)
	}
	id, e := queue("job-1", "library")
	if e != nil || id != "job-1" {
		t.Fatal(id, e)
	}
	run, e := bulk.Observe(ctx, "job-1")
	if e != nil || run.State != "queued" || run.Candidates != 2 || run.LibraryID != "library" {
		t.Fatal("run admission", run, e)
	}
	// Re-admission of the same console operation is idempotent.
	if _, e = queue("job-1", "library"); e != nil {
		t.Fatal(e)
	}
	again, e := bulk.Observe(ctx, "job-1")
	if e != nil || again.Candidates != 2 || again.Processed != 0 {
		t.Fatal("re-admission changed the run", again, e)
	}
	// A library whose songs already have library lyrics admits zero candidates.
	if _, e = db.Exec(`INSERT INTO lyric_resources(id,item_id,asset_id,source_version,scope,authority,account_id,profile_id,revision,source_duration,source_end) VALUES('r',?,?,'v','library','local','','',1,5400,5400),('r2',?,?,'v','library','local','','',1,5400,5400)`, names["song-a"].ID, names["song-a"].Token, names["song-b"].ID, names["song-b"].Token); e != nil {
		t.Fatal(e)
	}
	if _, e = queue("job-2", "library"); e != nil {
		t.Fatal(e)
	}
	empty, e := bulk.Observe(ctx, "job-2")
	if e != nil || empty.Candidates != 0 {
		t.Fatal("songs with lyrics counted as candidates", empty, e)
	}
}

func TestBulkLyricFetchPublishesMissingAndSkipsPresent(t *testing.T) {
	c, bulk, names := bulkFixture(t)
	db := c.DB
	ctx := context.Background()
	// song-b already has a library lyric and must never reach the provider.
	if _, e := db.Exec(`INSERT INTO lyric_resources(id,item_id,asset_id,source_version,scope,authority,account_id,profile_id,revision,source_duration,source_end) VALUES('present',?,?,'v','library','local','','',1,5400,5400)`, names["song-b"].ID, names["song-b"].Token); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO lyric_revisions(resource_id,revision,document,digest,language,offset_ms,provenance,actor,created_at,deleted) VALUES('present',1,'{}','d','en',0,'{}','actor','now',0)`); e != nil {
		t.Fatal(e)
	}
	asked := []string{}
	document, e := Parse([]byte("[00:01.00]First line\n[00:10.00]Second line"), "lrc")
	if e != nil {
		t.Fatal(e)
	}
	bulk.provider = func(_ context.Context, query string) ([]acquired, error) {
		asked = append(asked, query)
		return []acquired{{doc: document, language: "en", title: strings.Fields(query)[0], artist: "Fixture Artist", duration: 5400, provenance: Provenance{Origin: "lrclib", ProviderID: "7", Label: "Alpha", Rights: "LRCLIB record."}}}, nil
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		more, e := bulk.Step(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !more {
			break
		}
		run, e := bulk.Observe(ctx, "job")
		if e != nil {
			t.Fatal(e)
		}
		if run.State == "succeeded" {
			break
		}
	}
	run, e := bulk.Observe(ctx, "job")
	if e != nil || run.State != "succeeded" {
		t.Fatal("run did not complete", run, e)
	}
	if run.Published != 1 || run.Processed != 1 || run.Missing != 0 {
		t.Fatal("unexpected counters", run)
	}
	if len(asked) != 1 || asked[0] != "Alpha Fixture Artist" {
		t.Fatal("provider asked for the wrong songs", asked)
	}
	var scope, origin string
	var revision int64
	if e = db.QueryRow(`SELECT r.scope,r.revision,json_extract(v.provenance,'$.origin') FROM lyric_resources r JOIN lyric_revisions v ON v.resource_id=r.id WHERE r.item_id=?`, names["song-a"].ID).Scan(&scope, &revision, &origin); e != nil {
		t.Fatal(e)
	}
	if scope != "library" || revision != 1 || origin != "lrclib" {
		t.Fatal("published lyric shape", scope, revision, origin)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM lyric_resources WHERE item_id=?`, names["song-b"].ID).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 1 {
		t.Fatal("a song that already had lyrics was republished", count)
	}
	// A second run over the same library now finds nothing left to do.
	tx, e = db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job-2", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.Step(ctx); e != nil {
		t.Fatal(e)
	}
	second, e := bulk.Observe(ctx, "job-2")
	if e != nil || second.State != "succeeded" || second.Published != 0 || second.Candidates != 0 {
		t.Fatal("second run republished", second, e)
	}
	if len(asked) != 1 {
		t.Fatal("provider called again for a song with lyrics", asked)
	}
}

func TestBulkLyricFetchStopsWhenTheProviderIsDisabled(t *testing.T) {
	c, bulk, _ := bulkFixture(t)
	db := c.DB
	ctx := context.Background()
	bulk.provider = func(context.Context, string) ([]acquired, error) {
		t.Fatal("provider consulted after the switch was turned off")
		return nil, nil
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE configuration SET value='false' WHERE key='lyrics_lrclib_enabled'`); e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.Step(ctx); e != nil {
		t.Fatal(e)
	}
	run, e := bulk.Observe(ctx, "job")
	if e != nil || run.State != "failed" || run.ErrorCode != "lyrics_provider_unavailable" {
		t.Fatal("disabled provider did not stop the run", run, e)
	}
}

func TestBulkLyricFetchCancellationIsFenced(t *testing.T) {
	c, bulk, _ := bulkFixture(t)
	db := c.DB
	ctx := context.Background()
	bulk.provider = func(context.Context, string) ([]acquired, error) {
		t.Fatal("cancelled run consulted the provider")
		return nil, nil
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	tx, e = db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = bulk.CancelTx(ctx, tx, "job"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.Step(ctx); e != nil {
		t.Fatal(e)
	}
	run, e := bulk.Observe(ctx, "job")
	if e != nil || run.State != "cancelled" {
		t.Fatal("cancellation not honoured", run, e)
	}
}
