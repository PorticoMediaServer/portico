package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type InventoryPreparationSnapshot struct {
	Identity     CommandIdentity
	Input        InventoryAcceptInput
	IdentityRoot []byte // exact catalog_libraries.root, distinct from physical root
}

type N1Preparation struct {
	self                                       *N1Preparation
	driver                                     *ScanDriver
	preparation                                *scanPreparation
	lease                                      *ScanRunLease
	identity                                   CommandIdentity
	template                                   InventoryAcceptInput
	command                                    *inventoryAcceptCommand
	preparedBytes                              uint64
	reference                                  ScanReceiptRequest
	last                                       InventoryResult
	historyOnly, acknowledged, resultPublished bool
	batchFeedback                              bool
	read, closed                               bool
	observationFailed                          atomic.Bool
}

func inventoryPreparationDefinitions() []stmt.Definition {
	defs := append(inventoryDisclosureDefinitions(), scanSourceDefinitions()...)
	for _, def := range scanRunLifecycleDefinitions() {
		switch def.ID {
		case scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot, scanLifecycleActive:
			defs = append(defs, def)
		}
	}
	return append(defs, scanSnapshotByteDefinitions()...)
}

func composeInventoryPreparation(ctx context.Context, d *ScanDriver, plan, history effectivePlan) (err error) {
	var runtimeLocked, driverLocked, executorLocked, installed bool
	var r *effectiveRuntime
	var oldRegistry *stmt.Registry
	var oldRetention *scanOwnerRetention
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.inventoryPreparationPlan, r.inventoryPlan = nil, nil
			r.registry, d.retention = oldRegistry, oldRetention
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
		if driverLocked {
			d.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if err = d.current(); err != nil {
		return err
	}
	if !d.mu.TryLock() {
		return ErrBusy
	}
	driverLocked = true
	r = d.writer.effective
	if !r.mu.TryLock() {
		return ErrBusy
	}
	runtimeLocked = true
	if d.preparation != nil || d.operation != nil || d.work != nil || r.inventoryPreparationPlan != nil || r.inventoryPlan != nil {
		return ErrBusy
	}
	plan, err = freezeCommandPlan(r.executor, plan, inventoryPreparationDefinitions(), InventoryAccept)
	if err != nil {
		return err
	}
	history, err = freezeCommandPlan(r.executor, history, inventoryHistoryDefinitions(), InventoryAccept)
	if err != nil {
		return err
	}
	if plan.inventoryMemory.preparationBytes < scanByteFixedWorkspace || history.inventoryMemory.preparationBytes < scanByteFixedWorkspace {
		return &Refusal{Code: RefusalUnqualified}
	}
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(inventoryHistoryDefinitions()))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, def := range append(inventoryPreparationDefinitions(), inventoryHistoryDefinitions()...) {
		found := false
		for _, previous := range all {
			if previous.ID != def.ID {
				continue
			}
			if !reflect.DeepEqual(previous, def) {
				return invalid("inventory_preparation_definition")
			}
			found = true
			break
		}
		if !found {
			all = append(all, def)
		}
	}
	receipts := []stmt.Receipt{r.plan.registration, plan.registration, history.registration}
	for _, installed := range r.lifecyclePlans {
		if installed != nil {
			receipts = append(receipts, installed.registration)
		}
	}
	for _, installed := range r.issuerPlans {
		if installed != nil {
			receipts = append(receipts, installed.registration)
		}
	}
	registry, err := stmt.New(all, receipts...)
	if err != nil {
		return err
	}
	retention, err := scanOwnerRetentionFor(r.executor, registry)
	if err != nil {
		return err
	}
	if err = r.authority.VerifyComposition(r.governor, r.wal); err != nil {
		return err
	}
	oldRegistry, oldRetention = r.registry, d.retention
	r.registry, d.retention = registry, retention
	r.inventoryPlan = &history
	r.inventoryPreparationPlan = &plan
	installed = true
	return nil
}

func runInventoryPreparationTurn(ctx context.Context, turn *commandTurn, in *inventoryInvocation) (out commandOutput, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Out        commandOutput
			Historical bool
		}{out, historical}, err)
		out, historical, err = v.Out, v.Historical, e
	}()
	v, q := in.issuer, in.request
	if err = turn.requireInventoryMemory(ctx, scanLifecycleHistoryBytes); err != nil {
		return out, false, err
	}
	out.run, err = readCheckedScanRunSnapshot(ctx, turn, q.RunID)
	if err != nil {
		return out, false, err
	}
	r := out.run.run
	original := scanOriginalFromNative(r.scope)
	current := inventoryRun{leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, leaseExpiresMS: r.leaseExpiresMS}
	if !v.lease.matches(original, current) || v.lease.incarnation != r.scope.commandIncarnation || r.scope.libraryID != q.LibraryID ||
		r.scope.libraryKey != q.LibraryKey || r.acknowledgedSequence != r.lastSequence || r.phase != "inventory" || out.run.root.state != "pending" {
		return out, false, ErrConflict
	}
	if q.Receipt.Identity.Sequence > r.acknowledgedSequence {
		return out, false, ErrResolveRequired
	}
	if _, err = turn.commandTimeMS(ctx, InventoryAccept, r.leaseExpiresMS); err != nil {
		return out, false, err
	}
	if err = checkScanSourceActive(ctx, turn, r.scope.sourceID, q.RunID); err != nil {
		return out, false, err
	}
	out.source, err = captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, r.scope.sourceID)
	if err != nil {
		return out, false, err
	}
	if !out.source.matches(original) {
		return out, false, ErrConflict
	}
	extra, err := scanSnapshotWorkingBytes(out.run)
	if err != nil {
		return out, false, err
	}
	_, err = v.driver.retainSourcePreparation(ctx, turn, in, out.source, extra)
	return out, true, err
}

