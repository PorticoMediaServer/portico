package administration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestLogoImportBatchesAndBacksOffFailedURLs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "logos.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	for n := 0; n < 10; n++ {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"%02d\" tvg-logo=\"https://logos.invalid/%02d.png\",Channel %02d\nhttps://stream.invalid/%02d\n", n, n, n, n)
	}
	source := strings.Repeat("ef", 24)
	_, err = store.Save(context.Background(), func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}, livechannels.SourceInput{ID: source, RequestID: strings.Repeat("cd", 24), Name: "Ten channels", Playlist: playlist.String()})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err = png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	admin := NewAt(db, t.TempDir())
	calls := map[string]int{}
	admin.LogoFetch = func(_ context.Context, locator string) ([]byte, error) {
		calls[locator]++
		if strings.HasSuffix(locator, "/04.png") {
			return nil, errors.New("fixture unavailable")
		}
		return raw.Bytes(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { admin.RunLogoImports(ctx); close(done) }()
	var state string
	var imported, skipped int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err = db.QueryRow(`SELECT state,imported,skipped FROM live_logo_jobs WHERE source_id=?`, source).Scan(&state, &imported, &skipped)
		if err == nil && state == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if err != nil || state != "complete" || imported != 9 || skipped != 1 || len(calls) != 10 {
		t.Fatalf("batch receipt: state=%s imported=%d skipped=%d calls=%d err=%v", state, imported, skipped, len(calls), err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT attempts FROM live_logo_failures WHERE url='https://logos.invalid/04.png'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("failed locator backoff: %d %v", attempts, err)
	}
	after, complete := "", false
	for pass := 0; pass < 3 && !complete; pass++ {
		_, after, complete, err = admin.importLogosBatch(context.Background(), nil, source, "logo-retry", after, 8)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !complete || calls["https://logos.invalid/04.png"] != 1 {
		t.Fatalf("backoff refetched failed URL: complete=%t calls=%d", complete, calls["https://logos.invalid/04.png"])
	}
}
