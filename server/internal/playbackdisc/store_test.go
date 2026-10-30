//go:build linux || darwin

package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Regression SOURCES only during campaign work. These drive the real gated
// writer, the baseline schema and the canonical disc decoder; the fixture rows
// are test evidence, not production authority for a presentation.
func init() { persistence.UseSchemaTemplate() }

const (
	discServerID      = "server-disc-test"
	discSessionID     = "session-disc-test"
	discAccountID     = "account-disc-test"
	discProfileID     = "profile-disc-test"
	discLibraryID     = "library-disc-test"
	discLibrarySource = "source-disc-test"
	discRemoteSource  = "remote-disc-test"
	discObjectID      = "object-disc-test"
	discAssetID       = "asset-disc-test"
	discRequestID     = "request-disc-test"
	discQueueID       = "queue-disc-test"
	discQueueEntry    = "queue-entry-disc-test"
	discSize          = int64(5_000_000_000)
	discModifiedNS    = int64(1_700_000_000_000_000_000)
	discInventoryRev  = "inventory-revision-test"
)

type discFixture struct {
	t         *testing.T
	db        *sql.DB
	store     *Store
	version   mediasource.Version
	snapshot  Snapshot
	publicPID string
	// until is the exact deadline PublishPendingTx must store: the earlier of
	// the session's own expiry and the original measurement horizon.
	until int64
}

func newDiscFixture(t *testing.T) *discFixture {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "playbackdisc.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := New(discServerID, nil)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Exec(query, args...); e != nil {
			t.Fatalf("seed fixture: %v", e)
		}
	}
	// One canonical catalogue item and one asset whose physical facts the source
	// witness repeats. The entity's public id is a real 16-byte stored id, not a
	// fabricated pid_text column.
	publicID := make([]byte, entityid.Size)
	publicID[0], publicID[entityid.Size-1] = 0x51, 0x9e
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,'Discs','movie','/discs')`, discLibraryID)
	var catalogLibraryID int64
	if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, discLibraryID).Scan(&catalogLibraryID); err != nil {
		t.Fatalf("read catalogue library handle: %v", err)
	}
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,year) VALUES(7,?,?,1,'Dune: Part Two','Dune Part Two','dune part two','D',2024)`, publicID, catalogLibraryID)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available) VALUES(11,?,'/discs/dune.iso',?,?,'iso','hevc','truehd',1920,1080,5400.0,1)`, discAssetID, discSize, discModifiedNS)
	exec(`INSERT INTO remote_sources(id,kind,name,root,generation,state,range_support,removed) VALUES(?,'webdav','Discs','/discs',9,'ready','conditional',0)`, discRemoteSource)
	// Replace the trigger-created local folder with this fixture's one remote folder.
	exec(`DELETE FROM library_sources WHERE id=? AND library_id=?`, discLibraryID, discLibraryID)
	exec(`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,identity_confirmed,health,enabled,remote_source_id) VALUES(?,?,'remote','Discs','/discs','/discs','network',12,'incarnation-test','root-identity-test',1,'healthy',1,?)`, discLibrarySource, discLibraryID, discRemoteSource)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,retired,unsupported_reason) VALUES(?,?,?,'incarnation-test','Dune.iso',?,'{}',?,?,'available',0,'')`, discObjectID, discLibrarySource, discAssetID, discInventoryRev, discSize, discModifiedNS)
	exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,allow_advanced_media_analysis,connections,requests_per_second,bytes_per_second) VALUES(?,4,'basic',0,0,4,8,0)`, discRemoteSource)
	exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES(?,'session-hash-test',?,?,7,?,1,'playing','grant-hash-test','grant-token-test',?,?,5400.0)`, discSessionID, discAccountID, discProfileID, discAssetID, expires, discRequestID)
	version, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: "disc-provider-scope", Object: "disc-provider-object", Revision: `"disc-etag-v1"`, Size: discSize})
	if err != nil {
		t.Fatalf("conditional version: %v", err)
	}
	f := &discFixture{t: t, db: db, store: store, version: version, until: time.Now().Add(30 * time.Minute).UnixMilli(), publicPID: entityid.Encode(publicID)}
	f.snapshot = f.candidate()
	return f
}

