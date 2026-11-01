package enrichment

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// testdata/published holds two releases staged by the pipeline's own
// publisher (portico-internal recommendations/scripts/publish.py) with a
// throwaway key whose private half was deleted: 1 is the first full release,
// 2 adds the next version as a full file and a delta. This proves that what
// the pipeline signs, gzips and diffs is what the server verifies and applies.
func TestTheServerTakesWhatThePipelinePublishes(t *testing.T) {
	raw, err := os.ReadFile("testdata/published/PUBLIC_KEY")
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		t.Fatal("fixture public key", err)
	}
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	heat := c.Movie(films, "/m/heat.mkv", "Heat", 1995)
	thief := c.Movie(films, "/m/thief.mkv", "Thief", 1981)
	ronin := c.Movie(films, "/m/ronin.mkv", "Ronin", 1998)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','949'),(?,'tmdb','movie','11524'),(?,'tmdb','movie','8195')`, heat.ID, thief.ID, ronin.ID)
	c.Drain()

	release := "1"
	requests := map[string]int{}
	files := http.FileServer(http.Dir("."))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		r2 := r.Clone(r.Context())
		r2.URL.Path = filepath.ToSlash(filepath.Join("/testdata/published", release, r.URL.Path))
		files.ServeHTTP(w, r2)
	})
	s := New(c.DB, Config{ManifestURL: "https://data.example/v3/manifest.json", PublicKey: key, Dir: t.TempDir()})
	s.resolver = datasetResolver(publicDatasetResolver)
	s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	tags := func(id int64) string {
		var out []string
		rows, _ := c.DB.Query(`SELECT tag||'='||strength FROM catalog_dataset_tags WHERE entity_id=? ORDER BY tag`, id)
		for rows.Next() {
			var v string
			rows.Scan(&v)
			out = append(out, v)
		}
		rows.Close()
		return strings.Join(out, ",")
	}

	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if got := tags(heat.ID); got != "theme:heist=3,tone:gritty=3" {
		t.Fatal("first release, Heat:", got)
	}
	if got := tags(ronin.ID); got != "tone:gritty=2" {
		t.Fatal("first release, Ronin:", got)
	}
	var relation string
	c.DB.QueryRow(`SELECT relation FROM catalog_similar WHERE entity_id=? AND source='portico-dataset' AND provider_id='1538'`, heat.ID).Scan(&relation)
	if relation != "creator" {
		t.Fatal("Heat → Collateral relation", relation)
	}

	// A week (and the largest offset) later, release 2 arrives as a delta.
	release = "2"
	now = now.Add(DefaultInterval + JitterWindow)
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if requests["/v3/screen/2026.44..2026.45.sqlite.gz"] != 1 || requests["/v3/screen/2026.45.sqlite.gz"] != 0 {
		t.Fatal("expected the delta, not the full file", requests)
	}
	if got := tags(heat.ID); got != "pacing:slow-burn=2,subgenre:folk-horror=3,tone:gritty=3" {
		t.Fatal("after the delta, Heat:", got)
	}
	if got := tags(ronin.ID); got != "" {
		t.Fatal("Ronin left the dataset but kept tags:", got)
	}
	c.DB.QueryRow(`SELECT relation FROM catalog_similar WHERE entity_id=? AND source='portico-dataset' AND provider_id='38985'`, thief.ID).Scan(&relation)
	if relation != "premise" {
		t.Fatal("Thief → Sorcerer relation", relation)
	}
	var rowLabel string
	c.DB.QueryRow(`SELECT row_label FROM catalog_dataset_vocabulary WHERE tag='appeal:comfort'`).Scan(&rowLabel)
	if rowLabel != "comfort watches" {
		t.Fatal("the vocabulary's row label didn't arrive", rowLabel)
	}
	var version string
	c.DB.QueryRow(`SELECT version FROM enrichment_domains WHERE domain='screen'`).Scan(&version)
	if version != "2026.45" {
		t.Fatal("installed version", version)
	}
}