func (d *ScanDriver) PrepareInventory(ctx context.Context, lease *ScanRunLease, last ScanRunReference, directory []byte) (out *N1Preparation, err error) {
	life := ctx
	locked := false
	var candidate *N1Preparation
	defer func() {
		if !locked {
			return
		}
		if err != nil && candidate != nil {
			// The final lifetime check discarded the capability. Keep the funded
			// preparation reachable for ReleasePending, without an orphan wrapper
			// that would prevent cleanup. Native/grant custody is not refunded here.
			if d.preparation != nil && d.preparation.inventory == candidate {
				d.preparation.inventory = nil
			}
			candidate.template = InventoryAcceptInput{}
			candidate.closed, candidate.preparation = true, nil
		}
		d.mu.Unlock()
	}()
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { out, err = finishCommand(ctx, life, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	locked = true
	if d.operation != nil || d.preparation != nil || d.work != nil {
		return nil, ErrBusy
	}
	if !bytes.Equal(directory, []byte(".")) {
		return nil, &Refusal{Code: RefusalUnqualified}
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerInventory, lease: lease}
	result, err := d.invokeIssuer(ctx, last.Scan, InventoryAccept, call, false)
	if err != nil {
		return nil, err
	}
	p := d.preparation
	if p == nil || p.source == nil {
		return nil, ErrInvalid
	}
	n, s := result.run, *p.source
	if n.run.lastSequence == math.MaxInt64 {
		return nil, ErrCap
	}
	identity := CommandIdentity{Incarnation: n.run.scope.commandIncarnation, Sequence: n.run.lastSequence + 1}
	if _, err = rand.Read(identity.OperationID[:]); err != nil {
		return nil, err
	}
	p.request.DirectoryRevision, p.request.Cursor = n.root.revision, n.root.cursor
	out = &N1Preparation{driver: d, preparation: p, lease: lease, identity: identity, template: InventoryAcceptInput{
		Scope:  InventoryScopeInput{RunID: n.run.scope.runID, LibraryID: s.libraryID, LibraryKey: s.libraryKey},
		Source: InventorySourceInput{ID: s.source.id, Incarnation: s.source.incarnation, RootIdentity: s.source.rootIdentity, ResolvedRoot: s.source.resolvedRoot, Generation: s.source.generation, FollowSymlinks: s.source.followSymlinks},
		Run: InventoryRunInput{Revision: n.run.revision, PolicyRevision: s.policyRevision, LeaseOwner: n.run.leaseOwner, LeaseGeneration: n.run.leaseGeneration, LeaseExpiresMS: n.run.leaseExpiresMS,
			LastSequence: n.run.lastSequence, AcknowledgedSequence: n.run.acknowledgedSequence, Phase: n.run.phase, ScanMode: n.run.scope.scanMode},
		Directory: InventoryDirectoryInput{Path: []byte("."), Identity: n.root.identity, Revision: n.root.revision, ExpectedCursor: n.root.cursor, Pages: n.root.pages, Skipped: n.root.skipped, CursorKind: inventoryLinuxCursor}}}
	out.self = out
	candidate = out
	p.inventory = out
	return out, nil
}

func (p *N1Preparation) Snapshot(ctx context.Context) (value InventoryPreparationSnapshot, err error) {
	life := ctx
	if p != nil && p.self == p && p.driver != nil {
		life = p.driver.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if p == nil || p.self != p || p.driver == nil {
		return value, ErrInvalid
	}
	d := p.driver
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	defer d.mu.Unlock()
	if p.closed || p.preparation == nil || p.read || d.preparation != p.preparation {
		return value, ErrClosed
	}
	life = p.preparation.lifetime
	if err = p.preparation.current(); err != nil {
		return value, err
	}
	value = InventoryPreparationSnapshot{Identity: p.identity, Input: p.template, IdentityRoot: bytes.Clone(p.preparation.source.identityRoot)}
	value.Input.Source.ResolvedRoot = bytes.Clone(value.Input.Source.ResolvedRoot)
	value.Input.Directory.Path = bytes.Clone(value.Input.Directory.Path)
	p.read = true
	return value, nil
}

func (p *N1Preparation) ObserveInventory(ctx context.Context) (value nativeinventory.Page, err error) {
	life := ctx
	locked, attempted := false, false
	// This wrapper has its own final publication boundary. Even a cancellation
	// AFTER the inner observer returned must invalidate this preparation.
	defer func() {
		primary := recover()
		if attempted && (err != nil || primary != nil) {
			p.observationFailed.Store(true)
		}
		if locked {
			p.driver.mu.Unlock()
		}
		if primary != nil {
			panic(primary)
		}
	}()
	if p != nil && p.self == p && p.driver != nil {
		life = p.driver.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if p == nil || p.self != p || p.driver == nil || ctx == nil {
		return value, ErrInvalid
	}
	d := p.driver
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	locked = true
	if p.closed || p.observationFailed.Load() || p.historyOnly || p.command != nil || p.preparation == nil || d.preparation != p.preparation {
		return value, ErrClosed
	}
	b := p.preparation
	life = b.lifetime
	// A refused repeat never reaches the inner observer, whose first-attempt
	// failure rule is deliberately sticky. Keep a completed page/frozen command
	// intact; no new native observation or admission has happened here.
	b.mu.Lock()
	finalized := b.closed || b.retiring || b.retired || b.failed || b.observing || b.observed != nil
	b.mu.Unlock()
	if finalized {
		return value, ErrClosed
	}
	if err = d.current(); err != nil {
		return value, err
	}
	attempted = true
	return b.ObserveInventory(ctx)
}

func (p *N1Preparation) Accept(ctx context.Context, command InventoryAcceptCommand) (value InventoryResult, err error) {
	life := ctx
	locked, published := false, false
	defer func() {
		if !locked {
			return
		}
		if err != nil && published {
			// A final lifetime check can discard an already committed result.
			// Keep its charged buffers, but allow recovery of the same original.
			p.resultPublished = false
		}
		p.driver.mu.Unlock()
	}()
	if p != nil && p.self == p && p.driver != nil {
		life = p.driver.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if p == nil || p.self != p || p.driver == nil {
		return value, ErrInvalid
	}
	d := p.driver
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	locked = true
	if p.closed || p.observationFailed.Load() || p.preparation == nil || d.preparation != p.preparation || p.lease == nil {
		return value, ErrClosed
	}
	life = p.preparation.lifetime
	if err = d.current(); err != nil {
		return value, err
	}
	if err = p.preparation.current(); err != nil {
		return value, err
	}
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || c != p.command || !c.valid || p.historyOnly {
		return value, ErrInvalid
	}
	ref, err := InventoryAcceptReference(c)
	if err != nil || ref != p.reference {
		return value, errors.Join(ErrConflict, err)
	}
	if p.resultPublished && p.last.Outcome.State != Pending {
		return value, ErrBusy
	}
	work := d.work
	if work == nil {
		work, err = d.prepareWork(ctx, InventoryAccept, ref, p.lease.generation, p.preparation)
		if err != nil {
			return value, err
		}
	} else {
		if work.purpose != InventoryAccept || work.original != ref || work.preparation != p.preparation {
			return value, ErrConflict
		}
		if work.submission == scanNoGrant || work.nativeReturned() && p.last.Outcome.State == Pending {
			work.submission = scanPrepared
		}
		if work.submission != scanPrepared {
			return value, ErrResolveRequired
		}
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerN1Apply, retained: p.preparation, work: work, deadline: p.preparation.expected.Deadline}
	out, _, err := d.writer.effective.runCommand(ctx, d.writer, nil, EnrichmentReceiptRequest{}, nil, false,
		&inventoryInvocation{disclosure: d.sourceDisclosure(ref), request: ref, input: c, issuer: call})
	if !p.batchFeedback && !out.inventory.Outcome.Replayed && d.writer.effective.inventoryDriverPlan != nil {
		// Replay is history, not a new throughput sample. Failure only backs off
		// future originals; this preparation and any retry keep the frozen limit.
		hold := time.Duration(0)
		if e := out.inventory.Outcome.Evidence; e != nil && e.PhysicalReturnedAt != nil {
			hold = e.PhysicalReturnedAt.Sub(e.HoldStartedAt)
		}
		d.batchPolicy.observe(inventoryPlanBatchFiles(*d.writer.effective.inventoryDriverPlan), len(c.request.files), hold, call.writerWait, err == nil && out.inventory.Outcome.State == Applied)
		p.batchFeedback = true
	}
	if err != nil {
		return value, err
	}
	p.last = out.inventory
	p.last.Outcome.Evidence = nil
	p.last.Members = slices.Clone(out.inventory.Members)
	p.resultPublished = true
	published = true
	return out.inventory, nil
}

func (p *N1Preparation) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if p.self != p || ctx == nil {
		return ErrInvalid
	}
	d := p.driver
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if p.closed {
		return nil
	}
	if p.preparation == nil {
		return ErrClosed
	}
	if err := p.preparation.Close(ctx); err != nil {
		return err
	}
	p.template = InventoryAcceptInput{}
	p.closed = true
	p.preparation = nil
	d.preparation = nil
	return nil
}
