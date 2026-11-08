package writer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type ec2NativeComponent struct {
	opening            *scanOpening
	keeper             *canonicalN1MaintenanceOwner
	receipt            stmt.Receipt
	queue              chan struct{}
	writer             *Writer
	progress           *WCProgressDriver
	security           *WCSecurityDriver
	reclaim            *WCReclaimDriver
	lastProgress       ProgressSnapshot
	targetOrdinal      int
	retainedBackground governor.Amounts
}

// TEST-ONLY exact composition, using the same original seed/fixture and owners.
func NewEC2PairedComponent(t *testing.T, data []byte, warmup int, cohort string) *N1PairedComponent {
	t.Helper()
	return newN1PairedPopulationComponent(t, data, warmup, cohort, attachN1PairedPopulation(t), true)
}

func newEC2NativeOwner(t *testing.T, seed *executor, path string, original *inventoryAcceptCommand, trace *n1Trace, gc governor.Config) (*inventoryMemoryFixture, *ec2NativeComponent) {
	t.Helper()
	seedConfig := seed.config
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	seed.physicalFaults.mu.Lock()
	failed := seed.physicalFaults.failed
	seed.physicalFaults.mu.Unlock()
	if s := seed.db.Stats(); failed || s.InUse != 0 || s.OpenConnections != 0 {
		t.Fatal("EC2 seed did not join")
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	w := opening.executor
	if w.config != seedConfig {
		t.Fatal("EC2 reopen config drift")
	}
	// Reuse the existing connector decoration seam after its initial pool joins.
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	w.physicalFaults.mu.Lock()
	failed = w.physicalFaults.failed
	w.physicalFaults.mu.Unlock()
	if s := w.db.Stats(); failed || s.InUse != 0 || s.OpenConnections != 0 {
		t.Fatal("EC2 opening pool did not join")
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	w.openPool(connector, trace.decorate)
	w.now = time.Now
	defs := inventoryCommandDefinitions()
	for _, group := range [][]stmt.Definition{enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions(), effectiveDispatchDefinitions(), wcProgressCommandDefinitions(), wcProgressBirthDefinitions(), wcSecurityCommandDefinitions(), wcReclaimCommandDefinitions()} {
		for _, d := range group {
			found := false
			for _, old := range defs {
				if old.ID == d.ID {
					if !reflect.DeepEqual(old, d) {
						t.Fatal("conflicting complete registry", d.ID)
					}
					found = true
					break
				}
			}
			if !found {
				defs = append(defs, d)
			}
		}
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, w.config, defs, "ec2_native_component_test.go", "scan_fresh_test.go", "wc_component_fixture_test.go", "wc_progress_fixture.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	// Add the unchanged WC unit/preparation capacities to independently funded
	// classes. Read LIVE/N1 command profiles and WAL limits remain unchanged.
	wc := effectiveFixturePlan().need
	wc[governor.MemoryBytes] += 2 << 20
	for resource, n := range wc {
		gc.Partitions[governor.Foreground][resource] += n
		gc.Partitions[governor.Background][resource] = n
	}
	maintenance := canonicalN1Maintenance()
	for resource, n := range wc {
		maintenance.need[resource] += n
	}
	owner := newInventoryMemoryFixtureComposition(t, w, registry, original, inventoryMemoryBudget{4 << 20, 4 << 20}, &gc, maintenance)
	keeper := newCanonicalCheckpointOwner(t, opening, owner.governor, owner.authority, owner.wal, path)
	return owner, &ec2NativeComponent{opening: opening, keeper: keeper, receipt: golden.Receipt(), queue: make(chan struct{}, 1)}
}

func (c *N1PairedComponent) attachEC2WC(t *testing.T, population *QualificationPopulationManifest) {
	t.Helper()
	n := c.native
	catalog := wcProgressExistingCatalog{libraryID: 1, subjectID: 1, libraryKey: "movies"}
	binary.LittleEndian.PutUint64(catalog.publicID[:], 1)
	if population != nil {
		catalog = wcProgressExistingCatalog{7, population.Facts.FirstID, "qualification-movies", qualificationMovieBlob("QMP1", population.Facts.Seed, 1)}
	}
	w, p, _, k := wcComponentFixture(t, wcFixtureOptions{kind: "mixed", actorTier: "owner", catalog: &catalog, maintenance: true, realClock: true, attached: &wcFixtureAttachment{n.opening, c.Governor, c.Authority, c.fixture.owner.wal, n.receipt, n.keeper}})
	if k != n.keeper {
		t.Fatal("second EC2 keeper")
	}
	n.writer, n.progress = w, p
	n.security = wcSecurityDriverForFixture(t, w, p)
	n.reclaim = wcReclaimForProgressFixture(t, w, p)
	n.retainedBackground = n.reclaim.expected.Need
	c.fixture.retainedBackground = n.retainedBackground
	c.trace.useRegistry(c.fixture.owner.registry)
	if err := c.stream.append("ec2_native_binding", map[string]any{"same_executor": true, "writer_native_begins_at_setup_return": c.trace.nativeBegins, "same_governor": true, "full_registry": c.fixture.owner.registry.Manifest(), "wal_policy": canonicalN1Maintenance().policy, "read_partition_unchanged": true, "additional_wc_unit": effectiveFixturePlan().need, "retained_preparation_deadlines": []time.Time{p.expected.Deadline, n.security.expected.Deadline, n.reclaim.expected.Deadline}, "qualification": false}); err != nil {
		t.Fatal(err)
	}
}
func (c *N1PairedComponent) EC2Writer() *Writer {
	if c.native == nil {
		return nil
	}
	return c.native.writer
}
func (c *N1PairedComponent) EC2Progress() *WCProgressDriver {
	if c.native == nil {
		return nil
	}
	return c.native.progress
}
func (c *N1PairedComponent) EC2Security() *WCSecurityDriver {
	if c.native == nil {
		return nil
	}
	return c.native.security
}
func (c *N1PairedComponent) EC2Reclaim() *WCReclaimDriver {
	if c.native == nil {
		return nil
	}
	return c.native.reclaim
}
func (c *N1PairedComponent) EC2ReportSequence() int64 {
	return c.native.lastProgress.LastAcceptedSequence + 1
}
func (c *N1PairedComponent) EC2ProgressSnapshot(v ProgressSnapshot) { c.native.lastProgress = v }
func (c *N1PairedComponent) EC2SecurityTarget(t *testing.T, ctx context.Context) (string, error) {
	if c.native == nil {
		return "", ErrInvalid
	}
	n := c.native
	n.targetOrdinal++
	nativeBefore := c.trace.nativeBegins
	start := time.Now()
	id, err := wcSecurityTargetForFixture(t, n.writer, n.progress, "member", fmt.Sprintf("ec2-target-%d", n.targetOrdinal))
	end := time.Now()
	return id, errors.Join(err, c.stream.append("ec2_security_target_birth", map[string]any{"target": id, "writer_native_begins_before": nativeBefore, "writer_native_begins_after": c.trace.nativeBegins, "started_ns": n1NS(c.stream.origin, start), "returned_ns": n1NS(c.stream.origin, end), "error": n1Error(err), "scope": "real native auxiliary birth, not another measured FULL commit"}))
}

// Bounded scheduler queue: submission remains outside the queue; no replacement
// identity or renewed deadline is introduced while waiting for this ONE writer.
func (c *N1PairedComponent) EnterEC2Writer(ctx context.Context) (func(), error) {
	if c.native == nil {
		return func() {}, nil
	}
	select {
	case c.native.queue <- struct{}{}:
		return func() { <-c.native.queue }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c *N1PairedComponent) EC2Checkpoint(ctx context.Context, recovery bool) (governor.CheckpointResult, error) {
	if c.native == nil {
		return governor.CheckpointResult{}, ErrInvalid
	}
	r := c.native.keeper.request("passive")
	var out governor.CheckpointResult
	var err error
	if recovery {
		r.Key.ID = "canonical-ec2-cp/recovery"
		out, err = c.native.keeper.cp.RecoverWAL(ctx, r)
	} else {
		out, err = c.native.keeper.cp.Step(ctx, r)
	}
	record := canonicalN1Checkpoint{Result: out, Error: n1Error(err)}
	record.observeClock(c.stream.origin)
	return out, errors.Join(err, c.stream.append("ec2_checkpoint", map[string]any{"checkpoint": record, "recovery_requested": recovery, "recovery_drain_started_ns": n1NS(c.stream.origin, out.RecoveryDrainStarted), "recovery_drain_ended_ns": n1NS(c.stream.origin, out.RecoveryDrainEnded), "qualification": false}))
}
func (c *N1PairedComponent) ObserveEC2WC(v CanonicalWCObservation, purpose CommandID) error {
	var window *n1OwnerWindow
	if v.Started != nil && v.Returned != nil {
		if view, ok := c.fixture.owner.executor.lastOwnerWindow(); ok {
			window = n1BindOwnerWindowForPurpose(view, c.stream.origin, *v.Started, *v.Returned, purpose)
		}
	}
	raw, err := json.Marshal(v.Raw)
	if err != nil {
		return err
	}
	return c.stream.append("ec2_wc", map[string]any{"phase": v.Phase, "started_ns": n1NullableTime(c.stream.origin, v.Started), "returned_ns": n1NullableTime(c.stream.origin, v.Returned), "purpose": purpose, "private_owner_window": window, "raw": json.RawMessage(raw), "native_begin_census": c.trace.nativeBegins, "error": v.Error, "scope": "call-bound observation; failed public Evidence stays zero; no later Resolve reconstruction"})
}
func n1NullableTime(origin time.Time, p *time.Time) *int64 {
	if p == nil {
		return nil
	}
	return n1NS(origin, *p)
}
func (c *N1PairedComponent) closeEC2Native() error {
	if c.native == nil {
		return nil
	}
	n := c.native
	var err error
	if n.reclaim != nil {
		err = errors.Join(err, n.reclaim.Close(context.Background()))
	}
	if n.security != nil {
		err = errors.Join(err, n.security.Close(context.Background()))
	}
	if n.progress != nil {
		err = errors.Join(err, n.progress.Close(context.Background()))
	}
	if n.keeper != nil {
		err = errors.Join(err, n.keeper.close())
	}
	return err
}

// Tiny actual artifact, same owner path; no relaxation of the public1M gate.
func EC2NativeFunctionalComponent(t *testing.T) *N1PairedComponent {
	t.Helper()
	if os.Getenv("PORTICO_EC2_NATIVE_FUNCTIONAL") != "1" {
		t.Skip("opt-in real-clock EC2 native composition")
	}
	dir, pin := canonicalN1ControlArtifact(t)
	manifest := UseQualificationPopulation(t, dir, pin)
	data, _ := json.Marshal(firstLightConfig{RunID: "EC2_NATIVE_CONTROL", HostEpoch: "CONTROL", SourceCommit: strings.Repeat("a", 40), OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Fixtures: 1})
	return newN1PairedPopulationComponent(t, data, 1, "timing", &manifest, true)
}
func (c *N1PairedComponent) EC2ProgressPending() bool { return c.native.lastProgress.Pending }

func (c *N1PairedComponent) missingEC2Bindings() []string {
	if c.native != nil {
		return []string{"Effective Actions"}
	}
	return []string{"WC/Effective Actions", "GV concurrent/recovery"}
}
