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
)

func TestArtworkUploadsUseCurrentSizeAndKeepDerivedBucketsFixed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, edge := range []int{1280, 1920, 2560} {
		s, db, target := repairFixture(t)
		ctx := context.Background()
		setArtworkPolicy(t, s, edge)
		before, err := s.RepairState(ctx, target, allowAll)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UploadArtwork(ctx, target, "poster", "", before.Revision, editorJPEG(t, 2700, 1080), editorActor(), allowAll); err != nil {
			t.Fatal(edge, err)
		}
		var width, height, thumbWidth, largeEdge int
		entity := resolveArtworkTestEntity(t, db, target)
		if err = db.QueryRow(`SELECT large.width,large.height,small.width,o.large_edge FROM artwork_selections s JOIN artwork_sources o ON o.digest=s.digest JOIN artwork_blobs large ON large.digest=o.large_blob JOIN artwork_blobs small ON small.digest=o.small_blob WHERE s.entity_id=? AND s.role='poster'`, entity).Scan(&width, &height, &thumbWidth, &largeEdge); err != nil {
			t.Fatal(err)
		}
		if width != edge || height != edge*1080/2700 || thumbWidth != artworkSmallEdge || largeEdge != edge {
			t.Fatal("stored slots ignored actual policy", edge, width, height, thumbWidth, largeEdge)
		}
		// The stored original is the policy edge. A request for the supported
		// 2560 representation is at or above that edge, so it opens the selected
		// original itself instead of a derived bucket.
		selected, _, err := s.EntityArtworkVariant(ctx, target, "poster", "", ArtworkLarge, "")
		if err != nil {
			t.Fatal(edge, err)
		}
		cfg, _, err := image.DecodeConfig(selected)
		selected.Close()
		if err != nil || cfg.Width != edge {
			t.Fatalf("supported 2560 representation opened %d px (%v), want the %d px selected original", cfg.Width, err, edge)
		}
		// The default representation is the large slot itself: nothing is
		// resized on request, so it is the policy edge.
		legacy, _, err := s.Artwork(ctx, target.ID, "poster")
		if err != nil {
			t.Fatal(edge, err)
		}
		cfg, _, err = image.DecodeConfig(legacy)
		legacy.Close()
		if err != nil || cfg.Width != edge {
			t.Fatalf("default served %d px (%v), want %d", cfg.Width, err, edge)
		}
	}
}

func TestArtworkUploadPolicyRevisionIsGuardedInThePublicationTransaction(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	before, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UploadArtwork(ctx, target, "poster", "", before.Revision, editorJPEG(t, 2000, 800), editorActor(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE metadata_runtime_settings SET revision=revision+1,artwork_max_edge=1280 WHERE singleton=1`)
		return err
	})
	if !errors.Is(err, ErrRepairConflict) {
		t.Fatal("upload published under a different policy revision", err)
	}
	var selections, uploads int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM artwork_selections),(SELECT count(*) FROM artwork_uploads)`).Scan(&selections, &uploads); err != nil || selections != 0 || uploads != 0 {
		t.Fatal("stale upload partially published", selections, uploads, err)
	}
}

func TestProviderArtworkRetriesWhenPolicyChangesDuringAcquisition(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	setArtworkPolicy(t, s, 1280)
	body := p08cPNG(t, 2700, 1080)
	requests := 0
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			setArtworkPolicy(t, s, 1920)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var selected int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_selections`).Scan(&selected); err != nil || selected != 0 {
		t.Fatal("old-policy provider pixels published", selected, err)
	}
	var retry bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM artwork_jobs WHERE status='retry' AND error='encoding_policy_changed' AND attempts=0 AND lease='')`).Scan(&retry); err != nil || !retry {
		t.Fatal("policy change lost retryable work", retry, err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Artwork(ctx, target.ID, "poster")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 1920 || requests != 2 {
		t.Fatal("next bounded turn did not reacquire using new policy", cfg, requests, err)
	}
}
