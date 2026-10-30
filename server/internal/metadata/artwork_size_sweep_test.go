package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func saveArtworkEdge(t *testing.T, s *Service, edge int) MetadataSettings {
	t.Helper()
	current, err := s.MetadataSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.UpdateMetadataSettings(context.Background(), MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult, ArtworkMaxEdge: &edge}, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestArtworkSizeSettingsCASAndSweepAreOneTransaction(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	current, err := s.MetadataSettings(ctx)
	if err != nil || current.ArtworkMaxEdge != 1920 {
		t.Fatal(current, err)
	}
	in := MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes}
	for _, invalid := range []int{0, 1279, 1281, 1921, 2561} {
		in.ArtworkMaxEdge = &invalid
		if _, err = s.UpdateMetadataSettings(ctx, in, allowAll); !errors.Is(err, ErrRepairInput) {
			t.Fatal(invalid, err)
		}
	}
	edge := 2560
	in.ArtworkMaxEdge = &edge
	denied := errors.New("current owner refused")
	if _, err = s.UpdateMetadataSettings(ctx, in, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	w, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplyMetadataSettingsTx(ctx, w.Tx(), in, time.Now())
	if err != nil || out.ArtworkMaxEdge != 2560 {
		t.Fatal(out, err)
	}
	var running bool
	if err = w.Tx().QueryRow(`SELECT running FROM metadata_artwork_size_sweep`).Scan(&running); err != nil || !running {
		t.Fatal(running, err)
	}
	w.Rollback()
	out, err = s.MetadataSettings(ctx)
	if err != nil || out.ArtworkMaxEdge != 1920 || out.Revision != current.Revision {
		t.Fatal("rollback lost settings", out, err)
	}
	if err = db.QueryRow(`SELECT running FROM metadata_artwork_size_sweep`).Scan(&running); err != nil || running {
		t.Fatal("rollback left sweep", running, err)
	}
	out = saveArtworkEdge(t, s, edge)
	if _, err = s.UpdateMetadataSettings(ctx, in, allowAll); !errors.Is(err, ErrScreenConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	// Existing callers that omit the additive field preserve the actual size.
	out, err = s.UpdateMetadataSettings(ctx, MetadataSettingsUpdate{ExpectedRevision: out.Revision, LocalArtworkLimitBytes: out.LocalArtworkLimitBytes}, allowAll)
	if err != nil || out.ArtworkMaxEdge != edge {
		t.Fatal(out, err)
	}
}

func TestArtworkSizeRefreshReacquiresExactSelectionWithoutOwnerAuthority(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	saveArtworkEdge(t, s, 1280)
	body := editorJPEG(t, 2700, 1080)
	requests := 0
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	entity := resolveArtworkTestEntity(t, db, target)
	var candidate, oldDigest string
	var revision int64
	if err := db.QueryRow(`SELECT candidate_id,digest,revision FROM artwork_selections WHERE entity_id=? AND role='poster'`, entity).Scan(&candidate, &oldDigest, &revision); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `UPDATE artwork_selections SET locked=1,actor='local:owner:profile' WHERE entity_id=? AND role='poster'`, entity)
	saveArtworkEdge(t, s, 2560)
	if err := s.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM metadata_artwork_size_requests`); got != "1" {
		t.Fatal("lost durable intent", got)
	}
	// A new service instance resumes the same durable intent, not an in-memory queue.
	restarted := New(db, "test-token")
	restarted.cacheRoot, restarted.artClient, restarted.now = s.cacheRoot, s.artClient, s.now
	now = now.Add(6 * time.Minute)
	if err := restarted.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	var actor string
	var refresh, locked bool
	if err := db.QueryRow(`SELECT actor,size_refresh,locked FROM artwork_jobs WHERE entity_id=? AND role='poster' AND preview=0`, entity).Scan(&actor, &refresh, &locked); err != nil || actor != "" || !refresh || !locked {
		t.Fatal(actor, refresh, locked, err)
	}
	if err := restarted.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var current, digest string
	var after int64
	// The same accepted bytes are the same source: only its large slot is
	// re-made, and the selection moves to a new revision.
	var largeEdge int
	if err := db.QueryRow(`SELECT a.candidate_id,a.digest,a.revision,a.actor,a.locked,o.large_edge FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.entity_id=? AND a.role='poster'`, entity).Scan(&current, &digest, &after, &actor, &locked, &largeEdge); err != nil || current != candidate || digest != oldDigest || largeEdge != 2560 || after <= revision || actor != "local:owner:profile" || !locked || requests != 2 {
		t.Fatal(current, digest, after, actor, locked, largeEdge, requests, err)
	}
	f, _, err := restarted.EntityArtworkVariant(ctx, target, "poster", "", ArtworkLarge, digest)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 2560 {
		t.Fatal(cfg, err)
	}
	now = now.Add(2 * time.Minute)
	if err := restarted.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM metadata_artwork_size_requests`); got != "0" {
		t.Fatal("completed request retained", got)
	}
}

