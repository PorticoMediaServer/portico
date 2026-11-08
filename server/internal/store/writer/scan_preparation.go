package writer

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// scanPreparation is W-private physical custody, not a public source, lease or
// birth capability. The issuer supplies an ALREADY admitted, complete original
// preparation forecast from the SAME composition; no default budget or Try is
// hidden here. It must retain this object through Close, even after errors.
type scanPreparation struct {
	self                                         *scanPreparation
	mu                                           sync.Mutex
	opening                                      *scanOpening
	runtime                                      *effectiveRuntime
	ownership                                    *stateownership.Lease
	observer                                     nativeinventory.Observer
	request                                      nativeinventory.Request
	frame                                        *transaction          // SAME actual native frame
	source                                       *scanSourceFacts      // actual native capture; owned by this reservation
	observed                                     *nativeinventory.Page // actual read, private backing
	key                                          string
	scope                                        *sourceops.Scope
	lifetime                                     context.Context
	cancel                                       context.CancelFunc
	stopOpening                                  func() bool
	stopDriver                                   func() bool
	operation                                    *ScanRunOperation
	inventory                                    *N1Preparation
	mode                                         scanPreparationMode
	stopScope                                    func() bool
	grant                                        *governor.Grant
	expected                                     governor.Request
	observing, failed, closed, retiring, retired bool
}

type scanPreparationMode uint8

const (
	scanNativePreparation scanPreparationMode = iota + 1
	scanHistoryPreparation
	scanBasicPreparation // reserved dedicated Basic custody; never native observation
)

