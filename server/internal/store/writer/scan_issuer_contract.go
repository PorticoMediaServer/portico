package writer

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Selection is input, never authority. The trusted opening supplies ScanDriver;
// there is no public principal, lease, profile, budget or clock constructor.
type ScanRunInput struct {
	LibraryID                      int64
	LibraryKey, SourceID, ScanMode string
}

// A saved ORIGINAL locator, not a current run/lease snapshot or permission.
type ScanRunReference struct {
	Scan    ScanReceiptRequest
	Purpose CommandID
}

// Snapshot values are owned by the consumer until its result is closed.
type ScanRunSnapshot struct {
	Outcome                                     ReceiptOutcome
	RunID                                       string
	AcceptedRunRevision, AppliedMS              int64
	LeaseOwner, LeaseGeneration, LeaseExpiresMS int64
	AcknowledgedSequence, Discovered, Processed int64
	RootIdentity, RootRevision, RootCursor      string
	RootPages, RootSkipped                      int64
	Digest                                      ResultDigest
}

// Preparation and result share ONE original reservation; no success or error
// can detach retained result buffers from their physical memory owner.
type ScanRunOperation struct {
	self         *ScanRunOperation
	driver       *ScanDriver
	preparation  *scanPreparation
	reference    ScanRunReference
	create       *inventoryCreateCommand
	claim        *inventoryClaimCommand
	result       *ScanRunResult
	last         scanLifecycleCommandResult
	cleanup      *scanRunCleanup
	acknowledged bool
	historyOnly  bool
	closed       bool
}

// Created only after this operation's actual native return and settled result
// have been checked. It carries no command, payload or resource amount. Failed
// joins keep this exact proof and preparation so Close can retry retirement.
type scanRunCleanup struct {
	operation   *ScanRunOperation
	preparation *scanPreparation
	predecessor *scanReturnedWork
}

type ScanRunResult struct {
	self         *ScanRunResult
	operation    *ScanRunOperation
	value        scanLifecycleCommandResult
	read, closed bool
}

// Fixed-size same-opening capability. No native root, source payload, command,
// context from a completed frame or returned result is retained here.
type ScanRunLease struct {
	self                         *ScanRunLease
	driver                       *ScanDriver
	createOperation, incarnation [16]byte
	owner, generation, expiresMS int64
	revoked                      atomic.Bool
}

func (l *ScanRunLease) matches(o scanRunOriginalScope, r inventoryRun) bool {
	return l != nil && l.self == l && !l.revoked.Load() && l.driver != nil && l.driver.self == l.driver &&
		l.driver.lease == l && o.CreateOperationID == l.createOperation && l.owner == r.leaseOwner &&
		l.generation == r.leaseGeneration && l.expiresMS == r.leaseExpiresMS
}

// Issued only after observing actual frame return and settled ACK, while the
// old reservation is still owned. It retains fixed native identity facts only.
type scanReturnedWork struct {
	driver                       *ScanDriver
	createOperation, incarnation [16]byte
	sequence, generation         int64
}

type scanPreparationPlan struct {
	need   governor.Amounts
	io     []governor.DeviceIO
	budget governor.UnitBudget
	ready  time.Duration
}

func (d *ScanDriver) sourceDisclosure(q ScanReceiptRequest) *ScanDisclosure {
	return &ScanDisclosure{issuer: d.writer, scope: &scanScope{runID: q.RunID, libraryID: q.LibraryID, libraryKey: q.LibraryKey,
		incarnation: q.Receipt.Identity.Incarnation, lifetime: d.lifetime, driver: d}}
}

func (w *scanDriverWork) admissionReturned(c *governedTurn) {
	if w == nil || w.submission != scanSubmitting || c == nil || c.state.Load() != 2 {
		return
	}
	w.submission = scanNoGrant // actual acquired holding returned; no native command body ran
}

// SAME protected claim frame verifies both current native state and the bounded
// predecessor proof. A captured lease/elapsed timer is never physical exclusion.
func (d *ScanDriver) excludePredecessor(ctx context.Context, turn *commandTurn, before scanRunSnapshot, current *scanDriverWork) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if d.work != current || current == nil || current.frame != turn.transaction {
		return ErrInvalid
	}
	actual, err := readCheckedScanRunSnapshot(ctx, turn, before.run.scope.runID)
	if err != nil {
		return err
	}
	if actual != before {
		return ErrConflict
	}
	if before.run.lastSequence != before.run.acknowledgedSequence {
		return ErrResolveRequired
	}
	if p := d.predecessor; p != nil {
		if p.driver != d {
			return ErrConflict
		}
		// create_operation_id is the immutable globally UNIQUE run anchor.
		// The one-slot cache says nothing about a different settled run; its
		// complete current-native snapshot and CAS above remain mandatory.
		if p.createOperation == before.run.scope.createOperation &&
			(p.incarnation != before.run.scope.commandIncarnation || p.sequence > before.run.acknowledgedSequence || p.generation != before.run.leaseGeneration) {
			return ErrConflict
		}
	}
	return nil
}
