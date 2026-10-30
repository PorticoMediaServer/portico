package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These are source-only, unrun vectors. They use the real native test fixture
// for source/owner/receipt state and never open a provider or execute a helper
// against a remote endpoint.

type testSourceChoiceFixture struct {
	f          observationFixture
	managed    *mounts.Service
	identities *identity.Service
	owner      identity.Principal
	binary     string
}

func newTestSourceChoiceFixture(t *testing.T) testSourceChoiceFixture {
	t.Helper()
	f, managed, identities, owner, _, binary := nativeSaveFixture(t)
	return testSourceChoiceFixture{f: f, managed: managed, identities: identities, owner: owner, binary: binary}
}

func (f testSourceChoiceFixture) sealDAV(t *testing.T, source string) {
	t.Helper()
	sealed, err := f.managed.Seal([]byte("[portico]\ntype=webdav\nurl=https://example.invalid/media/\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.s.db.Exec(`UPDATE remote_sources SET config=? WHERE id=?`, sealed, source); err != nil {
		t.Fatal(err)
	}
}

func testSourceChoiceDefinition(t *testing.T, f testSourceChoiceFixture, name string) SourceChoiceDefinition {
	t.Helper()
	snapshot, err := dbwork.BeginSnapshot(context.Background(), f.f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	definition, err := readSourceChoiceDefinitionTx(context.Background(), snapshot.Tx(), name, 1)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func (f testSourceChoiceFixture) input(t *testing.T, source, name, operation string) SourceReadSettingsChange {
	t.Helper()
	ctx := context.Background()
	settings, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, source)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := f.f.s.ReadSourceAdvanced(ctx, f.identities, f.owner, source)
	if err != nil {
		t.Fatal(err)
	}
	definition := testSourceChoiceDefinition(t, f, name)
	return SourceReadSettingsChange{
		OperationID:        operation,
		ExpectedGeneration: settings.Generation,
		ExpectedRevision:   settings.Revision,
		Values:             definition.Reading,
		StartChoice: &SourceStartChoice{
			Name:                            definition.Name,
			DefinitionRevision:              definition.DefinitionRevision,
			ExpectedAdvancedRevision:        advanced.Revision,
			ExpectedConfigurationGeneration: settings.ConfigurationGeneration,
		},
	}
}

func (f testSourceChoiceFixture) apply(t *testing.T, source, name, operation string) (Receipt, SourceReadSettingsChange) {
	t.Helper()
	input := f.input(t, source, name, operation)
	receipt, err := f.f.s.SaveSourceReadSettings(context.Background(), f.identities, f.owner, source, input)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, input
}

// A receipt replay promises the exact stored response, not a re-render of
// current state. SourceAdvanced keeps an unexported wire-presence map that is
// populated whenever a command document is decoded; a replayed response is
// decoded the same way, so compare the encoded outcome the API actually
// returns instead of that input-decode state.
func sameReadSettings(t *testing.T, got, want *SourceReadSettings) bool {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(gotJSON, wantJSON)
}

func testSourceChoiceExpected() map[string]SourceChoiceDefinition {
	return map[string]SourceChoiceDefinition{
		"list": {Name: "list", DefinitionRevision: 1,
			Reading: SourceReadValues{ScanDepth: "file_list_only", WholeFileReads: false, Connections: 2, RequestsPerSecond: 2, BytesPerSecond: 0},
			Advanced: SourceAdvanced{
				ScanIntervalSeconds: 86400, WatchForChanges: false, ScanOnStart: false, FullRecheckDays: 30, DoubleCheckFolders: false,
				SeekPreviews: false, ChapterImages: false, Waveforms: false, Loudness: false, Checksums: false, IntrosMode: "off",
				CacheBytes: 0, CacheFloorBytes: 2 << 30, DirCacheSeconds: 60, ReadBufferBytes: 8 << 20, ConnectTimeoutSeconds: 10, IdleTimeoutSeconds: 30, Retries: 1, LowLevelRetries: 2,
			}},
		"once": {Name: "once", DefinitionRevision: 1,
			Reading: SourceReadValues{ScanDepth: "basic", WholeFileReads: false, Connections: 4, RequestsPerSecond: 4, BytesPerSecond: 0},
			Advanced: SourceAdvanced{
				ScanIntervalSeconds: 21600, WatchForChanges: false, ScanOnStart: false, FullRecheckDays: 14, DoubleCheckFolders: false,
				SeekPreviews: false, ChapterImages: false, Waveforms: false, Loudness: false, Checksums: false, IntrosMode: "off",
				CacheBytes: 8 << 30, CacheFloorBytes: 2 << 30, DirCacheSeconds: 60, ReadBufferBytes: 8 << 20, ConnectTimeoutSeconds: 10, IdleTimeoutSeconds: 30, Retries: 1, LowLevelRetries: 2,
			}},
		"everything": {Name: "everything", DefinitionRevision: 1,
			Reading: SourceReadValues{ScanDepth: "complete", WholeFileReads: false, Connections: 8, RequestsPerSecond: 10, BytesPerSecond: 0},
			Advanced: SourceAdvanced{
				ScanIntervalSeconds: 3600, WatchForChanges: true, ScanOnStart: true, FullRecheckDays: 7, DoubleCheckFolders: true,
				SeekPreviews: true, ChapterImages: true, Waveforms: true, Loudness: true, Checksums: true, IntrosMode: "inherit",
				CacheBytes: 32 << 30, CacheFloorBytes: 2 << 30, DirCacheSeconds: 60, ReadBufferBytes: 8 << 20, ConnectTimeoutSeconds: 10, IdleTimeoutSeconds: 30, Retries: 1, LowLevelRetries: 2,
			}},
		"ask": {Name: "ask", DefinitionRevision: 1,
			Reading: SourceReadValues{ScanDepth: "basic", WholeFileReads: false, Connections: 2, RequestsPerSecond: 2, BytesPerSecond: 0},
			Advanced: SourceAdvanced{
				ScanIntervalSeconds: 0, WatchForChanges: false, ScanOnStart: false, FullRecheckDays: 0, DoubleCheckFolders: false,
				SeekPreviews: false, ChapterImages: false, Waveforms: false, Loudness: false, Checksums: false, IntrosMode: "off",
				CacheBytes: 0, CacheFloorBytes: 2 << 30, DirCacheSeconds: 60, ReadBufferBytes: 8 << 20, ConnectTimeoutSeconds: 10, IdleTimeoutSeconds: 30, Retries: 1, LowLevelRetries: 2,
			}},
	}
}

func TestSourceChoiceDefinitionsAreExactV1Seeds(t *testing.T) {
	f := newTestSourceChoiceFixture(t)
	expected := testSourceChoiceExpected()
	for name, want := range expected {
		got := testSourceChoiceDefinition(t, f, name)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s seed mismatch: got %#v want %#v", name, got, want)
		}
		if got.Reading.AllowAdvancedMediaAnalysis != nil {
			t.Fatalf("%s definition carries independent analysis consent", name)
		}
	}
	snapshot, err := dbwork.BeginSnapshot(context.Background(), f.f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	if _, err = readSourceChoiceDefinitionTx(context.Background(), snapshot.Tx(), "list", 2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown definition version was inferred: %v", err)
	}
}

func TestSourceChoiceNoInferredRecordedAssociation(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	definition := testSourceChoiceDefinition(t, f, "once")
	if _, err := f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(100), ExpectedGeneration: "1", ExpectedRevision: 0, Values: definition.Reading,
	}); err != nil {
		t.Fatal(err)
	}
	advanced, err := f.f.s.ReadSourceAdvanced(ctx, f.identities, f.owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.s.SaveSourceAdvanced(ctx, f.identities, f.owner, "remote", SourceAdvancedChange{
		ExpectedGeneration: "1", ExpectedRevision: advanced.Revision, Values: definition.Advanced,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if out.ChoiceBaseline != nil {
		t.Fatal("matching values invented a recorded choice")
	}
	if len(out.ChoiceDefinitions) != 4 || len(out.ChoiceApplicableFields) != 17 || out.ConfigurationGeneration != "1" {
		t.Fatalf("optional choice facts: %#v", out)
	}
}

func TestSourceChoiceUnavailableMountProofWithholdsOnlyOptionalAssociation(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	f.apply(t, "remote", "once", nativeSaveOperation(120))
	// Keep the recorded settings, but remove the effective authority by changing
	// the fixture to a mount-backed source with no such mount. This must never
	// substitute the still-existing raw generation as effective proof.
	if _, err := f.f.s.db.Exec(`UPDATE remote_sources SET kind='rclone',mount_id='unavailable-mount' WHERE id='remote'`); err != nil {
		t.Fatal(err)
	}
	out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil || !out.Saved || out.Revision != 1 || out.Values.ScanDepth != "basic" || len(out.ChoiceDefinitions) != 4 {
		t.Fatalf("stored current owner facts were hidden: %#v %v", out, err)
	}
	if out.ConfigurationGeneration != "" || out.ChoiceBaseline != nil {
		t.Fatal("missing effective authority was relabelled from raw generation", out)
	}
	view, err := dbwork.BeginSnapshot(ctx, f.f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	if _, err = SourceReadProfileTx(ctx, view.Tx(), "remote"); err == nil {
		t.Fatal("stored owner facts were accepted as source-read authority")
	}
}

func TestSourceChoiceRevisionMismatchWritesNothing(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*SourceReadSettingsChange)
	}{
		{"read", func(in *SourceReadSettingsChange) { in.ExpectedRevision++ }},
		{"advanced", func(in *SourceReadSettingsChange) { in.StartChoice.ExpectedAdvancedRevision++ }},
		{"both", func(in *SourceReadSettingsChange) { in.ExpectedRevision++; in.StartChoice.ExpectedAdvancedRevision++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTestSourceChoiceFixture(t)
			f.sealDAV(t, "remote")
			input := f.input(t, "remote", "once", nativeSaveOperation(200))
			test.mutate(&input)
			if _, err := f.f.s.SaveSourceReadSettings(context.Background(), f.identities, f.owner, "remote", input); !errors.Is(err, storage.ErrRemoteChanged) {
				t.Fatalf("stale revision accepted: %v", err)
			}
			for _, query := range []string{
				`SELECT count(*) FROM remote_source_read_profiles WHERE source_id='remote'`,
				`SELECT count(*) FROM remote_source_settings WHERE source_id='remote'`,
				`SELECT count(*) FROM remote_source_choice_refs WHERE source_id='remote'`,
			} {
				var count int
				if err := f.f.s.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
					t.Fatalf("revision refusal left a row: %s %d %v", query, count, err)
				}
			}
			var receipts int
			if err := f.f.s.db.QueryRow(`SELECT count(*) FROM remote_source_operations WHERE operation_id=?`, input.OperationID).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("revision refusal left a receipt: %d %v", receipts, err)
			}
		})
	}
}