func newScanPreparation(ctx context.Context, opening *scanOpening, owner *stateownership.Lock, r *effectiveRuntime,
	observer nativeinventory.Observer, expected governor.Request, grant *governor.Grant, request nativeinventory.Request, modes ...scanPreparationMode) (out *scanPreparation, err error) {
	var candidate *scanPreparation
	var lease *stateownership.Lease
	lifetime := ctx
	// Registered FIRST, so cleanup runs AFTER the final release check below.
	// Every check capable of discarding the handle is therefore observed here.
	defer func() {
		out, err = finishCommand(ctx, lifetime, out, err)
		if out == nil {
			if candidate != nil {
				candidate.cancel()
				candidate.scope.Close()
				candidate.stopScope()
				candidate.stopOpening()
			}
			if lease != nil {
				err = errors.Join(err, lease.Close())
			}
			// The issuer still owns its unconsumed grant on constructor error.
		}
	}()
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	mode := scanNativePreparation
	if len(modes) > 1 || len(modes) == 1 && modes[0] != scanHistoryPreparation && modes[0] != scanBasicPreparation {
		return nil, ErrInvalid
	}
	if len(modes) == 1 {
		mode = modes[0]
	}
	purpose := CommandID(expected.Key.Domain)
	ordinary := mode != scanBasicPreparation && (purpose == InventoryCreate || purpose == InventoryClaim || purpose == InventoryAccept)
	basic := mode == scanBasicPreparation && (purpose == BasicStage || purpose == BasicSeal || purpose == BasicAccept)
	if ctx == nil || opening == nil || opening.binding == nil || opening.executor == nil || owner == nil || r == nil || r.executor != opening.executor || r.path != opening.binding.path ||
		expected.Writer || expected.Class != governor.ScanMinimum || expected.Owner != r.owner || grant == nil ||
		(!ordinary && !basic) || expected.Key.ID == "" ||
		expected.Need[governor.MemoryBytes] <= 0 || len(expected.Footprint) != 1 || expected.Footprint[0].Library <= 0 ||
		expected.Footprint[0].Domain != expected.Key.Domain {
		return nil, ErrInvalid
	}
	if mode != scanNativePreparation {
		// History and dedicated Basic custody have no native inventory observation
		// authority. Basic retains only its exact-purpose ORIGINAL funded holding.
		if len(request.Root) != 0 || len(request.RelativePath) != 0 ||
			request.RootIdentity != "" || request.DirectoryRevision != "" || request.Cursor != "" || request.Limit != 0 || request.FollowSymlinks {
			return nil, ErrInvalid
		}
	} else if request.Limit != 1 && (purpose != InventoryAccept || request.Limit < 1 || request.Limit > maxInventoryBatchFiles || r.inventoryDriverPlan == nil || request.Limit > inventoryPlanBatchFiles(*r.inventoryDriverPlan)) {
		return nil, ErrInvalid
	}
	// Complete retained-input bytes precede scans, path conversion, clones or
	// construction. This uses the SAME admitted preparation grant, no borrowing.
	remaining := uint64(expected.Need[governor.MemoryBytes])
	fixed := uint64(8192) + uint64(len(expected.IO))*256 + uint64(len(expected.Footprint))*128
	if remaining < fixed {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	remaining -= fixed
	texts := [...]string{expected.Key.ID, request.RootIdentity, request.DirectoryRevision, request.Cursor}
	for _, s := range texts {
		if uint64(len(s)) > remaining/4 {
			return nil, &Refusal{Code: RefusalCapacity}
		}
		remaining -= uint64(len(s)) * 4
	}
	for _, b := range [][]byte{request.Root, request.RelativePath} {
		if uint64(len(b)) > remaining/4 {
			return nil, &Refusal{Code: RefusalCapacity}
		}
		remaining -= uint64(len(b)) * 4
	}
	for _, d := range expected.IO {
		if uint64(len(d.DeviceID)) > remaining/4 {
			return nil, &Refusal{Code: RefusalCapacity}
		}
		remaining -= uint64(len(d.DeviceID)) * 4
	}
	if mode == scanNativePreparation && !nativeinventory.ValidRequest(request) {
		return nil, ErrInvalid
	}
	if err = errors.Join(ctx.Err(), opening.binding.check()); err != nil {
		return nil, err
	}
	if err = r.authority.VerifyComposition(r.governor, r.wal); err != nil {
		return nil, err
	}
	if err = r.authority.Check(grant, expected); err != nil {
		return nil, err
	}
	lease, err = owner.Borrow()
	if err != nil {
		return nil, err
	}
	path, err := lease.LockPath()
	want, wantErr := opening.ownership.LockPath()
	if err != nil || wantErr != nil || path != want {
		return nil, errors.Join(ErrInvalid, err, wantErr)
	}
	p := &scanPreparation{opening: opening, runtime: r, ownership: lease, observer: observer, scope: sourceops.New(), grant: grant, mode: mode}
	p.self = p
	// Deadline is the grant's ORIGINAL funded lifetime. Child transport settings
	// may shorten an operation, never extend this context or the grant.
	p.lifetime, p.cancel = context.WithDeadline(ctx, expected.Deadline)
	p.stopOpening = context.AfterFunc(opening.lifetime, p.cancel)
	p.stopScope = context.AfterFunc(p.lifetime, p.scope.Close)
	candidate, lifetime = p, p.lifetime
	expected.Key.ID = strings.Clone(expected.Key.ID)
	expected.IO = slices.Clone(expected.IO)
	for i := range expected.IO {
		expected.IO[i].DeviceID = strings.Clone(expected.IO[i].DeviceID)
	}
	expected.Footprint = slices.Clone(expected.Footprint)
	p.expected = expected
	p.key = expected.Key.ID
	request.Root, request.RelativePath = bytes.Clone(request.Root), bytes.Clone(request.RelativePath)
	request.RootIdentity, request.DirectoryRevision, request.Cursor = strings.Clone(request.RootIdentity), strings.Clone(request.DirectoryRevision), strings.Clone(request.Cursor)
	p.request = request
	if err = p.current(); err != nil {
		return nil, err
	}
	return p, nil
}

// current is checked independently of the read's caller cancellation, so an
// opening/ownership revocation cannot be hidden by an ended request lifetime.
func (p *scanPreparation) current() error {
	if p == nil || p.self != p || p.opening == nil || p.runtime == nil || p.ownership == nil || p.lifetime == nil {
		return ErrInvalid
	}
	if err := errors.Join(p.opening.binding.check(), p.ownership.Check(), p.lifetime.Err()); err != nil {
		return errors.Join(ErrClosed, err)
	}
	return p.runtime.authority.Check(p.grant, p.expected)
}

// ObserveInventory exposes owned raw observation values only. The actual Scope,
// Supervisor, grant and native ownership remain private; no callback or second
// runner is accepted. Any failure is sticky for this original preparation.
func (p *scanPreparation) ObserveInventory(ctx context.Context) (value nativeinventory.Page, err error) {
	lifetime := ctx
	// Run after the release check, including ALL early exits before physical
	// scope acquisition. A canceled/busy attempt cannot leave a reusable owner.
	defer func() {
		primary := recover()
		if p != nil && p.self == p && (err != nil || primary != nil) {
			p.mu.Lock()
			p.failed = true
			p.mu.Unlock()
		}
		if primary != nil {
			panic(primary)
		}
	}()
	defer func() { value, err = finishCommand(ctx, lifetime, value, err) }()
	if p == nil || p.self != p || ctx == nil {
		return value, ErrInvalid
	}
	lifetime = p.lifetime
	outer, err := p.beginObservation()
	if err != nil {
		return value, err
	}
	defer func() {
		primary := recover()
		outer.Release()
		func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.observing = false
			err = errors.Join(err, p.current())
			if p.closed || p.failed {
				err = errors.Join(err, ErrClosed)
			}
		}()
		if primary != nil {
			panic(primary)
		}
	}()
	call, cancel := context.WithCancel(p.lifetime)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	if ctx.Err() != nil {
		cancel()
	}
	actual, err := p.observer.Observe(call, p.scope, p.key, p.request)
	if err != nil {
		return value, err
	}
	// Retain the actual observation privately. A producer cannot edit a later
	// issuer witness through the owned raw values returned here. Output-copy
	// bytes remain in the ORIGINAL grant until consumer Close.
	needed := uint64(len(actual.RootIdentity) + len(actual.DirectoryIdentity) + len(actual.DirectoryRevision) + len(actual.NextCursor) + len(actual.ErrorCode))
	for _, e := range actual.Entries {
		needed += uint64(len(e.Path) + len(e.Name) + len(e.ObjectIdentity) + len(e.ChangeToken))
	}
	for _, i := range actual.Issues {
		needed += uint64(len(i.Path) + len(i.Reason))
	}
	needed = needed*4 + uint64(len(actual.Entries))*256 + uint64(len(actual.Issues))*128 + 8192
	if needed > uint64(p.expected.Need[governor.MemoryBytes]) {
		return value, &Refusal{Code: RefusalCapacity}
	}
	p.mu.Lock()
	p.observed = &actual
	p.mu.Unlock()
	value = actual
	value.RootIdentity, value.DirectoryIdentity, value.DirectoryRevision = strings.Clone(actual.RootIdentity), strings.Clone(actual.DirectoryIdentity), strings.Clone(actual.DirectoryRevision)
	value.NextCursor, value.ErrorCode = strings.Clone(actual.NextCursor), strings.Clone(actual.ErrorCode)
	value.Entries = slices.Clone(actual.Entries)
	for i := range value.Entries {
		e := &value.Entries[i]
		e.Path, e.Name = bytes.Clone(e.Path), bytes.Clone(e.Name)
		e.ObjectIdentity, e.ChangeToken = strings.Clone(e.ObjectIdentity), strings.Clone(e.ChangeToken)
	}
	value.Issues = slices.Clone(actual.Issues)
	for i := range value.Issues {
		value.Issues[i].Path = bytes.Clone(value.Issues[i].Path)
		value.Issues[i].Reason = bytes.Clone(value.Issues[i].Reason)
	}
	return value, nil
}

