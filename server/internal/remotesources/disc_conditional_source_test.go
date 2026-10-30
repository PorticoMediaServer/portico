package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Regression SOURCES only. The existing observation/capture selection fixture
// supplies real normalized source/catalogue/family/device facts and a sealed
// DAV configuration. No source/network/helper work is performed by these cases.
func conditionalSelectionFixture(t *testing.T) (observationFixture, *DiscConditionalSource, identity.Principal, string) {
	t.Helper()
	f, _, p, principal, _, _ := playbackSelectionFixture(t)
	if _, err := f.s.db.Exec(`UPDATE remote_sources SET range_support='conditional' WHERE id='remote'`); err != nil {
		t.Fatal(err)
	}
	// A conditional disc read requires the folder's last scan to have published
	// the healthy state (the observation fixture records facts without running
	// the scan's finish step, which would otherwise leave the pre-scan default).
	if _, err := f.s.db.Exec(`UPDATE library_sources SET health='healthy' WHERE remote_source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	// Conditional ranges require their real source limits, not a full-capture
	// cache reservation or advanced-analysis consent.
	if _, err := f.s.db.Exec(`DELETE FROM remote_source_settings WHERE source_id='remote'`); err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscConditionalSource(CaptureProducerOptions{Sources: f.s, Identities: p.identities, AuthorizeTx: p.authorize})
	if err != nil {
		t.Fatal(err)
	}
	return f, d, principal, identity.Token()
}

func resolveConditionalSelection(t *testing.T, f observationFixture, d *DiscConditionalSource, principal identity.Principal, witness string) (playbackdisc.Source, error) {
	t.Helper()
	asset := selectionAsset(t, f)
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	return d.ResolveTx(view.Context(), view.Tx(), principal, asset, witness)
}

func checkConditionalSelection(t *testing.T, f observationFixture, d *DiscConditionalSource, source playbackdisc.Source) error {
	t.Helper()
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	return d.CheckTx(view.Context(), view.Tx(), source)
}

func assertConditionalPreflightNoWork(t *testing.T, f observationFixture) {
	t.Helper()
	for _, table := range []string{"remote_source_captures", "remote_source_capture_physical", "remote_source_capture_actor", "remote_source_capture_policy", "remote_source_capture_quota"} {
		var count int
		if err := f.s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("conditional preflight allocated capture state", table, count, err)
		}
	}
	if len(f.s.readLeases) != 0 || len(f.s.serveLeases) != 0 || len(f.s.active) != 0 {
		t.Fatal("conditional preflight acquired a source/process/read lease")
	}
}

func TestConditionalDiscSelectionIsDBOnlyAndIndependentOfCaptureCache(t *testing.T) {
	f, d, principal, witness := conditionalSelectionFixture(t)
	source, err := resolveConditionalSelection(t, f, d, principal, witness)
	if err != nil {
		t.Fatal(err)
	}
	if source.InventoryObjectID != "disc-object" || source.AssetID != "disc-asset" || source.LibrarySourceID != f.library || source.LibraryID != f.library ||
		source.RemoteSourceID != "remote" || source.RawConfigurationGeneration != 1 || source.EffectiveConfiguration != "1" ||
		source.Size != 16 || source.ModifiedNS != 23 || source.ReadAuthorityWitness != witness || len(source.ReadPolicyWitness) != 64 {
		t.Fatal("source did not retain the actual selected physical tuple", source)
	}
	if err := checkConditionalSelection(t, f, d, source); err != nil {
		t.Fatal(err)
	}
	other, err := resolveConditionalSelection(t, f, d, principal, identity.Token())
	if err != nil {
		t.Fatal(err)
	}
	source.ReadAuthorityWitness, other.ReadAuthorityWitness = "", ""
	if source != other {
		t.Fatal("an opaque operation identifier changed physical/policy facts")
	}
	assertConditionalPreflightNoWork(t, f)
}

func TestConditionalDiscSelectionRefusesDriftWithoutAdoptingReplacement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []struct{ name, mutation string }{
		{"observation", `UPDATE remote_scan_observations SET revision=revision+1 WHERE source_id='remote'`},
		{"profile", `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',2,'basic',0,2,4,0)`},
		{"source generation", `UPDATE remote_sources SET generation=generation+1 WHERE id='remote'`},
		{"unqualified range", `UPDATE remote_sources SET range_support='unsupported' WHERE id='remote'`},
		{"weak validator", `UPDATE remote_scan_observations SET provider_etag='W/"provider-iso"' WHERE source_id='remote'`},
		{"disabled physical source", `UPDATE library_sources SET enabled=0 WHERE remote_source_id='remote'`},
		{"new inventory object", `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,unsupported_reason) SELECT 'replacement-object',source_id,asset_id,root_incarnation,'disc-replacement.iso',revision,evidence_json,size,modified_ns,state,unsupported_reason FROM inventory_objects WHERE id='disc-object'; UPDATE inventory_objects SET retired=1 WHERE id='disc-object'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, d, principal, witness := conditionalSelectionFixture(t)
			source, err := resolveConditionalSelection(t, f, d, principal, witness)
			if err != nil {
				t.Fatal("positive control", err)
			}
			if _, err = f.s.db.Exec(test.mutation); err != nil {
				t.Fatal(err)
			}
			if err = checkConditionalSelection(t, f, d, source); err == nil {
				t.Fatal("source proof adopted changed or replacement state")
			}
			assertConditionalPreflightNoWork(t, f)
		})
	}
}

func TestConditionalDiscResolveRequiresRealActorAndUniqueOriginalSelection(t *testing.T) {
	f, d, principal, witness := conditionalSelectionFixture(t)
	if _, err := resolveConditionalSelection(t, f, d, principal, witness); err != nil {
		t.Fatal(err)
	}
	d.authority.authorize = func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error) {
		return identity.Principal{}, identity.ErrForbidden
	}
	if _, err := resolveConditionalSelection(t, f, d, principal, witness); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("conditional source skipped the actual content authorizer", err)
	}
	d.authority.authorize = func(_ context.Context, _ *sql.Tx, p identity.Principal, _ string) (identity.Principal, error) {
		return p, nil
	}
	// The schema already forbids the same physical path twice in one source, so
	// the ambiguous selection is a real second copy of the asset at another path.
	if _, err := f.s.db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,unsupported_reason) SELECT 'duplicate-object',source_id,asset_id,root_incarnation,'disc-copy.iso',revision,evidence_json,size,modified_ns,state,unsupported_reason FROM inventory_objects WHERE id='disc-object'`); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConditionalSelection(t, f, d, principal, witness); err == nil {
		t.Fatal("ambiguous physical selection picked a first object")
	}
	assertConditionalPreflightNoWork(t, f)
}

func TestConditionalDiscOpenRefusesBeforeLeasingOrProviderWork(t *testing.T) {
	f, d, principal, witness := conditionalSelectionFixture(t)
	source, err := resolveConditionalSelection(t, f, d, principal, witness)
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("original preparation or active presentation retired")
	if _, err = d.Open(context.Background(), source, func(context.Context, *sql.Tx) error { return refused }); !errors.Is(err, refused) {
		t.Fatal("Open ignored the original accepting proof", err)
	}
	if _, err = d.Open(context.Background(), source, nil); err == nil {
		t.Fatal("Open accepted no caller proof")
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = d.Open(view.Context(), source, func(context.Context, *sql.Tx) error { called = true; return nil })
	view.Rollback()
	if err == nil || called {
		t.Fatal("SQL context entered caller/provider work", err, called)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.Open(ctx, source, func(context.Context, *sql.Tx) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled context reached caller/provider work", err, called)
	}
	altered := source
	altered.ReadPolicyWitness = strings.Repeat("0", 64)
	if _, err = d.Open(context.Background(), altered, func(context.Context, *sql.Tx) error { return nil }); err == nil {
		t.Fatal("a rewritten policy witness reached acquisition")
	}
	assertConditionalPreflightNoWork(t, f)
}

func TestConditionalDiscAcquisitionRetainsSelectedBindingAndChecksBeforeProvider(t *testing.T) {
	f, _, _, _ := conditionalSelectionFixture(t)
	selected, relative, ok := f.s.find(f.file)
	if !ok || selected.ID != "remote" || relative != "disc.iso" {
		t.Fatal("fixture did not select its actual DAV object")
	}
	// A successor registry must not redirect an already selected acquisition.
	// The exact binding still opens its own sealed configuration, then the
	// original lease proof may refuse before stat/version discovery or caching.
	f.s.bindings.Store([]binding{{ID: "other-source", Kind: "webdav", Root: selected.Root}})
	f.s.playback = make(chan struct{}, 1)
	f.s.active = make(map[string]map[string]activeRead)
	refused := errors.New("original source proof retired")
	called := false
	_, err := f.s.acquireBinding(context.Background(), f.file, "playback", selected, relative, func(_ context.Context, generation string) error {
		called = true
		if generation != "1" {
			t.Fatal("configuration did not belong to original selected source", generation)
		}
		return refused
	})
	if !called || !errors.Is(err, refused) || len(f.s.playback) != 0 {
		t.Fatal("selected acquisition skipped proof or leaked its slot", called, err)
	}
	called = false
	selected.Kind = "sftp"
	_, err = f.s.acquireBinding(context.Background(), f.file, "playback", selected, relative, func(context.Context, string) error { called = true; return nil })
	if !errors.Is(err, storage.ErrRemoteRange) || called {
		t.Fatal("conditional handoff entered a native fallback", err, called)
	}
	assertConditionalPreflightNoWork(t, f)
}