func TestSourceChoiceReceiptReplayDoesNotReapply(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	first, input := f.apply(t, "remote", "once", nativeSaveOperation(300))
	if _, err := f.f.s.db.Exec(`UPDATE remote_source_read_profiles SET scan_depth='file_list_only' WHERE source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.f.s.db.Exec(`UPDATE remote_source_settings SET revision=revision+1,dir_cache_seconds=7 WHERE source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	replay, err := f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", input)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReadSettings(t, replay.ReadSettings, first.ReadSettings) {
		t.Fatal("replay did not return the historical outcome")
	}
	var depth string
	var revision int64
	var dirCache int64
	if err := f.f.s.db.QueryRow(`SELECT scan_depth FROM remote_source_read_profiles WHERE source_id='remote'`).Scan(&depth); err != nil {
		t.Fatal(err)
	}
	if err := f.f.s.db.QueryRow(`SELECT revision,dir_cache_seconds FROM remote_source_settings WHERE source_id='remote'`).Scan(&revision, &dirCache); err != nil {
		t.Fatal(err)
	}
	if depth != "file_list_only" || revision != 2 || dirCache != 7 {
		t.Fatalf("replay rewrote accepted state: %q %d %d", depth, revision, dirCache)
	}
	var operations, refs int
	if err := f.f.s.db.QueryRow(`SELECT count(*) FROM remote_source_operations WHERE operation_id=?`, input.OperationID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := f.f.s.db.QueryRow(`SELECT count(*) FROM remote_source_choice_refs WHERE source_id='remote'`).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || refs != 1 {
		t.Fatalf("replay duplicated receipt/reference: %d/%d", operations, refs)
	}
}

func TestSourceChoiceApplicableAndChangedFields17And23(t *testing.T) {
	ctx := context.Background()
	t.Run("mountless", func(t *testing.T) {
		f := newTestSourceChoiceFixture(t)
		f.sealDAV(t, "remote")
		f.apply(t, "remote", "once", nativeSaveOperation(400))
		out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
		if err != nil || out.ChoiceBaseline == nil {
			t.Fatal("missing baseline", err)
		}
		if len(out.ChoiceApplicableFields) != 17 || len(out.ChoiceBaseline.ApplicableFields) != 17 || len(out.ChoiceBaseline.ChangedFields) != 0 {
			t.Fatalf("mountless applicability: %#v", out.ChoiceBaseline)
		}
		if _, err = f.f.s.db.Exec(`UPDATE remote_source_settings SET revision=revision+1,dir_cache_seconds=7 WHERE source_id='remote'`); err != nil {
			t.Fatal(err)
		}
		out, err = f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
		if err != nil || out.ChoiceBaseline == nil {
			t.Fatal(err)
		}
		if len(out.ChoiceBaseline.ChangedFields) != 0 {
			t.Fatalf("inapplicable transport field counted: %#v", out.ChoiceBaseline.ChangedFields)
		}
	})
	t.Run("mounted", func(t *testing.T) {
		f := newTestSourceChoiceFixture(t)
		root := filepath.Join(t.TempDir(), "mounted")
		sealed, err := f.managed.Seal([]byte("[portico]\ntype=webdav\nurl=https://example.invalid/mounted/\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.f.s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,config,mount_id) VALUES('mounted','webdav','Mounted',?,1,?,'mount-fixture')`, root, sealed); err != nil {
			t.Fatal(err)
		}
		f.apply(t, "mounted", "once", nativeSaveOperation(401))
		out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "mounted")
		if err != nil || out.ChoiceBaseline == nil {
			t.Fatal("missing mounted baseline", err)
		}
		if len(out.ChoiceApplicableFields) != 23 || len(out.ChoiceBaseline.ApplicableFields) != 23 || len(out.ChoiceBaseline.ChangedFields) != 0 {
			t.Fatalf("mounted applicability: %#v", out.ChoiceBaseline)
		}
		if _, err = f.f.s.db.Exec(`UPDATE remote_source_settings SET revision=revision+1,dir_cache_seconds=7 WHERE source_id='mounted'`); err != nil {
			t.Fatal(err)
		}
		out, err = f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "mounted")
		if err != nil || out.ChoiceBaseline == nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.ChoiceBaseline.ChangedFields, []string{"dirCacheSeconds"}) {
			t.Fatalf("mounted changed fields: %#v", out.ChoiceBaseline.ChangedFields)
		}
	})
}