// beginObservation changes only the private admission state. Deferred unlock
// also covers a fallible authority clock before the outer lease is acquired.
func (p *scanPreparation) beginObservation() (*sourceops.Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.retiring || p.retired || p.failed || p.observing || p.observed != nil || p.mode != scanNativePreparation || p.expected.Key.Domain == uint16(InventoryClaim) {
		return nil, ErrClosed
	}
	if err := p.current(); err != nil {
		return nil, err
	}
	// Retain the invocation BEFORE observer preflight; the observer retains its
	// own lease through physical helper/permit retirement.
	outer, err := p.scope.Acquire()
	if err != nil {
		return nil, err
	}
	p.observing = true
	return outer, nil
}

// Close revokes before waiting. Cleanup grace never renews execution time.
// A timeout leaves BOTH the grant and native ownership retained; the same
// object can later join actual retirement. There is no timer-based refund.
func (p *scanPreparation) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if p.self != p || ctx == nil {
		return ErrInvalid
	}
	p.mu.Lock()
	if p.retired {
		p.mu.Unlock()
		return nil
	}
	if p.retiring {
		p.mu.Unlock()
		return ErrClosed
	}
	p.revokeLocked()
	if p.frame != nil && (!p.frame.done || !p.frame.connectionReturned || !p.frame.retirementComplete || !p.frame.cleanupComplete) {
		p.mu.Unlock()
		return ErrResolveRequired
	}
	p.mu.Unlock()
	cleanup, cancel := context.WithTimeout(ctx, nativeinventory.RetirementGrace)
	defer cancel()
	if err := p.scope.Wait(cleanup); err != nil {
		return errors.Join(nativeinventory.ErrHelperRetirementPending, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired {
		return nil
	}
	if p.retiring {
		return ErrClosed
	}
	if p.operation != nil && p.operation.result != nil && !p.operation.result.closed && p.operation.result.read {
		return ErrBusy
	}
	p.retiring = true // panic/error cannot authorize a second retirement attempt
	if err := p.ownership.Close(); err != nil {
		return err
	}
	// Native helper/permit and borrower have actually returned. Discard every
	// owned payload BEFORE returning its reservation, including data retained by
	// closed handles outside the driver. No tombstone or cancelled Scope is a
	// memory owner after Retired. Keep only bounded closed-custody identity.
	if o := p.operation; o != nil {
		// Native work and helper custody have joined. Drop the driver's roots
		// here, not before a potentially failed wait and not after the refund.
		if w := o.driver.work; w != nil && w.preparation == p {
			o.driver.work = nil
		}
		if b := o.driver.birth; b != nil && b.preparation == p {
			o.driver.birth = nil
		}
		if o.result != nil {
			o.result.value = scanLifecycleCommandResult{}
			o.result.closed = true
		}
		o.create, o.claim, o.last, o.result = nil, nil, scanLifecycleCommandResult{}, nil
		o.reference = ScanRunReference{}
		o.preparation = nil
		o.closed = true
		if o.driver.operation == o {
			o.driver.operation = nil
		}
		p.operation = nil
	}
	if n := p.inventory; n != nil {
		if work := n.driver.work; work != nil && work.preparation == p {
			n.driver.work = nil
		}
		if n.command != nil {
			n.command.valid, n.command.request = false, inventoryAcceptRequest{}
			n.command = nil
		}
		n.preparedBytes = 0
		n.reference, n.last = ScanReceiptRequest{}, InventoryResult{}
		n.template = InventoryAcceptInput{}
		n.closed, n.preparation = true, nil
		p.inventory = nil
	}
	p.request, p.observed, p.expected, p.key = nativeinventory.Request{}, nil, governor.Request{}, ""
	if p.source != nil {
		*p.source = scanSourceFacts{}
		p.source = nil
	}
	p.observer = nativeinventory.Observer{}
	p.frame = nil
	if err := p.runtime.authority.Retired(p.grant); err != nil {
		return err
	}
	p.retired = true
	p.stopScope()
	p.stopOpening()
	if p.stopDriver != nil {
		p.stopDriver()
	}
	return nil
}

// Revocation retains all physical and memory custody until Close joins it.
func (p *scanPreparation) revoke() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.retired {
		p.revokeLocked()
	}
}

func (p *scanPreparation) revokeLocked() {
	p.closed = true
	p.cancel()
	p.scope.Close()
	p.runtime.governor.Cancel(p.grant)
}
