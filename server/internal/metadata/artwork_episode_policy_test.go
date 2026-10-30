package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func artworkEpisodeMode(t *testing.T, c *catalogtest.Catalog, mode string) {
	t.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, "episodes")
		if err != nil {
			return err
		}
		return catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "episodes", mode, policy.Revision)
	})
	c.Drain()
}

func queueEpisodeProviderArtwork(t *testing.T, s *Service, c *catalogtest.Catalog, target RepairTarget, role, actor, provider string) string {
	t.Helper()
	var candidate string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		enabled, err := s.artworkProviderEnabled(ctx, tx, target, provider)
		if err != nil {
			return err
		}
		if !enabled {
			return errors.New("fixture provider is not enabled")
		}
		fence, err := artworkFence(ctx, tx, target)
		if err != nil {
			return err
		}
		origin := "https://image.tmdb.org/t/p/original/episode.png"
		if provider == "local" {
			origin = "local:episode-sidecar"
		}
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		candidate, err = insertArtworkCandidate(ctx, tx, target, role, "", provider, "episode-image", origin, "", "", fence, stamp, 0)
		if err != nil {
			return err
		}
		return queueArtworkCandidate(ctx, tx, target, candidate, actor, stamp, false, false)
	})
	return candidate
}

func TestArtworkWorkerChecksEpisodeModeBeforeAcquisition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		mode, role, actor, provider string
		acquire                     bool
	}{
		{"off", "still", "", "tmdb", false},
		{"off", "thumbnail", "", "tmdb", false},
		{"provider", "still", "", "tmdb", true},
		{"provider_then_made", "still", "", "tmdb", true},
		{"off", "still", "local:owner:profile", "tmdb", true},
		{"off", "still", "", "local", true},
		{"off", "poster", "", "tmdb", true},
	} {
		t.Run(tc.mode+"/"+tc.role+"/"+tc.provider+"/"+tc.actor, func(t *testing.T) {
			s, c, target, _ := episodePublicationFixture(t)
			discoveryConfirmLibrary(t, s, "episodes", []string{"tmdb"}, "US")
			artworkEpisodeMode(t, c, tc.mode)
			candidate := queueEpisodeProviderArtwork(t, s, c, target, tc.role, tc.actor, tc.provider)
			body := p08cPNG(t, 20, 20)
			requests, localReads := 0, 0
			s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
			})}
			path := filepath.Join(t.TempDir(), "episode.png")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			s.LocalArtwork = func(string) (*os.File, string, error) {
				localReads++
				file, err := os.Open(path)
				return file, "image/png", err
			}
			if err := s.ArtworkStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantRequests, wantLocal := 0, 0
			if tc.acquire {
				if tc.provider == "local" {
					wantLocal = 1
				} else {
					wantRequests = 1
				}
			}
			var selected bool
			if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE candidate_id=?)`, candidate).Scan(&selected); err != nil {
				t.Fatal(err)
			}
			if requests != wantRequests || localReads != wantLocal || selected != tc.acquire {
				t.Fatalf("mode=%s actor=%q provider=%s: requests=%d local=%d selected=%t", tc.mode, tc.actor, tc.provider, requests, localReads, selected)
			}
		})
	}
}

func TestArtworkWorkerRejectsEpisodeModeRevokedDuringFetch(t *testing.T) {
	s, c, target, _ := episodePublicationFixture(t)
	discoveryConfirmLibrary(t, s, "episodes", []string{"tmdb"}, "US")
	candidate := queueEpisodeProviderArtwork(t, s, c, target, "still", "", "tmdb")
	requests := 0
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		artworkEpisodeMode(t, c, "off")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ArtworkStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var selected bool
	var status string
	if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE candidate_id=?)`, candidate).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT status FROM artwork_jobs WHERE candidate_id=?`, candidate).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || selected || status != "stale" {
		t.Fatalf("revoked publication: requests=%d selected=%t status=%s", requests, selected, status)
	}
}