func TestSourceChoiceManualZeroGatesLaterFolders(t *testing.T) {
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	f.apply(t, "remote", "ask", nativeSaveOperation(500))
	var interval int64
	var due string
	if err := f.f.s.db.QueryRow(`SELECT interval_seconds,next_scan_at FROM library_sources WHERE id=?`, f.f.library).Scan(&interval, &due); err != nil {
		t.Fatal(err)
	}
	if interval != 0 || due != "" {
		t.Fatalf("current folder kept automatic work: %d %q", interval, due)
	}
	futureRoot := filepath.Join(t.TempDir(), "future")
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := f.f.s.db.Exec(`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,root_identity,identity_confirmed,enabled,follow_symlinks,interval_seconds,next_scan_at,remote_source_id) VALUES('future-folder',?,'remote','Future',?,?,'local',1,'',1,1,0,21600,?,'remote')`, f.f.library, futureRoot, futureRoot, past); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var ungated int
	if err := f.f.s.db.QueryRow(`SELECT count(*) FROM library_sources ls WHERE ls.enabled=1 AND ls.remote_source_id='remote' AND ls.interval_seconds>0 AND ls.next_scan_at!='' AND ls.next_scan_at<=?`, now).Scan(&ungated); err != nil || ungated != 1 {
		t.Fatalf("future folder was not due before the gate: %d %v", ungated, err)
	}
	var gated int
	if err := f.f.s.db.QueryRow(`SELECT count(*) FROM library_sources ls WHERE ls.enabled=1 AND NOT EXISTS(SELECT 1 FROM remote_source_settings r WHERE r.source_id=ls.remote_source_id AND r.scan_interval_seconds=0) AND ls.remote_source_id='remote' AND ls.interval_seconds>0 AND ls.next_scan_at!='' AND ls.next_scan_at<=?`, now).Scan(&gated); err != nil || gated != 0 {
		t.Fatalf("manual source gate admitted a later folder: %d %v", gated, err)
	}
}