// candidate is one exact measured presentation: a valid probe, every planning
// stream fact, two ordered titles and a queue-bound legacy request.
func (f *discFixture) candidate() Snapshot {
	one, two := 1, 2
	structure := disc.Probe{
		Schema: disc.Schema, Kind: "bluray", Packaging: "iso", Encryption: "none",
		Titles: []disc.Title{
			{DiscOrder: 0, Playlist: &one, PlaylistFile: "00001.mpls", DurationMs: 5_400_000, AngleCount: 1,
				Clips:    []disc.Clip{{Sequence: 0, File: "00001.m2ts", InTime90k: 0, OutTime90k: 90_000}, {Sequence: 1, File: "00002.m2ts", InTime90k: 0, OutTime90k: 90_000}},
				Streams:  []disc.Stream{{ClipSequence: 0, PID: 4113, Kind: "video", Coding: 0x24, Language: "eng", VideoRole: "primary"}},
				Chapters: []disc.Chapter{{StartMs: 0}, {StartMs: 60_000}}},
			{DiscOrder: 1, Playlist: &two, PlaylistFile: "00002.mpls", DurationMs: 3_600_000, AngleCount: 1,
				Clips:    []disc.Clip{{Sequence: 0, File: "00003.m2ts", InTime90k: 0, OutTime90k: 90_000}},
				Streams:  []disc.Stream{},
				Chapters: []disc.Chapter{}},
		},
	}
	return Snapshot{
		ID: "snapshot-disc-test", SessionID: discSessionID, Generation: 1,
		Actor: Actor{
			ServerID: discServerID, Authority: "profile", AccountID: discAccountID, ProfileID: discProfileID,
			DeviceID: "device-disc-test", FamilyID: "family-disc-test", Grant: "grant-disc-test",
			SessionHash: "session-hash-test", Epoch: 2, TokenGeneration: 3, OriginalHorizonMs: f.until,
		},
		Operation: Operation{
			LegacyRequestID: discRequestID,
			StartKeyDigest:  strings.Repeat("a", 64), StartBodyDigest: strings.Repeat("b", 64),
			QueueID: discQueueID, QueueEntryID: discQueueEntry, QueueOwnerKind: "profile",
			QueueOwnerID: discProfileID, QueueRevision: 5, QueueCustodyGeneration: 6,
		},
		Source: Source{
			LibraryID: discLibraryID, LibrarySourceID: discLibrarySource, AssetID: discAssetID,
			InventoryObjectID: discObjectID, InventoryRevision: discInventoryRev,
			RootIdentity: "root-identity-test", Incarnation: "incarnation-test",
			RemoteSourceID: discRemoteSource, EffectiveConfiguration: "9",
			ReadPolicyWitness: "policy-witness-test", ReadAuthorityWitness: "authority-witness-test",
			LibraryGeneration: 12, RawConfigurationGeneration: 9, ProfileRevision: 4,
			Size: discSize, ModifiedNS: discModifiedNS,
		},
		Input: Input{Conditional: &Conditional{Version: f.version}},
		Disc: DiscFacts{
			Structure: structure, SelectedTitleKey: structure.Titles[0].Key(structure.Kind),
			StructureRevision: "structure-revision-test", StreamRevision: "stream-revision-test",
			ChapterRevision: "chapter-revision-test", FactsRevision: "facts-revision-test",
		},
		Media: MediaFacts{
			Container: "mpegts", VideoCodec: "hevc", AudioCodec: "truehd",
			Width: 3840, Height: 2160, DurationMs: 5_400_000, OriginUS: -6_000_000, TimingKnown: true,
			Streams: discStreams(),
			Chapters: []assets.Chapter{
				{Title: "Opening", Start: 0, End: 60},
				{Title: "Second", Start: 60, End: 120},
			},
		},
		Tools: ToolPins{
			DiscHelperDigest: strings.Repeat("c", 64), FFmpegDigest: strings.Repeat("d", 64),
			FFprobeDigest: strings.Repeat("e", 64), RuntimeIdentity: "portico-disc-runtime-test",
		},
	}
}

// discStreams sets every planning field, breadth included, so a lost column is
// visible as a roundtrip difference rather than a silent zero.
func discStreams() []assets.Stream {
	return []assets.Stream{
		{
			Index: 0, Type: "video", Codec: "hevc", Language: "eng", Title: "Main",
			Default: true, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020",
			Detail: assets.StreamDetail{
				StreamBreadth: assets.StreamBreadth{
					AudioVariant: "truehd_atmos", ChromaSubsampling: "yuv420p10le",
					FrameRateNumerator: 24000, FrameRateDenominator: 1001, FrameRateMode: "cfr",
					InterlaceAnalysis: "progressive",
					DolbyVisionRPU:    true, DolbyVisionBaseLayer: true, DolbyVisionMetadataKnown: true,
					DolbyVisionEnhancementType: "mel", DolbyVisionCMVersion: "dvhe.08.06",
				},
				MaxBitRate: 80_000_000, HDR10Plus: true, Profile: "Main 10", Level: 51,
				PixelFormat: "yuv420p10le", BitDepth: 10, Width: 3840, Height: 2160,
				FrameRate: 23.976, Interlaced: false, Rotation: 0, SampleAspectRatio: "1:1",
				Anamorphic: false, RefFrames: 4, CodecTag: "hvc1", BitRate: 75_000_000,
				SampleRate: 48_000, BitsPerSample: 24, DynamicRange: assets.RangeDolbyVision,
				DolbyVisionProfile: 8, DolbyVisionLevel: 6, DolbyVisionCompatibility: 1,
				MasterDisplay: "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1)",
				MaxCLL:        "1000,400", ObjectAudio: "atmos",
			},
		},
		{
			Index: 1, Type: "audio", Codec: "truehd", Language: "eng", Title: "Atmos",
			Channels: 8, ChannelLayout: "7.1", Default: true, HearingImpaired: true,
			Detail: assets.StreamDetail{
				Profile: "Dolby TrueHD + Atmos", SampleRate: 48_000, BitsPerSample: 24,
				BitRate: 4_000_000, MaxBitRate: 4_500_000, ObjectAudio: "atmos",
			},
		},
		{
			Index: 2, Type: "subtitle", Codec: "pgs", Language: "eng", Forced: true, Commentary: true,
			Detail: assets.StreamDetail{CodecTag: "pgs "},
		},
	}
}

