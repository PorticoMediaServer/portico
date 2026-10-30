package administration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

const guideImageFixtureHost = "img.invalid"

func guideImageFixture() (playlist, guide string) {
	playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",Channel one\nhttps://stream.invalid/one\n"
	guide = `<?xml version="1.0"?><tv><channel id="one"/>` +
		`<programme id="p1" channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>One</title><icon src="https://img.invalid/good.png"/></programme>` +
		`<programme id="p2" channel="one" start="20260905130000 +0000" stop="20260905140000 +0000"><title>Two</title><icon src="https://img.invalid/good.png"/></programme>` +
		`<programme id="p3" channel="one" start="20260905140000 +0000" stop="20260905150000 +0000"><title>Three</title><icon src="https://img.invalid/broken.png"/></programme>` +
		`<programme id="p4" channel="one" start="20260905150000 +0000" stop="20260905160000 +0000"><title>Four</title><icon src="https://img.invalid/huge.png"/></programme>` +
		`</tv>`
	return playlist, guide
}

func runGuideImagesToCompletion(t *testing.T, admin *Service, db *sql.DB, source string) (imported, skipped int) {
	t.Helper()
	return runGuideImagesUntil(t, admin, db, source, func() bool { return true })
}

// runGuideImagesUntil runs the worker until the job is complete and settled()
// holds. The worker cleans up after it records completion, so a test that
// checks the cleanup keeps the worker running until it has happened instead of
// cancelling it the moment the job reads complete.
func runGuideImagesUntil(t *testing.T, admin *Service, db *sql.DB, source string, settled func() bool) (imported, skipped int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { admin.RunGuideImageImports(ctx); close(done) }()
	var state string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := db.QueryRow(`SELECT state,imported,skipped FROM live_icon_jobs WHERE source_id=?`, source).Scan(&state, &imported, &skipped)
		if err == nil && state == "complete" && settled() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if state != "complete" {
		t.Fatalf("guide image job state=%q imported=%d skipped=%d", state, imported, skipped)
	}
	return imported, skipped
}