func TestSourceChoiceCacheContinuesToExactAcceptedChild(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, f.identities, f.owner) }
	created, err := f.managed.Command(ctx, "actual-owner-fixture", "", mounts.Command{
		OperationID: nativeSaveOperation(600), Action: "create", Name: "Choice NAS", Executable: f.binary,
		Remote: "native:/media", Config: "[native]\ntype=smb\nhost=192.168.42.7\n",
	}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	source := created.MountID
	first, input := f.apply(t, source, "once", nativeSaveOperation(601))
	if first.ReadSettings == nil || first.ReadSettings.ChoiceBaseline == nil || first.ReadSettings.ChoiceBaseline.ConfigurationGeneration != "1" {
		t.Fatalf("initial receipt did not preserve the pre-cache association: %#v", first.ReadSettings)
	}
	var rawGeneration, configGeneration int64
	var fingerprint string
	if err = f.f.s.db.QueryRow(`SELECT source_row_generation,config_generation,config_fingerprint FROM remote_source_choice_refs WHERE source_id=?`, source).Scan(&rawGeneration, &configGeneration, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if rawGeneration != 2 || configGeneration != 2 || len(fingerprint) != 64 {
		t.Fatalf("choice reference did not continue to the child: %d/%d/%q", rawGeneration, configGeneration, fingerprint)
	}
	var appliedBytes, appliedFloor int64
	if err = f.f.s.db.QueryRow(`SELECT cache_applied_bytes,cache_applied_floor_bytes FROM remote_source_settings WHERE source_id=?`, source).Scan(&appliedBytes, &appliedFloor); err != nil {
		t.Fatal(err)
	}
	if appliedBytes != 8<<30 || appliedFloor != 2<<30 {
		t.Fatalf("accepted cache projection missing: %d/%d", appliedBytes, appliedFloor)
	}
	out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, source)
	if err != nil || out.ChoiceBaseline == nil {
		t.Fatal(err)
	}
	if out.Generation != "2" || out.ConfigurationGeneration != "2" || out.ChoiceBaseline.Generation != "2" ||
		out.ChoiceBaseline.ConfigurationGeneration != "2" || len(out.ChoiceBaseline.ApplicableFields) != 23 || len(out.ChoiceBaseline.ChangedFields) != 0 {
		t.Fatalf("accepted child baseline: %#v", out)
	}
	replay, err := f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, source, input)
	if err != nil || !sameReadSettings(t, replay.ReadSettings, first.ReadSettings) {
		t.Fatalf("cache replay did not return the historical receipt: %v", err)
	}
	var backendGeneration int64
	if err = f.f.s.db.QueryRow(`SELECT generation FROM mount_backend_configs WHERE mount_id=?`, source).Scan(&backendGeneration); err != nil || backendGeneration != 2 {
		t.Fatalf("replay reran the cache command: %d %v", backendGeneration, err)
	}
	var replayedBytes, replayedFloor int64
	if err = f.f.s.db.QueryRow(`SELECT cache_applied_bytes,cache_applied_floor_bytes FROM remote_source_settings WHERE source_id=?`, source).Scan(&replayedBytes, &replayedFloor); err != nil || replayedBytes != 8<<30 || replayedFloor != 2<<30 {
		t.Fatalf("replay changed the applied projection: %d/%d %v", replayedBytes, replayedFloor, err)
	}
}