func plainCheck() Check {
	return func(context.Context, *sql.Tx, Snapshot) error { return nil }
}

func (f *discFixture) inWrite(class dbwork.Class, fn func(context.Context, *sql.Tx) error) error {
	f.t.Helper()
	return dbwork.WithWriteTxContext(context.Background(), f.db, class, fn)
}

func (f *discFixture) publish(snap Snapshot, check Check) error {
	f.t.Helper()
	return f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.PublishPendingTx(ctx, tx, snap, check)
	})
}

func (f *discFixture) activate(key Key, id string, check Check) (Snapshot, error) {
	f.t.Helper()
	var out Snapshot
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.ActivateTx(ctx, tx, key, id, check)
		return e
	})
	return out, err
}

func (f *discFixture) resolve(key Key, id string, check Check) (Snapshot, error) {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return f.store.ResolveTx(ctx, tx, key, id, check)
}

func (f *discFixture) retire(key Key, id string) error {
	f.t.Helper()
	return f.inWrite(dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.RetireTx(ctx, tx, key, id)
	})
}

func (f *discFixture) pin(key Key, id, pinID string, expires time.Time, check Check) (Pin, error) {
	f.t.Helper()
	var pin Pin
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		pin, e = f.store.PinTx(ctx, tx, key, id, pinID, expires, check)
		return e
	})
	return pin, err
}

func (f *discFixture) release(pin Pin) error {
	f.t.Helper()
	return f.inWrite(dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.ReleasePinTx(ctx, tx, pin)
	})
}

func (f *discFixture) collect(after string, limit int) (string, int, error) {
	f.t.Helper()
	var next string
	var collected int
	err := f.inWrite(dbwork.ClassMaintenance, func(ctx context.Context, tx *sql.Tx) (e error) {
		next, collected, e = f.store.CollectTx(ctx, tx, after, limit)
		return e
	})
	return next, collected, err
}

func (f *discFixture) exec(query string, args ...any) error {
	f.t.Helper()
	_, err := f.db.Exec(query, args...)
	return err
}

func mustInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func mustText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func assertNoSnapshotRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM playback_disc_snapshots`,
		`SELECT count(*) FROM playback_disc_lifecycle`,
		`SELECT count(*) FROM playback_disc_snapshot_actor`,
		`SELECT count(*) FROM playback_disc_snapshot_operation`,
		`SELECT count(*) FROM playback_disc_snapshot_source`,
		`SELECT count(*) FROM playback_disc_conditional_inputs`,
	} {
		if n := mustInt(t, db, query); n != 0 {
			t.Fatalf("%s = %d after a refused write, want 0", query, n)
		}
	}
}

// TestPublishRefusesUngatedWriters holds the accepting-writer gate: no snapshot
// identity, lifecycle or child row may appear from an untracked, read-only or
// snapshot transaction, and a refused call must not leave partial rows.
func TestPublishRefusesUngatedWriters(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot

	if _, err := New("", nil); !errors.Is(err, ErrInput) {
		t.Fatalf("New with no server id = %v, want ErrInput", err)
	}
	if _, err := New("server\tcontrol", nil); !errors.Is(err, ErrInput) {
		t.Fatalf("New with a control character = %v, want ErrInput", err)
	}
	var absent *Store
	if err := absent.PublishPendingTx(context.Background(), nil, snap, plainCheck()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil store = %v, want ErrInput", err)
	}
	if err := f.store.PublishPendingTx(nil, nil, snap, plainCheck()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil context = %v, want ErrInput", err)
	}
	if err := f.store.PublishPendingTx(context.Background(), nil, snap, plainCheck()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil transaction = %v, want ErrInput", err)
	}
	invalid := snap
	invalid.Generation = 0
	if err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.PublishPendingTx(ctx, tx, invalid, plainCheck())
	}); !errors.Is(err, ErrInput) {
		t.Fatalf("generation zero = %v, want ErrInput", err)
	}

	untracked, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin untracked: %v", err)
	}
	if err = f.store.PublishPendingTx(context.Background(), untracked, snap, plainCheck()); err == nil {
		t.Fatal("an untracked transaction was accepted")
	}
	_ = untracked.Rollback()

	readonly, err := f.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only: %v", err)
	}
	if err = f.store.PublishPendingTx(context.Background(), readonly, snap, plainCheck()); err == nil {
		_ = readonly.Rollback()
		t.Fatal("a read-only transaction was accepted")
	}
	_ = readonly.Rollback()

	err = dbwork.WithReadSnapshot(context.Background(), f.db, func(ctx context.Context) error {
		return f.store.PublishPendingTx(ctx, dbwork.Snapshot(ctx), snap, plainCheck())
	})
	if err == nil {
		t.Fatal("a read snapshot context was accepted as an accepting writer")
	}
	assertNoSnapshotRows(t, f.db)
}

// TestPublishRequiresOriginalCheck keeps the mandatory original proof: an absent
// or refusing Check publishes and activates nothing, and leaves a held candidate
// pending rather than silently sealing or approving it.
func TestPublishRequiresOriginalCheck(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	if err := f.publish(snap, nil); !errors.Is(err, ErrInput) {
		t.Fatalf("publish with nil check = %v, want ErrInput", err)
	}
	refused := errors.New("original playback measurement refused")
	if err := f.publish(snap, func(context.Context, *sql.Tx, Snapshot) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("publish with refusing check = %v, want %v", err, refused)
	}
	assertNoSnapshotRows(t, f.db)

	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, nil); !errors.Is(err, ErrInput) {
		t.Fatalf("activate with nil check = %v, want ErrInput", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, func(context.Context, *sql.Tx, Snapshot) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("activate with refusing check = %v, want %v", err, refused)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "pending" {
		t.Fatalf("refused activation changed state to %q", state)
	}
	if sealed := mustInt(t, f.db, `SELECT sealed FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); sealed != 1 {
		t.Fatalf("refused activation changed sealed to %d", sealed)
	}
}

