package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"math"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type InventoryPreparationSnapshot struct {
	Identity     CommandIdentity
	Input        InventoryAcceptInput
	IdentityRoot []byte // exact catalog_libraries.root, distinct from physical root
}

type N1Preparation struct {
	self         *N1Preparation
	driver       *ScanDriver
	preparation  *scanPreparation
	lease        *ScanRunLease
	identity     CommandIdentity
	template     InventoryAcceptInput
	read, closed bool
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

func composeInventoryPreparation(ctx context.Context, d *ScanDriver, plan effectivePlan) (err error) {
	var runtimeLocked, driverLocked, installed bool
	var r *effectiveRuntime
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.inventoryPreparationPlan = nil
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
	if d.preparation != nil || d.operation != nil || r.inventoryPreparationPlan != nil {
		return ErrBusy
	}
	plan, err = freezeCommandPlan(r.executor, plan, inventoryPreparationDefinitions(), InventoryAccept)
	if err != nil {
		return err
	}
	for _, def := range inventoryPreparationDefinitions() {
		if _, e := r.registry.Lookup(def.ID); e != nil {
			return e
		}
		matched := false
		for _, entry := range r.registry.Manifest() {
			if entry.ID == def.ID && reflect.DeepEqual(entry.Definition, def) {
				matched = true
				break
			}
		}
		if !matched {
			return invalid("inventory_preparation_definition")
		}
	}
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
	if p.closed || p.preparation == nil || d.preparation != p.preparation {
		return value, ErrClosed
	}
	life = p.preparation.lifetime
	if err = d.current(); err != nil {
		return value, err
	}
	if err = p.preparation.current(); err != nil {
		return value, err
	}
	return p.preparation.ObserveInventory(ctx)
}

func (p *N1Preparation) Accept(ctx context.Context, command InventoryAcceptCommand) (value InventoryResult, err error) {
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
	if p.closed || p.preparation == nil || d.preparation != p.preparation {
		return value, ErrClosed
	}
	life = p.preparation.lifetime
	if err = d.current(); err != nil {
		return value, err
	}
	if err = p.preparation.current(); err != nil {
		return value, err
	}
	ref, err := InventoryAcceptReference(command)
	if err != nil {
		return value, err
	}
	if ref.Receipt.Identity != p.identity || ref.RunID != p.template.Scope.RunID || ref.LibraryID != p.template.Scope.LibraryID || ref.LibraryKey != p.template.Scope.LibraryKey {
		return value, ErrConflict
	}
	// Fresh enable remains at the complete native-composition gate. Neither a
	// prepared snapshot nor a valid constructor supplies that qualification.
	return p.driver.writer.AcceptInventory(ctx, p.driver.sourceDisclosure(ref), command)
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
