package playback

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestDeferredFramesInvalidateOffersAndChangeDeliveryWithoutRescan(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, asset := catalogFixture(t, db, "frames", "/frames", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/frames/film.mp4", Size: 1000, ModifiedNS: 1, Container: "mp4", VideoCodec: "hevc", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return assets.PersistStreams(tx, asset, 1000, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "hevc", ColorTransfer: "smpte2084", Detail: assets.StreamDetail{DynamicRange: assets.RangeHDR10, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 10, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive}}}, {Index: 1, Type: "audio", Codec: "aac", Channels: 2, Detail: assets.StreamDetail{SampleRate: 48000}}})
	})
	c.Drain()
	p := identity.Principal{Hash: "fixture-session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	service := New(db)
	scope := OffersScope{"server", "frames", item, "fence"}
	before, err := service.Offers(ctx, p, scope, "", "")
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadDeliverySource(ctx, db, asset)
	if err != nil {
		t.Fatal(err)
	}
	firstPlan := decide(t, &source, appleTVProfile(), nil)
	if firstPlan.Deinterlace || source.Video.DynamicRange != assets.RangeHDR10 || source.Video.HDR10Plus || source.Video.FrameRateMode != "" {
		t.Fatal("header-only answer lost", firstPlan, source)
	}
	cat := catalog.New(db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return catalog.ApplyScanPolicyTx(ctx, tx, "frames", catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", catalog.PlaybackFramesOperation}})
	})
	policy, err := cat.ScanPolicy(ctx, "frames")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE library_sources SET incarnation='frame-inc',root_identity='frame-root',generation=1,enabled=1,identity_confirmed=1 WHERE library_id='frames'`)
	var sid string
	if err = db.QueryRow(`SELECT id FROM library_sources WHERE library_id='frames'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('frame-job','frames','running','fixture')`)
	c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES('frame-job',?,1,'frame-inc','frame-root',?)`, sid, policy.Revision)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,analysis_revision) VALUES('frame-object',?,?,'frame-inc','film.mp4','unchanged','{}',1000,1,'available','unchanged')`, sid, asset)
	input, err := cat.InventoryStreams(ctx, "frame-job", "frame-object", "unchanged", policy.Revision, catalog.PlaybackFramesOperation)
	if err != nil {
		t.Fatal(err)
	}
	if err = assets.ApplyFrameCadence([]byte(`{"frames":[{"best_effort_timestamp_time":"0.00","interlaced_frame":1},{"best_effort_timestamp_time":"0.04","interlaced_frame":1}]}`), &input.Streams[0].Detail); err != nil {
		t.Fatal(err)
	}
	if err = assets.ApplyFrameHDR([]byte(`{"frames":[{"media_type":"video","width":1920,"height":1080,"side_data_list":[{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}]}]}`), &input.Streams[0].Detail); err != nil {
		t.Fatal(err)
	}
	input.Streams[0].Detail.PlaybackFramesRevision = assets.PlaybackFramesRevision
	receipt, err := cat.AcceptInventoryStreams(ctx, input)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal(receipt, err)
	}
	c.Drain()
	if _, err = service.Offers(ctx, p, scope, "", before.Revision); !errors.Is(err, ErrStaleOffer) {
		t.Fatal("old playback options were not invalidated", err)
	}
	after, err := service.Offers(ctx, p, scope, "", "")
	if err != nil || after.Revision == before.Revision || after.Sources[0].FactsRevision <= before.Sources[0].FactsRevision {
		t.Fatal("technical generation did not advance", before, after, err)
	}
	source, err = loadDeliverySource(ctx, db, asset)
	if err != nil {
		t.Fatal(err)
	}
	nextPlan := decide(t, &source, appleTVProfile(), nil)
	if !nextPlan.Deinterlace || !source.Video.HDR10Plus || source.Video.DynamicRange != assets.RangeHDR10 {
		t.Fatal("new delivery did not use deferred facts", nextPlan, source)
	}
	var revision string
	var size, modified int64
	if err = db.QueryRow(`SELECT o.revision,a.size,a.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id='frame-object'`).Scan(&revision, &size, &modified); err != nil || revision != "unchanged" || size != 1000 || modified != 1 {
		t.Fatal("test depended on rescan", revision, size, modified, err)
	}
}