// TestPublishRoundTripActivateAndResolve covers the single happy path and the
// exact values it must retain: queue, request, source witness, ordering and every
// planning stream field, plus the nil-versus-empty chapter state.
func TestPublishRoundTripActivateAndResolve(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	want, err := freeze(snap)
	if err != nil {
		t.Fatalf("freeze candidate: %v", err)
	}
	if err = f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if stored := mustText(t, f.db, `SELECT typeof(public_id) FROM catalog_entities WHERE id=7`); stored != "blob" {
		t.Fatalf("public_id typeof = %q, want the stored blob", stored)
	}
	if raw := mustInt(t, f.db, `SELECT length(public_id) FROM catalog_entities WHERE id=7`); raw != int64(entityid.Size) {
		t.Fatalf("public_id length = %d, want %d", raw, entityid.Size)
	}
	if pid := mustText(t, f.db, `SELECT pid(public_id) FROM catalog_entities WHERE id=7`); pid != f.publicPID {
		t.Fatalf("pid(public_id) = %q, want the canonical %q", pid, f.publicPID)
	}
	if got := mustText(t, f.db, `SELECT input_kind FROM playback_disc_snapshots WHERE id=?`, snap.ID); got != "conditional" {
		t.Fatalf("input_kind = %q, want conditional", got)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_capture_inputs WHERE snapshot_id=?`, snap.ID); n != 0 {
		t.Fatalf("capture inputs = %d, want 0 for a conditional candidate", n)
	}
	var kind, scope, object, revision string
	var size int64
	if err = f.db.QueryRow(`SELECT kind,scope,object_id,revision,size FROM playback_disc_conditional_inputs WHERE snapshot_id=?`, snap.ID).Scan(&kind, &scope, &object, &revision, &size); err != nil {
		t.Fatalf("read conditional input: %v", err)
	}
	evidence := f.version.Evidence()
	if kind != string(evidence.Kind) || scope != evidence.Scope || object != evidence.Object || revision != evidence.Revision || size != evidence.Size {
		t.Fatalf("conditional input row = (%q,%q,%q,%q,%d), want the measured evidence", kind, scope, object, revision, size)
	}
	if got := mustText(t, f.db, `SELECT version_id FROM playback_disc_conditional_inputs WHERE snapshot_id=?`, snap.ID); got != f.version.ID() {
		t.Fatalf("version_id = %q, want %q", got, f.version.ID())
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "pending" {
		t.Fatalf("published state = %q, want pending", state)
	}
	if sealed := mustInt(t, f.db, `SELECT sealed FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); sealed != 1 {
		t.Fatalf("sealed = %d, want 1", sealed)
	}
	if created := mustInt(t, f.db, `SELECT created_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); created <= 0 {
		t.Fatalf("created_ms = %d, want a positive UnixMilli value", created)
	}
	if expires := mustInt(t, f.db, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); expires != f.until {
		t.Fatalf("expires_ms = %d, want the exact horizon %d", expires, f.until)
	}
	for _, column := range []string{"created_ms", "expires_ms"} {
		if typ := mustText(t, f.db, `SELECT typeof(`+column+`) FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); typ != "integer" {
			t.Fatalf("%s typeof = %q, want integer", column, typ)
		}
	}

	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_titles WHERE snapshot_id=?`, snap.ID); got != 2 {
		t.Fatalf("title rows = %d, want 2", got)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_clips WHERE snapshot_id=?`, snap.ID); got != 3 {
		t.Fatalf("clip rows = %d, want 3", got)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_disc_streams WHERE snapshot_id=?`, snap.ID); got != 1 {
		t.Fatalf("disc stream rows = %d, want 1", got)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_disc_chapters WHERE snapshot_id=?`, snap.ID); got != 2 {
		t.Fatalf("disc chapter rows = %d, want 2", got)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_streams WHERE snapshot_id=?`, snap.ID); got != int64(len(snap.Media.Streams)) {
		t.Fatalf("planning stream rows = %d, want %d", got, len(snap.Media.Streams))
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_chapters WHERE snapshot_id=?`, snap.ID); got != 2 {
		t.Fatalf("chapter rows = %d, want 2", got)
	}
	selectedKey := snap.Disc.Structure.Titles[0].Key(snap.Disc.Structure.Kind)
	if selectedKey == "" || selectedKey != snap.Disc.SelectedTitleKey {
		t.Fatalf("selected title key %q is not the probe title's own key", snap.Disc.SelectedTitleKey)
	}
	if got := mustText(t, f.db, `SELECT selected_title_key FROM playback_disc_snapshot_disc WHERE snapshot_id=?`, snap.ID); got != selectedKey {
		t.Fatalf("selected_title_key = %q, want the probe's own %q", got, selectedKey)
	}
	if got := mustText(t, f.db, `SELECT title_key FROM playback_disc_snapshot_titles WHERE snapshot_id=? AND disc_order=0`, snap.ID); got != selectedKey {
		t.Fatalf("first title key = %q, want the probe's own %q", got, selectedKey)
	}

	got, err := f.activate(snap.key(), snap.ID, plainCheck())
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activated snapshot differs from the measured candidate:\n got %#v\nwant %#v", got, want)
	}
	if got.Operation != want.Operation {
		t.Fatalf("operation = %#v, want the exact original request and queue values %#v", got.Operation, want.Operation)
	}
	if got.Media.Streams[0].Detail != want.Media.Streams[0].Detail {
		t.Fatalf("first stream detail did not roundtrip: got %#v", got.Media.Streams[0].Detail)
	}
	if got.Disc.Structure.Titles[0].PlaylistFile != "00001.mpls" || got.Disc.Structure.Titles[1].PlaylistFile != "00002.mpls" {
		t.Fatalf("structure order changed: %#v", got.Disc.Structure.Titles)
	}
	if len(got.Disc.Structure.Titles[0].Clips) != 2 || got.Disc.Structure.Titles[0].Clips[1].Sequence != 1 {
		t.Fatalf("clip order changed: %#v", got.Disc.Structure.Titles[0].Clips)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "active" {
		t.Fatalf("state after activation = %q, want active", state)
	}

	resolved, err := f.resolve(snap.key(), snap.ID, plainCheck())
	if err != nil {
		t.Fatalf("resolve active: %v", err)
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("resolved snapshot differs from the activated candidate")
	}
}

