package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Constructor/retirement component only. Native source values are read from
// the existing fixture on its real opening/registry/governor; the observer's
// transport is an explicit fault fixture. This issues no run/lease or effects.
func n1FreezeFixture(t *testing.T, multipart ...bool) (*N1Preparation, InventoryAcceptInput) {
	t.Helper()
	if len(multipart) > 1 {
		t.Fatal("invalid multipart fixture selector")
	}
	return n1PreparationFixture(t, len(multipart) == 1 && multipart[0], true)
}

func n1PreparationFixture(t *testing.T, multipart, observe bool) (*N1Preparation, InventoryAcceptInput) {
	t.Helper()
	f := newScanDriverFixture(t, scanIssuerStatementFixture)
	turn := f.turn(t, InventoryCreate, f.create.scope.runID)
	source, err := captureScanSource(t.Context(), turn, 7, "lifecycle-library", "lifecycle-new-source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	b := f.preparation(t, InventoryAccept, f.create.scope.runID)
	b.source = &source
	in, id := inventoryPublicInput(t)
	s := source.source
	in.Scope = InventoryScopeInput{f.create.scope.runID, source.libraryKey, source.libraryID}
	in.Source = InventorySourceInput{s.id, s.incarnation, s.rootIdentity, bytes.Clone(s.resolvedRoot), s.generation, s.followSymlinks}
	in.Directory.Path, in.Directory.RawNames = []byte("."), 1
	in.Directory.Identity, in.Directory.Revision, in.Directory.NextCursor = "actual-directory", "directory-revision", "42"
	in.Directory.Complete, in.Directory.Unstable = false, false
	file := &in.Files[0]
	file.RelativePath, file.AssetPath = []byte("The Film (2020).mkv"), []byte("/media-new/The Film (2020).mkv")
	if multipart {
		file.RelativePath, file.AssetPath = []byte("The Film (2020) - pt2.mkv"), []byte("/media-new/The Film (2020) - pt2.mkv")
	}
	file.ObjectIdentity, file.ChangeToken = "dev:7:ino:31", "native-change"
	projection, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{source.identityRoot, file.AssetPath, file.RelativePath, "", "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	m := projection.Metadata
	file.SourceKey, file.Year, file.Part = projection.SourceKey, projection.Year, projection.Part
	file.Title, file.SortTitle, file.SortKey, file.SortHead = m.Title, m.SortTitle, m.SortKey, m.SortHead
	file.SortKeyKeepArticles, file.SortHeadKeepArticles, file.MetadataLanguage = m.SortKeyKeepArticles, m.SortHeadKeepArticles, m.MetadataLanguage
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: s.id + ":" + s.incarnation,
		Object: objectidentity.StableObjectIdentity(file.ObjectIdentity, s.rootIdentity), Size: file.Size, ModifiedNS: file.ModifiedNS, ChangeToken: file.ChangeToken}
	file.Revision, err = evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	page := nativeinventory.Page{Version: 1, RootIdentity: s.rootIdentity, DirectoryIdentity: in.Directory.Identity,
		DirectoryRevision: in.Directory.Revision, NextCursor: in.Directory.NextCursor,
		Entries: []nativeinventory.Entry{{Path: file.AssetPath, Name: file.RelativePath, ObjectIdentity: file.ObjectIdentity, ChangeToken: file.ChangeToken, Size: file.Size, ModifiedNS: file.ModifiedNS}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	b.observer = nativeinventory.NewObserver(&scanObservationFaultTransport{page: string(raw)})
	b.request.RootIdentity, b.request.DirectoryRevision = s.rootIdentity, in.Directory.Revision
	n := &N1Preparation{driver: f.driver, preparation: b, identity: id, template: in}
	n.self, b.inventory = n, n
	if observe {
		if _, err = n.ObserveInventory(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return n, in
}

func TestN1FreezeUsesOwnedObservationAndRetainsUntilClose(t *testing.T) {
	n, in := n1FreezeFixture(t)
	bad := in
	bad.Files = append([]InventoryFilenameInput(nil), in.Files...)
	bad.Files[0].Revision = strings.Repeat("x", 1<<20)
	if command, err := n.Freeze(t.Context(), bad); command != nil || !isN1CapacityRefusal(err) {
		t.Fatal("derived construction began without its full same-grant envelope", command, err)
	}
	command, err := n.Freeze(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	c := command.(*inventoryAcceptCommand)
	ref, err := InventoryAcceptReference(c)
	if err != nil || ref != n.reference {
		t.Fatal("frozen original diverged", ref, err)
	}
	in.Files[0].AssetPath[0] = 'X'
	if c.request.files[0].assetPath[0] == 'X' {
		t.Fatal("retained caller buffer")
	}
	a := n.driver.writer.effective.authority
	if a.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("published command lost original preparation custody")
	}
	if err = n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.valid || len(c.request.files) != 0 || n.command != nil || n.preparation != nil || n.driver.preparation != nil || a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("closed/refunded tombstone retained command buffers")
	}
}

func TestN1FreezeFinalCancellationDiscardsCommandWithoutRefund(t *testing.T) {
	n, in := n1FreezeFixture(t)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanIssuerPublicationContext{Context: base, cancel: cancel, installed: func() bool { return n.command != nil }}
	if command, err := n.Freeze(ctx, in); command != nil || !errors.Is(err, context.Canceled) || n.command != nil || n.reference != (ScanReceiptRequest{}) {
		t.Fatal("discarded constructor remained published", command, err)
	}
	if n.preparation == nil || n.driver.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("cancellation refunded retained observation custody")
	}
	if err := n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestN1FreezeBindsReviewedPartCarrier(t *testing.T) {
	n, in := n1FreezeFixture(t, true)
	if in.Files[0].Part != 2 {
		t.Fatal("fixture did not retain the actual parsed part", in.Files[0].Part)
	}
	forged := in
	forged.Files = append([]InventoryFilenameInput(nil), in.Files...)
	forged.Files[0].Part = 7
	if command, err := n.Freeze(t.Context(), forged); command != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("caller selected an unobserved part", command, err)
	}
	command, err := n.Freeze(t.Context(), in)
	if err != nil || command.(*inventoryAcceptCommand).request.files[0].part != 2 {
		t.Fatal("reviewed native Part carrier was not frozen", command, err)
	}
	if err = n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type n1ObserveFinalCancelContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c *n1ObserveFinalCancelContext) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	outer, inner := false, false
	for {
		frame, more := frames.Next()
		outer = outer || strings.Contains(frame.Function, "(*N1Preparation).ObserveInventory")
		inner = inner || strings.Contains(frame.Function, "(*scanPreparation).ObserveInventory")
		if !more {
			break
		}
	}
	if outer && !inner {
		c.cancel()
	}
	return c.Context.Err()
}

func TestN1ObserveFinalCancellationCannotAuthorizeFreeze(t *testing.T) {
	n, in := n1PreparationFixture(t, false, false)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &n1ObserveFinalCancelContext{Context: base, cancel: cancel}
	if page, err := n.ObserveInventory(ctx); !errors.Is(err, context.Canceled) || page.Version != 0 || !n.observationFailed.Load() {
		t.Fatal("discarded raw observation remained usable", page, err)
	}
	if command, err := n.Freeze(t.Context(), in); command != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("failed observation authorized a producer command", command, err)
	}
	if err := n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func isN1CapacityRefusal(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == RefusalCapacity
}

func TestN1PreparationUsesTheIssuerAndKeepsFreshClosed(t *testing.T) {
	f := scanIssuerRuntime(t)
	d, r := f.driver, f.driver.writer.effective
	// Reuse the ONE existing fixture recipe and exact same governor. Capture
	// both finite profiles during FULL startup, before create or claim begins.
	makePlan := func(defs []stmt.Definition) effectivePlan {
		p := *r.issuerPlans[0]
		p.scanLifecycleMemory = commandMemoryBudget{}
		p.inventoryMemory = inventoryMemoryBudget{4 << 20, 4 << 20}
		unit := p.steps[0].work
		p.steps = nil
		for _, def := range defs {
			p.steps = append(p.steps, statementStep{def.ID, unit})
		}
		conn, err := r.executor.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		golden := captureEffectiveFixture(t, conn, r.executor.config, defs, "scan_inventory_preparation_test.go", "scan_run_lifecycle_test.go")
		if err = conn.Close(); err != nil {
			t.Fatal(err)
		}
		p.registration = golden.Receipt()
		return p
	}
	prepPlan, historyPlan := makePlan(inventoryPreparationDefinitions()), makePlan(inventoryHistoryDefinitions())
	if err := composeInventoryPreparation(t.Context(), d, prepPlan, historyPlan); err != nil {
		t.Fatal(err)
	}
	creation, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := creation.Reference()
	if err != nil {
		t.Fatal(err)
	}
	consume := func(operation *ScanRunOperation) {
		result, err := operation.Apply(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = result.Snapshot(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = result.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = operation.Ack(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	consume(creation)
	if err = creation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	consume(claim)
	ref, err = claim.Reference()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := claim.Lease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	n, err := d.PrepareInventory(t.Context(), lease, ref, []byte("."))
	if err != nil {
		t.Fatal("owned N1 preparation", err)
	}
	snapshot, err := n.Snapshot(t.Context())
	if err != nil || snapshot.Input.Run.AcknowledgedSequence != 2 || snapshot.Identity.Sequence != 3 || !bytes.Equal(snapshot.IdentityRoot, []byte("/retained-library-identity")) {
		t.Fatal("current post-claim snapshot", snapshot, err)
	}
	_, in := n1InputForSnapshot(t, snapshot)
	page := nativeinventory.Page{Version: 1, RootIdentity: in.Source.RootIdentity, DirectoryIdentity: in.Directory.Identity,
		DirectoryRevision: in.Directory.Revision, NextCursor: in.Directory.NextCursor,
		Entries: []nativeinventory.Entry{{Path: in.Files[0].AssetPath, Name: in.Files[0].RelativePath, ObjectIdentity: in.Files[0].ObjectIdentity,
			ChangeToken: in.Files[0].ChangeToken, Size: in.Files[0].Size, ModifiedNS: in.Files[0].ModifiedNS}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	n.preparation.observer = nativeinventory.NewObserver(&scanObservationFaultTransport{page: string(raw)})
	if _, err = n.ObserveInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	command, err := n.Freeze(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	q, err := InventoryAcceptReference(command)
	if err != nil {
		t.Fatal(err)
	}
	var refusal *Refusal
	if value, err := n.Accept(t.Context(), command); !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || value.Outcome.State != 0 || len(value.Members) != 0 {
		t.Fatal("unqualified composition became executable", value, err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanIssuerPublicationContext{Context: base, cancel: cancel, installed: func() bool { return n.resultPublished }}
	if value, err := d.ResolveScan(ctx, q); !errors.Is(err, context.Canceled) || value.Outcome.State != 0 || n.resultPublished {
		t.Fatal("discarded result advanced consumer state", value, err)
	}
	if value, err := d.ResolveScan(t.Context(), q); err != nil || value.Outcome.State != Pending || len(value.Members) != 0 {
		t.Fatal("same-original recovery lost the pending decision", value, err)
	}
	if _, err := d.AckScan(t.Context(), q); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("pending original advanced the native floor", err)
	}
	if err = n.Close(context.Background()); err != nil || r.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("N1 custody did not physically retire", err)
	}
}

func n1InputForSnapshot(t *testing.T, snapshot InventoryPreparationSnapshot) (InventoryOrdinaryFilename, InventoryAcceptInput) {
	t.Helper()
	in := snapshot.Input
	in.Directory.RawNames, in.Directory.NextCursor = 1, "42"
	file := InventoryFilenameInput{RelativePath: []byte("The Film (2020).mkv"), AssetPath: []byte(string(in.Source.ResolvedRoot) + "/The Film (2020).mkv"), ObjectIdentity: "dev:7:ino:31", ChangeToken: "native-change", Size: 42, ModifiedNS: 1}
	projection, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{snapshot.IdentityRoot, file.AssetPath, file.RelativePath, "", "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	m := projection.Metadata
	file.SourceKey, file.Year, file.Part = projection.SourceKey, projection.Year, projection.Part
	file.Title, file.SortTitle, file.SortKey, file.SortHead = m.Title, m.SortTitle, m.SortKey, m.SortHead
	file.SortKeyKeepArticles, file.SortHeadKeepArticles, file.MetadataLanguage = m.SortKeyKeepArticles, m.SortHeadKeepArticles, m.MetadataLanguage
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: in.Source.ID + ":" + in.Source.Incarnation,
		Object: objectidentity.StableObjectIdentity(file.ObjectIdentity, in.Source.RootIdentity), Size: file.Size, ModifiedNS: file.ModifiedNS, ChangeToken: file.ChangeToken}
	file.Revision, err = evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	in.Files = []InventoryFilenameInput{file}
	return projection, in
}