// Root §64 adds an independent consent beside the Root §57 choice. A choice
// never carries or changes that consent, and the retired whole-file value stays
// compatibility data rather than an authored choice row.
func TestSourceChoiceStartChoiceRefusesPresentConsentAtomically(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(map[bool]string{true: "true", false: "false"}[present], func(t *testing.T) {
			ctx := context.Background()
			f := newTestSourceChoiceFixture(t)
			f.sealDAV(t, "remote")
			before, err := catalog.New(f.f.s.db).ScanPolicy(ctx, f.f.library)
			if err != nil {
				t.Fatal(err)
			}
			input := f.input(t, "remote", "once", nativeSaveOperation(700))
			input.Values.AllowAdvancedMediaAnalysis = analysisConsentPointer(present)
			if _, err = f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", input); !errors.Is(err, storage.ErrRemoteConfig) {
				t.Fatalf("combined consent %v and choice accepted: %v", present, err)
			}
			// The public body decodes as written and is still refused by the
			// accepting writer, not merely by the test's struct construction.
			raw, encodeErr := json.Marshal(input)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			var wire SourceReadSettingsChange
			if encodeErr = json.Unmarshal(raw, &wire); encodeErr != nil || wire.StartChoice == nil || wire.Values.AllowAdvancedMediaAnalysis == nil {
				t.Fatalf("combined body did not decode to its startChoice/consent: %#v %v", wire, encodeErr)
			}
			if _, err = f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", wire); !errors.Is(err, storage.ErrRemoteConfig) {
				t.Fatalf("decoded combined consent %v and choice accepted: %v", present, err)
			}
			for _, query := range []string{
				`SELECT count(*) FROM remote_source_read_profiles WHERE source_id='remote'`,
				`SELECT count(*) FROM remote_source_settings WHERE source_id='remote'`,
				`SELECT count(*) FROM remote_source_choice_refs WHERE source_id='remote'`,
				`SELECT count(*) FROM remote_source_operations WHERE operation_id=?`,
			} {
				var count int
				args := []any(nil)
				if strings.Contains(query, "operation_id") {
					args = []any{input.OperationID}
				}
				if err = f.f.s.db.QueryRow(query, args...).Scan(&count); err != nil || count != 0 {
					t.Fatalf("refused combined command left state: %s %d %v", query, count, err)
				}
			}
			after, err := catalog.New(f.f.s.db).ScanPolicy(ctx, f.f.library)
			if err != nil || after.Revision != before.Revision {
				t.Fatalf("refused combined command advanced library authority: %d -> %d %v", before.Revision, after.Revision, err)
			}

			// The ordinary consent command carries no choice and remains the one
			// explicit way to record or revoke it.
			settings, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
			if err != nil {
				t.Fatal(err)
			}
			values := settings.Values
			values.AllowAdvancedMediaAnalysis = analysisConsentPointer(true)
			if _, err = f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", SourceReadSettingsChange{
				OperationID: nativeSaveOperation(701), ExpectedGeneration: settings.Generation, ExpectedRevision: settings.Revision, Values: values,
			}); err != nil {
				t.Fatalf("ordinary explicit consent was refused: %v", err)
			}
		})
	}
}

