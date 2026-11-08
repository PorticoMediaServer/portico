package writer

import (
	"bytes"
	"context"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Closed actions of the sole dispatcher, not callbacks, new command kinds or
// permission flags. Source/snapshot actions cannot mutate a run or return Applied.
type scanIssuerAction uint8

const (
	scanIssuerSource scanIssuerAction = iota + 1
	scanIssuerSnapshot
	scanIssuerCreate
	scanIssuerClaim
	scanIssuerHistory
	scanIssuerCurrent
	scanIssuerRecover
	scanIssuerInventory
	scanIssuerN1History
	scanIssuerN1Recover
	scanIssuerN1Apply
	scanIssuerBasicSource // read-only issuer snapshot; issues no domain effect
)

type scanIssuerInvocation struct {
	writerWait   time.Duration // physical-writer admission queue for THIS original
	lease        *ScanRunLease
	driver       *ScanDriver
	action       scanIssuerAction
	sourceID     string
	create       *inventoryCreateCommand
	claim        *inventoryClaimCommand
	birth        *scanBirth
	work         *scanDriverWork
	deadline     time.Time // ORIGINAL already-funded preparation deadline
	retained     *scanPreparation
	frame        *transaction
	claimMode    scanRunClaimMode
	expiresMS    int64
	basicPurpose CommandID // exact target of the retained non-writer holding
}

func validateScanIssuerInvocation(r *effectiveRuntime, w *Writer, in *inventoryInvocation) error {
	if in == nil || in.issuer == nil || in.disclosure == nil || in.disclosure.scope == nil {
		return ErrInvalid
	}
	v, d := in.issuer, in.issuer.driver
	if d == nil || d.self != d || d.writer != w || w == nil || w.effective != r ||
		in.disclosure.scope.driver != d || in.disclosure.issuer != w {
		return ErrDisclosureDenied
	}
	if err := d.current(); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	if v.action != scanIssuerBasicSource && v.basicPurpose != 0 {
		return ErrInvalid
	}
	if isScanInventoryHistory(v.action) {
		if in.lifecycle != 0 || (in.input != nil && v.action != scanIssuerN1Apply) || v.create != nil || v.claim != nil || v.lease != nil || (v.work != nil && v.action != scanIssuerN1Apply) {
			return ErrInvalid
		}
	} else if in.lifecycle != InventoryCreate && in.lifecycle != InventoryClaim && (in.lifecycle != InventoryAccept || v.action != scanIssuerInventory) {
		return ErrInvalid
	}
	switch v.action {
	case scanIssuerN1Recover:
		if d.preparation != nil || v.retained != nil || d.operation != nil || d.work != nil {
			return ErrBusy
		}
	case scanIssuerN1History, scanIssuerN1Apply:
		if v.retained == nil || d.preparation != v.retained || v.deadline.IsZero() || v.deadline != v.retained.expected.Deadline {
			return ErrInvalid
		}
		n := v.retained.inventory
		if n == nil || n.closed || n.preparation != v.retained || n.reference != in.request {
			return ErrConflict
		}
		if err := v.retained.current(); err != nil {
			return err
		}
		if v.action == scanIssuerN1Apply && (n.historyOnly || n.command == nil || in.input != n.command || v.work == nil || d.work != v.work || v.work.preparation != v.retained || v.work.original != in.request || v.work.purpose != InventoryAccept) {
			return ErrInvalid
		}
	case scanIssuerSource:
		if v.sourceID == "" || v.create != nil || v.claim != nil || v.work != nil {
			return ErrInvalid
		}
	case scanIssuerRecover:
		if v.create != nil || v.claim != nil || v.work != nil || v.retained != nil {
			return ErrInvalid
		}
	case scanIssuerBasicSource:
		if in.lifecycle != InventoryClaim || !basicSourcePreparationPurpose(v.basicPurpose) || v.sourceID != "" || v.create != nil || v.claim != nil || v.birth != nil || v.work != nil || v.retained != nil {
			return ErrInvalid
		}
		if v.lease == nil || v.lease.self != v.lease || v.lease.driver != d || d.lease != v.lease || v.lease.revoked.Load() {
			return ErrDisclosureDenied
		}
	case scanIssuerInventory:
		if v.lease == nil || v.lease.self != v.lease || v.lease.driver != d || d.lease != v.lease || v.lease.revoked.Load() {
			return ErrDisclosureDenied
		}
	case scanIssuerSnapshot, scanIssuerCurrent, scanIssuerHistory:
		if v.create != nil || v.claim != nil || v.work != nil {
			return ErrInvalid
		}
		if v.action != scanIssuerSnapshot {
			if v.retained == nil || d.preparation != v.retained || v.deadline.IsZero() || v.deadline != v.retained.expected.Deadline {
				return ErrInvalid
			}
			if err := v.retained.current(); err != nil {
				return err
			}
		}
	case scanIssuerCreate:
		if in.lifecycle != InventoryCreate || v.create == nil || !v.create.valid || v.claim != nil ||
			v.birth == nil || d.birth != v.birth || v.birth.command != v.create || v.work == nil || d.work != v.work ||
			v.work.preparation != v.birth.preparation || v.work.original != in.request ||
			v.deadline.IsZero() || v.deadline != v.work.preparation.expected.Deadline {
			return ErrInvalid
		}
		c := v.create
		if in.request != (ScanReceiptRequest{RunID: c.request.Scope.runID, LibraryID: c.request.Scope.libraryID, LibraryKey: c.request.Scope.libraryKey,
			Receipt: ReceiptReference{c.identity, c.digest}}) {
			return ErrInvalid
		}
	case scanIssuerClaim:
		if in.lifecycle != InventoryClaim || v.claim == nil || !v.claim.valid || v.create != nil || v.work == nil ||
			d.work != v.work || v.work.original != in.request || v.deadline.IsZero() || v.deadline != v.work.preparation.expected.Deadline {
			return ErrInvalid
		}
		c := v.claim
		if in.request != (ScanReceiptRequest{RunID: c.request.Original.RunID, LibraryID: c.request.Original.LibraryID, LibraryKey: c.request.Original.LibraryKey,
			Receipt: ReceiptReference{c.identity, c.digest}}) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func checkScanIssuerLibrary(ctx context.Context, turn *commandTurn, q ScanReceiptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[inventoryDisclosureLibrary]
	if !ok {
		return ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, q.LibraryID, q.LibraryKey)
	if err != nil {
		return err
	}
	var id int64
	var key string
	if err = scanSourcePoint(rows, &id, &key); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	if id != q.LibraryID || key != q.LibraryKey {
		return ErrDisclosureDenied
	}
	return nil
}

// Runs only inside runCommand's already-admitted, protected turn. Driver.mu is
// held by the issuer for this whole invocation; no source or lease is recaptured
// from a receipt. Historical classification always precedes fresh eligibility.
func runScanIssuerTurn(ctx context.Context, turn *commandTurn, in *inventoryInvocation, ack bool) (out commandOutput, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Out        commandOutput
			Historical bool
		}{out, historical}, err)
		out, historical, err = v.Out, v.Historical, e
	}()
	v, q := in.issuer, in.request
	if v.action == scanIssuerSource {
		if ack {
			return out, false, ErrInvalid
		}
		out.source, err = captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, v.sourceID)
		if err == nil {
			_, err = v.driver.retainSourcePreparation(ctx, turn, in, out.source, 0)
		}
		return out, true, err
	}
	if v.action == scanIssuerInventory {
		return runInventoryPreparationTurn(ctx, turn, in)
	}
	if v.action == scanIssuerBasicSource {
		if ack {
			return out, false, ErrInvalid
		}
		return runBasicSourcePreparationTurn(ctx, turn, in)
	}
	if err = turn.requireScanLifecycleMemory(ctx, scanLifecycleHistoryBytes); err != nil {
		return out, false, err
	}
	if v.action == scanIssuerSnapshot || v.action == scanIssuerCurrent {
		// This is a current driver snapshot, not historical resolution or an
		// Applied claim. The native floor must settle all preceding commands.
		out.run, err = readCheckedScanRunSnapshot(ctx, turn, q.RunID)
		if err == nil && (out.run.run.scope.commandIncarnation != q.Receipt.Identity.Incarnation || out.run.run.scope.libraryID != q.LibraryID ||
			out.run.run.scope.libraryKey != q.LibraryKey || out.run.run.acknowledgedSequence != out.run.run.lastSequence || q.Receipt.Identity.Sequence > out.run.run.acknowledgedSequence) {
			err = ErrResolveRequired
		}
		if err == nil {
			out.source, err = captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, out.run.run.scope.sourceID)
			if err == nil {
				if !out.source.matches(scanOriginalFromNative(out.run.run.scope)) {
					err = ErrConflict
				} else {
					extra, e := scanSnapshotWorkingBytes(out.run)
					if e != nil {
						return out, false, e
					}
					if v.action == scanIssuerCurrent {
						if v.retained == nil || extra > uint64(v.retained.expected.Need[governor.MemoryBytes]) {
							return out, false, &Refusal{Code: RefusalCapacity}
						}
						if err = checkScanSourceActive(ctx, turn, out.run.run.scope.sourceID, q.RunID); err == nil {
							_, err = turn.commandTimeMS(ctx, InventoryClaim, out.run.run.leaseExpiresMS)
						}
					} else {
						_, err = v.driver.retainSourcePreparation(ctx, turn, in, out.source, extra)
					}
				}
			}
		}
		return out, true, err
	}
	lookup, lookupErr := lookupScanRunLifecycle(ctx, turn, q, in.lifecycle)
	if lookupErr != nil {
		if v.action != scanIssuerCreate || !errors.Is(lookupErr, ErrOwnerUnavailable) || turn.checkLifetimes(ctx) != nil {
			return out, false, lookupErr
		}
		if err = v.driver.checkBirthAbsence(ctx, turn, v.birth); err != nil {
			return out, false, err
		}
		lookup.decision = receiptFresh // this EXACT birth-only absence proof, never a generic miss
	}
	if ack {
		if v.action != scanIssuerHistory {
			return out, false, ErrInvalid
		}
		return out, true, nil // dispatcher invokes the SAME exact ACK helper below
	}

	out.lifecycle = scanLifecycleHistoricalResult(q, lookup)
	if v.action == scanIssuerRecover {
		required, e := scanLifecycleRetainedBytes(q, out.lifecycle)
		if e != nil {
			return out, true, e
		}
		_, err = v.driver.retainIssuerPreparation(ctx, turn, in, nil, required)
		return out, true, err // a missing receipt never acquires fresh authority
	}
	if lookup.decision != receiptFresh || v.action == scanIssuerHistory {
		return out, lookup.decision != receiptFresh, nil
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, false, err
	}
	if v.action == scanIssuerCreate {
		r := v.create.request
		if v.work.preparation.source == nil {
			return out, false, ErrInvalid
		}
		actual, e := captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, r.Source.id)
		if e != nil {
			return out, false, e
		}
		if !sameScanSource(actual, scanSourceFacts{libraryID: r.Scope.libraryID, libraryKey: r.Scope.libraryKey, source: r.Source, policyRevision: r.PolicyRevision, identityRoot: v.work.preparation.source.identityRoot}) {
			return out, false, ErrConflict
		}
		out.lifecycle.Value, err = applyInventoryCreate(ctx, turn, v.create)
	} else if v.action == scanIssuerClaim {
		r := v.claim.request
		actual, e := captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, r.Original.SourceID)
		if e != nil {
			return out, false, e
		}
		if !actual.matches(r.Original) {
			return out, false, ErrConflict
		}
		before := scanClaimSnapshot(v.claim)
		if err = v.driver.excludePredecessor(ctx, turn, before, v.work); err != nil {
			return out, false, err
		}
		mode, expiry, e := v.driver.selectClaimLease(turn, v.claim)
		if e != nil {
			return out, false, e
		}
		v.claimMode, v.expiresMS = mode, expiry
		out.lifecycle.Value, err = applyInventoryClaim(ctx, turn, v.claim, mode, v.driver.leaseOwner, expiry)
	} else {
		return out, false, ErrInvalid
	}
	if err != nil {
		return out, false, err
	}
	// Digest and all echoes come from the verified native result. Physical
	// COMMIT/return in the common dispatcher alone may publish Applied.
	if in.lifecycle == InventoryCreate {
		out.lifecycle.Digest, err = inventoryCreateResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, inventoryCreateResult{out.lifecycle.Value})
	} else {
		out.lifecycle.Digest, err = inventoryClaimResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, inventoryClaimResult{out.lifecycle.Value})
	}
	return out, false, err
}

