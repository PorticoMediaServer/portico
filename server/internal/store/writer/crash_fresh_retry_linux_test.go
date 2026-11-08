package writer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
)

// Fresh retry after a crash BEFORE COMMIT. The first attempt leaves no receipt
// or partial effect; a client retry executes exactly one complete operation,
// and the same retry identity replays its saved result with zero new effects.
// These are two single cells, not a new boundary matrix.

// n1ComposedDriverPreparedForRetry is the same real composed setup, retaining
// the committed Claim reference the client needs after a crash. Zero-cohort
// lifecycle ACK removes its header, so the reference must be held by the
// caller exactly as a durable client would.
func n1ComposedDriverPreparedForRetry(t *testing.T) (*ScanDriver, *N1Preparation, InventoryAcceptCommand, ScanReceiptRequest, ScanRunReference) {
	t.Helper()
	d, transport := n1ComposedDriverFixture(t)
	creation, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	createRef, err := creation.Reference()
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
	claim, err := d.PrepareClaim(t.Context(), createRef)
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
	preparation, err := d.PrepareInventory(t.Context(), lease, claimRef, []byte("."))
	if err != nil {
		t.Fatal(err)
	}
	command, err := preparation.Freeze(t.Context(), n1RetryInput(t, preparation, transport))
	if err != nil {
		t.Fatal(err)
	}
	original, err := InventoryAcceptReference(command)
	if err != nil {
		t.Fatal(err)
	}
	return d, preparation, command, original, claimRef
}

func n1RetryInput(t *testing.T, prep *N1Preparation, transport *scanObservationFaultTransport) InventoryAcceptInput {
	t.Helper()
	page := nativeinventory.Page{Version: 1, RootIdentity: "source-root", DirectoryIdentity: "actual-directory", DirectoryRevision: "directory-revision", NextCursor: "42", Entries: []nativeinventory.Entry{{Path: []byte("/media-new/The Film (2020).mkv"), Name: []byte("The Film (2020).mkv"), ObjectIdentity: "dev:7:ino:31", ChangeToken: "native-change", Size: 42, ModifiedNS: 7}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	transport.page = string(raw)
	snapshot, err := prep.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := prep.ObserveInventory(t.Context())
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
	return in
}

func n1RetryCounts(t *testing.T, ctx context.Context, reader inventoryCrashReader, runID string) (accepts, children, objects int64) {
	t.Helper()
	if err := reader.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT count(*) FROM scan_command_receipts WHERE run_id=? AND command_kind='inventory_accept'),0),
 COALESCE((SELECT count(*) FROM scan_command_results WHERE run_id=?),0),
 COALESCE((SELECT count(*) FROM inventory_objects),0)`,
		runID, runID, runID).Scan(&accepts, &children, &objects); err != nil {
		t.Fatal(err)
	}
	return accepts, children, objects
}

// crashN1RetryFixture stops the real public Accept immediately before COMMIT.
// Nothing is written; the client-side retry happens in the parent.
func crashN1RetryFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	d, preparation, command, original, claimRef := n1ComposedDriverPreparedForRetry(t)
	e, path := d.opening.executor, d.opening.binding.path
	clock := e.now()
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		t.Fatal("retry fixture lost its compiled original command")
	}
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = clock.UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	witness.Path, witness.Reference, witness.N1Driver, witness.N1Claim = path, original.Receipt, &source, &claimRef
	witness.ScanClockMS = clock.UnixMilli()
	installCrashDriverAt(t, e, path, witness, clock, nil)
	if _, err := preparation.Accept(t.Context(), command); err == nil {
		t.Fatal("pre-COMMIT retry crash was not observed")
	}
	t.Fatal("retry crash boundary was never reached", witness.Case)
}

func TestCrashRetryN1AcceptExecutesOnceThenReplays(t *testing.T) {
	witness := runCrashChild(t, crashCase{"n1_retry", "accept", "before_commit", ""})
	source := witness.N1Driver
	if source == nil {
		t.Fatal("retry witness lost the original N1 source")
	}
	d, transport := n1ComposedDriverOnExistingTransport(t, witness.Path)
	e := d.opening.executor
	// A client retries only after the crashed process's claim lease expired.
	// Model that durable elapsed input explicitly; this is not an effect under
	// test and both the raw checkpoint and the retry below still see it.
	if _, err := e.db.ExecContext(t.Context(), `UPDATE inventory_runs SET job_lease_expires_ms=job_lease_expires_ms-3600000 WHERE job_id=?`, source.Input.RunID); err != nil {
		t.Fatal(err)
	}
	if ack, last := n1HistoryFloor(t, t.Context(), e.db, source.Input.RunID); last != source.Input.IdentitySequence-1 && last != source.Input.LastSequence {
		t.Fatal("crashed Accept advanced the run sequence", ack, last)
	}
	if accepts, children, objects := n1RetryCounts(t, t.Context(), e.db, source.Input.RunID); accepts != 0 || children != 0 || objects != 0 {
		t.Fatal("pre-COMMIT Accept left effects", accepts, children, objects)
	}
	if witness.N1Claim == nil {
		t.Fatal("retry witness lost the committed claim reference")
	}
	claimRef := *witness.N1Claim
	claim, err := d.PrepareClaim(t.Context(), claimRef)
	if err != nil {
		t.Fatal("retry claim preparation", err)
	}
	claimRef, err = claim.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := claim.Apply(t.Context())
	if err != nil {
		t.Fatal("retry claim", err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = claim.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	lease, err := claim.Lease(t.Context())
	if err != nil {
		t.Fatal("retry lease", err)
	}
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	preparation, err := d.PrepareInventory(t.Context(), lease, claimRef, []byte("."))
	if err != nil {
		t.Fatal("retry inventory preparation", err)
	}
	in := n1RetryInput(t, preparation, transport)
	command, err := preparation.Freeze(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	original, err := InventoryAcceptReference(command)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := preparation.Accept(t.Context(), command)
	if err != nil {
		t.Fatal("retry execution", err)
	}
	if applied.Outcome.State != Applied || applied.Outcome.Replayed || len(applied.Members) != 1 {
		t.Fatal("retry did not execute exactly once", applied)
	}
	if err := preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if accepts, children, objects := n1RetryCounts(t, t.Context(), e.db, source.Input.RunID); accepts != 1 || children != 1 || objects != 1 {
		t.Fatal("retry did not produce exactly one visible effect", accepts, children, objects)
	}
	settled, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := d.ResolveScan(t.Context(), original)
	if err != nil {
		t.Fatal("retry replay", err)
	}
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || len(replay.Members) != 1 || replay.Digest != applied.Digest || replay.Members[0] != applied.Members[0] {
		t.Fatal("second retry did not replay the saved result", replay)
	}
	after, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, after); err != nil {
		t.Fatal("second retry wrote state", err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// crashScanRetryFixture stops the real public Create immediately before COMMIT.
func crashScanRetryFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	f := scanIssuerRuntime(t)
	d := f.driver
	clock := d.opening.executor.now()
	op, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := op.Reference()
	if err != nil {
		t.Fatal(err)
	}
	witness.Path, witness.ScanRun, witness.ScanClockMS = d.opening.binding.path, &reference, clock.UnixMilli()
	installCrashDriverAt(t, d.opening.executor, witness.Path, witness, clock, nil)
	if _, err = op.Apply(t.Context()); err == nil {
		t.Fatal("pre-COMMIT create retry crash was not observed")
	}
	t.Fatal("scan retry crash boundary was never reached", witness.Case)
}

func scanRetryCounts(t *testing.T, ctx context.Context, reader inventoryCrashReader, runID string) (receipts, runs, roots int64) {
	t.Helper()
	if err := reader.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM scan_command_receipts WHERE run_id=? AND command_kind='inventory_create'),
 (SELECT count(*) FROM inventory_runs WHERE job_id=?),
 COALESCE((SELECT count(*) FROM inventory_directories WHERE job_id=?),0)`, runID, runID, runID).Scan(&receipts, &runs, &roots); err != nil {
		t.Fatal(err)
	}
	return receipts, runs, roots
}

