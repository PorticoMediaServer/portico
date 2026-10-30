package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type p08cTransport func(*http.Request) (*http.Response, error)

func (f p08cTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func p08cPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestArtworkRepairOriginPolicy(t *testing.T) {
	for _, v := range []struct {
		url, provider string
		allow         bool
	}{
		{"https://image.tmdb.org/t/p/original/photo.jpg", "tmdb", true},
		{"https://artworks.thetvdb.com/banners/poster.jpg", "tvdb", true},
		{"https://coverartarchive.org/release/abc/123-1200", "coverartarchive", true},
		{"https://ia800101.us.archive.org/12/items/mbid-abc/image.jpg", "coverartarchive", true},
		{"https://upload.wikimedia.org/wikipedia/commons/a/ab/Portrait.jpg", "commons", true},
		{"http://image.tmdb.org/t/p/a", "tmdb", false},
		{"https://image.tmdb.org.evil.invalid/t/p/a", "tmdb", false},
		{"https://token@image.tmdb.org/t/p/a", "tmdb", false},
		{"https://image.tmdb.org:8443/t/p/a", "tmdb", false},
		{"https://image.tmdb.org/t/p/../private", "tmdb", false},
		{"https://image.tmdb.org/t/p/%2e%2e/private", "tmdb", false},
		{"https://archive.org/download/unrelated/file", "coverartarchive", false},
		{"https://127.0.0.1/t/p/a", "tmdb", false},
		{"https://image.tmdb.org/t/p/a?token=secret", "tmdb", false},
	} {
		if got := artworkURLAllowed(v.url, v.provider); got != v.allow {
			t.Errorf("%s: got %v", v.url, got)
		}
	}
}
func TestArtworkRepairRedirectDoesNotLeakCredentials(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	allowed, epoch, err := localonly.Online(context.Background(), db)
	if err != nil || !allowed || epoch < 1 {
		t.Fatalf("fixture must reach the redirect guard through real lookup admission: allowed=%v epoch=%d err=%v", allowed, epoch, err)
	}
	calls := 0
	s := New(db, "in-process-fixture")
	s.artClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: p08cTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("credentials reached image origin")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})}
	if _, err := s.fetchArtwork(context.Background(), "https://image.tmdb.org/t/p/original/a.jpg", "tmdb"); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 1 {
		t.Fatal("unsafe redirect was followed")
	}
}
func TestArtworkRepairNormalizeBoundsAndAspect(t *testing.T) {
	original, thumb, w, h, err := normalizeArtwork(append(p08cPNG(t, 800, 400), []byte("PRIVATE METADATA")...))
	if err != nil {
		t.Fatal(err)
	}
	if w != 800 || h != 400 {
		t.Fatal("original dimensions changed")
	}
	if bytes.Contains(original, []byte("PRIVATE")) {
		t.Fatal("metadata not stripped")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || cfg.Width != 800 || cfg.Height != 400 {
		t.Fatalf("thumbnail changed aspect: %+v %v", cfg, err)
	}
	if _, _, _, _, err = normalizeArtwork([]byte(`<svg onload="active"/>`)); err == nil {
		t.Fatal("active image accepted")
	}
	if _, err = boundedArtworkRead(bytes.NewReader(make([]byte, artworkBytes+1))); err == nil {
		t.Fatal("oversized source accepted")
	}
}
func TestArtworkRepairInstallPreservesOpenReaderAndRejectsMissingRestore(t *testing.T) {
	s := &Service{}
	if err := s.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	raw := p08cPNG(t, 40, 30)
	a, err := s.installArtwork(raw, 40, 30)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(s.cacheRoot, a.digest+".img"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	again, err := s.installArtwork(raw, 40, 30)
	if err != nil || again.digest != a.digest {
		t.Fatal("idempotent install failed", err)
	}
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatal("reader lost its bytes")
	}
}

// Undo restores a source only while it is published with all three files.
func TestArtworkRestoreNeedsEveryFileOfTheSource(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	m := publishTestArtwork(t, s, p08cPNG(t, 40, 30), "2026-01-01T00:00:00Z")
	choice := ArtworkChoice{Digest: m.source}
	if err := s.verifyRestorableArtwork(ctx, []ArtworkChoice{choice}); err != nil {
		t.Fatal(err)
	}
	_, large, _ := testSourceBlobs(t, db, m.source)
	if err := os.Remove(filepath.Join(s.cacheRoot, large+".img")); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyRestorableArtwork(ctx, []ArtworkChoice{choice}); err == nil {
		t.Fatal("restore accepted a source missing its large file")
	}
	choice.Digest = string(bytes.Repeat([]byte("a"), 64))
	if err := s.verifyRestorableArtwork(ctx, []ArtworkChoice{choice}); err == nil {
		t.Fatal("dangling restore accepted")
	}
}
func TestArtworkRepairHonorsBoundedRetryAfter(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	allowed, epoch, err := localonly.Online(context.Background(), db)
	if err != nil || !allowed || epoch < 1 {
		t.Fatalf("fixture must reach provider backoff through real lookup admission: allowed=%v epoch=%d err=%v", allowed, epoch, err)
	}
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	if delay := artworkRetryAfter("3600", now).Sub(now); delay != time.Hour {
		t.Fatal(delay)
	}
	if delay := artworkRetryAfter("999999999", now).Sub(now); delay != 24*time.Hour {
		t.Fatal(delay)
	}
	s := New(db, "in-process-fixture")
	s.now = func() time.Time { return now }
	s.artClient = &http.Client{Transport: p08cTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	_, err = s.fetchArtwork(context.Background(), "https://image.tmdb.org/t/p/original/a.jpg", "tmdb")
	var retry *artworkRetry
	if !errors.As(err, &retry) || retry.after.Sub(now) != time.Hour {
		t.Fatal("provider backoff lost", err)
	}
}

func TestArtworkJPEGNormalizationPreservesCompactEncoding(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	for y := 0; y < 1500; y++ {
		for x := 0; x < 1000; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x * y), 255})
		}
	}
	var source bytes.Buffer
	if err := jpeg.Encode(&source, im, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	original, thumb, w, h, err := normalizeArtwork(source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if http.DetectContentType(original) != "image/jpeg" || http.DetectContentType(thumb) != "image/jpeg" {
		t.Fatal("JPEG expanded to PNG")
	}
	if len(original) > 2*len(source.Bytes()) || len(thumb) > artworkSmallCap*3/2 {
		t.Fatalf("unexpected artwork expansion %d %d", len(original), len(thumb))
	}
	if w > 1000 || h > 1500 || (w*3-h*2 > 3 || w*3-h*2 < -3) {
		t.Fatal("dimensions or aspect changed", w, h)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || cfg.Height != artworkSmallEdge {
		t.Fatal(cfg, err)
	}
}

func TestArtworkStorageFailureRecoversWithoutLosingSelection(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.seedArtwork(ctx); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `UPDATE artwork_jobs SET status='failed',error='storage_unavailable',attempts=6,next_attempt='' WHERE role='poster'`)
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Artwork(ctx, target.ID, "poster")
	if err != nil {
		t.Fatal("permanent storage failure did not recover", err)
	}
	f.Close()
	var id string
	if err = db.QueryRow(`SELECT id FROM artwork_jobs WHERE entity_id=? AND role='poster'`, resolveArtworkTestEntity(t, db, target)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	integrationExec(t, db, `UPDATE artwork_jobs SET status='running',lease='test' WHERE id=?`, id)
	if err = s.failArtwork(ctx, artworkWork{id: id, lease: "test", attempts: 20}, errors.New("storage_unavailable")); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM artwork_jobs WHERE id=?`, id).Scan(&status); err != nil || status != "retry" {
		t.Fatal(status, err)
	}
}
