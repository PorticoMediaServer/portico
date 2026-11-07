package writer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func (*wcReclaimInvocation) isWCCommandInvocation() {}

type ReclaimCommand interface{ isReclaimCommand() }

func (*wcReclaimCommand) isReclaimCommand() {}

// Internal retained-owner capability, not a user session or public minter. It
// can access only the exact native claim born under this owning composition.
type ReclaimDisclosure struct{ driver *WCReclaimDriver }
type ReclaimSnapshot struct {
	Evidence                                                                   *ExecutionEvidence
	Last, Acknowledged, AfterSequence, ReceiptRows, ReceiptBytes, ControlBytes int64
	Due, LastEvent                                                             sql.NullInt64
	SlotPresent                                                                bool
	Slot                                                                       ReclaimValue
	LastRemovedChildPresent                                                    bool
	source                                                                     wcReclaimSnapshot
}
type WCReclaimDriver struct {
	self           *WCReclaimDriver
	mu             sync.Mutex
	commandCustody *wcClaimCommandCustody
	writer         *Writer
	scope          wcProgressScope
	lifetime       context.Context
	cancel         context.CancelFunc
	stopOpening    func() bool
	closed         atomic.Bool
	grant          *governor.Grant
	expected       governor.Request
	work           *wcReclaimCommand
	retired        bool
}
type wcReclaimCommand struct {
	driver      *WCReclaimDriver
	request     wcReclaimRequest
	reference   ReclaimReceiptRequest
	preparation ReclaimSnapshot
	retired     bool
	abandoned   bool
	custody     *wcClaimCommandReservation
}
type wcReclaimInvocation struct {
	disclosure *ReclaimDisclosure
	request    ReclaimReceiptRequest
	input      *wcReclaimCommand
	snapshot   bool
}

func (d *WCReclaimDriver) current() error {
	if d == nil || d.self != d || d.writer == nil || d.writer.effective == nil || d.closed.Load() || d.lifetime == nil || d.lifetime.Err() != nil || d.commandCustody == nil {
		return ErrDisclosureDenied
	}
	r := d.writer.effective
	if r.executor == nil || r.executor.fileBinding == nil {
		return ErrDisclosureDenied
	}
	return errors.Join(r.executor.fileBinding.check(), r.authority.Check(d.grant, d.expected))
}