// TestPendingCannotResolveAndActivationNeedsExactBinding keeps a held candidate
// unreachable and binds activation to the exact presentation key and snapshot id.
func TestPendingCannotResolveAndActivationNeedsExactBinding(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := f.resolve(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("resolving a pending candidate = %v, want ErrCurrent", err)
	}
	for name, key := range map[string]Key{
		"another session":    {SessionID: "session-other", Generation: 1},
		"another generation": {SessionID: discSessionID, Generation: 2},
	} {
		if _, err := f.activate(key, snap.ID, plainCheck()); err == nil {
			t.Fatalf("activation with %s was accepted", name)
		}
	}
	if _, err := f.activate(snap.key(), "snapshot-other", plainCheck()); err == nil {
		t.Fatal("activation with another snapshot id was accepted")
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
		t.Fatalf("activate exact key: %v", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("second activation = %v, want ErrCurrent", err)
	}
	if _, err := f.resolve(Key{SessionID: discSessionID, Generation: 2}, snap.ID, plainCheck()); err == nil {
		t.Fatal("resolution with another generation was accepted")
	}
	if err := f.retire(snap.key(), snap.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("activation of a retired snapshot = %v, want ErrCurrent", err)
	}
	if _, err := f.resolve(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("resolution of a retired snapshot = %v, want ErrCurrent", err)
	}
}

// TestChaptersNilVersusEmptyRoundTrip keeps unobserved chapters distinct from an
// observed empty chapter list.
func TestChaptersNilVersusEmptyRoundTrip(t *testing.T) {
	f := newDiscFixture(t)
	absent := f.snapshot
	absent.Media.Chapters = nil
	if err := f.publish(absent, plainCheck()); err != nil {
		t.Fatalf("publish absent chapters: %v", err)
	}
	got, err := f.activate(absent.key(), absent.ID, plainCheck())
	if err != nil {
		t.Fatalf("activate absent chapters: %v", err)
	}
	if got.Media.Chapters != nil {
		t.Fatalf("unobserved chapters returned %#v, want nil", got.Media.Chapters)
	}
	if present := mustInt(t, f.db, `SELECT chapters_present FROM playback_disc_snapshot_media WHERE snapshot_id=?`, absent.ID); present != 0 {
		t.Fatalf("chapters_present = %d, want 0", present)
	}

	if err = f.exec(`UPDATE playback_sessions SET generation=2, session_hash='session-hash-two', request_id='request-disc-two', grant_hash='grant-hash-two' WHERE id=?`, discSessionID); err != nil {
		t.Fatalf("advance session generation: %v", err)
	}
	empty := f.snapshot
	empty.ID, empty.Generation = "snapshot-disc-empty-chapters", 2
	empty.Actor.SessionHash = "session-hash-two"
	empty.Operation.LegacyRequestID = "request-disc-two"
	empty.Media.Chapters = []assets.Chapter{}
	if err = f.publish(empty, plainCheck()); err != nil {
		t.Fatalf("publish empty chapters: %v", err)
	}
	got, err = f.activate(empty.key(), empty.ID, plainCheck())
	if err != nil {
		t.Fatalf("activate empty chapters: %v", err)
	}
	if got.Media.Chapters == nil || len(got.Media.Chapters) != 0 {
		t.Fatalf("observed empty chapters returned %#v, want a non-nil empty list", got.Media.Chapters)
	}
	if present := mustInt(t, f.db, `SELECT chapters_present FROM playback_disc_snapshot_media WHERE snapshot_id=?`, empty.ID); present != 1 {
		t.Fatalf("chapters_present = %d, want 1", present)
	}
}

// TestDuplicateBindingIsConflictNotReplacement refuses a second candidate for one
// session generation instead of replacing or selecting the latest.
func TestDuplicateBindingIsConflictNotReplacement(t *testing.T) {
	f := newDiscFixture(t)
	first := f.snapshot
	if err := f.publish(first, plainCheck()); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	duplicate := f.snapshot
	duplicate.ID = "snapshot-disc-duplicate"
	duplicate.Operation.QueueRevision = 9
	duplicate.Operation.QueueEntryID = "queue-entry-replacement"
	if err := f.publish(duplicate, plainCheck()); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate session generation = %v, want ErrConflict", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshots`); n != 1 {
		t.Fatalf("snapshots = %d, want the original only", n)
	}
	if id := mustText(t, f.db, `SELECT id FROM playback_disc_snapshots`); id != first.ID {
		t.Fatalf("retained snapshot = %q, want %q", id, first.ID)
	}
	if entry := mustText(t, f.db, `SELECT queue_entry_id FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, first.ID); entry != discQueueEntry {
		t.Fatalf("queue entry = %q, want the original %q", entry, discQueueEntry)
	}
	if revision := mustInt(t, f.db, `SELECT queue_revision FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, first.ID); revision != 5 {
		t.Fatalf("queue revision = %d, want the original 5", revision)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, first.ID); state != "pending" {
		t.Fatalf("state = %q, want the untouched pending candidate", state)
	}
}

