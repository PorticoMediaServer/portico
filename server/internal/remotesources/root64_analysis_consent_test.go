package remotesources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Root §64: one separately recorded source opt-in, default off, independent of
// the scan-depth presets and the legacy whole-file flag. These regressions use
// the real owner/fixture writers; no test-only Allow callback supplies consent.

func analysisConsentPointer(value bool) *bool { return &value }

func TestReadSettingsConsentDecodeIsOptionalAndClosed(t *testing.T) {
	base := `"scanDepth":"complete","wholeFileReads":false,"connections":8,"requestsPerSecond":10,"bytesPerSecond":0`
	for _, tc := range []struct {
		name    string
		raw     string
		want    *bool
		wantErr bool
	}{
		{name: "omitted", raw: `{` + base + `}`},
		{name: "true", raw: `{` + base + `,"allowAdvancedMediaAnalysis":true}`, want: analysisConsentPointer(true)},
		{name: "false", raw: `{` + base + `,"allowAdvancedMediaAnalysis":false}`, want: analysisConsentPointer(false)},
		{name: "null", raw: `{` + base + `,"allowAdvancedMediaAnalysis":null}`, wantErr: true},
		{name: "string", raw: `{` + base + `,"allowAdvancedMediaAnalysis":"true"}`, wantErr: true},
		{name: "number", raw: `{` + base + `,"allowAdvancedMediaAnalysis":1}`, wantErr: true},
		{name: "object", raw: `{` + base + `,"allowAdvancedMediaAnalysis":{}}`, wantErr: true},
		{name: "array", raw: `{` + base + `,"allowAdvancedMediaAnalysis":[]}`, wantErr: true},
		{name: "duplicate same", raw: `{` + base + `,"allowAdvancedMediaAnalysis":true,"allowAdvancedMediaAnalysis":true}`, wantErr: true},
		{name: "duplicate changed", raw: `{` + base + `,"allowAdvancedMediaAnalysis":true,"allowAdvancedMediaAnalysis":false}`, wantErr: true},
		{name: "unknown field", raw: `{` + base + `,"allowAdvancedMedia":true}`, wantErr: true},
		{name: "too many fields", raw: `{` + base + `,"allowAdvancedMediaAnalysis":true,"extra":1}`, wantErr: true},
		{name: "missing required with consent", raw: `"wholeFileReads":false,"connections":8,"requestsPerSecond":10,"bytesPerSecond":0,"allowAdvancedMediaAnalysis":true`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got SourceReadValues
			raw := tc.raw
			if !strings.HasPrefix(raw, "{") {
				raw = `{` + raw + `}`
			}
			err := json.Unmarshal([]byte(raw), &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("decode accepted=%v wantErr=%v: %v", err == nil, tc.wantErr, err)
			}
			if tc.wantErr {
				return
			}
			if tc.want == nil {
				if got.AllowAdvancedMediaAnalysis != nil {
					t.Fatalf("omitted consent decoded as explicit %v", *got.AllowAdvancedMediaAnalysis)
				}
				return
			}
			if got.AllowAdvancedMediaAnalysis == nil || *got.AllowAdvancedMediaAnalysis != *tc.want {
				t.Fatalf("consent decoded as %v", got.AllowAdvancedMediaAnalysis)
			}
		})
	}
	values := `"values":{` + base + `}`
	for _, raw := range []string{
		`{"operationId":"op","expectedGeneration":"1","expectedRevision":0,` + values + `,"values":{` + base + `}}`,
		`{"operationId":"op","expectedGeneration":"1","expectedRevision":0,` + values + `,"extra":1}`,
	} {
		var change SourceReadSettingsChange
		if err := json.Unmarshal([]byte(raw), &change); err == nil {
			t.Fatalf("duplicated or unknown command field accepted: %s", raw)
		}
	}
	nestedDuplicate := `{"operationId":"op","expectedGeneration":"1","expectedRevision":0,"values":{` + base + `,"allowAdvancedMediaAnalysis":true,"allowAdvancedMediaAnalysis":false}}`
	var change SourceReadSettingsChange
	if err := json.Unmarshal([]byte(nestedDuplicate), &change); err == nil {
		t.Fatal("duplicated consent inside a command accepted")
	}
}