func sameScanSource(a, b scanSourceFacts) bool {
	return a.libraryID == b.libraryID && a.libraryKey == b.libraryKey && a.policyRevision == b.policyRevision &&
		a.source.id == b.source.id && a.source.incarnation == b.source.incarnation && a.source.rootIdentity == b.source.rootIdentity &&
		a.source.generation == b.source.generation && a.source.followSymlinks == b.source.followSymlinks &&
		bytes.Equal(a.source.resolvedRoot, b.source.resolvedRoot) && bytes.Equal(a.identityRoot, b.identityRoot)
}

func (d *ScanDriver) selectClaimLease(turn *commandTurn, c *inventoryClaimCommand) (scanRunClaimMode, int64, error) {
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || d.leaseDuration <= 0 {
		return 0, 0, ErrInvalid
	}
	ms := deadline.UnixMilli()
	if deadline.After(time.UnixMilli(ms)) {
		ms++
	}
	ttl := d.leaseDuration.Milliseconds()
	if ttl <= 0 || ms > math.MaxInt64-ttl {
		return 0, 0, ErrCap
	}
	expiry := ms + ttl
	old := c.request.Run
	if old.leaseOwner == 0 && old.leaseGeneration == 0 && old.leaseExpiresMS == 0 {
		return scanRunInitialClaim, expiry, nil
	}
	if d.lease != nil && d.lease.driver == d && d.lease.matches(c.request.Original, old) && old.leaseOwner == d.leaseOwner && !deadline.After(time.UnixMilli(old.leaseExpiresMS)) {
		if expiry <= old.leaseExpiresMS {
			return 0, 0, ErrBusy // no synthetic extension beyond the trusted horizon
		}
		return scanRunLiveRenew, expiry, nil
	}
	return scanRunExpiredReclaim, expiry, nil // actual owned time helper refuses another owner's LIVE lease
}

func checkScanSourceActive(ctx context.Context, turn *commandTurn, source, run string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[scanLifecycleActive]
	if !ok {
		return ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, source)
	if err != nil {
		return err
	}
	var actual string
	if err = scanSourcePoint(rows, &actual); err != nil {
		return err
	}
	if actual != run {
		return ErrConflict
	}
	return nil
}