func guideImageJobGeneration(t *testing.T, db *sql.DB, source string) string {
	t.Helper()
	var generation string
	if err := db.QueryRow(`SELECT generation_id FROM live_icon_jobs WHERE source_id=?`, source).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

// A valid PNG is stored content-addressed under guide-images with its row; a
// non-image and an oversized body are refused, backed off and not retried
// before retry_after_ms; nothing is fetched with logo import off; the provider
// URL appears in no error string; orphaned rows and files are removed after
// completion.
func TestGuideImageImportStoresValidatesAndCleansUp(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "guide-images.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	playlist, guide := guideImageFixture()
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source := strings.Repeat("ab", 24)
	saved, err := store.Save(context.Background(), authority, livechannels.SourceInput{ID: source, RequestID: strings.Repeat("cd", 24), Name: "Icons", Playlist: playlist, Guide: guide})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err = png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	admin := NewAt(db, state)
	calls := map[string]int{}
	admin.LogoFetch = func(_ context.Context, locator string) ([]byte, error) {
		calls[locator]++
		switch {
		case strings.HasSuffix(locator, "/good.png"):
			return raw.Bytes(), nil
		case strings.HasSuffix(locator, "/huge.png"):
			return make([]byte, LogoUploadBytes+1), nil
		default:
			return []byte("this is not an image"), nil
		}
	}
	imported, skipped := runGuideImagesToCompletion(t, admin, db, source)
	// Two programmes share one URL: one fetch, one stored row, two skipped.
	if imported != 1 || skipped != 2 || len(calls) != 3 {
		t.Fatalf("batch receipt: imported=%d skipped=%d calls=%v", imported, skipped, calls)
	}
	// The provider original is not kept: the import stores the two card
	// renditions, here lossless PNG for a picture with transparency, never
	// enlarged.
	var digest, large, mime string
	var width, height, largeWidth, largeHeight, smallBytes int
	if err = db.QueryRow(`SELECT small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height FROM live_programme_images WHERE url='https://img.invalid/good.png'`).Scan(&digest, &large, &mime, &width, &height, &smallBytes, &largeWidth, &largeHeight); err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || width != 64 || height != 64 || largeWidth != 64 || largeHeight != 64 {
		t.Fatalf("stored image row: %q %dx%d %dx%d", mime, width, height, largeWidth, largeHeight)
	}
	for _, name := range []string{digest, large} {
		stored, err := os.ReadFile(storagepaths.Path(state, "guide-images", name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(stored)
		if hex.EncodeToString(sum[:]) != name || bytes.Equal(stored, raw.Bytes()) {
			t.Fatal("rendition is not content-addressed or is the original", name)
		}
	}
	got, err := admin.GuideImageBytes(context.Background(), digest, mime)
	if err != nil || len(got) != smallBytes {
		t.Fatalf("stored image bytes: bytes=%d err=%v", len(got), err)
	}
	for _, url := range []string{"https://img.invalid/broken.png", "https://img.invalid/huge.png"} {
		var attempts int
		var retry int64
		if err = db.QueryRow(`SELECT attempts,retry_after_ms FROM live_logo_failures WHERE url=?`, url).Scan(&attempts, &retry); err != nil || attempts != 1 || retry <= time.Now().UnixMilli() {
			t.Fatalf("failure backoff for %q: attempts=%d retry=%d err=%v", url, attempts, retry, err)
		}
	}
	// A fresh pass before the backoff expires fetches nothing and reports the
	// same failures as skipped.
	before := len(calls)
	out, _, done, err := admin.importGuideImagesBatch(context.Background(), source, saved.Generation, "", 16)
	if err != nil || !done || out.Skipped != 2 || out.Imported != 0 || len(calls) != before {
		t.Fatalf("backoff refetch: done=%t out=%+v calls=%d err=%v", done, out, len(calls), err)
	}
	if strings.Contains(out.Message, guideImageFixtureHost) {
		t.Fatalf("provider URL in message: %q", out.Message)
	}
	if _, err = admin.GuideImageBytes(context.Background(), strings.Repeat("0", 64), "image/png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown digest: %v", err)
	}

	// Orphaned rows and files are removed after the next completed job, while
	// an unrelated file and the live image survive.
	orphanDigest := strings.Repeat("de", 32)
	strayDigest := strings.Repeat("cd", 32)
	if _, err = db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) VALUES(?,?,?,?,16,16,1,16,16,1,?)`, "https://img.invalid/orphan.png", orphanDigest, orphanDigest, "image/png", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	root := storagepaths.Path(state, "guide-images")
	if err = os.WriteFile(filepath.Join(root, strayDigest+".png"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	// The sweep leaves files younger than an hour for an import that may be
	// about to record them.
	stale := time.Now().Add(-2 * time.Hour)
	if err = os.Chtimes(filepath.Join(root, strayDigest+".png"), stale, stale); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "readme.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	republished, err := store.Save(context.Background(), authority, livechannels.SourceInput{ID: source, ExpectedRevision: saved.Revision, RequestID: strings.Repeat("ef", 24), Name: "Icons", Playlist: playlist, Guide: guide})
	if err != nil {
		t.Fatal(err)
	}
	_ = republished
	runGuideImagesUntil(t, admin, db, source, func() bool {
		_, err := os.Stat(filepath.Join(root, strayDigest+".png"))
		return os.IsNotExist(err)
	})
	var orphans int
	if err = db.QueryRow(`SELECT count(*) FROM live_programme_images WHERE url='https://img.invalid/orphan.png'`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("orphaned row remains: %d %v", orphans, err)
	}
	if _, err = os.Stat(filepath.Join(root, strayDigest+".png")); !os.IsNotExist(err) {
		t.Fatalf("orphaned file remains: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "readme.txt")); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, digest+".png")); err != nil {
		t.Fatalf("live image removed: %v", err)
	}

	// With logo import switched off, a fresh job completes with nothing fetched.
	if _, err = db.Exec(`INSERT INTO admin_documents VALUES(?,?,?,?) ON CONFLICT(scope) DO UPDATE SET revision=excluded.revision,body=excluded.body,updated_ms=excluded.updated_ms`, "live-source:"+source, 2, `{"kind":"playlist","logoImport":{"enabled":false}}`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(context.Background(), authority, livechannels.SourceInput{ID: source, ExpectedRevision: republished.Revision, RequestID: strings.Repeat("12", 24), Name: "Icons", Playlist: playlist, Guide: guide}); err != nil {
		t.Fatal(err)
	}
	generation := guideImageJobGeneration(t, db, source)
	fresh, _, done, err := admin.importGuideImagesBatch(context.Background(), source, generation, "", 16)
	if err != nil || !done || fresh.Imported != 0 || fresh.Requested != 0 {
		t.Fatalf("import-off batch: done=%t out=%+v err=%v", done, fresh, err)
	}
	if strings.Contains(fresh.Message, guideImageFixtureHost) {
		t.Fatalf("provider URL in message: %q", fresh.Message)
	}
	fetched := len(calls)
	runGuideImagesToCompletion(t, admin, db, source)
	if len(calls) != fetched {
		t.Fatalf("fetched with logo import off: %v", calls)
	}
}