func TestReadConsentDefaultsAreOffEverywhere(t *testing.T) {
	for _, preset := range []string{"list", "ask", "once", "everything"} {
		profile, err := SourceReadPreset(preset)
		if err != nil {
			t.Fatalf("%s: %v", preset, err)
		}
		if profile.AllowAdvancedMediaAnalysis {
			t.Fatalf("%s granted advanced analysis consent", preset)
		}
	}
	everything, _ := SourceReadPreset("everything")
	if everything.ScanDepth != "complete" || everything.WholeFileReads {
		t.Fatalf("Everything lost its scan depth or kept the legacy whole-file grant: %+v", everything)
	}

	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ctx := context.Background()
	absent, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil || absent.Saved || absent.Values.AllowAdvancedMediaAnalysis == nil || *absent.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("absent profile did not report consent off: %+v %v", absent, err)
	}

	// A legacy saved profile can still have whole_file_reads=1. It must never be
	// read as advanced-analysis consent; the new column keeps its stored default.
	writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Tx().Exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'complete',1,8,10,0)`); err != nil {
		writer.Rollback()
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	legacy, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil || !legacy.Saved || !legacy.Values.WholeFileReads || legacy.Values.AllowAdvancedMediaAnalysis == nil || *legacy.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("legacy whole-file profile supplied analysis consent: %+v %v", legacy, err)
	}

	snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !fact.Remote || fact.Allowed || fact.ProfileRevision != 1 || fact.ConfigurationGeneration != "1" {
		t.Fatalf("legacy profile fact: %+v %v", fact, err)
	}
	switches, remote, err := AnalysisSwitchesTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !remote {
		t.Fatalf("remote analysis switches: %v %v", remote, err)
	}
	operations := append([]string{"probe", "local_metadata", "subtitles"}, catalog.DeepScanOperations...)
	kept := switches.Filter(operations)
	for _, operation := range catalog.DeepScanOperations {
		if containsOperation(kept, operation) {
			t.Fatalf("consent-off source kept heavy analysis %q", operation)
		}
	}
	for _, operation := range []string{"probe", "local_metadata", "subtitles"} {
		if !containsOperation(kept, operation) {
			t.Fatalf("consent-off source dropped basic %q", operation)
		}
	}
	if err = snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}

	local, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Rollback()
	root := t.TempDir()
	if _, err = local.Tx().Exec(`INSERT INTO library_sources(id,library_id,kind,configured_root,root,root_identity,identity_confirmed,enabled) VALUES('local-source',?,'local',?,?,'',0,1)`, f.library, root, root); err != nil {
		t.Fatal(err)
	}
	if _, remote, err = AnalysisSwitchesTx(ctx, local.Tx(), "local-source"); err != nil || remote {
		t.Fatalf("local folder was treated as a remote source: %v %v", remote, err)
	}
}

func TestExplicitConsentSurvivesOrdinaryPresetAndProfileSaves(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	before, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	once, _ := SourceReadPreset("once")
	values := sourceReadValues(once)
	values.AllowAdvancedMediaAnalysis = analysisConsentPointer(true)
	first, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(900), ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Values: values})
	if err != nil || first.ReadSettings == nil || first.ReadSettings.Values.AllowAdvancedMediaAnalysis == nil || !*first.ReadSettings.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("explicit consent save: %+v %v", first, err)
	}

	// An ordinary preset or profile caller omits the new field. It must preserve
	// the recorded consent rather than resetting it with the preset.
	current, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	everything, _ := SourceReadPreset("everything")
	writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{
		SourceID: "remote", Generation: current.Generation, ExpectedRevision: current.Revision, Profile: everything})
	if err == nil {
		err = writer.Commit()
	} else {
		writer.Rollback()
	}
	if err != nil || !applied.Profile.AllowAdvancedMediaAnalysis || applied.Profile.Revision == current.Revision {
		t.Fatalf("ordinary preset cleared consent: %+v %v", applied, err)
	}

	// The public command shape with the field genuinely omitted preserves it too.
	current, err = f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"operationId":        nativeSaveOperation(901),
		"expectedGeneration": current.Generation,
		"expectedRevision":   current.Revision,
		"values": map[string]any{
			"scanDepth": "basic", "wholeFileReads": false,
			"connections": 4, "requestsPerSecond": 4, "bytesPerSecond": 0,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var omitted SourceReadSettingsChange
	if err = json.Unmarshal(raw, &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Values.AllowAdvancedMediaAnalysis != nil {
		t.Fatal("omitted consent command decoded as an explicit value")
	}
	receipt, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", omitted)
	if err != nil || receipt.ReadSettings == nil || receipt.ReadSettings.Values.AllowAdvancedMediaAnalysis == nil || !*receipt.ReadSettings.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("omitted consent command reset consent: %+v %v", receipt, err)
	}

	snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	fact, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !fact.Allowed {
		t.Fatalf("source analysis fact lost explicit consent: %+v %v", fact, err)
	}
}

func TestExplicitConsentFalseRevokesProfileAndFact(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	before, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	once, _ := SourceReadPreset("once")
	grant := sourceReadValues(once)
	grant.AllowAdvancedMediaAnalysis = analysisConsentPointer(true)
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(910), ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Values: grant}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	original, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	snapshot.Rollback()
	if err != nil || !original.Allowed || !original.Remote {
		t.Fatalf("recorded consent: %+v %v", original, err)
	}

	current, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	revoke := current.Values
	revoke.AllowAdvancedMediaAnalysis = analysisConsentPointer(false)
	revoked, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(911), ExpectedGeneration: current.Generation, ExpectedRevision: current.Revision, Values: revoke})
	if err != nil || revoked.ReadSettings == nil || revoked.ReadSettings.Values.AllowAdvancedMediaAnalysis == nil || *revoked.ReadSettings.Values.AllowAdvancedMediaAnalysis {
		t.Fatalf("explicit revocation: %+v %v", revoked, err)
	}
	if revoked.ReadSettings.Revision != current.Revision+1 {
		t.Fatalf("revocation did not advance the same profile: %d -> %d", current.Revision, revoked.ReadSettings.Revision)
	}
	snapshot, err = dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	fact, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !fact.Remote || fact.Allowed || fact.ProfileRevision != revoked.ReadSettings.Revision {
		t.Fatalf("revoked fact: %+v %v", fact, err)
	}
	if err = catalog.CheckSourceAnalysisConsentTx(ctx, snapshot.Tx(), original); !errors.Is(err, storage.ErrRemoteAnalysisDisabled) {
		t.Fatalf("captured consent remained authorized after revocation: %v", err)
	}
}

func TestConsentIsBoundToItsOwnSourceProfile(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	before, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := SourceReadPreset("list")
	grant := sourceReadValues(listing)
	grant.AllowAdvancedMediaAnalysis = analysisConsentPointer(true)
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", SourceReadSettingsChange{
		OperationID: nativeSaveOperation(920), ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Values: grant}); err != nil {
		t.Fatal(err)
	}

	// A profile naming a different source cannot be written through this one.
	otherProfile := listing
	otherProfile.SourceID = "remote2"
	current, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{
		SourceID: "remote", Generation: current.Generation, ExpectedRevision: current.Revision, Profile: otherProfile}); !errors.Is(err, storage.ErrRemoteConfig) {
		writer.Rollback()
		t.Fatalf("profile/source mismatch accepted: %v", err)
	}
	writer.Rollback()

	// A second remote source with no saved profile has no consent of its own,
	// even though another profile in the same server says yes.
	otherRoot := t.TempDir()
	if _, err = f.s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('remote2','webdav','Other',?,1)`, otherRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`INSERT INTO library_sources(id,library_id,kind,configured_root,root,root_identity,identity_confirmed,enabled,remote_source_id) VALUES('other-source',?,'remote',?,?,?,1,1,'remote2')`,
		f.library, otherRoot, otherRoot, storage.RemoteDirectoryIdentity("remote2", otherRoot)+":generation:1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	other, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), "other-source")
	if err != nil || !other.Remote || other.Allowed {
		snapshot.Rollback()
		t.Fatalf("another source profile supplied consent: %+v %v", other, err)
	}
	// A real allowed witness cannot be pointed at another library source: the
	// check re-reads that binding's current fact. This is fact currentness, not
	// an authority proof on its own.
	own, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !own.Allowed {
		snapshot.Rollback()
		t.Fatalf("own source witness: %+v %v", own, err)
	}
	borrowed := own
	borrowed.LibrarySourceID = "other-source"
	if err = catalog.CheckSourceAnalysisConsentTx(ctx, snapshot.Tx(), borrowed); err == nil {
		snapshot.Rollback()
		t.Fatal("a witness rebound to another source passed the current fact")
	}
	snapshot.Rollback()

	snapshot, err = dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	fact, err := catalog.SourceAnalysisConsentTx(ctx, snapshot.Tx(), f.library)
	if err != nil || !fact.Allowed {
		t.Fatalf("the source's own consent was lost: %+v %v", fact, err)
	}
}