func TestSourceChoicePreservesRecordedConsentAndFreshEverythingGrantsNone(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	listing := testSourceChoiceDefinition(t, f, "list")
	grant := listing.Reading
	// Hold a non-default legacy compatibility value so the choice's preservation
	// of the current stored value is observable.
	grant.WholeFileReads = true
	grant.AllowAdvancedMediaAnalysis = analysisConsentPointer(true)
	if _, err := f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(710), ExpectedGeneration: "1", ExpectedRevision: 0, Values: grant,
	}); err != nil {
		t.Fatal(err)
	}
	f.apply(t, "remote", "once", nativeSaveOperation(711))
	out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil || out.Values.AllowAdvancedMediaAnalysis == nil || !*out.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("startChoice changed the separately recorded consent: %#v %v", out, err)
	}
	if !out.Values.WholeFileReads {
		t.Fatalf("startChoice did not preserve the stored legacy compatibility value: %#v", out.Values)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, f.f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.f.library)
	snapshot.Rollback()
	if err != nil || !fact.Allowed {
		t.Fatalf("source analysis fact lost the separately recorded consent: %+v %v", fact, err)
	}

	// A fresh source choosing Everything gets no consent from the definition.
	freshRoot := t.TempDir()
	if _, err = f.f.s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('fresh','webdav','Fresh',?,1)`, freshRoot); err != nil {
		t.Fatal(err)
	}
	f.sealDAV(t, "fresh")
	f.apply(t, "fresh", "everything", nativeSaveOperation(712))
	fresh, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "fresh")
	if err != nil || fresh.Values.AllowAdvancedMediaAnalysis == nil || *fresh.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("fresh Everything granted analysis consent: %#v %v", fresh, err)
	}
	if fresh.ChoiceBaseline == nil || fresh.ChoiceBaseline.Reading.WholeFileReads {
		t.Fatalf("fresh Everything kept the retired whole-file grant: %#v", fresh.ChoiceBaseline)
	}

	// Revoking through the ordinary command is still separate and effective.
	current, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	revoke := current.Values
	revoke.AllowAdvancedMediaAnalysis = analysisConsentPointer(false)
	if _, err = f.f.s.SaveSourceReadSettings(ctx, f.identities, f.owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(713), ExpectedGeneration: current.Generation, ExpectedRevision: current.Revision, Values: revoke,
	}); err != nil {
		t.Fatalf("ordinary explicit revocation was refused: %v", err)
	}
	revoked, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil || revoked.Values.AllowAdvancedMediaAnalysis == nil || *revoked.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("ordinary revocation did not record off: %#v %v", revoked, err)
	}
}

func TestSourceChoiceComparisonExcludesRetiredAndConsentFlags(t *testing.T) {
	ctx := context.Background()
	f := newTestSourceChoiceFixture(t)
	f.sealDAV(t, "remote")
	f.apply(t, "remote", "once", nativeSaveOperation(720))
	if len(sourceChoiceWireFields) != 23 {
		t.Fatalf("choice wire rows: %d, want four read controls and nineteen Advanced", len(sourceChoiceWireFields))
	}
	if got := append([]string(nil), sourceChoiceWireFields[:4]...); !reflect.DeepEqual(got, []string{"scanDepth", "connections", "requestsPerSecond", "bytesPerSecond"}) {
		t.Fatalf("the four applied read controls changed: %#v", got)
	}
	for _, field := range []string{"wholeFileReads", "allowAdvancedMediaAnalysis"} {
		for _, wire := range sourceChoiceWireFields {
			if wire == field {
				t.Fatalf("retired/independent field %q is an authored choice row", field)
			}
		}
	}
	out, err := f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil || out.ChoiceBaseline == nil {
		t.Fatalf("missing baseline: %v", err)
	}
	applicable := map[string]bool{}
	for _, field := range out.ChoiceBaseline.ApplicableFields {
		applicable[field] = true
	}
	for _, field := range []string{"wholeFileReads", "allowAdvancedMediaAnalysis"} {
		if applicable[field] {
			t.Fatalf("applicable fields include %q", field)
		}
	}
	if len(out.ChoiceBaseline.ChangedFields) != 0 {
		t.Fatalf("new baseline changed fields: %#v", out.ChoiceBaseline.ChangedFields)
	}
	if _, err = f.f.s.db.Exec(`UPDATE remote_source_read_profiles SET whole_file_reads=1,allow_advanced_media_analysis=1 WHERE source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	out, err = f.f.s.ReadSourceReadSettings(ctx, f.identities, f.owner, "remote")
	if err != nil || out.ChoiceBaseline == nil {
		t.Fatalf("baseline after compatibility edits: %v", err)
	}
	if len(out.ChoiceBaseline.ChangedFields) != 0 {
		t.Fatalf("retired/independent flags counted as changed: %#v", out.ChoiceBaseline.ChangedFields)
	}

	// The accepted body comparison ignores the compatibility value but refuses
	// either presence of the independent consent.
	definition := testSourceChoiceDefinition(t, f, "once")
	match := definition.Reading
	match.WholeFileReads = !definition.Reading.WholeFileReads
	if !sourceChoiceReadingMatches(definition, match) {
		t.Fatal("retired whole-file compatibility value changed the accepted body")
	}
	for _, present := range []bool{true, false} {
		match.AllowAdvancedMediaAnalysis = analysisConsentPointer(present)
		if sourceChoiceReadingMatches(definition, match) {
			t.Fatalf("present consent %v matched a startChoice body", present)
		}
	}

	// The projection carries arrays, never a count that could be mistaken for
	// applicability or a reset claim.
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for key := range top {
		if strings.Contains(strings.ToLower(key), "count") {
			t.Fatalf("settings projection carries a count field %q", key)
		}
	}
	var baseline map[string]json.RawMessage
	if err = json.Unmarshal(top["choiceBaseline"], &baseline); err != nil {
		t.Fatal(err)
	}
	for key := range baseline {
		if strings.Contains(strings.ToLower(key), "count") {
			t.Fatalf("choice baseline carries a count field %q", key)
		}
	}
}