// A source whose accepted image was no larger than its large slot gains
// nothing from a bigger edge, and is not fetched again.
func TestArtworkSizeRefreshSkipsSourcesThatCannotGrow(t *testing.T) {
	s, db, _, _ := integrationScreen(t)
	ctx := context.Background()
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	saveArtworkEdge(t, s, 2560)
	for n := 0; n < 2; n++ {
		if err := s.GlobalArtworkSizeStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM artwork_jobs WHERE size_refresh=1`); got != "0" {
		t.Fatal("a source that cannot grow was fetched again", got)
	}
}

func TestArtworkSizeRefreshNeverRebindsSourceOrRegeneratesPictures(t *testing.T) {
	for _, provider := range []string{"generated", "upload", "stale-source", "consent-withdrawn"} {
		t.Run(provider, func(t *testing.T) {
			s, db, target, _ := integrationScreen(t)
			ctx := context.Background()
			s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
			})}
			if err := s.ScreenStep(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.ArtworkStep(ctx); err != nil {
				t.Fatal(err)
			}
			entity := resolveArtworkTestEntity(t, db, target)
			if provider == "consent-withdrawn" {
				integrationExec(t, db, `UPDATE screen_metadata_consent SET confirmed=0,revision=revision+1`)
			} else if provider == "stale-source" {
				integrationExec(t, db, `UPDATE artwork_candidates SET source_fence='previous-source' WHERE id IN(SELECT candidate_id FROM artwork_selections WHERE entity_id=?)`, entity)
			} else {
				integrationExec(t, db, `UPDATE artwork_candidates SET provider=? WHERE id IN(SELECT candidate_id FROM artwork_selections WHERE entity_id=?)`, provider, entity)
			}
			saveArtworkEdge(t, s, 2560)
			if err := s.GlobalArtworkSizeStep(ctx); err != nil {
				t.Fatal(err)
			}
			if got := integrationValue(t, db, `SELECT count(*) FROM metadata_artwork_size_requests`); got != "0" {
				t.Fatal("ineligible request retained", got)
			}
			if got := integrationValue(t, db, `SELECT count(*) FROM artwork_jobs WHERE size_refresh=1`); got != "0" {
				t.Fatal("ineligible acquisition queued", got)
			}
			if provider == "stale-source" && integrationValue(t, db, `SELECT source_fence FROM artwork_candidates WHERE id IN(SELECT candidate_id FROM artwork_selections WHERE role='poster')`) != "previous-source" {
				t.Fatal("candidate authority rebound")
			}
		})
	}
}

func TestArtworkSizeRefreshRetainsWorkerFailureBudget(t *testing.T) {
	s, db, _, _ := integrationScreen(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	body := editorJPEG(t, 2700, 1080)
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	saveArtworkEdge(t, s, 2560)
	if err := s.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `UPDATE artwork_jobs SET status='failed',attempts=5,error='acquisition_failed' WHERE size_refresh=1`)
	now = now.Add(2 * time.Minute)
	if err := s.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM metadata_artwork_size_requests`); got != "0" {
		t.Fatal("exhausted intent retried", got)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM artwork_jobs WHERE size_refresh=1 AND attempts=5 AND status='failed'`); got != "1" {
		t.Fatal("failure budget reset", got)
	}
}

func TestArtworkSizeSweepIsBoundedAndIdleDoesNotWrite(t *testing.T) {
	s, db, _, _ := integrationScreen(t)
	ctx := context.Background()
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	// Generated pictures are deliberately ineligible for this acquisition lane.
	// A large real selection set still advances at most one 32-row page.
	integrationExec(t, db, `UPDATE artwork_candidates SET provider='generated'; WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<40) INSERT INTO artwork_selections SELECT kind,entity_id,role,printf('%03d',x),candidate_id,digest,locked,revision,actor,observed_at FROM artwork_selections CROSS JOIN n WHERE subject=''`)
	saveArtworkEdge(t, s, 2560)
	if err := s.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	var running bool
	var cursor string
	if err := db.QueryRow(`SELECT running,cursor_subject FROM metadata_artwork_size_sweep`).Scan(&running, &cursor); err != nil || !running || cursor != "031" {
		t.Fatal("unbounded page", running, cursor, err)
	}
	if err := s.GlobalArtworkSizeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT running FROM metadata_artwork_size_sweep`).Scan(&running); err != nil || running {
		t.Fatal(running, err)
	}
	before := dbwork.ChangingCommits()
	for range 3 {
		if err := s.GlobalArtworkSizeStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after := dbwork.ChangingCommits(); after != before {
		t.Fatal("idle sweep writes", before, after)
	}
}
