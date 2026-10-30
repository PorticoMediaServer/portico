package metadata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Seeding runs again whenever a title's artwork is marked dirty. A slot that
// already holds this provider's image under the current state, or the owner's
// local image, is taken: it is neither downloaded again nor replaced.
func TestProviderArtworkIsNotRequeuedForAnOccupiedSlot(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	requests := 0
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.seedArtwork(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	entity := resolveArtworkTestEntity(t, db, target)
	var selected string
	if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, entity).Scan(&selected); err != nil || requests != 1 {
		t.Fatal("fixture poster was not downloaded once", err, requests)
	}
	pending := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role='poster' AND status IN('pending','retry','running')`, entity).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	reseed := func() {
		integrationExec(t, db, `INSERT OR IGNORE INTO artwork_dirty(kind,entity_id) VALUES('item',?)`, entity)
		if err := s.seedArtwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reseed()
	if n := pending(); n != 0 {
		t.Fatalf("a poster already held under the current state was queued again (%d jobs)", n)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("held poster downloaded again: %d requests", requests)
	}

	// The owner's local image now holds the slot, selected under an earlier state.
	integrationExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) SELECT 'local-poster',kind,entity_id,role,subject,'local','sidecar','local:sidecar','Local file','earlier-state',observed_at FROM artwork_candidates WHERE id=?`, selected)
	integrationExec(t, db, `UPDATE artwork_selections SET candidate_id='local-poster' WHERE kind='item' AND entity_id=? AND role='poster'`, entity)
	integrationExec(t, db, `UPDATE artwork_candidates SET source_fence='earlier-state' WHERE id=?`, selected)
	reseed()
	if n := pending(); n != 0 {
		t.Fatalf("a provider poster was queued over the local selection (%d jobs)", n)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var now string
	if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, entity).Scan(&now); err != nil || now != "local-poster" || requests != 1 {
		t.Fatalf("local selection replaced: %q after %d requests (%v)", now, requests, err)
	}
}

// A provider job that is already in the queue when a local image holds the
// slot (queued before the image was chosen, or by an earlier build) gives way:
// it is retired without a download and the selection stays as it is. An image
// the owner asked for is not an automatic job and is still installed.
func TestQueuedProviderArtworkGivesWayToALocalOrLockedSelection(t *testing.T) {
	for _, held := range []string{"local", "locked"} {
		t.Run(held, func(t *testing.T) {
			s, db, target, _ := integrationScreen(t)
			ctx := context.Background()
			requests := 0
			s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
			})}
			if err := s.ScreenStep(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.seedArtwork(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.ArtworkStep(ctx); err != nil {
				t.Fatal(err)
			}
			entity := resolveArtworkTestEntity(t, db, target)
			var provider string
			if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, entity).Scan(&provider); err != nil || requests != 1 {
				t.Fatal("fixture poster was not downloaded once", err, requests)
			}
			want := provider
			if held == "local" {
				integrationExec(t, db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) SELECT 'local-poster',kind,entity_id,role,subject,'local','sidecar','local:sidecar','Local file','earlier-state',observed_at FROM artwork_candidates WHERE id=?`, provider)
				integrationExec(t, db, `UPDATE artwork_selections SET candidate_id='local-poster' WHERE kind='item' AND entity_id=? AND role='poster'`, entity)
				want = "local-poster"
			} else {
				integrationExec(t, db, `UPDATE artwork_selections SET locked=1 WHERE kind='item' AND entity_id=? AND role='poster'`, entity)
			}
			queue := func(actor string) {
				t.Helper()
				gated, err := dbwork.Begin(ctx, db, dbwork.ClassBackgroundMedia)
				if err != nil {
					t.Fatal(err)
				}
				defer gated.Rollback()
				// The job as a seeder would write it: this slot's provider image,
				// under the selection's current revision and the current state.
				if err = queueArtworkCandidate(ctx, gated.Tx(), target, provider, actor, time.Now().UTC().Format(time.RFC3339Nano), false, true); err != nil {
					t.Fatal(err)
				}
				if err = gated.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			queue("")
			if err := s.ArtworkStep(ctx); err != nil {
				t.Fatal(err)
			}
			var now, status string
			if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, entity).Scan(&now); err != nil || now != want || requests != 1 {
				t.Fatalf("queued provider job replaced the %s selection: %q after %d requests (%v)", held, now, requests, err)
			}
			if err := db.QueryRow(`SELECT status FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role='poster' AND preview=0`, entity).Scan(&status); err != nil || status != "stale" {
				t.Fatalf("the job that gave way was left %q (%v)", status, err)
			}
			// The owner choosing the provider image is honoured.
			queue("local:owner:profile")
			if err := s.ArtworkStep(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT candidate_id FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster'`, entity).Scan(&now); err != nil || now != provider || requests != 2 {
				t.Fatalf("the owner's own choice was not installed: %q after %d requests (%v)", now, requests, err)
			}
		})
	}
}

func TestRetainedSourceArtworkIsAcquiredOnlyOnce(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	requests := 0
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.ArtworkStep(ctx); e != nil {
		t.Fatal(e)
	}
	entity := resolveArtworkTestEntity(t, db, target)
	var origin string
	if e := db.QueryRow(`SELECT c.origin FROM artwork_candidates c JOIN artwork_selections a ON a.candidate_id=c.id WHERE a.entity_id=? AND a.role='poster'`, entity).Scan(&origin); e != nil {
		t.Fatal(e)
	}
	m, e := s.acquireArtworkMaterial(ctx, artworkWork{provider: "tmdb", origin: origin})
	if e != nil || !m.reused || m.source == "" || requests != 1 {
		t.Fatal("source was downloaded again", requests, e)
	}
}
