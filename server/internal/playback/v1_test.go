package playback

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Review P4: remote caps (the member's and the server-wide one) apply only to a
// remote request; a LAN viewer is never capped or told "admin_cap_remote_bitrate".
func TestV1ChoiceAppliesRemoteCapsOnlyRemotely(t *testing.T) {
	cfg := DeliveryConfiguration{MaxVideoBitrateBPS: 4_000_000}
	lan := V1Choice{AdminMaxVideoBitrateBPS: 2_000_000}
	if got := lan.target(ResolvedDeliveryPolicy{NetworkClass: NetworkLocal}, cfg); got.MaxVideoBitrateBPS != 0 {
		t.Fatalf("LAN original capped at %d", got.MaxVideoBitrateBPS)
	}
	if lan.AdminCapped() {
		t.Fatal("LAN request reported as admin-capped")
	}
	remote := V1Choice{AdminMaxVideoBitrateBPS: 2_000_000, Remote: true}
	if got := remote.target(ResolvedDeliveryPolicy{}, cfg); got.MaxVideoBitrateBPS != 2_000_000 || !remote.AdminCapped() {
		t.Fatalf("remote member cap: %d", got.MaxVideoBitrateBPS)
	}
	server := V1Choice{Remote: true, Limit: true, MaxVideoBitrateBPS: 20_000_000}
	if got := server.target(ResolvedDeliveryPolicy{}, cfg); got.MaxVideoBitrateBPS != 4_000_000 {
		t.Fatalf("server-wide remote limit: %d", got.MaxVideoBitrateBPS)
	}
	away := V1Choice{}
	if got := away.target(ResolvedDeliveryPolicy{NetworkClass: NetworkRemote}, cfg); got.MaxVideoBitrateBPS != 4_000_000 {
		t.Fatalf("a policy the edge called remote is remote: %d", got.MaxVideoBitrateBPS)
	}
	// A named quality is its ceilings, and the plan keeps its name.
	fixed, ok := namedQualityChoice("720p-high")
	if got := fixed.target(ResolvedDeliveryPolicy{NetworkClass: NetworkLocal}, cfg); !ok || got.RungID != "720p-high" || got.MaxVideoHeight != 720 || got.MaxVideoBitrateBPS != 4_000_000 || got.Kind != QualityAutomatic {
		t.Fatalf("fixed quality %+v", got)
	}
	if _, ok = namedQualityChoice("900p"); ok {
		t.Fatal("an unpublished quality was accepted")
	}
	limited := V1Choice{Limit: true, MaxVideoBitrateBPS: 1_000_000, MaxVideoHeight: 720}
	if got := limited.target(ResolvedDeliveryPolicy{NetworkClass: NetworkLocal}, cfg); got.MaxVideoBitrateBPS != 1_000_000 || got.MaxVideoHeight != 720 || got.Kind != QualityAutomatic {
		t.Fatalf("client limit %+v", got)
	}
}

func TestSelectedAssetReturnsTheScannedVersion(t *testing.T) {
	db, service, _, _, item, entity := activityFixture(t)
	defer db.Close()
	first := firstCatalogAssetToken(t, db)
	if got, err := service.SelectedAsset(context.Background(), item, nil); err != nil || got != first {
		t.Fatalf("default asset %q, want %q: %v", got, first, err)
	}
	var second string
	catalogtest.New(t, db).Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/media/second.mkv", Size: 10, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 60})
		if err != nil {
			return err
		}
		second = token
		return compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{})
	})
	if got, err := service.SelectedAsset(context.Background(), item, &V1Choice{AssetID: second}); err != nil || got != second {
		t.Fatalf("explicit asset %q, want %q: %v", got, second, err)
	}
	if got, err := service.SelectedAsset(context.Background(), item, &V1Choice{AssetID: "absent"}); !errors.Is(err, sql.ErrNoRows) || got != "" {
		t.Fatalf("absent asset %q: %v", got, err)
	}
}
