package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestRegistryDeliveryReadsNormalizedMusicAndFilePolicy(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := os.WriteFile(path, []byte("[streaming]\nmusic_away_quality='96'\nallow_conversion='sound'\noutput_format='h264'\n[downloads]\nmax_quality='720p-high'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.Console.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	adapter := &RegistryDeliverySettings{Console: f.deps.Console}
	adapter.Refresh(ctx)
	delivery := adapter.Delivery()
	if delivery.MaxMusicQuality != "96" || delivery.ConversionMode != "sound" || delivery.OutputFormat != "h264" || delivery.DownloadMaxQuality != "720p-high" {
		t.Fatal("imported typed policy did not reach the next delivery read")
	}
	doc, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.ConversionMode = "never"
	values.MusicAwayQuality = "320"
	owner := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "owner-profile", Role: "owner"}}
	if _, err = f.deps.Console.ApplySettings(ctx, owner, operations.AllowServerScope, operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "owner-music-mode", Values: values}); err != nil {
		t.Fatal(err)
	}
	adapter.Refresh(ctx)
	delivery = adapter.Delivery()
	if delivery.MaxMusicQuality != "320" || delivery.ConversionMode != "never" || adapter.Transcoding(ctx).Enabled {
		t.Fatal("committed current policy was replaced by legacy/default policy")
	}
}