func TestOmittedConsentCommandKeepsItsLegacyReceipt(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	once, _ := SourceReadPreset("once")
	values := sourceReadValues(once)
	values.AllowAdvancedMediaAnalysis = nil
	command := SourceReadSettingsChange{OperationID: nativeSaveOperation(930), ExpectedGeneration: "1", ExpectedRevision: 0, Values: values}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("allowAdvancedMediaAnalysis")) {
		t.Fatal("legacy command marshal changed by the omitted consent field")
	}
	first, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", command)
	if err != nil || first.ReadSettings == nil {
		t.Fatalf("legacy command: %+v %v", first, err)
	}
	replay, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", command)
	if err != nil || replay.ReadSettings == nil || !reflect.DeepEqual(*replay.ReadSettings, *first.ReadSettings) {
		t.Fatalf("legacy receipt did not replay identically: %+v %v", replay, err)
	}
	changed := command
	changed.Values.AllowAdvancedMediaAnalysis = analysisConsentPointer(false)
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", changed); !errors.Is(err, mounts.ErrCommandConflict) {
		t.Fatalf("explicit consent reused a legacy operation identity: %v", err)
	}
}

func TestMediaAnalysisLeaseNeedsConsentWhilePlaybackDoesNot(t *testing.T) {
	ctx := context.Background()
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("analysis-consent"), 0)
	if err != nil {
		t.Fatal(err)
	}
	check := func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
		return currentLocalOwnerTx(ctx, tx, identities, owner)
	}
	admit := func(purpose SourceReadPurpose) (*SourceReadLease, error) {
		snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Rollback()
		return f.s.CaptureReadSourceTx(ctx, snapshot.Tx(), SourceSessionAdmission{Purpose: purpose, Observation: ref, CheckTx: check, OnChanged: func() {}})
	}

	if _, err = admit(SourceMediaAnalysis); !errors.Is(err, storage.ErrRemoteAnalysisDisabled) {
		t.Fatalf("analysis lease without consent: %v", err)
	}
	if len(f.s.serveLeases) != 0 {
		t.Fatal("refused analysis lease opened the provider")
	}
	playback, err := admit(SourcePlayback)
	if err != nil {
		t.Fatalf("no-consent playback lease was refused: %v", err)
	}
	if playback.Binding().Purpose != SourcePlayback || len(f.s.serveLeases) != 0 {
		playback.Close()
		t.Fatal("playback lease did not stay a DB-only capture")
	}
	playback.Close()

	// The same source admits analysis once the explicit consent is recorded by
	// the real writer; the capture still opens nothing by itself.
	writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	listing, _ := SourceReadPreset("list")
	if _, err = f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{
		SourceID: "remote", Generation: "1", ExpectedRevision: 0, Profile: listing, AdvancedMediaAnalysis: analysisConsentPointer(true)}); err != nil {
		writer.Rollback()
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	analysis, err := admit(SourceMediaAnalysis)
	if err != nil {
		t.Fatalf("recorded consent did not admit analysis: %v", err)
	}
	defer analysis.Close()
	if analysis.Binding().Purpose != SourceMediaAnalysis || len(f.s.serveLeases) != 0 {
		t.Fatal("analysis capture opened a provider or lost its purpose")
	}
}

func containsOperation(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
