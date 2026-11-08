package writer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

const canonicalBasicRecipe = "canonical-basic-first-prepared-leaf-v1"
const canonicalBasicWarmup = 8

type canonicalBasicComponent struct {
	writer                        *Writer
	opening                       *scanOpening
	maintenance                   *canonicalN1MaintenanceOwner
	seed                          canonicalBasicSeed
	trace                         *n1Trace
	stream                        *n1Stream
	original                      basicOriginalScope
	identity                      CommandIdentity
	measured                      bool
	calls, physical, fresh, acked int
}

func newCanonicalBasicComponent(t *testing.T, count int, stream *n1Stream, canonical, measured bool) *canonicalBasicComponent {
	t.Helper()
	seed, path, _ := testExecutor(t)
	if !canonical {
		seedQualificationPopulation(t, seed, qualificationPopulationSpec{Seed: 17, Residents: 4})
	}
	prepared := seedCanonicalBasicInputs(t, seed, count)
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	e := opening.executor
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	trace := newN1Trace(stream.origin, true)
	connector, err := sqliteconn.Connector(path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	e.openPool(connector, trace.decorate)

	first, err := prepared.packet(0, prepared.original, prepared.identity)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _, _, _ := basicAcceptRuntimeFixture(t, e, first, canonicalN1Maintenance())
	trace.useRegistry(w.effective.registry)
	m := newCanonicalCheckpointOwner(t, opening, w.effective.governor, w.effective.authority, w.effective.wal, path, canonicalCheckpointShape{BasicAccept, "canonical-basic-cp", 1 << 29})
	c := &canonicalBasicComponent{writer: w, opening: opening, maintenance: m, seed: prepared, trace: trace, stream: stream, original: prepared.original, identity: prepared.identity, measured: measured}
	if err := stream.append("prepared", map[string]any{"recipe": canonicalBasicRecipe, "prepared_inputs": count, "scope": "actual native frozen ordinary inputs; physical probe/Stage/copy clocks excluded and unqualified", "preparation_clock": nil, "probe_clock": nil, "product_enable": false, "profile_memory": w.effective.basicAcceptPlan.basicMemory, "qualification": false}); err != nil {
		t.Fatal(err)
	}
	m.start(t.Context())
	return c
}

func (c *canonicalBasicComponent) call(t *testing.T, ctx context.Context, phase string, in basicInvocation, population string) (BasicResult, AckResult, error) {
	t.Helper()
	c.trace.nextTurn()
	c.trace.armed = true
	before := c.trace.nativeBegins
	c.calls++
	if err := c.stream.append("attempt_start", map[string]any{"original_id": fmt.Sprintf("basic/%d", in.request.Scan.Receipt.Identity.Sequence), "call_ordinal": c.calls, "turn_role": phase, "original_population": population, "requested_reference": in.request, "qualification": false}); err != nil {
		return BasicResult{}, AckResult{}, err
	}
	start := time.Now()
	disclosure := inventoryTestDisclosure(t, c.writer, in.request.Scan)
	value, ack, err := c.writer.effective.runBasic(ctx, c.writer, disclosure, in, phase == "ACK")
	returned := time.Now()
	began := c.trace.nativeBegins - before
	if began > 0 {
		c.physical += int(began)
	}
	var clock *n1Clock
	if c.measured {
		v := &n1Clock{InvocationStartedNS: n1NS(c.stream.origin, start), InvocationReturnedNS: n1NS(c.stream.origin, returned), InvocationNS: n1Duration(start, returned)}
		evidence := value.Outcome.Evidence
		if phase == "ACK" {
			evidence = ack.Evidence
		}
		if evidence != nil {
			v.HoldStartedNS = n1NS(c.stream.origin, evidence.HoldStartedAt)
			if evidence.PhysicalReturnedAt != nil {
				v.PhysicalReturnedNS = n1NS(c.stream.origin, *evidence.PhysicalReturnedAt)
				v.HoldNS = n1Duration(evidence.HoldStartedAt, *evidence.PhysicalReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil {
				v.CommitAttemptedNS = n1NS(c.stream.origin, *evidence.CommitAttemptedAt)
			}
			if evidence.CommitReturnedAt != nil {
				v.CommitReturnedNS = n1NS(c.stream.origin, *evidence.CommitReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil && evidence.CommitReturnedAt != nil {
				v.CommitNS = n1Duration(*evidence.CommitAttemptedAt, *evidence.CommitReturnedAt)
			}
			if evidence.Purpose != BasicAccept || evidence.Class != governor.ScanMinimum || evidence.Durability != Normal || evidence.Policy == nil || evidence.Policy.Synchronous != 1 || evidence.Commit != CommitSucceeded || v.HoldNS == nil || *v.HoldNS <= 0 {
				err = errors.Join(err, errors.New("actual Basic physical class/policy/clock missing"))
			}
		} else {
			err = errors.Join(err, errors.New("Basic physical evidence missing"))
		}
		clock = v
	}
	var window *n1OwnerWindow
	if view, ok := c.opening.executor.lastOwnerWindow(); ok {
		window = n1BindOwnerWindowForPurpose(view, c.stream.origin, start, returned, BasicAccept)
	}
	if err == nil && (began != 1 || c.trace.activeRows != 0 || c.trace.overflow || c.writer.effective.authority.Usage()[governor.ScanMinimum] != (governor.Amounts{})) {
		err = errors.New("Basic physical turn/row/grant custody incomplete")
	}
	err = errors.Join(err, c.stream.append("attempt", map[string]any{"original_id": fmt.Sprintf("basic/%d", in.request.Scan.Receipt.Identity.Sequence), "call_ordinal": c.calls, "actual_physical_attempt_ordinal": c.physical, "native_begins_in_call": began, "turn_role": phase, "original_population": population, "requested_reference": in.request, "owner_clock": clock, "owner_window": window, "raw": value, "ack": ack, "native_units": c.trace.units[:c.trace.used], "error": checkpointError(err), "qualification": false}))
	c.trace.armed = false
	err = errors.Join(err, c.maintenance.drain(c.stream))
	return value, ack, err
}
func (c *canonicalBasicComponent) action(t *testing.T, ctx context.Context, n int, population string) error {
	t.Helper()
	identity := c.identity
	identity.OperationID = canonicalBasicBlob(2, n)
	command, err := c.seed.packet(n, c.original, identity)
	if err != nil {
		return err
	}
	q, err := command.receiptRequest()
	if err != nil {
		return err
	}
	accepted, _, err := c.call(t, ctx, "ACCEPT", basicInvocation{request: q, accept: command}, population)
	if err != nil {
		return err
	}
	if accepted.Outcome.State != Applied || accepted.Outcome.Replayed || accepted.Outcome.Reference != q.Scan.Receipt || accepted.AcceptedRunRevision != c.original.run.revision+1 || accepted.ResultCount != 1 || len(accepted.Members) != 1 || accepted.Members[0].AcceptedAssetRevision != command.request.activation.source.expectedEpoch+1 {
		return ErrResolveRequired
	}
	c.fresh++
	replay, _, err := c.call(t, ctx, "RESOLVE", basicInvocation{request: q}, population)
	if err != nil {
		return err
	}
	if !replay.Outcome.Replayed || replay.Digest != accepted.Digest {
		return ErrResolveRequired
	}
	_, ack, err := c.call(t, ctx, "ACK", basicInvocation{request: q}, population)
	if err != nil {
		return err
	}
	if ack.AcknowledgedThrough != identity.Sequence {
		return ErrResolveRequired
	}
	c.acked++
	c.original.run.revision = accepted.AcceptedRunRevision
	c.original.run.lastSequence = identity.Sequence
	c.original.run.acknowledgedSequence = identity.Sequence
	c.identity.Sequence++
	return nil
}
func runCanonicalHeldBasic(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest) (primary error) {
	if manifest.Facts.Residents != canonicalHeldItems || cfg.Attempts != 10000 {
		return errors.New("Basic class requires the canonical 1M artifact and 10000 originals")
	}
	c := newCanonicalBasicComponent(t, cfg.Attempts+canonicalBasicWarmup, stream, true, true)
	defer func() {
		primary = errors.Join(primary, c.maintenance.close(), c.maintenance.drain(stream))
		usage := c.writer.effective.authority.Usage()
		if usage != ([4]governor.Amounts{}) {
			primary = errors.Join(primary, errors.New("Basic shutdown retained custody"))
		}
		primary = errors.Join(primary, stream.append("final", map[string]any{"complete": primary == nil && c.fresh == cfg.Attempts+canonicalBasicWarmup && c.acked == c.fresh, "fresh": c.fresh, "acked": c.acked, "calls": c.calls, "physical_turns": c.physical, "recipe": canonicalBasicRecipe, "usage": usage, "error": checkpointError(primary), "qualification": false}))
	}()
	for n := 0; n < cfg.Attempts+canonicalBasicWarmup; n++ {
		population := "measured"
		if n < canonicalBasicWarmup {
			population = "warmup"
		}
		if err := c.action(t, ctx, n, population); err != nil {
			return err
		}
	}
	return nil
}
func TestCanonicalBasicAcceptActionUsesExistingRuntimeAndSavedReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "basic.ndjson")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"qualification": false, "scope": "tiny functional control; no wall-clock hold assertions"}}
	c := newCanonicalBasicComponent(t, 2, stream, false, false)
	for n := 0; n < 2; n++ {
		if err := c.action(t, t.Context(), n, "control"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.maintenance.close(); err != nil {
		t.Fatal(err)
	}
	if err := c.maintenance.drain(stream); err != nil {
		t.Fatal(err)
	}
	if c.fresh != 2 || c.acked != 2 || c.calls != 6 || c.physical != 6 || c.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("incomplete Basic Action", c)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