// TestSnapshotRowsAreImmutable keeps held rows sealed: later updates, a late
// child row and deleting a retained snapshot are all refused by the schema.
func TestSnapshotRowsAreImmutable(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for name, statement := range map[string]struct {
		query string
		args  []any
	}{
		"actor update":             {`UPDATE playback_disc_snapshot_actor SET account_id='account-other' WHERE snapshot_id=?`, []any{snap.ID}},
		"source update":            {`UPDATE playback_disc_snapshot_source SET size=1 WHERE snapshot_id=?`, []any{snap.ID}},
		"snapshot identity update": {`UPDATE playback_disc_snapshots SET generation=9 WHERE id=?`, []any{snap.ID}},
		"late title insert":        {`INSERT INTO playback_disc_snapshot_titles(snapshot_id,disc_order,title_key,playlist,playlist_file,dvd_title,vts,pgc,duration_ms,angle_count) VALUES(?,2,'bd:00003',3,'00003.mpls',0,0,0,3600000,1)`, []any{snap.ID}},
		"late lifecycle seal":      {`UPDATE playback_disc_lifecycle SET sealed=0 WHERE snapshot_id=?`, []any{snap.ID}},
		"retained snapshot delete": {`DELETE FROM playback_disc_snapshots WHERE id=?`, []any{snap.ID}},
	} {
		if err := f.exec(statement.query, statement.args...); err == nil {
			t.Fatalf("%s was accepted after sealing", name)
		}
	}
	if account := mustText(t, f.db, `SELECT account_id FROM playback_disc_snapshot_actor WHERE snapshot_id=?`, snap.ID); account != discAccountID {
		t.Fatalf("actor account = %q, want %q", account, discAccountID)
	}
	if size := mustInt(t, f.db, `SELECT size FROM playback_disc_snapshot_source WHERE snapshot_id=?`, snap.ID); size != discSize {
		t.Fatalf("source size = %d, want %d", size, discSize)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshot_titles WHERE snapshot_id=?`, snap.ID); n != 2 {
		t.Fatalf("title rows = %d, want the original 2", n)
	}
}

// TestDriftRefused reproves actor, session, request, profile and source facts in
// the accepting transaction; any drift leaves the candidate pending.
func TestDriftRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	cases := []struct {
		name    string
		drift   string
		args    []any
		current bool
	}{
		{"account", `UPDATE playback_sessions SET account_id='account-other' WHERE id=?`, []any{discSessionID}, false},
		{"profile", `UPDATE playback_sessions SET profile_id='profile-other' WHERE id=?`, []any{discSessionID}, false},
		{"session hash", `UPDATE playback_sessions SET session_hash='session-hash-other' WHERE id=?`, []any{discSessionID}, false},
		{"session generation", `UPDATE playback_sessions SET generation=2 WHERE id=?`, []any{discSessionID}, false},
		{"request", `UPDATE playback_sessions SET request_id='request-other' WHERE id=?`, []any{discSessionID}, false},
		{"read profile revision", `UPDATE remote_source_read_profiles SET revision=5 WHERE source_id=?`, []any{discRemoteSource}, true},
		{"inventory revision", `UPDATE inventory_objects SET revision='inventory-revision-other' WHERE id=?`, []any{discObjectID}, true},
		{"library generation", `UPDATE library_sources SET generation=13 WHERE id=?`, []any{discLibrarySource}, true},
		{"root identity", `UPDATE library_sources SET root_identity='root-identity-other' WHERE id=?`, []any{discLibrarySource}, true},
		{"asset availability", `UPDATE catalog_assets SET available=0 WHERE token=?`, []any{discAssetID}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDiscFixture(t)
			snap := f.snapshot
			if err := f.publish(snap, plainCheck()); err != nil {
				t.Fatalf("publish: %v", err)
			}
			if err := f.exec(tc.drift, tc.args...); err != nil {
				t.Fatalf("apply drift: %v", err)
			}
			_, err := f.activate(snap.key(), snap.ID, plainCheck())
			if err == nil {
				t.Fatal("activation after drift was accepted")
			}
			if tc.current && !errors.Is(err, ErrCurrent) {
				t.Fatalf("activation after drift = %v, want ErrCurrent", err)
			}
			if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "pending" {
				t.Fatalf("state after refused activation = %q, want pending", state)
			}
		})
	}
}

// TestCallbackMutationCannotRetarget proves the Check callback receives its own
// deep copy: mutating it cannot change the candidate the store writes or returns.
func TestCallbackMutationCannotRetarget(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	want, err := freeze(snap)
	if err != nil {
		t.Fatalf("freeze candidate: %v", err)
	}
	other, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: "other-scope", Object: "other-object", Revision: `"other-etag"`, Size: discSize})
	if err != nil {
		t.Fatalf("other version: %v", err)
	}
	mutate := func(_ context.Context, _ *sql.Tx, v Snapshot) error {
		v.ID = "snapshot-retargeted"
		v.SessionID = "session-retargeted"
		v.Generation = 7
		v.Actor.AccountID = "account-retargeted"
		v.Operation.LegacyRequestID = "request-retargeted"
		v.Operation.QueueID = "queue-retargeted"
		v.Source.AssetID = "asset-retargeted"
		v.Media.Streams[0].Codec = "hacked"
		v.Media.Chapters[0].Start = 999
		v.Disc.Structure.Titles[0].PlaylistFile = "00009.mpls"
		v.Input.Conditional.Version = other
		return nil
	}
	if err = f.publish(snap, mutate); err != nil {
		t.Fatalf("publish with mutating check: %v", err)
	}
	checks := map[string]string{
		"snapshot id":      mustText(t, f.db, `SELECT id FROM playback_disc_snapshots`),
		"account":          mustText(t, f.db, `SELECT account_id FROM playback_disc_snapshot_actor WHERE snapshot_id=?`, snap.ID),
		"asset":            mustText(t, f.db, `SELECT asset_id FROM playback_disc_snapshot_source WHERE snapshot_id=?`, snap.ID),
		"request":          mustText(t, f.db, `SELECT legacy_request_id FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, snap.ID),
		"queue":            mustText(t, f.db, `SELECT queue_id FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, snap.ID),
		"selected title":   mustText(t, f.db, `SELECT selected_title_key FROM playback_disc_snapshot_disc WHERE snapshot_id=?`, snap.ID),
		"first stream":     mustText(t, f.db, `SELECT codec FROM playback_disc_snapshot_streams WHERE snapshot_id=? AND stream_order=0`, snap.ID),
		"first chapter at": mustText(t, f.db, `SELECT CAST(start_seconds AS INTEGER) FROM playback_disc_snapshot_chapters WHERE snapshot_id=? AND chapter_order=0`, snap.ID),
	}
	for name, got := range checks {
		switch name {
		case "snapshot id":
			if got != snap.ID {
				t.Fatalf("%s = %q, want %q", name, got, snap.ID)
			}
		case "account":
			if got != discAccountID {
				t.Fatalf("%s = %q, want %q", name, got, discAccountID)
			}
		case "asset":
			if got != discAssetID {
				t.Fatalf("%s = %q, want %q", name, got, discAssetID)
			}
		case "request":
			if got != discRequestID {
				t.Fatalf("%s = %q, want %q", name, got, discRequestID)
			}
		case "queue":
			if got != discQueueID {
				t.Fatalf("%s = %q, want %q", name, got, discQueueID)
			}
		case "selected title":
			if got != snap.Disc.SelectedTitleKey {
				t.Fatalf("%s = %q, want %q", name, got, snap.Disc.SelectedTitleKey)
			}
		case "first stream":
			if got != "hevc" {
				t.Fatalf("%s = %q, want the measured hevc codec", name, got)
			}
		case "first chapter at":
			if got != "0" {
				t.Fatalf("%s = %q, want the measured start 0", name, got)
			}
		}
	}
	got, err := f.activate(snap.key(), snap.ID, plainCheck())
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activated snapshot was retargeted by the callback")
	}
}

// TestRetiredPinBlocksCollectionUntilOwnRelease keeps cleanup exact: an active
// reader pin holds the retired rows, and only that reader's own release lets the
// same collection pass remove them.
func TestRetiredPinBlocksCollectionUntilOwnRelease(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := f.pin(snap.key(), snap.ID, "pin-pending", time.UnixMilli(f.until-60_000), plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("pin on a pending candidate = %v, want ErrCurrent", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := f.pin(snap.key(), snap.ID, "pin-beyond-lifecycle", time.UnixMilli(f.until+1_000), plainCheck()); !errors.Is(err, ErrInput) {
		t.Fatalf("pin beyond the presentation deadline = %v, want ErrInput", err)
	}
	pin, err := f.pin(snap.key(), snap.ID, "pin-reader-one", time.UnixMilli(f.until-10*60_000), plainCheck())
	if err != nil {
		t.Fatalf("pin active presentation: %v", err)
	}
	if pin.SnapshotID != snap.ID || pin.SessionID != discSessionID || pin.Generation != 1 {
		t.Fatalf("pin = %#v, want the exact presentation binding", pin)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_reader_pins WHERE snapshot_id=?`, snap.ID); n != 1 {
		t.Fatalf("pin rows = %d, want 1", n)
	}
	if err = f.retire(snap.key(), snap.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	next, collected, err := f.collect("", 8)
	if err != nil {
		t.Fatalf("collect with a live pin: %v", err)
	}
	if collected != 0 || next != "" {
		t.Fatalf("collect with a live pin = (%q,%d), want no cleanup", next, collected)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshots WHERE id=?`, snap.ID); n != 1 {
		t.Fatal("a live reader pin did not retain the retired snapshot")
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "retired" {
		t.Fatalf("state = %q, want retired", state)
	}
	if err = f.release(pin); err != nil {
		t.Fatalf("release own pin: %v", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_reader_pins WHERE snapshot_id=?`, snap.ID); n != 0 {
		t.Fatalf("pin rows after release = %d, want 0", n)
	}
	if _, collected, err = f.collect("", 8); err != nil {
		t.Fatalf("collect after release: %v", err)
	}
	if collected != 1 {
		t.Fatalf("collected = %d, want 1", collected)
	}
	for _, query := range []string{
		`SELECT count(*) FROM playback_disc_snapshots WHERE id=?`,
		`SELECT count(*) FROM playback_disc_lifecycle WHERE snapshot_id=?`,
		`SELECT count(*) FROM playback_disc_snapshot_actor WHERE snapshot_id=?`,
		`SELECT count(*) FROM playback_disc_snapshot_source WHERE snapshot_id=?`,
		`SELECT count(*) FROM playback_disc_conditional_inputs WHERE snapshot_id=?`,
		`SELECT count(*) FROM playback_disc_snapshot_titles WHERE snapshot_id=?`,
		`SELECT count(*) FROM playback_disc_snapshot_streams WHERE snapshot_id=?`,
	} {
		if n := mustInt(t, f.db, query, snap.ID); n != 0 {
			t.Fatalf("%s = %d after collection, want 0", query, n)
		}
	}
}