func TestSourceChoiceStrictDecoderRefusesDuplicates(t *testing.T) {
	values := `"scanDepth":"basic","wholeFileReads":false,"connections":4,"requestsPerSecond":4,"bytesPerSecond":0`
	choice := `"name":"once","definitionRevision":1,"expectedAdvancedRevision":0,"expectedConfigurationGeneration":"1"`
	for _, tc := range []struct{ name, raw string }{
		{"outer operation", `{"operationId":"a","operationId":"b","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `}}`},
		{"outer values", `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `},"values":{` + values + `}}`},
		{"outer choice", `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `},"startChoice":{` + choice + `},"startChoice":{` + choice + `}}`},
		{"inner read control", `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{"scanDepth":"basic","scanDepth":"complete","wholeFileReads":false,"connections":4,"requestsPerSecond":4,"bytesPerSecond":0}}`},
		{"inner consent", `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `,"allowAdvancedMediaAnalysis":true,"allowAdvancedMediaAnalysis":false}}`},
		{"inner choice field", `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `},"startChoice":{"name":"once","name":"ask","definitionRevision":1,"expectedAdvancedRevision":0,"expectedConfigurationGeneration":"1"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded SourceReadSettingsChange
			if err := json.Unmarshal([]byte(tc.raw), &decoded); err == nil {
				t.Fatalf("duplicate key accepted: %s", tc.name)
			}
		})
	}
	good := `{"operationId":"a","expectedGeneration":"1","expectedRevision":0,"values":{` + values + `},"startChoice":{` + choice + `}}`
	var decoded SourceReadSettingsChange
	if err := json.Unmarshal([]byte(good), &decoded); err != nil || decoded.StartChoice == nil || decoded.Values.AllowAdvancedMediaAnalysis != nil {
		t.Fatalf("valid choice command refused or carried consent: %#v %v", decoded, err)
	}
}
