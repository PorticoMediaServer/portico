package writer

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"math"
)

func (d *ScanDriver) PrepareCreate(ctx context.Context, in ScanRunInput) (op *ScanRunOperation, err error) {
	life := ctx
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { op, err = finishCommand(ctx, life, op, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.operation != nil || d.preparation != nil || d.work != nil {
		return nil, ErrBusy
	}
	if in.LibraryID <= 0 || in.LibraryKey == "" || in.SourceID == "" || !scanLifecycleMode(in.ScanMode) ||
		len(in.LibraryKey) > maxCanonicalRequestBytes || len(in.SourceID) > maxCanonicalRequestBytes-len(in.LibraryKey) {
		return nil, ErrInvalid
	}
	b, err := d.allocateBirth(ctx)
	if err != nil {
		return nil, err
	}
	q := ScanReceiptRequest{RunID: b.runID, LibraryID: in.LibraryID, LibraryKey: in.LibraryKey, Receipt: ReceiptReference{Identity: b.identity}}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerSource, sourceID: in.SourceID}
	_, err = d.invokeIssuer(ctx, q, InventoryCreate, call, false)
	if err != nil {
		return nil, err
	}
	p := d.preparation
	if p == nil || p.source == nil {
		return nil, ErrInvalid
	}
	if _, err = p.ObserveInventory(ctx); err != nil {
		return nil, err
	}
	source := *p.source
	required, err := scanLifecycleWorkingBytes(source.source.resolvedRoot, source.libraryKey, source.source.id, source.source.incarnation, source.source.rootIdentity,
		in.ScanMode, p.observed.DirectoryIdentity, p.observed.DirectoryRevision)
	if err != nil {
		return nil, err
	}
	if required > uint64(p.expected.Need[governor.MemoryBytes]) {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	command, err := d.prepareBirth(ctx, b, p, source, in.ScanMode)
	if err != nil {
		return nil, err
	}
	q.Receipt = ReceiptReference{command.identity, command.digest}
	if _, err = d.prepareWork(ctx, InventoryCreate, q, 0, p); err != nil {
		return nil, err
	}
	op = &ScanRunOperation{driver: d, preparation: p, reference: ScanRunReference{q, InventoryCreate}, create: command}
	op.self = op
	p.operation = op
	d.operation = op
	return op, nil
}

func (d *ScanDriver) PrepareClaim(ctx context.Context, previous ScanRunReference) (op *ScanRunOperation, err error) {
	life := ctx
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { op, err = finishCommand(ctx, life, op, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.operation != nil || d.preparation != nil || d.work != nil {
		return nil, ErrBusy
	}
	if previous.Purpose != InventoryCreate && previous.Purpose != InventoryClaim {
		return nil, ErrInvalid
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerSnapshot}
	out, err := d.invokeIssuer(ctx, previous.Scan, InventoryClaim, call, false)
	if err != nil {
		return nil, err
	}
	p := d.preparation
	if p == nil {
		return nil, ErrInvalid
	}
	n := out.run
	if n.run.lastSequence == math.MaxInt64 || n.run.acknowledgedSequence != n.run.lastSequence {
		return nil, ErrCap
	}
	identity := CommandIdentity{Incarnation: n.run.scope.commandIncarnation, Sequence: n.run.lastSequence + 1}
	if _, err = rand.Read(identity.OperationID[:]); err != nil {
		return nil, err
	}
	request := scanClaimRequest(n)
	required, err := scanLifecycleWorkingBytes(request.Original.ResolvedRoot, request.Original.RunID, request.Original.LibraryKey, request.Original.SourceID,
		request.Original.SourceIncarnation, request.Original.RootIdentity, request.Root.Identity, request.Root.Revision, request.Root.Cursor,
		request.JobCreatedAt, request.JobErrorText, request.FinishedAt)
	if err != nil {
		return nil, err
	}
	if required > uint64(p.expected.Need[governor.MemoryBytes]) {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	command, err := newInventoryClaimCommand(identity, request)
	if err != nil {
		return nil, err
	}
	q := previous.Scan
	q.Receipt = ReceiptReference{command.identity, command.digest}
	if _, err = d.prepareWork(ctx, InventoryClaim, q, n.run.leaseGeneration, p); err != nil {
		return nil, err
	}
	op = &ScanRunOperation{driver: d, preparation: p, reference: ScanRunReference{q, InventoryClaim}, claim: command}
	op.self = op
	p.operation = op
	d.operation = op
	return op, nil
}

// OpenRun retains the ORIGINAL reference for recovery. It never reads a live
// source or turns a saved lease into a current capability. Resolve/consumer
// release/ACK use the same operation protocol as a newly submitted command.
func (d *ScanDriver) OpenRun(ctx context.Context, reference ScanRunReference) (op *ScanRunOperation, err error) {
	life := ctx
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { op, err = finishCommand(ctx, life, op, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.operation != nil || d.preparation != nil || d.work != nil || d.birth != nil {
		return nil, ErrBusy
	}
	if reference.Purpose != InventoryCreate && reference.Purpose != InventoryClaim {
		return nil, ErrInvalid
	}
	out, err := d.invokeIssuer(ctx, reference.Scan, reference.Purpose, &scanIssuerInvocation{driver: d, action: scanIssuerRecover}, false)
	if err != nil {
		return nil, err
	}
	p := d.preparation
	if p == nil || p.mode != scanHistoryPreparation {
		return nil, ErrInvalid
	}
	op = &ScanRunOperation{driver: d, preparation: p, reference: reference, historyOnly: true, last: out.lifecycle}
	op.self = op
	p.operation, d.operation = op, op
	return op, nil
}

func (d *ScanDriver) invokeIssuer(ctx context.Context, q ScanReceiptRequest, purpose CommandID, call *scanIssuerInvocation, ack bool) (out commandOutput, err error) {
	defer func() { err = errors.Join(err, d.current()); out, err = finishCommand(ctx, d.lifetime, out, err) }()
	in := &inventoryInvocation{disclosure: d.sourceDisclosure(q), request: q, lifecycle: purpose, issuer: call}
	out, _, err = d.writer.effective.dispatchCommand(ctx, d.writer, nil, EnrichmentReceiptRequest{}, nil, ack, in, nil)
	if err != nil && d.preparation != nil {
		d.preparation.mu.Lock()
		d.preparation.failed = true
		d.preparation.mu.Unlock()
	}
	return out, err
}

// ReleasePending abandons only preparation that has never entered a native
// command frame. It also covers a failed Prepare call that returned no handle.
// Submitted work must use its original resolution/consumer/ACK protocol.
func (d *ScanDriver) ReleasePending(ctx context.Context) error {
	if d == nil || d.self != d || ctx == nil {
		return ErrInvalid
	}
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if d.preparation != nil && d.preparation.inventory != nil {
		// A recovered history has no public preparation handle. This explicit
		// release is its consumer Close; it never ACKs, clears a receipt or
		// infers physical return. A live producer uses its own N1 Close.
		if !d.preparation.inventory.historyOnly {
			return ErrBusy
		}
		if err := d.preparation.Close(ctx); err != nil {
			return err
		}
		d.preparation = nil
		return nil
	}
	if d.work != nil && d.work.submission != scanPrepared && d.work.submission != scanNoGrant {
		return ErrResolveRequired
	}
	// No native command can still read these roots. The preparation retains its
	// own frame/helper custody and refuses its refund until they actually join.
	d.work, d.birth = nil, nil
	if d.preparation != nil {
		if err := d.preparation.Close(ctx); err != nil {
			return err
		}
		d.preparation = nil
	}
	return nil
}

func (o *ScanRunOperation) Reference() (ScanRunReference, error) {
	if o == nil || o.self != o || o.driver == nil {
		return ScanRunReference{}, ErrInvalid
	}
	if !o.driver.mu.TryLock() {
		return ScanRunReference{}, ErrBusy
	}
	defer o.driver.mu.Unlock()
	if o == nil || o.self != o || o.closed {
		return ScanRunReference{}, ErrInvalid
	}
	return o.reference, nil // exact original, never recomputed from later state
}

func (o *ScanRunOperation) Apply(ctx context.Context) (result *ScanRunResult, err error) {
	life := ctx
	defer func() { result, err = finishCommand(ctx, life, result, err) }()
	if o == nil || o.self != o || o.driver == nil {
		return nil, ErrInvalid
	}
	if !o.driver.mu.TryLock() {
		return nil, ErrBusy
	}
	defer o.driver.mu.Unlock()
	if o.preparation != nil {
		life = o.preparation.lifetime
	}
	return o.runLocked(ctx, false)
}

func (o *ScanRunOperation) Resolve(ctx context.Context) (result *ScanRunResult, err error) {
	life := ctx
	defer func() { result, err = finishCommand(ctx, life, result, err) }()
	if o == nil || o.self != o || o.driver == nil {
		return nil, ErrInvalid
	}
	if !o.driver.mu.TryLock() {
		return nil, ErrBusy
	}
	defer o.driver.mu.Unlock()
	if o.preparation != nil {
		life = o.preparation.lifetime
	}
	return o.runLocked(ctx, true)
}

func (o *ScanRunOperation) runLocked(ctx context.Context, resolve bool) (result *ScanRunResult, err error) {
	life := ctx
	if o != nil && o.self == o && o.preparation != nil {
		life = o.preparation.lifetime
	}
	defer func() { result, err = finishCommand(ctx, life, result, err) }()
	if o == nil || o.self != o || o.driver == nil || o.closed {
		return nil, ErrInvalid
	}
	d := o.driver
	if d.operation != o || (!o.historyOnly && d.work == nil) || (o.historyOnly && !resolve) {
		return nil, ErrInvalid
	}
	if o.result != nil && !o.result.closed {
		return nil, ErrBusy
	}
	if o.preparation.failed || o.preparation.closed {
		return nil, ErrClosed
	}
	if err = o.preparation.current(); err != nil {
		return nil, err
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerHistory, retained: o.preparation, deadline: o.preparation.expected.Deadline}
	if !resolve {
		if d.work.submission == scanNoGrant {
			d.work.submission = scanPrepared
		}
		if d.work.submission != scanPrepared {
			return nil, ErrResolveRequired
		}
		call.work, call.deadline = d.work, o.preparation.expected.Deadline
		if o.create != nil {
			call.action, call.create, call.birth = scanIssuerCreate, o.create, d.birth
		} else {
			call.action, call.claim = scanIssuerClaim, o.claim
		}
	}
	out, err := d.invokeIssuer(ctx, o.reference.Scan, o.reference.Purpose, call, false)
	if err != nil {
		return nil, err
	}
	if err = retainScanLifecycleResult(o.preparation, out.lifecycle); err != nil {
		return nil, err
	}
	o.last = out.lifecycle
	result = &ScanRunResult{operation: o, value: out.lifecycle}
	result.self = result
	o.result = result
	return result, nil
}

func (r *ScanRunResult) Snapshot(ctx context.Context) (value ScanRunSnapshot, err error) {
	life := ctx
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if r == nil || r.self != r || r.operation == nil {
		return value, ErrInvalid
	}
	d := r.operation.driver
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	defer d.mu.Unlock()
	if r.operation.preparation != nil {
		life = r.operation.preparation.lifetime
	}
	if r.closed || r.read || r.operation.closed {
		return value, ErrClosed
	}
	if err = d.current(); err != nil {
		return value, err
	}
	if r.operation.preparation == nil {
		return value, ErrClosed
	}
	if err = r.operation.preparation.current(); err != nil {
		return value, err
	}
	v := r.value.Value
	value = ScanRunSnapshot{Outcome: r.value.Outcome, RunID: v.Original.RunID, AcceptedRunRevision: v.AcceptedRunRevision, AppliedMS: v.AppliedMS,
		LeaseOwner: v.LeaseOwner, LeaseGeneration: v.LeaseGeneration, LeaseExpiresMS: v.LeaseExpiresMS, AcknowledgedSequence: v.AcknowledgedSequence,
		Discovered: v.Discovered, Processed: v.Processed, RootIdentity: v.RootIdentity, RootRevision: v.RootRevision, RootCursor: v.RootCursor,
		RootPages: v.RootPages, RootSkipped: v.RootSkipped, Digest: r.value.Digest}
	r.read = true
	return value, nil
}

func (r *ScanRunResult) Close() error {
	if r == nil {
		return nil
	}
	if r.self != r || r.operation == nil {
		return ErrInvalid
	}
	d := r.operation.driver
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	r.value = scanLifecycleCommandResult{}
	r.closed = true
	return nil
}

func (o *ScanRunOperation) Ack(ctx context.Context) (value AckResult, err error) {
	life := ctx
	locked := false
	defer func() {
		if locked {
			if err != nil {
				o.acknowledged = false
			}
			o.driver.mu.Unlock()
		}
	}()
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if o == nil || o.self != o || o.driver == nil {
		return value, ErrInvalid
	}
	d := o.driver
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	locked = true
	if o.closed || o.preparation == nil {
		return value, ErrClosed
	}
	life = o.preparation.lifetime
	if o.result != nil && !o.result.closed {
		return value, ErrBusy
	}
	if o.last.Outcome.State != Applied && o.last.Outcome.State != ResultRetired {
		return value, ErrResolveRequired
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerHistory, retained: o.preparation, deadline: o.preparation.expected.Deadline}
	in := &inventoryInvocation{disclosure: d.sourceDisclosure(o.reference.Scan), request: o.reference.Scan, lifecycle: o.reference.Purpose, issuer: call}
	_, value, err = d.writer.effective.dispatchCommand(ctx, d.writer, nil, EnrichmentReceiptRequest{}, nil, true, in, nil)
	if err == nil {
		o.acknowledged = true
	}
	return value, err
}

func (o *ScanRunOperation) Lease(ctx context.Context) (value *ScanRunLease, err error) {
	life := ctx
	locked := false
	var candidate *ScanRunLease
	defer func() {
		if locked {
			if err != nil && candidate != nil {
				candidate.revoked.Store(true)
				if o.driver.lease == candidate {
					o.driver.lease = nil
				}
			}
			o.driver.mu.Unlock()
		}
	}()
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if o == nil || o.self != o || o.driver == nil {
		return nil, ErrInvalid
	}
	d := o.driver
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	locked = true
	if o.closed || o.preparation == nil || o.historyOnly || !o.acknowledged || o.reference.Purpose != InventoryClaim {
		return nil, ErrInvalid
	}
	life = o.preparation.lifetime
	out, err := d.invokeIssuer(ctx, o.reference.Scan, InventoryClaim, &scanIssuerInvocation{driver: d, action: scanIssuerCurrent, retained: o.preparation, deadline: o.preparation.expected.Deadline}, false)
	if err != nil {
		return nil, err
	}
	n := out.run.run
	if n.leaseOwner != d.leaseOwner || n.leaseGeneration != o.last.Value.LeaseGeneration || n.leaseExpiresMS != o.last.Value.LeaseExpiresMS ||
		n.scope.createOperation != o.last.Value.Original.CreateOperationID {
		return nil, ErrConflict
	}
	if d.lease != nil {
		d.lease.revoked.Store(true)
	}
	value = &ScanRunLease{driver: d, createOperation: n.scope.createOperation, incarnation: n.scope.commandIncarnation, owner: n.leaseOwner, generation: n.leaseGeneration, expiresMS: n.leaseExpiresMS}
	value.self = value
	candidate = value
	d.lease = value
	return value, nil
}

func (o *ScanRunOperation) Close(ctx context.Context) error {
	if o == nil {
		return nil
	}
	if o.self != o || o.driver == nil || ctx == nil {
		return ErrInvalid
	}
	d := o.driver
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if o.closed && o.cleanup == nil {
		return nil
	}
	if o.cleanup == nil {
		if o.preparation == nil || d.preparation != o.preparation || d.operation != o {
			return ErrInvalid
		}
		if o.result != nil && !o.result.closed {
			if o.result.read {
				return ErrBusy
			}
			o.result.value = scanLifecycleCommandResult{}
			o.result.closed = true
		}
		if !o.historyOnly {
			w := d.work
			if w == nil || w.original != o.reference.Scan || w.purpose != o.reference.Purpose || w.preparation != o.preparation || !w.nativeReturned() {
				return ErrResolveRequired
			}
		}
		if !o.acknowledged && o.last.Outcome.State == Applied {
			return ErrResolveRequired
		}
		proof := &scanRunCleanup{operation: o, preparation: o.preparation}
		if o.acknowledged && !o.historyOnly {
			v := o.last.Value
			proof.predecessor = &scanReturnedWork{driver: d, createOperation: v.Original.CreateOperationID, incarnation: o.reference.Scan.Receipt.Identity.Incarnation,
				sequence: o.reference.Scan.Receipt.Identity.Sequence, generation: v.LeaseGeneration}
		}
		o.cleanup = proof
	}
	proof := o.cleanup
	if proof.operation != o || proof.preparation == nil {
		return ErrInvalid
	}
	// Wait failures retain the same proof, payloads, native-return state and
	// charged preparation. Its actual refund boundary clears all payload roots.
	if err := proof.preparation.Close(ctx); err != nil {
		return err
	}
	if proof.predecessor != nil {
		d.predecessor = proof.predecessor
	}
	o.preparation, o.cleanup = nil, nil
	o.reference = ScanRunReference{}
	o.closed = true
	d.preparation, d.operation = nil, nil
	return nil
}

func retainScanLifecycleResult(p *scanPreparation, v scanLifecycleCommandResult) error {
	n, err := scanLifecycleRetainedBytes(ScanReceiptRequest{}, v)
	if err != nil {
		return err
	}
	if n > uint64(p.expected.Need[governor.MemoryBytes]) {
		return &Refusal{Code: RefusalCapacity}
	}
	return p.current()
}

func scanLifecycleRetainedBytes(q ScanReceiptRequest, v scanLifecycleCommandResult) (uint64, error) {
	r := v.Value
	o := r.Original
	return scanLifecycleWorkingBytes(o.ResolvedRoot, q.RunID, q.LibraryKey, o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation, o.RootIdentity, o.ScanMode, r.RootIdentity, r.RootRevision, r.RootCursor)
}

func scanOriginalFromNative(s scanRunScope) scanRunOriginalScope {
	return scanRunOriginalScope{RunID: s.runID, LibraryID: s.libraryID, LibraryKey: s.libraryKey, SourceID: s.sourceID, SourceGeneration: s.sourceGeneration,
		SourceIncarnation: s.incarnation, RootIdentity: s.rootIdentity, ResolvedRoot: []byte(s.resolvedRoot), FollowSymlinks: s.followSymlinks == 1,
		PolicyRevision: s.policyRevision, ScanMode: s.scanMode, CreateOperationID: s.createOperation, CreateRequestDigest: s.createDigest}
}

func scanClaimRequest(n scanRunSnapshot) inventoryClaimRequest {
	r := n.run
	s := r.scope
	return inventoryClaimRequest{Original: scanOriginalFromNative(s), Run: inventoryRun{revision: r.revision, policyRevision: s.policyRevision, phase: r.phase, scanMode: s.scanMode,
		leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, leaseExpiresMS: r.leaseExpiresMS, lastSequence: r.lastSequence, acknowledgedSequence: r.acknowledgedSequence},
		Root:       scanRootWitness{Path: ".", Identity: n.root.identity, Revision: n.root.revision, Cursor: n.root.cursor, State: n.root.state, Pages: n.root.pages, Skipped: n.root.skipped},
		Discovered: r.discovered, Processed: n.job.processed, Authoritative: r.authoritative, RootRebound: r.rootRebound, FinishedAt: r.finishedAt,
		JobStatus: n.job.status, JobCreatedAt: n.job.createdAt, JobErrorText: n.job.errorText, Attempts: r.attempts,
		NextAttemptMS: scanOptionalMS{Present: r.nextAttemptMS.Valid, Value: r.nextAttemptMS.Int64}}
}

func scanSnapshotWorkingBytes(n scanRunSnapshot) (uint64, error) {
	s := n.run.scope
	return scanLifecycleWorkingBytes(nil, s.runID, s.libraryKey, s.resolvedRoot, s.sourceID, s.incarnation, s.rootIdentity, s.scanMode, n.run.phase, n.run.finishedAt,
		n.job.createdAt, n.job.errorText, n.job.status, n.root.identity, n.root.revision, n.root.cursor, n.root.state)
}
