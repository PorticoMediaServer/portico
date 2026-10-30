package administration

import (
	"bytes"
	"context"
	"database/sql"
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

func TestSourcePublicationQueuesAndImportsAdvertisedLogo(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "channel.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source := strings.Repeat("ab", 24)
	_, err = store.Save(context.Background(), authority, livechannels.SourceInput{
		ID: source, RequestID: strings.Repeat("cd", 24), Name: "Local fixture",
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-logo=\"https://logos.example/one.png\",One\nhttps://stream.example/one\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var candidate, channel, state string
	if err = db.QueryRow(`SELECT l.url,l.channel_id,j.state FROM live_channel_logos l JOIN live_logo_jobs j ON j.generation_id=l.generation_id WHERE j.source_id=?`, source).Scan(&candidate, &channel, &state); err != nil {
		t.Fatal(err)
	}
	if candidate != "https://logos.example/one.png" || state != "pending" {
		t.Fatalf("logo publication %q %q", candidate, state)
	}
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var raw bytes.Buffer
	if err = png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	admin := NewAt(db, t.TempDir())
	calls := 0
	admin.LogoFetch = func(_ context.Context, url string) ([]byte, error) {
		calls++
		if url != candidate {
			t.Errorf("unexpected logo locator %q", url)
		}
		return raw.Bytes(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { admin.RunLogoImports(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err = db.QueryRow(`SELECT state FROM live_logo_jobs WHERE source_id=?`, source).Scan(&state); err == nil && state == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if state != "complete" || calls != 1 {
		t.Fatalf("background import state=%q calls=%d err=%v", state, calls, err)
	}
	// The stored logo is its one rendition, made from the advertised image.
	got, mime, _, err := admin.ChannelLogoBytes(context.Background(), "live:"+source+":"+channel)
	logo, format, decodeErr := image.DecodeConfig(bytes.NewReader(got))
	want, _, _ := image.DecodeConfig(bytes.NewReader(raw.Bytes()))
	if err != nil || mime != "image/png" || decodeErr != nil || format != "png" || logo.Width != min(want.Width, 256) || logo.Height != min(want.Height, 256) {
		t.Fatalf("stored logo mime=%q bytes=%d %+v err=%v %v", mime, len(got), logo, err, decodeErr)
	}
	guide, err := store.Guide(context.Background(), authority, livechannels.GuideQuery{Kind: livechannels.LiveSource, Start: time.Now().Truncate(time.Hour), End: time.Now().Truncate(time.Hour).Add(time.Hour), Timezone: "UTC", Limit: 10})
	if err != nil || len(guide.Channels) != 1 {
		t.Fatalf("guide: %+v, %v", guide.Channels, err)
	}
	paths, err := admin.ChannelLogoPaths(context.Background(), []string{"live:" + source + ":" + channel})
	if err != nil || paths["live:"+source+":"+channel] == "" {
		t.Fatalf("guide logo path: %+v, %v", paths, err)
	}
}
