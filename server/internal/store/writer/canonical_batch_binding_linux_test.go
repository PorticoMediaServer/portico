//go:build linux

package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

const canonicalBatchRecipe = "canonical-inventory-batch-public-driver-v1"

type canonicalBatchComponent struct {
	driver                        *ScanDriver
	transport                     *scanObservationFaultTransport
	keeper                        *canonicalN1MaintenanceOwner
	trace                         *n1Trace
	stream                        *n1Stream
	claim, last                   ScanRunReference
	lease                         *ScanRunLease
	files, calls, accepted, acked int
	widths                        map[int]int
	measured                      bool
	measuredFiles                 int
	measuredWidths                map[int]int
}

// Reuse the N1 seed, complete native composition, resource owner and funded
// keeper; only this class's source and native observation stream are new.
func newCanonicalBatchComponent(t *testing.T, stream *n1Stream, canonical, measured bool) *canonicalBatchComponent {
	t.Helper()
	seed, path, _ := testExecutor(t)
	if !canonical {
		seedQualificationPopulation(t, seed, qualificationPopulationSpec{Seed: 17, Residents: 4})
	}
	seedInventoryFreshPopulation(t, seed, true)
	root := qualificationMovieRoot + "/batch"
	if _, err := seed.db.ExecContext(t.Context(), `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('canonical-batch-source','qualification-movies',?,?,'source-incarnation','source-root',23,1)`, root, root); err != nil {
		t.Fatal(err)
	}
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner)
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
	primeEffectiveFixturePool(t, e)
	// Actual preparation (8MiB) and writer (16MiB) coexist under one
	// 32MiB ScanMinimum partition; neither lends memory to the other.
	maintenance := canonicalN1Maintenance()
	batchUnit := effectiveFixturePlan().recipe
	batchUnit.limit.WALBytes = inventoryBatchWALBytes
	g, a, wal, _ := governedFixtureComposition(t, time.Now, governor.Amounts{governor.CPUMilli: 2000, governor.IOBytes: inventoryBatchIOBytes}, nil, &maintenance, governedFixtureProfile{batchUnit, 32 << 20})
	m := newCanonicalCheckpointOwner(t, opening, g, a, wal, path)
	cp := &checkpointComponent{owner: owner, opening: opening, w: e, path: path, g: g, a: a, wal: wal, now: time.Now, origin: stream.origin}
	d, transport, _ := n1ComposedDriverCompose(t, cp, true, n1BatchTimingFixture)
	d.writer.effective.maintenance = &checkpointMaintenance{request: m.request("passive"), results: m.results}
	bound, err := beginClassTarget(t, "inventory_batch", classTargetParts{Opening: opening, Governor: cp.g, Authority: cp.a, WAL: cp.wal, Path: path, Writer: d.writer, Keeper: m})
	if err != nil {
		_ = stream.append("failed_binding", map[string]any{"reason": err.Error(), "native_calls_started": 0})
		t.Fatal(err)
	}
	if bound.WAL.Explicit || bound.WAL.Policy != governor.Phase1aWALPolicy() {
		t.Fatal("batch class changed adopted WAL policy")
	}
	trace.useRegistry(d.writer.effective.registry)
	c := &canonicalBatchComponent{driver: d, transport: transport, keeper: m, trace: trace, stream: stream, widths: map[int]int{}, measuredWidths: map[int]int{}, measured: measured}
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "qualification-movies", "canonical-batch-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := consumeCanonicalBatchLifecycle(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	c.claim, c.lease, err = consumeCanonicalBatchLifecycle(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	c.last = c.claim
	var residents int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities WHERE library_id=7 AND retired=0`).Scan(&residents); err != nil {
		t.Fatal(err)
	}
	if err := stream.append("prepared", map[string]any{"recipe": canonicalBatchRecipe, "actual_library_items": residents, "library_numeric_id": 7, "library_key": "qualification-movies", "native_observations": "bounded trusted fixture pages; not physical-filesystem EC4", "max_batch_files": maxInventoryBatchFiles, "need": d.writer.effective.inventoryDriverPlan.need, "recipe_limit": d.writer.effective.inventoryDriverPlan.recipe.limit, "maintenance_policy": bound.WAL.Policy, "maintenance_override": bound.WAL.Explicit, "qualification": false}); err != nil {
		t.Fatal(err)
	}
	m.start(t.Context())
	return c
}

func consumeCanonicalBatchLifecycle(ctx context.Context, op *ScanRunOperation) (ScanRunReference, *ScanRunLease, error) {
	ref, err := op.Reference()
	if err != nil {
		return ref, nil, err
	}
	result, err := op.Apply(ctx)
	if err != nil {
		return ref, nil, err
	}
	if err = result.Close(); err != nil {
		return ref, nil, err
	}
	if _, err = op.Ack(ctx); err != nil {
		return ref, nil, err
	}
	var lease *ScanRunLease
	if ref.Purpose == InventoryClaim {
		lease, err = op.Lease(ctx)
		if err != nil {
			return ref, nil, err
		}
	}
	return ref, lease, op.Close(ctx)
}

func canonicalBatchObserved(snapshot InventoryPreparationSnapshot, page nativeinventory.Page) (InventoryAcceptInput, error) {
	in := snapshot.Input
	in.Directory.NextCursor, in.Directory.RawNames = page.NextCursor, int64(len(page.Entries))
	for _, entry := range page.Entries {
		p, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{IdentityRoot: snapshot.IdentityRoot, AssetPath: entry.Path, RelativePath: entry.Name, MetadataLanguage: "en-US"})
		if err != nil {
			return in, err
		}
		m := p.Metadata
		evidence := mediasource.InventoryEvidence{Kind: "local", Scope: in.Source.ID + ":" + in.Source.Incarnation, Object: objectidentity.StableObjectIdentity(entry.ObjectIdentity, in.Source.RootIdentity), Size: entry.Size, ModifiedNS: entry.ModifiedNS, ChangeToken: entry.ChangeToken}
		revision, err := evidence.Revision()
		if err != nil {
			return in, err
		}
		in.Files = append(in.Files, InventoryFilenameInput{RelativePath: entry.Name, AssetPath: entry.Path, SourceKey: p.SourceKey, Year: p.Year, Part: p.Part, ObjectIdentity: entry.ObjectIdentity, ChangeToken: entry.ChangeToken, Size: entry.Size, ModifiedNS: entry.ModifiedNS, Revision: revision, Title: m.Title, SortTitle: m.SortTitle, SortKey: m.SortKey, SortHead: m.SortHead, SortKeyKeepArticles: m.SortKeyKeepArticles, SortHeadKeepArticles: m.SortHeadKeepArticles, MetadataLanguage: m.MetadataLanguage})
	}
	return in, nil
}

func (c *canonicalBatchComponent) action(t *testing.T, ctx context.Context, ordinal int, population string) error {
	if time.Until(time.UnixMilli(c.lease.expiresMS)) < 20*time.Second {
		claim, err := c.driver.PrepareClaim(ctx, c.claim)
		if err != nil {
			return err
		}
		c.claim, c.lease, err = consumeCanonicalBatchLifecycle(ctx, claim)
		if err != nil {
			return err
		}
		c.last = c.claim
	}
	p, err := c.driver.PrepareInventory(ctx, c.lease, c.last, []byte("."))
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	snapshot, err := p.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	width := p.preparation.request.Limit // actual frozen issuer width, never a caller override
	next, err := inventoryFreshCookie(snapshot.Input.Directory.ExpectedCursor)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	page := nativeinventory.Page{Version: 1, RootIdentity: snapshot.Input.Source.RootIdentity, DirectoryIdentity: snapshot.Input.Directory.Identity, DirectoryRevision: snapshot.Input.Directory.Revision, NextCursor: strconv.FormatInt(next+int64(width), 10)}
	for i := 0; i < width; i++ {
		n := c.files + i
		name := fmt.Sprintf("Batch %09d.mkv", n)
		page.Entries = append(page.Entries, nativeinventory.Entry{Path: []byte(string(snapshot.Input.Source.ResolvedRoot) + "/" + name), Name: []byte(name), ObjectIdentity: fmt.Sprintf("dev:7:ino:%d", n+100), ChangeToken: "batch-native-observation", Size: int64(42 + n), ModifiedNS: int64(n)})
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	c.transport.page = string(raw)
	observed, err := p.ObserveInventory(ctx)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	in, err := canonicalBatchObserved(snapshot, observed)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	command, err := p.Freeze(ctx, in)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	q, err := InventoryAcceptReference(command)
	if err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	id := fmt.Sprintf("batch/%d", q.Receipt.Identity.Sequence)
	record := func(role string, start time.Time, value any, evidence *ExecutionEvidence, callErr error) error {
		returned := time.Now()
		var clock *n1Clock
		var actualClass, actualDurability any
		if evidence != nil {
			actualClass, actualDurability = evidence.Class, evidence.Durability
			clock = &n1Clock{InvocationStartedNS: n1NS(c.stream.origin, start), InvocationReturnedNS: n1NS(c.stream.origin, returned), InvocationNS: n1Duration(start, returned), HoldStartedNS: n1NS(c.stream.origin, evidence.HoldStartedAt)}
			if evidence.PhysicalReturnedAt != nil {
				clock.HoldNS = n1Duration(evidence.HoldStartedAt, *evidence.PhysicalReturnedAt)
				clock.PhysicalReturnedNS = n1NS(c.stream.origin, *evidence.PhysicalReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil {
				clock.CommitAttemptedNS = n1NS(c.stream.origin, *evidence.CommitAttemptedAt)
			}
			if evidence.CommitReturnedAt != nil {
				clock.CommitReturnedNS = n1NS(c.stream.origin, *evidence.CommitReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil && evidence.CommitReturnedAt != nil {
				clock.CommitNS = n1Duration(*evidence.CommitAttemptedAt, *evidence.CommitReturnedAt)
			}
		}
		if c.measured && (evidence == nil || evidence.Purpose != InventoryAccept || evidence.Class != governor.ScanMinimum || evidence.Durability != Normal || evidence.Policy == nil || evidence.Commit != CommitSucceeded || clock.HoldNS == nil || *clock.HoldNS <= 0) {
			callErr = errors.Join(callErr, errors.New("batch physical evidence missing"))
		}
		var window *n1OwnerWindow
		if view, ok := c.driver.opening.executor.lastOwnerWindow(); ok {
			window = n1BindOwnerWindowForPurpose(view, c.stream.origin, start, returned, InventoryAccept)
		}
		c.calls++
		return errors.Join(callErr, c.stream.append("attempt", map[string]any{"original_id": id, "original_ordinal": ordinal, "turn_role": role, "original_population": population, "batch_files": width, "requested_reference": q, "actual_class": actualClass, "actual_durability": actualDurability, "owner_clock": clock, "owner_window": window, "raw": value, "error": checkpointError(callErr), "qualification": false}))
	}
	start := time.Now()
	out, err := p.Accept(ctx, command)
	if err = record("ACCEPT", start, out, out.Outcome.Evidence, err); err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || len(out.Members) != width {
		return ErrResolveRequired
	}
	c.accepted++
	c.files += width
	c.widths[width]++
	if population == "measured" {
		c.measuredFiles += width
		c.measuredWidths[width]++
	}
	// Release native preparation before history recovery, retaining the same
	// saved original. Resolve and ACK each take their own admitted physical turn.
	if err = p.Close(ctx); err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	start = time.Now()
	saved, err := c.driver.ResolveScan(ctx, q)
	if err = record("RESOLVE", start, saved, saved.Outcome.Evidence, err); err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	if !saved.Outcome.Replayed || saved.Digest != out.Digest || !reflect.DeepEqual(saved.Members, out.Members) {
		return ErrResolveRequired
	}
	start = time.Now()
	ack, err := c.driver.AckScan(ctx, q)
	if err = record("ACK", start, ack, ack.Evidence, err); err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	if ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
		return ErrResolveRequired
	}
	c.acked++
	if err = c.driver.ReleasePending(ctx); err != nil {
		return fmt.Errorf("batch prepare/observation: %w", err)
	}
	c.last = ScanRunReference{Purpose: InventoryAccept, Scan: q}
	if err := c.keeper.drain(c.stream); err != nil {
		return fmt.Errorf("batch keeper drain: %w", err)
	}
	return nil
}

func runCanonicalHeldBatch(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest) (primary error) {
	if manifest.Facts.Residents != canonicalHeldItems || cfg.Attempts != 10000 {
		return errors.New("batch class requires the canonical 1M artifact and 10000 originals")
	}
	c := newCanonicalBatchComponent(t, stream, true, true)
	started := time.Now()
	defer func() {
		primary = errors.Join(primary, c.driver.Close(context.Background()), c.keeper.close(), c.keeper.drain(stream))
		if usage := c.driver.writer.effective.authority.Usage(); usage != ([4]governor.Amounts{}) {
			primary = errors.Join(primary, errors.New("batch shutdown retained custody"))
		}
		primary = errors.Join(primary, stream.append("final", map[string]any{"complete": primary == nil && c.accepted == cfg.Attempts+canonicalN1Warmup && c.acked == c.accepted, "recipe": canonicalBatchRecipe, "fresh_batches": c.accepted, "files": c.measuredFiles, "width_distribution": c.measuredWidths, "all_files_including_warmup": c.files, "all_widths_including_warmup": c.widths, "calls": c.calls, "elapsed_ns": time.Since(started).Nanoseconds(), "foreground_write_wait_ns": nil, "foreground_unavailable_reason": "this normal-command class target does not compose EC3 foreground offers; paired EC3 remains separate", "physical_filesystem_scan": false, "qualification": false, "error": checkpointError(primary)}))
	}()
	for n := 0; n < cfg.Attempts+canonicalN1Warmup; n++ {
		if n == canonicalN1Warmup {
			started = time.Now()
		}
		population := "measured"
		if n < canonicalN1Warmup {
			population = "warmup"
		}
		if err := c.action(t, ctx, n, population); err != nil {
			return err
		}
	}
	return nil
}

func TestCanonicalHeldInventoryBatch(t *testing.T) { canonicalHeldRun(t, "inventory_batch") }
func TestCanonicalBatchClassRefusesMissingBindings(t *testing.T) {
	class, err := canonicalHeldClassFor("inventory_batch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = classTargetStartGate(class, classTargetStart{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatal("batch started degraded", err)
	}
}
func TestCanonicalBatchPublicDriverAction(t *testing.T) {
	dir := t.TempDir()
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		dir = evidence
	}
	file, err := os.Create(filepath.Join(dir, "batch.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"qualification": false, "scope": "tiny functional control; no real-clock assertions"}}
	c := newCanonicalBatchComponent(t, stream, false, false)
	for n := 0; n < 3; n++ {
		if err := c.action(t, t.Context(), n, "control"); err != nil {
			t.Fatal(err)
		}
	}
	if c.accepted != 3 || c.acked != 3 || c.files < 3 {
		t.Fatal("incomplete batch action")
	}
	if err := c.driver.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.keeper.close(); err != nil {
		t.Fatal(err)
	}
}
