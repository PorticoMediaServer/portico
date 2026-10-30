package administration

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

const liveLogoPlaylist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-logo=\"https://logos.example/one.png\",One\nhttps://stream.example/one\n"

func seedSingleLogoSource(t *testing.T, db *sql.DB) (string, string) {
	t.Helper()
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("9a", 24)
	_, err = store.Save(context.Background(), func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}, livechannels.SourceInput{ID: source, RequestID: strings.Repeat("9b", 24), Name: "Logo source", Playlist: liveLogoPlaylist})
	if err != nil {
		t.Fatal(err)
	}
	var channel string
	if err = db.QueryRow(`SELECT channel_id FROM live_channel_versions WHERE generation_id=(SELECT active_generation FROM live_sources WHERE id=?)`, source).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	return source, channel
}

func logoPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// setServerLogoImport rewrites the server-wide live settings through the same
// owner write path the settings page uses.
func setServerLogoImport(t *testing.T, s *Service, enabled bool) {
	t.Helper()
	ctx := context.Background()
	current, err := s.LiveDefaultsDocument(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	settings := current.Settings
	settings.LogoImport = enabled
	out, err := s.SaveLiveDefaults(ctx, allow, Change[LiveDefaults]{
		ExpectedRevision: current.Revision,
		OperationID:      fmt.Sprintf("logo-defaults-%t-%d", enabled, current.Revision),
		Settings:         settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != current.Revision+1 || out.Settings.LogoImport != enabled {
		t.Fatalf("server logo import %t: revision %d settings %+v", enabled, out.Revision, out.Settings)
	}
}

// saveSourceLogoImport writes one source's logo choice and returns the document
// the owner would read back.
func saveSourceLogoImport(t *testing.T, s *Service, source, operation string, logos LogoImport) LiveSourceDocument {
	t.Helper()
	ctx := context.Background()
	current, err := s.LiveSourceConfiguration(ctx, allow, source)
	if err != nil {
		t.Fatal(err)
	}
	settings := current.Settings
	settings.Logos = logos
	out, err := s.SaveLiveSourceConfiguration(ctx, allow, source, Change[LiveSourceSettings]{
		ExpectedRevision: current.Revision,
		OperationID:      operation,
		Settings:         settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLiveSourceLogoImportCanFollowTheServerSetting(t *testing.T) {
	s, db, _ := newService(t)
	source, _ := seedSingleLogoSource(t, db)
	setServerLogoImport(t, s, true)

	saved := saveSourceLogoImport(t, s, source, "logo-follow-one", LogoImport{FollowServer: true, Enabled: false, OverwriteExisting: true})
	if !saved.Settings.Logos.FollowServer {
		t.Fatalf("stored choice lost: %+v", saved.Settings.Logos)
	}
	if !saved.EffectiveLogoImport.Enabled || saved.EffectiveLogoImport.OverwriteExisting {
		t.Fatalf("following a serving server: %+v", saved.EffectiveLogoImport)
	}
	if !saved.Effective.LogoImport {
		t.Fatalf("effective defaults lost the logo switch: %+v", saved.Effective)
	}

	setServerLogoImport(t, s, false)
	after, err := s.LiveSourceConfiguration(context.Background(), allow, source)
	if err != nil {
		t.Fatal(err)
	}
	if after.EffectiveLogoImport.Enabled || after.EffectiveLogoImport.OverwriteExisting || after.Effective.LogoImport {
		t.Fatalf("following a silent server: %+v %+v", after.EffectiveLogoImport, after.Effective)
	}
	if !after.Settings.Logos.FollowServer || !after.Settings.Logos.OverwriteExisting {
		t.Fatalf("stored choice was rewritten: %+v", after.Settings.Logos)
	}

	setServerLogoImport(t, s, true)
	again, err := s.LiveSourceConfiguration(context.Background(), allow, source)
	if err != nil {
		t.Fatal(err)
	}
	if !again.EffectiveLogoImport.Enabled || again.EffectiveLogoImport.OverwriteExisting {
		t.Fatalf("re-enabled server: %+v", again.EffectiveLogoImport)
	}
}

func TestLiveSourceLogoImportExplicitChoiceKeepsItsOwnValues(t *testing.T) {
	s, db, _ := newService(t)
	source, _ := seedSingleLogoSource(t, db)
	setServerLogoImport(t, s, true)

	on := saveSourceLogoImport(t, s, source, "logo-explicit-on", LogoImport{Enabled: true, OverwriteExisting: true})
	if !on.EffectiveLogoImport.Enabled || !on.EffectiveLogoImport.OverwriteExisting {
		t.Fatalf("explicit on: %+v", on.EffectiveLogoImport)
	}

	off := saveSourceLogoImport(t, s, source, "logo-explicit-off", LogoImport{Enabled: false, OverwriteExisting: true})
	if off.EffectiveLogoImport.Enabled || off.EffectiveLogoImport.OverwriteExisting || off.Effective.LogoImport {
		t.Fatalf("explicit off: %+v %+v", off.EffectiveLogoImport, off.Effective)
	}

	setServerLogoImport(t, s, false)
	silent := saveSourceLogoImport(t, s, source, "logo-explicit-server-off", LogoImport{Enabled: true, OverwriteExisting: true})
	if silent.EffectiveLogoImport.Enabled || silent.EffectiveLogoImport.OverwriteExisting || silent.Effective.LogoImport {
		t.Fatalf("the server-wide switch must still gate every source: %+v", silent.EffectiveLogoImport)
	}
}

// The worker reads the resolved choice, not the stored one: a following source
// imports while the server allows it, and still keeps logos it already stored
// even though its own overwriteExisting says to replace them.
func TestLogoImportWorkerUsesTheEffectiveSourceChoice(t *testing.T) {
	s, db, _ := newService(t)
	source, channel := seedSingleLogoSource(t, db)
	setServerLogoImport(t, s, true)
	saveSourceLogoImport(t, s, source, "logo-worker-follow", LogoImport{FollowServer: true, Enabled: false, OverwriteExisting: true})

	calls := 0
	logo := logoPNG(t)
	s.LogoFetch = func(_ context.Context, locator string) ([]byte, error) {
		calls++
		if locator != "https://logos.example/one.png" {
			t.Errorf("unexpected logo locator %q", locator)
		}
		return logo, nil
	}

	first, _, done, err := s.importLogosBatch(context.Background(), nil, source, "logo-worker-follow-one", "", 0)
	if err != nil || !done || first.Imported != 1 || first.Skipped != 0 || calls != 1 {
		t.Fatalf("following source: %+v done=%t calls=%d err=%v", first, done, calls, err)
	}
	// The stored logo is the one rendition made from the advertised image.
	if stored, mime, _, err := s.ChannelLogoBytes(context.Background(), "live:"+source+":"+channel); err != nil || mime != "image/png" || !bytes.Equal(stored, logoRendition(t, logo)) {
		t.Fatalf("stored logo mime=%q bytes=%d err=%v", mime, len(stored), err)
	}

	// The stored overwriteExisting is true, but while following the server the
	// already-imported logo is left alone.
	second, _, done, err := s.importLogosBatch(context.Background(), nil, source, "logo-worker-follow-two", "", 0)
	if err != nil || !done || second.Imported != 0 || second.Skipped != 1 || calls != 1 {
		t.Fatalf("following source replaced a logo: %+v done=%t calls=%d err=%v", second, done, calls, err)
	}

	// An explicit off still refuses even though the server allows imports.
	saveSourceLogoImport(t, s, source, "logo-worker-explicit-off", LogoImport{Enabled: false})
	third, _, done, err := s.importLogosBatch(context.Background(), nil, source, "logo-worker-explicit-off", "", 0)
	if err != nil || !done || third.Imported != 0 || calls != 1 {
		t.Fatalf("explicit off imported: %+v done=%t calls=%d err=%v", third, done, calls, err)
	}
	if third.Message != "Logo import is switched off for this source." {
		t.Fatalf("explicit off message: %q", third.Message)
	}
}

// logoRendition is the stored rendition of an advertised logo.
func logoRendition(t *testing.T, raw []byte) []byte {
	t.Helper()
	logo, err := metadata.EncodeChannelLogo(context.Background(), raw, map[string]bool{"jpeg": true, "png": true, "webp": true})
	if err != nil {
		t.Fatal(err)
	}
	return logo.Data
}
