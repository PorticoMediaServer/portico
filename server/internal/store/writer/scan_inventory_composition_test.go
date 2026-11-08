package writer

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// ONE existing native opening, GV keeper, governor and W dispatcher. All work,
// byte amounts and the frozen clock are existing synthetic component inputs;
// they are not a qualified profile and no startup installs them in production.
type n1CompositionFixtureProfile uint8

const (
	n1OperatingForecastFixture n1CompositionFixtureProfile = 1
	// Opt-in measurement only. Real time is chosen BEFORE the existing keeper,
	// governor and driver are constructed; no frozen epoch is rebound later.
	n1OperatingForecastTimingFixture n1CompositionFixtureProfile = 2
	n1EC4ScreeningFixture            n1CompositionFixtureProfile = 3
)

func n1ComposedDriverFixture(t *testing.T, selected ...n1CompositionFixtureProfile) (*ScanDriver, *scanObservationFaultTransport) {
	t.Helper()
	d, transport, _ := n1ComposedDriverFixtureState(t, selected...)
	return d, transport
}

// The SAME fixture exposes existing owner/trace state for opt-in measurement.
// No callback, grant constructor, profile override or second opening is added.
func n1ComposedDriverFixtureState(t *testing.T, selected ...n1CompositionFixtureProfile) (*ScanDriver, *scanObservationFaultTransport, *checkpointComponent) {
	t.Helper()
	if len(selected) > 1 || (len(selected) == 1 && selected[0] != n1OperatingForecastFixture && selected[0] != n1OperatingForecastTimingFixture && selected[0] != n1EC4ScreeningFixture) {
		t.Fatal("unknown closed composition fixture profile")
	}
	realClock := len(selected) == 1 && (selected[0] == n1OperatingForecastTimingFixture || selected[0] == n1EC4ScreeningFixture)
	var modes []checkpointFixtureMode
	if len(selected) == 1 && selected[0] == n1EC4ScreeningFixture {
		modes = []checkpointFixtureMode{checkpointScreeningReader}
	}
	cp := newCheckpointComponent(t, true, realClock, modes...)
	seedScanLifecycle(t, cp.w)
	for _, q := range []string{
		`UPDATE libraries SET metadata_language='en-US',data_revision=71,data_order_revision=73,data_metadata_revision=79,age_input_revision=83,age_published_revision=83 WHERE id='lifecycle-library'`,
		`UPDATE catalog_libraries SET root='/retained-library-identity' WHERE id=7`,
		`INSERT INTO library_scan_policies(library_id,revision) VALUES('lifecycle-library',7)`,
		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,89)`,
	} {
		if _, err := cp.w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Same explicit RAW bank population as the existing N1 component. This
	// is required native configuration, never a runtime initialization fallback.
	if _, err := cp.w.db.ExecContext(t.Context(), `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`, searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.w.db.ExecContext(t.Context(), `UPDATE search_revision SET revision=103,input_sequence=107,active_generation_id=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	return n1ComposedDriverCompose(t, cp, realClock, selected...)
}

// n1ComposedDriverOnExisting rebinds the SAME composed public driver on an
// existing canonical file after a restart. It seeds nothing and replays no
// initialization SQL; only the real registry/plans/governor are reconstructed.
func n1ComposedDriverOnExisting(t *testing.T, path string) *ScanDriver {
	t.Helper()
	d, _, _ := n1ComposedDriverCompose(t, newCheckpointComponentAt(t, path, true, false), false)
	return d
}