// Trusted composition transfers an exact already-owned native claim binding;
// a live progress handle alone cannot bootstrap a missing owner. The first
// native Background snapshot must prove the full key before this driver escapes.
// Cleanup lifetime is independent of the original user's session lifetime.
func newWCReclaimDriver(ctx context.Context, w *Writer, owned *WCProgressDriver, expected governor.Request, grant *governor.Grant) (out *WCReclaimDriver, err error) {
	var candidate *WCReclaimDriver
	defer func() {
		out, err = finishCommand(ctx, ctx, out, err)
		if out == nil && candidate != nil {
			candidate.closed.Store(true)
			candidate.cancel()
			candidate.stopOpening()
		}
	}()
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if ctx == nil || w == nil || w.effective == nil || owned == nil || owned.writer != w || grant == nil {
		return nil, ErrInvalid
	}
	if !owned.mu.TryLock() {
		return nil, ErrBusy
	}
	defer owned.mu.Unlock()
	if err = owned.current(); err != nil {
		return nil, err
	}
	r := w.effective
	s := owned.scope
	if !validWCReclaimScope(s) || expected.Writer || expected.Owner != r.owner || expected.Class != governor.Background || expected.Key.Domain != uint16(ProgressReceiptReclaim) || expected.Key.ID != s.playbackID || len(expected.Footprint) != 1 || expected.Footprint[0] != (governor.Scope{Library: s.libraryID, Domain: uint16(ProgressReceiptReclaim)}) || len(expected.IO) != 0 || expected.Need[governor.MemoryBytes] <= 0 {
		return nil, ErrInvalid
	}
	if err = errors.Join(r.authority.VerifyComposition(r.governor, r.wal), r.authority.Check(grant, expected)); err != nil {
		return nil, err
	}
	needed := uint64(64 << 10)
	for _, x := range []string{s.playbackID, s.profileKey, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryKey} {
		if len(x) > maxCanonicalRequestBytes {
			return nil, ErrCap
		}
		needed += uint64(len(x)) * 32
	}
	if needed > uint64(expected.Need[governor.MemoryBytes]) {
		return nil, ErrCap
	}
	// Drop unrelated credential/current-role witnesses. This retained scope is
	// maintenance authority over old children, never an administrative bridge.
	s = wcProgressScope{playbackID: strings.Clone(s.playbackID), profileKey: strings.Clone(s.profileKey), accountID: strings.Clone(s.accountID), profileID: strings.Clone(s.profileID), memberBirth: strings.Clone(s.memberBirth), profileBirth: strings.Clone(s.profileBirth), libraryKey: strings.Clone(s.libraryKey), libraryID: s.libraryID, subjectID: s.subjectID, subjectPublicID: s.subjectPublicID, incarnation: s.incarnation}
	expected.Key.ID = strings.Clone(expected.Key.ID)
	expected.Footprint = slices.Clone(expected.Footprint)
	d := &WCReclaimDriver{writer: w, scope: s, grant: grant, expected: expected, commandCustody: owned.commandCustody}
	candidate = d
	d.self = d
	d.lifetime, d.cancel = context.WithDeadline(ctx, expected.Deadline)
	d.stopOpening = context.AfterFunc(r.executor.fileBinding.lifetime, d.cancel)
	if _, err = w.CurrentReclaim(ctx, d.Disclosure()); err != nil {
		return nil, err
	}
	return d, nil
}
func (d *WCReclaimDriver) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil || d.self != d {
		return ErrInvalid
	}
	d.closed.Store(true)
	d.cancel()
	r := d.writer.effective
	r.governor.Cancel(d.grant)
	if !r.mu.TryLock() {
		return ErrBusy
	}
	defer r.mu.Unlock()
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if d.retired {
		return nil
	}
	if r.executor.db.Stats().InUse != 0 {
		return ErrResolveRequired
	}
	if d.work != nil {
		d.commandCustody.releaseSettled(d.work.custody)
		d.work.request = wcReclaimRequest{}
		d.work.preparation = ReclaimSnapshot{}
		d.work.retired = true
	}
	d.work = nil
	d.scope = wcProgressScope{}
	if err := r.authority.Retired(d.grant); err != nil {
		return err
	}
	d.retired = true
	d.stopOpening()
	return nil
}
func (d *WCReclaimDriver) Disclosure() *ReclaimDisclosure {
	if d.current() != nil {
		return nil
	}
	return &ReclaimDisclosure{d}
}
func checkWCReclaimDisclosure(w *Writer, v *wcReclaimInvocation) error {
	if w == nil || w.effective == nil || v == nil || v.disclosure == nil || v.disclosure.driver == nil {
		return ErrDisclosureDenied
	}
	d := v.disclosure.driver
	if err := d.current(); err != nil {
		return err
	}
	if d.writer != w || !d.commandCustody.matches(d.scope) || v.request.PlaybackID != d.scope.playbackID || !validWCReclaimScope(d.scope) {
		return ErrDisclosureDenied
	}
	if !v.snapshot && (v.request.Receipt.Identity.Incarnation != d.scope.incarnation || v.request.Receipt.Identity.Sequence <= 0 || v.request.Receipt.Identity.OperationID == [16]byte{} || v.request.Receipt.RequestDigest == (RequestDigest{})) {
		return ErrInvalid
	}
	if v.input != nil && !v.input.retired && (v.input != d.work || !d.commandCustody.owns(v.input.custody, ProgressReceiptReclaim)) {
		return ErrResolveRequired
	}
	return nil
}
func reclaimReference(c *wcReclaimCommand) (ReclaimReceiptRequest, error) {
	if c.abandoned {
		return ReclaimReceiptRequest{}, ErrResolveRequired
	}
	if c.retired {
		return c.reference, nil
	}
	digest, err := wcReclaimRequestDigest(ProgressReceiptReclaim, c.reference.Receipt.Identity, c.request)
	if err != nil || digest != c.reference.Receipt.RequestDigest || c.reference.PlaybackID != c.request.Scope.playbackID {
		return ReclaimReceiptRequest{}, errors.Join(ErrConflict, err)
	}
	return c.reference, nil
}
func ReclaimReference(input ReclaimCommand) (ReclaimReceiptRequest, error) {
	c, ok := input.(*wcReclaimCommand)
	if !ok || c == nil || c.driver == nil {
		return ReclaimReceiptRequest{}, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return ReclaimReceiptRequest{}, ErrBusy
	}
	defer c.driver.mu.Unlock()
	return reclaimReference(c)
}
func ReclaimPreparationSnapshot(input ReclaimCommand) (ReclaimSnapshot, error) {
	c, ok := input.(*wcReclaimCommand)
	if !ok || c == nil || c.driver == nil {
		return ReclaimSnapshot{}, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return ReclaimSnapshot{}, ErrBusy
	}
	defer c.driver.mu.Unlock()
	if c.retired {
		return ReclaimSnapshot{}, ErrResolveRequired
	}
	return c.preparation, nil
}
func (d *WCReclaimDriver) Prepare(ctx context.Context) (out ReclaimCommand, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.work != nil {
		return d.work, nil
	}
	reservation, err := d.commandCustody.reserve(ProgressReceiptReclaim, d.lifetime)
	if err != nil {
		return nil, err
	}
	defer func() {
		if d.work == nil {
			d.commandCustody.release(reservation)
		}
	}()
	result, _, err := d.writer.runWCReclaim(ctx, &wcReclaimInvocation{disclosure: d.Disclosure(), request: ReclaimReceiptRequest{PlaybackID: d.scope.playbackID}, snapshot: true}, false)
	if err != nil {
		return nil, err
	}
	s := result.Snapshot
	if !s.Due.Valid {
		return nil, ErrConflict
	}
	if s.Last != s.Acknowledged {
		return nil, ErrResolveRequired
	}
	if s.Last == int64(^uint64(0)>>1) {
		return nil, ErrCap
	}
	id := CommandIdentity{Incarnation: d.scope.incarnation, Sequence: s.Last + 1}
	if _, err = rand.Read(id.OperationID[:]); err != nil {
		return nil, err
	}
	request := wcReclaimRequest{Scope: d.scope, Snapshot: s.source}
	digest, err := wcReclaimRequestDigest(ProgressReceiptReclaim, id, request)
	if err != nil {
		return nil, err
	}
	d.work = &wcReclaimCommand{driver: d, request: request, reference: ReclaimReceiptRequest{d.scope.playbackID, ReceiptReference{id, digest}}, preparation: s, custody: reservation}
	return d.work, nil
}
func (w *Writer) AcceptReclaim(ctx context.Context, cap *ReclaimDisclosure, input ReclaimCommand) (out ReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	c, ok := input.(*wcReclaimCommand)
	if !ok || c == nil || c.driver == nil {
		return out, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.driver.mu.Unlock()
	defer c.driver.abandonUncommittedReclaim(c)
	q, e := reclaimReference(c)
	if e != nil {
		return out, e
	}
	if cap == nil || cap.driver != c.driver {
		return out, ErrDisclosureDenied
	}
	out, _, err = w.runWCReclaim(ctx, &wcReclaimInvocation{disclosure: cap, request: q, input: c}, false)
	return
}

func (d *WCReclaimDriver) abandonUncommittedReclaim(c *wcReclaimCommand) {
	if d.work != c || c.custody == nil || c.custody.state.Load() != wcClaimPrepared {
		return
	}
	d.commandCustody.releaseSettled(c.custody)
	c.request = wcReclaimRequest{}
	c.preparation = ReclaimSnapshot{}
	c.abandoned = true
	d.work = nil
}
func (w *Writer) ResolveReclaim(ctx context.Context, cap *ReclaimDisclosure, q ReclaimReceiptRequest) (out ReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	if !cap.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer cap.driver.mu.Unlock()
	out, _, err = w.runWCReclaim(ctx, &wcReclaimInvocation{disclosure: cap, request: q}, false)
	return
}
func (w *Writer) AckReclaim(ctx context.Context, cap *ReclaimDisclosure, q ReclaimReceiptRequest) (out AckResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	d := cap.driver
	if !d.mu.TryLock() {
		return out, ErrBusy
	}
	defer d.mu.Unlock()
	_, out, err = w.runWCReclaim(ctx, &wcReclaimInvocation{disclosure: cap, request: q}, true)
	if err == nil && d.work != nil && d.work.reference == q && out.AcknowledgedThrough >= q.Receipt.Identity.Sequence {
		reservation := d.work.custody
		d.work.request = wcReclaimRequest{}
		d.work.preparation = ReclaimSnapshot{}
		d.work.retired = true
		d.work = nil
		d.commandCustody.release(reservation)
	}
	return
}
func (w *Writer) CurrentReclaim(ctx context.Context, cap *ReclaimDisclosure) (out ReclaimSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	d := cap.driver
	if !d.mu.TryLock() {
		return out, ErrBusy
	}
	defer d.mu.Unlock()
	r, _, err := w.runWCReclaim(ctx, &wcReclaimInvocation{disclosure: cap, request: ReclaimReceiptRequest{PlaybackID: d.scope.playbackID}, snapshot: true}, false)
	return r.Snapshot, err
}
func (w *Writer) runWCReclaim(ctx context.Context, v *wcReclaimInvocation, ack bool) (out ReclaimResult, floor AckResult, err error) {
	defer func() {
		r, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{reclaim: out}, ack: floor}, err)
		out, floor, err = r.output.reclaim, r.ack, e
	}()
	if w == nil || w.effective == nil {
		return out, floor, ErrNotImplemented
	}
	r, floor, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, nil, v)
	return r.reclaim, floor, err
}