func TestCrashRetryScanCreateExecutesOnceThenReplays(t *testing.T) {
	witness := runCrashChild(t, crashCase{"scan_retry", "create", "before_commit", ""})
	if witness.ScanRun == nil {
		t.Fatal("scan retry witness lost the original reference")
	}
	f := reopenScanAck(t, witness.Path)
	*f.clock = time.UnixMilli(witness.ScanClockMS)
	d := f.driver
	e := d.opening.executor
	op, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal("scan retry preparation", err)
	}
	reference, err := op.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := op.Apply(t.Context())
	if err != nil {
		t.Fatal("scan retry execution", err)
	}
	first, err := result.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if first.Outcome.State != Applied || first.Outcome.Replayed {
		t.Fatal("scan retry did not execute exactly once", first.Outcome)
	}
	if receipts, runs, roots := scanRetryCounts(t, t.Context(), e.db, reference.Scan.RunID); receipts != 1 || runs != 1 || roots != 1 {
		t.Fatal("scan retry did not produce exactly one visible run", receipts, runs, roots)
	}
	settled, err := readEffectiveJournal(t.Context(), e.db, scanIssuerPreparedCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	// A duplicated retry must not re-execute; it resolves the SAME original.
	replayResult, err := op.Resolve(t.Context())
	if err != nil {
		t.Fatal("second retry replay", err)
	}
	second, err := replayResult.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = replayResult.Close(); err != nil {
		t.Fatal(err)
	}
	if second.Outcome.State != Applied || !second.Outcome.Replayed || second.RunID != first.RunID ||
		second.Digest != first.Digest || second.AcceptedRunRevision != first.AcceptedRunRevision {
		t.Fatal("second retry did not replay the saved result", second, first)
	}
	after, err := readEffectiveJournal(t.Context(), e.db, scanIssuerPreparedCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(settled, after); err != nil {
		t.Fatal("second retry wrote state", err)
	}
	// Lifecycle retention deliberately removes the zero-cohort Create header
	// on ACK; do that last, after the replay comparison.
	if _, err = op.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
