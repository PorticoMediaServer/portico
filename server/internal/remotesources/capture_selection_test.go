package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Regression SOURCES for the optional playback capture preflight
// (ResolvePlaybackCaptureTx). The positive case is a weak direct WebDAV source
// on the real observation fixture: one ordinary movie asset, a saved cache
// policy, a real local owner family and device, and an injected content callback
// that records the exact item it admitted. The preflight must describe the exact
// target and must not reserve, charge, lease or allocate anything.

type selectionCall struct{ item string }

func playbackSelectionFixture(t *testing.T) (observationFixture, ObservationRef, *captureProducer, identity.Principal, string, *selectionCall) {
	t.Helper()
	f, ref := isoObservationFixture(t)
	identities, owner, family := observationOwner(t, f)
	config, err := json.Marshal(remotemedia.DAVConfig{
		Root:     "https://nas.example/media/",
		Approval: remotemedia.Approval{Root: "https://nas.example:443/", Addresses: []string{"192.168.42.7"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The private DAV opener is the real mounts service: its Open is the plain
	// bounded format, so no provider or helper process is involved.
	mountService, err := mounts.New(f.s.db, filepath.Join(t.TempDir(), "state"), t.TempDir(), "", storage.New("/unused/storage-helper"))
	if err != nil {
		t.Fatal(err)
	}
	f.s.mounts = mountService
	sealed, err := mountService.Seal(config)
	if err != nil {
		t.Fatal(err)
	}
	// remote_sources.config is a STRICT BLOB column: the sealed bytes are bound
	// as-is, never converted to TEXT.
	if _, err = f.s.db.Exec(`UPDATE remote_sources SET kind='webdav',config=?,range_support='unsupported',removed=0 WHERE id='remote'`, sealed); err != nil {
		t.Fatal(err)
	}
	advanced := defaultAdvanced()
	advanced.CacheBytes, advanced.CacheFloorBytes = 1<<30, 1<<30
	if _, err = f.s.db.Exec(`INSERT INTO remote_source_settings(source_id,revision,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,cache_bytes,cache_floor_bytes,cache_applied_bytes,cache_applied_floor_bytes,seek_previews,chapter_images,waveforms,loudness,checksums,intros_mode,double_check_folders,dir_cache_seconds,read_buffer_bytes,connect_timeout_seconds,idle_timeout_seconds,retries,low_level_retries)
 VALUES('remote',1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		advanced.ScanIntervalSeconds, advanced.WatchForChanges, advanced.ScanOnStart, advanced.FullRecheckDays,
		advanced.CacheBytes, advanced.CacheFloorBytes, advanced.CacheBytes, advanced.CacheFloorBytes,
		advanced.SeekPreviews, advanced.ChapterImages, advanced.Waveforms, advanced.Loudness, advanced.Checksums,
		advanced.IntrosMode, advanced.DoubleCheckFolders, advanced.DirCacheSeconds, advanced.ReadBufferBytes,
		advanced.ConnectTimeoutSeconds, advanced.IdleTimeoutSeconds, advanced.Retries, advanced.LowLevelRetries); err != nil {
		t.Fatal(err)
	}
	// One ordinary movie and its link, with the exact object revision the scan
	// observation recorded: the target names a real playable title.
	revision := f.catalogObservationRevision(t)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available) VALUES('disc-asset',?,16,23,'iso','h264','aac',1920,1080,60,1)`, []any{f.file}},
		{`INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head) SELECT X'00112233445566778899AABBCCDDEEFF',id,(SELECT id FROM catalog_kinds WHERE name='movie'),'Disc Film','disc film','d' FROM catalog_libraries WHERE library_id=?`, []any{f.library}},
		{`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT e.id,a.id FROM catalog_entities e JOIN catalog_assets a ON a.token='disc-asset' JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=X'00112233445566778899AABBCCDDEEFF' AND cl.library_id=?`, []any{f.library}},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,unsupported_reason) SELECT 'disc-object',id,'disc-asset',incarnation,'disc.iso',?,'{}',16,23,'available','' FROM library_sources WHERE id=?`, []any{revision, f.library}},
	} {
		result, err := f.s.db.Exec(statement.sql, statement.args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("seed affected %d rows, want exactly one: %s", n, statement.sql)
		}
	}
	call := &selectionCall{}
	producer := &captureProducer{sources: f.s, identities: identities, authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
		call.item = item
		return p, nil
	}}
	// Each negative starts from a source that actually resolves, so an unrelated
	// fixture defect cannot stand in for the mutation under test.
	if _, err = resolveSelection(t, f, producer, owner, selectionID); err != nil {
		t.Fatalf("initial source preflight: %v", err)
	}
	call.item = ""
	return f, ref, producer, owner, family, call
}

func resolveSelection(t *testing.T, f observationFixture, p *captureProducer, owner identity.Principal, id string) (ownedcapture.PlaybackCaptureSource, error) {
	t.Helper()
	asset := selectionAsset(t, f)
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	return p.ResolvePlaybackCaptureTx(context.Background(), view.Tx(), id, ownedcapture.Request{Principal: owner, AssetID: asset})
}

func selectionAsset(t *testing.T, f observationFixture) string {
	t.Helper()
	var asset string
	if err := f.s.db.QueryRow(`SELECT asset_id FROM inventory_objects WHERE id='disc-object'`).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	return asset
}

const selectionID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The preflight names the same physical/policy target the reservation would
// use, under one real owner family, and does no source work at all.
func TestPlaybackCaptureSelectionNamesTheExactTargetWithoutSourceWork(t *testing.T) {
	f, ref, p, owner, family, call := playbackSelectionFixture(t)
	var target captureTarget
	asset := selectionAsset(t, f)
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	target, err = p.resolveTargetTx(context.Background(), view.Tx(), asset)
	view.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	source, err := resolveSelection(t, f, p, owner, selectionID)
	if err != nil {
		t.Fatal(err)
	}
	if call.item != target.itemID {
		t.Fatalf("content callback admitted %q, want the target item %q", call.item, target.itemID)
	}
	if source.LibraryID != target.libraryID || source.LibrarySourceID != target.librarySourceID || source.AssetID != target.assetID ||
		source.InventoryObjectID != target.inventoryObjectID || source.InventoryRevision != target.inventoryRevision ||
		source.RootIdentity != target.rootIdentity || source.Incarnation != target.incarnation ||
		source.RemoteSourceID != target.sourceID || source.EffectiveConfiguration != target.configGeneration ||
		source.LibraryGeneration != target.libraryGeneration || source.RawConfigurationGeneration != target.sourceGeneration ||
		source.ProfileRevision != target.profile.Revision || source.Size != target.size {
		t.Fatalf("preflight %+v does not describe the resolved target", source)
	}
	if source.ReadPolicyWitness != capturePolicyWitness(&target) {
		t.Fatal("preflight policy witness does not match the target policy")
	}
	if source.ReadAuthorityWitness != selectionID {
		t.Fatalf("preflight authority witness %q, want the candidate id", source.ReadAuthorityWitness)
	}
	if ref.SourceID == "" || source.ModifiedNS != 23 || target.size != 16 {
		t.Fatalf("preflight modified time %d and target size %d, want the observed 23 and 16", source.ModifiedNS, target.size)
	}
	// The family the admission uses is the original issued one, with this device.
	if family == "" || owner.DeviceID == "" {
		t.Fatal("fixture did not issue a real family and device")
	}
	// Nothing was reserved, charged, leased or opened for a mere preflight.
	for _, table := range []string{"remote_source_captures", "remote_source_capture_physical", "remote_source_capture_actor", "remote_source_capture_policy", "remote_source_capture_quota"} {
		var n int
		if err = f.s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s gained %d rows from a preflight", table, n)
		}
	}
	if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
		t.Fatal("a preflight allocated a source process or read lease")
	}
}

// Two distinct candidate ids select the same physical and policy tuple; only the
// authority witness carries the candidate.
func TestPlaybackCaptureSelectionIsIndependentOfTheCandidateID(t *testing.T) {
	f, _, p, owner, _, _ := playbackSelectionFixture(t)
	first, err := resolveSelection(t, f, p, owner, selectionID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveSelection(t, f, p, owner, "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	if err != nil {
		t.Fatal(err)
	}
	first.ReadAuthorityWitness, second.ReadAuthorityWitness = "", ""
	if first != second {
		t.Fatalf("candidate id changed the selected target:\n%+v\n%+v", first, second)
	}
}

// Every currentness failure refuses instead of describing a target. Each
// mutation lands on a closed database state, and every resolve reads a fresh
// snapshot, so a refusal can never be hidden behind an old view.
func TestPlaybackCaptureSelectionRefusesStaleAuthorityAndInventory(t *testing.T) {
	badID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde" // 63 hex, not a capture id
	f, _, p, owner, family, _ := playbackSelectionFixture(t)
	if _, err := p.ResolvePlaybackCaptureTx(context.Background(), nil, selectionID, ownedcapture.Request{Principal: owner, AssetID: selectionAsset(t, f)}); !errors.Is(err, ownedcapture.ErrInput) {
		t.Fatalf("a nil transaction was accepted: %v", err)
	}
	if _, err := resolveSelection(t, f, p, owner, badID); !errors.Is(err, ownedcapture.ErrInput) {
		t.Fatalf("a short capture id was accepted: %v", err)
	}
	asset := selectionAsset(t, f)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ResolvePlaybackCaptureTx(canceled, view.Tx(), selectionID, ownedcapture.Request{Principal: owner, AssetID: asset})
	view.Rollback()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled context was accepted: %v", err)
	}
	// A refusing content callback is a refusal, never a described target.
	refusing := *p
	refusing.authorize = func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error) {
		return identity.Principal{}, identity.ErrForbidden
	}
	if _, err = resolveSelection(t, f, &refusing, owner, selectionID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("a refusing content callback was accepted: %v", err)
	}
	// A revoked original family refuses before any target is described.
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if _, err = resolveSelection(t, f, p, owner, selectionID); !errors.Is(err, ownedcapture.ErrCurrent) {
		t.Fatalf("a revoked family was accepted: %v", err)
	}
}

// A changed inventory revision refuses on its own fixture.
func TestPlaybackCaptureSelectionRefusesChangedInventory(t *testing.T) {
	f, _, p, owner, _, _ := playbackSelectionFixture(t)
	if _, err := f.s.db.Exec(`UPDATE inventory_objects SET revision='changed' WHERE id='disc-object'`); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSelection(t, f, p, owner, selectionID); err == nil {
		t.Fatal("a changed inventory revision was accepted")
	}
}

// A disabled source refuses on its own fixture, not masked by another refusal.
func TestPlaybackCaptureSelectionRefusesDisabledSource(t *testing.T) {
	f, _, p, owner, _, _ := playbackSelectionFixture(t)
	if _, err := f.s.db.Exec(`UPDATE library_sources SET enabled=0 WHERE remote_source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSelection(t, f, p, owner, selectionID); err == nil {
		t.Fatal("a disabled source was accepted")
	}
}

// A saved read-profile revision is a source-issued witness, not a refusal of the
// original ticket: the preflight describes the new profile and still reserves
// nothing.
func TestPlaybackCaptureSelectionReportsAChangedReadProfileWitness(t *testing.T) {
	f, _, p, owner, _, _ := playbackSelectionFixture(t)
	before, err := resolveSelection(t, f, p, owner, selectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'complete',1,4,5,0)`); err != nil {
		t.Fatal(err)
	}
	after, err := resolveSelection(t, f, p, owner, selectionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProfileRevision != 1 || after.ProfileRevision == before.ProfileRevision {
		t.Fatalf("profile revision %d (was %d), want the new source-issued revision", after.ProfileRevision, before.ProfileRevision)
	}
	if after.ReadPolicyWitness == before.ReadPolicyWitness {
		t.Fatal("the source-issued policy witness did not change with the profile")
	}
	for _, table := range []string{"remote_source_captures", "remote_source_capture_physical", "remote_source_capture_actor", "remote_source_capture_policy"} {
		var n int
		if err = f.s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s gained %d rows from a preflight", table, n)
		}
	}
}
