package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Same real native opening, full Capture and governor. The two-unit capacity,
// virtual clock and source observer are explicit protocol fixtures only.
type scanDriverFixture struct {
	driver   *ScanDriver
	registry *stmt.Registry
	request  governor.Request
	recipe   turnRecipe
	create   scanRunCreateEffects
	clock    *time.Time
}

// A closed protocol-only selector: callers cannot inject definitions, recipes,
// grants or constructors. Omission preserves the original fixture unchanged.
type scanDriverStatementFixture uint8

const scanIssuerStatementFixture scanDriverStatementFixture = 1
const scanIssuerRuntimeFixture scanDriverStatementFixture = 2

func newScanDriverFixture(t *testing.T, selections ...scanDriverStatementFixture) *scanDriverFixture {
	t.Helper()
	if len(selections) > 1 || len(selections) == 1 && selections[0] != scanIssuerStatementFixture && selections[0] != scanIssuerRuntimeFixture {
		t.Fatal("unsupported scan driver statement fixture")
	}
	path, owner := scanOpeningFixture(t)
	o := openScanFixture(t, path, owner)
	create := seedScanLifecycle(t, o.executor)
	return scanDriverFixtureOnOpening(t, o, owner, create, selections, true)
}

// Reopen controls reuse this ONE fixture composition without re-seeding,
// resetting a floor or constructing a second resource bridge.
func scanDriverFixtureOnOpening(t *testing.T, o *scanOpening, owner *stateownership.Lock, create scanRunCreateEffects, selections []scanDriverStatementFixture, initialize bool) *scanDriverFixture {
	t.Helper()
	path := o.binding.path
	defs := append(scanRunLifecycleDefinitions(), scanBirthDefinitions()...)
	defs = append(defs, scanSnapshotByteDefinitions()...)
	if len(selections) == 1 {
		defs = scanLifecycleCommandDefinitions()
		if initialize {
			for _, sql := range []string{
				`UPDATE catalog_libraries SET root='/retained-library-identity' WHERE id=7`,
				`INSERT INTO library_scan_policies(library_id,revision) VALUES('lifecycle-library',7)`,
			} {
				if _, err := o.executor.db.ExecContext(t.Context(), sql); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	registry := cachedNativeInventoryFixtureRegistry(t, o.executor, defs, "scan_driver_test.go", "scan_run_lifecycle_test.go")
	clock := time.Now().Add(time.Hour)
	o.executor.now = func() time.Time { return clock }
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	profile := governedFixtureProfile{recipe, 8 << 20}
	var extra governor.Amounts
	if len(selections) == 1 && selections[0] == scanIssuerRuntimeFixture {
		profile.memoryBytes = 8 << 20
		extra[governor.CPUMilli], extra[governor.IOBytes] = 100, 64<<20
	}
	g, a, wal, request := governedFixtureResources(t, o.executor.now, extra, profile, profile)
	r := &effectiveRuntime{executor: o.executor, path: path, governor: g, authority: a, wal: wal, owner: request.Owner, registry: registry}
	d, err := newScanDriver(t.Context(), o, owner, &Writer{effective: r}, nativeinventory.NewObserver(&scanObservationFaultTransport{page: `{"Version":1,"RootIdentity":"source-root","DirectoryIdentity":"actual-directory","DirectoryRevision":"directory-revision","Complete":true,"Entries":[{"Path":"L21lZGlhLW5ldy9hLm1rdg==","Name":"YS5ta3Y=","ObjectIdentity":"native-object","ChangeToken":"token","Size":1,"ModifiedNS":1}],"Issues":[]}`}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return &scanDriverFixture{d, registry, request, recipe, create, &clock}
}

func TestScanDriverIssuerFixtureCapturesCurrentSource(t *testing.T) {
	// Protocol only: same actual Capture/governor and existing recipe/footprint;
	// this supplies no production source, issuance or acceptance qualification.
	f := newScanDriverFixture(t, scanIssuerStatementFixture)
	if len(f.registry.Manifest()) != 36 {
		t.Fatal("issuer fixture changed its closed 36-ID inventory")
	}
	turn := f.turn(t, InventoryCreate, f.create.scope.runID)
	facts, err := captureScanSource(t.Context(), turn, 7, "lifecycle-library", "lifecycle-new-source")
	if err != nil || string(facts.identityRoot) != "/retained-library-identity" ||
		string(facts.source.resolvedRoot) != "/media-new" || string(facts.identityRoot) == string(facts.source.resolvedRoot) || facts.policyRevision != 7 {
		t.Fatal("protocol source capture lost retained root/policy", facts, err)
	}
}

func (f *scanDriverFixture) preparation(t *testing.T, purpose CommandID, runID string) *scanPreparation {
	t.Helper()
	d := f.driver
	r := d.writer.effective
	request := f.request
	request.Writer = false
	request.Key = governor.JobKey{Domain: uint16(purpose), ID: runID}
	request.Need[governor.WALBytes] = 0
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(purpose)}}
	if err := refreshWriterWAL(r.path, r.wal, r.executor.now); err != nil {
		t.Fatal(err)
	}
	decision := r.governor.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	p, err := newScanPreparation(t.Context(), d.opening, d.owner, r, d.observer, request, decision.Grant, nativeinventory.Request{Root: []byte("/media-new"), RelativePath: []byte("."), Limit: 1})
	if err != nil {
		_ = r.authority.Retired(decision.Grant)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := d.keepPreparation(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *scanDriverFixture) turn(t *testing.T, purpose CommandID, runID string, libraries ...int64) *commandTurn {
	t.Helper()
	r := f.driver.writer.effective
	request := f.request
	request.Key = governor.JobKey{Domain: uint16(purpose), ID: runID}
	library := int64(7)
	if len(libraries) > 1 {
		t.Fatal("multiple fixture footprints")
	}
	if len(libraries) == 1 {
		library = libraries[0]
	}
	request.Footprint = []governor.Scope{{Library: library, Domain: uint16(purpose)}}
	if err := refreshWriterWAL(r.path, r.wal, r.executor.now); err != nil {
		t.Fatal(err)
	}
	decision := r.governor.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(purpose, f.recipe.limit, r.governor, r.authority, r.wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	custody.scanLifecycleMemory = commandMemoryBudget{4 << 20, 4 << 20}
	tx, err := r.executor.begin(t.Context(), purpose, f.recipe, custody)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			_ = custody.retired()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, def := range f.registry.Manifest() {
		steps = append(steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, f.registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestScanDriverSoleNativeOwnerAndNoCopies(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	if other, err := newScanDriver(t.Context(), d.opening, d.owner, d.writer, d.observer); other != nil || !errors.Is(err, ErrBusy) {
		t.Fatal(other, err)
	}
	var copied ScanDriver
	reflect.ValueOf(&copied).Elem().Set(reflect.ValueOf(d).Elem())
	if err := copied.current(); !errors.Is(err, ErrInvalid) {
		t.Fatal("copied driver", err)
	}
	if err := d.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if other, err := newScanDriver(t.Context(), d.opening, d.owner, d.writer, d.observer); other != nil || !errors.Is(err, ErrBusy) {
		t.Fatal("closed opening reminted issuer", other, err)
	}
	if err := d.opening.close(); err != nil {
		t.Fatal(err)
	}
	if err := d.owner.Close(); err != nil {
		t.Fatal("driver native borrower leaked", err)
	}
}

func TestScanDriverFinalConstructorCancellationReleasesBorrow(t *testing.T) {
	o, owner, r, _, grant := scanPreparationFundingFixture(t, time.Now)
	defer r.authority.Retired(grant)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &preparationFinishCancelContext{Context: base, cancel: cancel}
	d, err := newScanDriver(ctx, o, owner, &Writer{effective: r}, nativeinventory.Observer{})
	if d != nil || !errors.Is(err, context.Canceled) || o.driver != nil {
		t.Fatal(d, err, o.driver)
	}
	if err := o.close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanDriverRetainsNativeBorrowUntilActualFrameReturn(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	p := f.preparation(t, InventoryCreate, "lifecycle-new")
	original := ScanReceiptRequest{RunID: "lifecycle-new", LibraryID: 7, LibraryKey: "lifecycle-library", Receipt: ReceiptReference{f.create.identity, f.create.request}}
	work, err := d.prepareWork(t.Context(), InventoryCreate, original, 0, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = work.submitting(); err != nil {
		t.Fatal(err)
	}
	turn := f.turn(t, InventoryCreate, original.RunID)
	if err = work.bindFrame(turn.transaction); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(t.Context()); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("open native frame released driver", err)
	}
	if work.physicallyReturned() {
		t.Fatal("cancel counted as return")
	}
	if p.retired || d.work != work || d.preparation != p {
		t.Fatal("live native work lost its preparation memory custody")
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestScanDriverExclusionRequiresActualPreparationAndSettledFloor(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	p := f.preparation(t, InventoryAccept, "lifecycle-run")
	original := ScanReceiptRequest{RunID: "lifecycle-run", LibraryID: 7, LibraryKey: "lifecycle-library", Receipt: ReceiptReference{CommandIdentity{f.create.identity.Incarnation, 2, [16]byte{17}}, RequestDigest{19}}}
	if _, err := d.prepareWork(t.Context(), InventoryAccept, original, 0, p); err != nil {
		t.Fatal(err)
	}
	turn := f.turn(t, InventoryClaim, original.RunID)
	before, err := readScanRunSnapshot(t.Context(), turn, original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.excludeOldWork(t.Context(), turn, before); !errors.Is(err, ErrBusy) {
		t.Fatal("prepared work excluded by settled floor", err)
	}
	_, _ = turn.transaction.rollback(nil)
	if err = p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	turn = f.turn(t, InventoryClaim, original.RunID)
	if err = d.excludeOldWork(t.Context(), turn, before); err != nil || d.work != nil {
		t.Fatal("joined unsubmitted preparation not excluded", err)
	}
	_, _ = turn.transaction.rollback(nil)
	// This is a legitimate native unsettled predecessor, not expiry or a local
	// completion flag. It must still prevent a new generation with no live work.
	if _, err = d.opening.executor.db.ExecContext(t.Context(), "UPDATE inventory_runs SET acknowledged_command_sequence=0 WHERE job_id='lifecycle-run'"); err != nil {
		t.Fatal(err)
	}
	turn = f.turn(t, InventoryClaim, original.RunID)
	before, err = readScanRunSnapshot(t.Context(), turn, original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.excludeOldWork(t.Context(), turn, before); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unsettled native floor allowed claim", err)
	}
}

func TestScanDriverBirthRequiresOriginalNativeAbsence(t *testing.T) {
	for _, conflict := range []string{"none", "job", "operation", "active", "retention"} {
		t.Run(conflict, func(t *testing.T) {
			f := newScanDriverFixture(t)
			d := f.driver
			b, err := d.allocateBirth(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.allocateBirth(t.Context()); !errors.Is(err, ErrBusy) {
				t.Fatal("second birth", err)
			}
			p := f.preparation(t, InventoryCreate, b.runID)
			if _, err = p.ObserveInventory(t.Context()); err != nil {
				t.Fatal(err)
			}
			source := scanSourceFacts{libraryID: 7, libraryKey: "lifecycle-library", policyRevision: 7, source: inventorySource{id: "lifecycle-new-source", incarnation: "source-incarnation", rootIdentity: "source-root", resolvedRoot: []byte("/media-new"), generation: 23}}
			command, err := d.prepareBirth(t.Context(), b, p, source, "first-import")
			if err != nil {
				t.Fatal(err)
			}
			original := ScanReceiptRequest{RunID: b.runID, LibraryID: 7, LibraryKey: "lifecycle-library", Receipt: ReceiptReference{command.identity, command.digest}}
			work, err := d.prepareWork(t.Context(), InventoryCreate, original, 0, p)
			if err != nil {
				t.Fatal(err)
			}
			switch conflict {
			case "job":
				_, err = d.opening.executor.db.ExecContext(t.Context(), "INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'lifecycle-library','queued','2026-11-06T00:00:00.000000000Z')", b.runID)
			case "operation":
				_, err = d.opening.executor.db.ExecContext(t.Context(), "UPDATE inventory_runs SET create_operation_id=? WHERE job_id='lifecycle-run'", b.identity.OperationID[:])
			case "active":
				_, err = d.opening.executor.db.ExecContext(t.Context(), "INSERT INTO jobs(id,library_id,status,created_at) VALUES('foreign-active','lifecycle-library','queued','2026-11-06T00:00:00.000000000Z')")
				if err == nil {
					_, err = d.opening.executor.db.ExecContext(t.Context(), "INSERT INTO inventory_source_active(source_id,job_id) VALUES('lifecycle-new-source','foreign-active')")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = work.submitting(); err != nil {
				t.Fatal(err)
			}
			turn := f.turn(t, InventoryCreate, b.runID)
			if err = work.bindFrame(turn.transaction); err != nil {
				t.Fatal(err)
			}
			if conflict == "retention" {
				d.retention = nil
			}
			err = d.checkBirthAbsence(t.Context(), turn, b)
			if conflict == "none" {
				if err != nil {
					t.Fatal(err)
				}
			} else if conflict == "retention" {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
					t.Fatal("unproved retention became absence", err)
				}
			} else if !errors.Is(err, ErrResolveRequired) {
				t.Fatal("conflicting owner became absence", err)
			}
			// This test checks original birth custody/absence only. Source revalidation
			// and actual create execution remain the separate same-turn adapter gate.
		})
	}
}

func TestScanDriverRetentionRejectsUnknownAndReplacedRegistries(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	if err := d.retention.check(d.opening.executor, f.registry); err != nil {
		t.Fatal(err)
	}
	defs := append(scanRunLifecycleDefinitions(), scanBirthDefinitions()...)
	defs = append(defs, scanSnapshotByteDefinitions()...)
	other := cachedNativeInventoryFixtureRegistry(t, d.opening.executor, defs, "scan_driver_test.go", "scan_run_lifecycle_test.go")
	if err := d.retention.check(d.opening.executor, other); err == nil {
		t.Fatal("registry replacement preserved birth retention witness")
	}
	added := defs[0]
	added.ID = "scan.issuer.unreviewed_owner_protocol"
	if scanOwnerRetentionID(added.ID) {
		t.Fatal("provider growth silently gained a retention policy")
	}
	extended := cachedNativeInventoryFixtureRegistry(t, d.opening.executor, append(defs, added), "scan_driver_test.go", "scan_run_lifecycle_test.go")
	if proof, err := scanOwnerRetentionFor(d.opening.executor, extended); proof != nil || err == nil {
		t.Fatal("unreviewed owner protocol gained no-GC proof", proof, err)
	}
	if _, err := scanOwnerRetentionFor(d.opening.executor, nil); err == nil {
		t.Fatal("nil registry gained no-GC proof")
	}
}

func TestScanDriverRejectedFrameKeepsPhysicalCustody(t *testing.T) {
	for _, fault := range []string{"footprint", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			f := newScanDriverFixture(t)
			d := f.driver
			p := f.preparation(t, InventoryAccept, "lifecycle-run")
			q := ScanReceiptRequest{RunID: "lifecycle-run", LibraryID: 7, LibraryKey: "lifecycle-library", Receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 1}}}
			work, err := d.prepareWork(t.Context(), InventoryAccept, q, 0, p)
			if err != nil {
				t.Fatal(err)
			}
			if err = work.submitting(); err != nil {
				t.Fatal(err)
			}
			library := int64(7)
			if fault == "footprint" {
				library = 8
			}
			turn := f.turn(t, InventoryAccept, q.RunID, library)
			if fault == "deadline" {
				deadline, ok := turn.transaction.ctx.Deadline()
				if !ok {
					t.Fatal("protected deadline missing")
				}
				p.expected.Deadline = deadline.Add(-time.Nanosecond)
			}
			if err = work.bindFrame(turn.transaction); !errors.Is(err, ErrInvalid) || work.submission != scanFrameRejected || work.frame != turn.transaction {
				t.Fatal("foreign frame granted effect custody", err, work.submission)
			}
			if work.physicallyReturned() {
				t.Fatal("rejected live frame was refunded")
			}
			if _, err = turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if err = p.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !work.physicallyReturned() {
				t.Fatal("joined rejected frame lost cleanup custody")
			}
		})
	}
}

func TestScanDriverCloseJoinsPreparationBeforeAnyCommand(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	p := f.preparation(t, InventoryAccept, "lifecycle-run")
	if d.work != nil || d.preparation != p {
		t.Fatal("fixture already submitted work")
	}
	if err := d.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !p.retired || !d.released || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("pre-command grant or native borrow survived driver close")
	}
}

// Independent review reproduction: follow the permanent opening root rather
// than a local pointer or GC timing. The tombstone must not retain refunded data.
func TestIndependentTombstoneRefundsRetainedMemory(t *testing.T) {
	f := newScanDriverFixture(t)
	p := f.preparation(t, InventoryAccept, "lifecycle-run")
	if _, err := p.ObserveInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	tombstone := f.driver.opening.driver
	if tombstone.preparation != nil && tombstone.preparation.observed != nil &&
		len(tombstone.preparation.observed.Entries) > 0 &&
		f.driver.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("permanent opening tombstone retains observed preparation payload after its memory grant was refunded")
	}
}

func TestScanDriverTombstoneDropsEveryPreparationRoot(t *testing.T) {
	f := newScanDriverFixture(t)
	d := f.driver
	p := f.preparation(t, InventoryAccept, "lifecycle-run")
	if _, err := p.ObserveInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	q := ScanReceiptRequest{RunID: "lifecycle-run", LibraryID: 7, LibraryKey: "lifecycle-library", Receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 1}}}
	if _, err := d.prepareWork(t.Context(), InventoryAccept, q, 0, p); err != nil {
		t.Fatal(err)
	}
	// Deliberate alias-retention control only; this does not mint birth proof.
	d.birth = &scanBirth{driver: d, preparation: p}
	if err := d.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.opening.driver != d || d.preparation != nil || d.work != nil || d.birth != nil || d.retention != nil {
		t.Fatal("closed tombstone retained preparation/birth/work roots")
	}
	if !p.retired || p.observed != nil || !reflect.DeepEqual(p.request, nativeinventory.Request{}) ||
		p.key != "" || !reflect.DeepEqual(p.expected, governor.Request{}) || !reflect.DeepEqual(p.observer, nativeinventory.Observer{}) {
		t.Fatal("closed preparation retained refunded payload buffers")
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("joined preparation reservation was not retired")
	}
	if err := d.Close(t.Context()); err != nil {
		t.Fatal("minimal tombstone close is not idempotent", err)
	}
	if other, err := newScanDriver(t.Context(), d.opening, d.owner, d.writer, d.observer); other != nil || !errors.Is(err, ErrBusy) {
		t.Fatal("minimal tombstone allowed a replacement issuer", other, err)
	}
}
