package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLibraryFilesOnlyRefusesBulkAndRevocationPreventsPublication(t *testing.T) {
	c, b, names := bulkFixture(t)
	ctx := context.Background()
	c.Exec(`UPDATE library_preferences SET lyrics_source='files' WHERE library_id='library'`)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.QueueTx(ctx, tx, "files-only", "library")
	tx.Rollback()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("default files-only admitted network job", err)
	}
	c.Exec(`UPDATE library_preferences SET lyrics_source='lrclib' WHERE library_id='library'`)
	tx, err = c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.QueueTx(ctx, tx, "revoke-in-flight", "library"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var duration float64
	if err = c.DB.QueryRow(`SELECT duration FROM catalog_assets WHERE token=?`, names["song-a"].Token).Scan(&duration); err != nil {
		t.Fatal(err)
	}
	requests := 0
	b.provider = func(context.Context, string) ([]acquired, error) {
		requests++
		c.Exec(`UPDATE library_preferences SET lyrics_source='files' WHERE library_id='library'`)
		d, _ := Parse([]byte("[00:01]Provider words"), "lrc")
		return []acquired{{doc: d, title: "Alpha", artist: "Fixture Artist", duration: duration}}, nil
	}
	if _, err = b.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("revocation test made no acquisition", requests)
	}
	var count int
	if err = c.DB.QueryRow(`SELECT count(*) FROM lyric_resources WHERE item_id=? AND scope='library'`, names["song-a"].ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked result published", count, err)
	}
	// Uncached local lyrics take priority before any provider request.
	c.Exec(`UPDATE library_preferences SET lyrics_source='lrclib' WHERE library_id='library'`)
	b.Service.Local = localReaderStub{}
	b.provider = func(context.Context, string) ([]acquired, error) {
		t.Fatal("local lyrics lost priority")
		return nil, nil
	}
	outcome, err := b.fetchOne(ctx, "revoke-in-flight", names["song-b"].Public, 1)
	if err != nil || outcome != "skipped" {
		t.Fatal(outcome, err)
	}
}

func TestLibraryLyricsPolicyFencesPerItemSearchAndCandidatePublication(t *testing.T) {
	c, b, names := bulkFixture(t)
	svc := b.Service
	ctx := context.Background()
	access := Access{Actor: Actor{Authority: "local", AccountID: "owner", ProfileID: "profile", Owner: true}, ServerID: "server", LibraryID: "library", ItemID: names["song-a"].Public, ViewerFence: "fence", Authorize: func(*sql.Tx) error { return nil }}
	requests := 0
	revoke := false
	provider := func() *LRCLIB {
		return &LRCLIB{origin: "https://lyrics.example", client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			requests++
			if revoke {
				c.Exec(`UPDATE library_preferences SET lyrics_source='files',revision=revision+1 WHERE library_id='library'`)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[{"id":1,"trackName":"Alpha","artistName":"Fixture Artist","duration":5400,"syncedLyrics":"[00:01]Words"}]`))}, nil
		})}}
	}
	svc.Provider = provider()
	c.Exec(`UPDATE library_preferences SET lyrics_source='files',revision=revision+1 WHERE library_id='library'`)
	view, err := svc.View(ctx, access, Target{})
	if err != nil || view.Source == nil || view.Providers.LRCLIB {
		t.Fatal(view, err)
	}
	input := SearchInput{Target: Target{SourceID: view.Source.ID, SourceVersion: view.Source.Version}, Provider: "lrclib", Language: "und"}
	if _, err = svc.Search(ctx, access, input, ""); !errors.Is(err, ErrUnavailable) || requests != 0 {
		t.Fatal("files only made a provider request", requests, err)
	}
	c.Exec(`UPDATE library_preferences SET lyrics_source='lrclib',revision=revision+1 WHERE library_id='library'`)
	found, err := svc.Search(ctx, access, input, "")
	if err != nil || len(found.Candidates) != 1 || requests != 1 {
		t.Fatal(found, requests, err)
	}
	c.Exec(`UPDATE library_preferences SET lyrics_source='files',revision=revision+1 WHERE library_id='library'`)
	if _, err = svc.Publish(ctx, access, Mutation{Target: input.Target, CandidateID: found.Candidates[0].ID, Scope: "library"}, "replace"); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked candidate published", err)
	}
	c.Exec(`UPDATE library_preferences SET lyrics_source='lrclib',revision=revision+1 WHERE library_id='library'`)
	svc.Provider = provider()
	revoke = true
	if _, err = svc.Search(ctx, access, input, ""); !errors.Is(err, ErrConflict) || requests != 2 {
		t.Fatal("in-flight provider result survived revocation", requests, err)
	}
	var n int
	if err = c.DB.QueryRow(`SELECT count(*) FROM lyric_resources`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