func n1ComposedDriverCompose(t *testing.T, cp *checkpointComponent, realClock bool, selected ...n1CompositionFixtureProfile) (*ScanDriver, *scanObservationFaultTransport, *checkpointComponent) {
	t.Helper()
	// Explicit component input, obtained from the actual existing file. Setting
	// the assertion changes no page size; Assert repeats the read before BEGIN.
	if err := cp.w.db.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&cp.w.config.WriterPageSize); err != nil {
		t.Fatal(err)
	}
	defs := scanLifecycleCommandDefinitions()
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		seen[d.ID] = true
	}
	for _, d := range inventoryDriverCommandDefinitions() {
		if !seen[d.ID] {
			defs = append(defs, d)
			seen[d.ID] = true
		}
	}
	if len(defs) != 84 {
		t.Fatal("complete native scan union", len(defs))
	}
	conn, err := cp.w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, cp.w.config, defs, "scan_inventory_composition_test.go", "scan_run_lifecycle_test.go")
	engine, err := sqliteconn.AttestEngine(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	r := &effectiveRuntime{executor: cp.w, path: cp.path, governor: cp.g, authority: cp.a, wal: cp.wal, owner: 1, registry: registry}
	r.plan.registration = golden.Receipt()
	transport := &scanObservationFaultTransport{page: `{"Version":1,"RootIdentity":"source-root","DirectoryIdentity":"actual-directory","DirectoryRevision":"directory-revision","Complete":true,"Entries":[],"Issues":[]}`}
	d, err := newScanDriver(t.Context(), cp.opening, cp.owner, &Writer{effective: r}, nativeinventory.NewObserver(transport))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	recipe := effectiveFixturePlan().recipe
	profile := func(defs []stmt.Definition, inventory bool) effectivePlan {
		p := effectivePlan{recipe: recipe, headerSync: Work{WALBytes: 32, Hold: 5 * time.Millisecond}, registration: golden.Receipt()}
		p.need[governor.MemoryBytes], p.need[governor.CPUMilli] = 8<<20, 100
		p.need[governor.WALBytes], p.need[governor.IOBytes] = int64(recipe.limit.WALBytes), 64<<20
		p.io = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", cp.opening.binding.device), Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
		if inventory {
			p.inventoryMemory = inventoryMemoryBudget{4 << 20, 4 << 20}
		} else {
			p.scanLifecycleMemory = commandMemoryBudget{4 << 20, 4 << 20}
		}
		if len(defs) == len(inventoryDriverCommandDefinitions()) {
			if len(selected) == 1 {
				forecast, err := inventoryN1Forecast(golden.Receipt(), engine, cp.w.config.WriterPageSize, cp.opening.binding.device)
				if err != nil {
					t.Fatal(err)
				}
				return forecast
			}
			p.inventoryEngine = engine
			unit := Work{16, 2000, 16, 65536, 100 * time.Microsecond}
			// Derive the synthetic record envelope from the UNCHANGED unit's
			// page/WAL funds, rather than claiming 64KiB fits 16 physical pages.
			usable, err := inventoryUsablePageFloor(cp.w.config.WriterPageSize)
			if err != nil {
				t.Fatal(err)
			}
			pages := min(unit.Pages, (unit.WALBytes-32)/(uint64(cp.w.config.WriterPageSize)+24))
			if pages <= 1 {
				t.Fatal("component has no record storage allowance")
			}
			p.inventoryRecords = inventoryRecordEnvelope{maxBytes: (pages - 1) * (usable - 4), work: unit}
		}
		for _, def := range defs {
			p.steps = append(p.steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
		}
		return p
	}
	lifecycle := profile(scanLifecycleCommandDefinitions(), false)
	readyNeed := lifecycle.need
	readyNeed[governor.WALBytes], readyNeed[governor.IOBytes] = 0, 0
	// Trusted issuer horizons. The frozen-clock default keeps its 1s synthetic
	// values. The opt-in real-clock timing profile uses realistic horizons: an
	// N1 preparation's funded deadline is min(now+ready, lease expiry) and a
	// renewal never extends one in flight, so a 1s lease would let one ordinary
	// hosted disk/checkpoint stall end a long scan. Product horizons remain an
	// unselected profile choice; per-command 3s safety is unchanged.
	readyWindow, leaseHorizon := time.Second, time.Second
	if realClock {
		readyWindow, leaseHorizon = 10*time.Second, 60*time.Second
	}
	ready := scanPreparationPlan{need: readyNeed, budget: governor.UnitBudget{RawRows: 4096, VMOps: 1000000, Pages: 4096}, ready: readyWindow}
	if err = configureScanIssuer(t.Context(), d, lifecycle, lifecycle, ready, leaseHorizon); err != nil {
		t.Fatal(err)
	}
	if err = composeInventoryPreparation(t.Context(), d, profile(inventoryPreparationDefinitions(), true), profile(inventoryHistoryDefinitions(), true)); err != nil {
		t.Fatal(err)
	}
	if err = composeInventoryDriver(t.Context(), d, profile(inventoryDriverCommandDefinitions(), true)); err != nil {
		t.Fatal(err)
	}
	return d, transport, cp
}

func n1ComposedDriverPrepared(t *testing.T, selected ...n1CompositionFixtureProfile) (*ScanDriver, *N1Preparation, InventoryAcceptCommand, ScanReceiptRequest) {
	t.Helper()
	d, transport := n1ComposedDriverFixture(t, selected...)
	creation, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := creation.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := creation.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = creation.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = creation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	claimRef, err := claim.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err = claim.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = claim.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	lease, err := claim.Lease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	page := nativeinventory.Page{Version: 1, RootIdentity: "source-root", DirectoryIdentity: "actual-directory", DirectoryRevision: "directory-revision", NextCursor: "42", Entries: []nativeinventory.Entry{{Path: []byte("/media-new/The Film (2020).mkv"), Name: []byte("The Film (2020).mkv"), ObjectIdentity: "dev:7:ino:31", ChangeToken: "native-change", Size: 42, ModifiedNS: 7}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	transport.page = string(raw)
	preparation, err := d.PrepareInventory(t.Context(), lease, claimRef, []byte("."))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := preparation.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := preparation.ObserveInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in := snapshot.Input
	e := actual.Entries[0]
	in.Directory.NextCursor, in.Directory.RawNames = actual.NextCursor, 1
	p, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{snapshot.IdentityRoot, e.Path, e.Name, "", "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	m := p.Metadata
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: in.Source.ID + ":" + in.Source.Incarnation, Object: objectidentity.StableObjectIdentity(e.ObjectIdentity, in.Source.RootIdentity), Size: e.Size, ModifiedNS: e.ModifiedNS, ChangeToken: e.ChangeToken}
	revision, err := evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	in.Files = []InventoryFilenameInput{{RelativePath: e.Name, AssetPath: e.Path, SourceKey: p.SourceKey, Year: p.Year, Part: p.Part, ObjectIdentity: e.ObjectIdentity, ChangeToken: e.ChangeToken, Size: e.Size, ModifiedNS: e.ModifiedNS, Revision: revision, Title: m.Title, SortTitle: m.SortTitle, SortKey: m.SortKey, SortHead: m.SortHead, SortKeyKeepArticles: m.SortKeyKeepArticles, SortHeadKeepArticles: m.SortHeadKeepArticles, MetadataLanguage: m.MetadataLanguage}}
	command, err := preparation.Freeze(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	original, err := InventoryAcceptReference(command)
	if err != nil {
		t.Fatal(err)
	}
	return d, preparation, command, original
}

func TestN1ComposedDriverUsesOneKeeperAndOriginalHistory(t *testing.T) {
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	applied, err := preparation.Accept(t.Context(), command)
	if err != nil || applied.Outcome.State != Applied || len(applied.Members) != 1 || applied.Outcome.Reference != original.Receipt {
		t.Fatal("whole driver did not publish committed N1", applied, err)
	}
	if err = preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Retirement is fresh eligibility, not an extra historical disclosure fence.
	if _, err = d.opening.executor.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	replay, err := d.ResolveScan(t.Context(), original)
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Digest != applied.Digest || len(replay.Members) != 1 || replay.Members[0] != applied.Members[0] {
		t.Fatal("same original did not replay after retirement", replay, err)
	}
	ack, err := d.AckScan(t.Context(), original)
	if err != nil || ack.AcknowledgedThrough != original.Receipt.Identity.Sequence {
		t.Fatal("same original did not ACK", ack, err)
	}
	if err = d.ReleasePending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.work != nil || d.preparation != nil {
		t.Fatal("original retained beyond physical retirement")
	}
}

func TestN1OwnerRetentionKeepsExactRegistryNormalizedDefinitions(t *testing.T) {
	seen := 0
	for _, def := range inventoryDriverCommandDefinitions() {
		if def.ID != "scan.inventory_accept.membership_clock" && def.ID != "search.publish.owner.revision_head" {
			continue
		}
		seen++
		catalogue, err := stmt.Describe([]stmt.Definition{def})
		if err != nil {
			t.Fatal(err)
		}
		actual := catalogue.Manifest()[0].Definition
		if !sameNormalizedStatementDefinition(actual, def) {
			t.Fatal("zero-arity registry normalization rejected exact statement", def.ID)
		}
		for _, mutate := range []func(*stmt.Definition){
			func(d *stmt.Definition) { d.SQL += " " },
			func(d *stmt.Definition) { d.Owner = "foreign-owner" },
			func(d *stmt.Definition) { d.Cohorts = nil },
		} {
			changed := actual
			mutate(&changed)
			if sameNormalizedStatementDefinition(changed, def) {
				t.Fatal("normalization widened the frozen definition", def.ID)
			}
		}
	}
	if seen != 2 {
		t.Fatal("missing zero-arity normalization controls", seen)
	}
}
