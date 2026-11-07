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
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// The dispatcher accepts only these private concrete envelopes. This is not
// a handler registry, execution callback or caller-selected command purpose.
type wcCommandInvocation interface{ isWCCommandInvocation() }

func (*wcProgressInvocation) isWCCommandInvocation() {}
func (*wcSecurityInvocation) isWCCommandInvocation() {}

type SecurityCommand interface{ isSecurityCommand() }

func (*wcSecurityCommand) isSecurityCommand() {}

type SecurityDisclosure struct{ driver *WCSecurityDriver }
type SecurityResult struct {
	Outcome  ReceiptOutcome
	Value    SecurityValue
	Digest   ResultDigest
	Snapshot SecuritySnapshot
}
type SecuritySnapshot struct {
	Evidence                                  *ExecutionEvidence
	FamilyID                                  string
	Reason, RevokedAt                         sql.NullString
	TeardownDue, LastEvent                    sql.NullInt64
	Last, Acknowledged, Revoked, ControlBytes int64
	source                                    wcSecuritySnapshot
}

// No public constructor exists. Trusted fixture composition supplies the actual
// current ordinary admin family, native birth witnesses and a funded Recovery
// preparation grant from this exact opening's existing governor.
type WCSecurityDriver struct {
	self                *WCSecurityDriver
	mu                  sync.Mutex
	writer              *Writer
	scope               wcProgressScope
	lifetime, principal context.Context
	cancel              context.CancelFunc
	stopOpening         func() bool
	closed              atomic.Bool
	grant               *governor.Grant
	expected            governor.Request
	work                *wcSecurityCommand
	retired             bool
}
type wcSecurityCommand struct {
	preparation SecuritySnapshot
	driver      *WCSecurityDriver
	prepared    wcSecurityPrepared
	reference   SecurityReceiptRequest
	retired     bool
}
type wcSecurityInvocation struct {
	disclosure *SecurityDisclosure
	request    SecurityReceiptRequest
	input      *wcSecurityCommand
	snapshot   bool
	current    wcSecurityCurrentActor
}

func (d *WCSecurityDriver) current() error {
	if d == nil || d.self != d || d.writer == nil || d.writer.effective == nil || d.closed.Load() || d.lifetime == nil || d.lifetime.Err() != nil || d.principal == nil || d.principal.Err() != nil {
		return ErrDisclosureDenied
	}
	r := d.writer.effective
	if r.executor == nil || r.executor.fileBinding == nil {
		return ErrDisclosureDenied
	}
	return errors.Join(r.executor.fileBinding.check(), r.authority.Check(d.grant, d.expected))
}

func newWCSecurityDriver(ctx, principal context.Context, w *Writer, scope wcProgressScope, expected governor.Request, grant *governor.Grant) (out *WCSecurityDriver, err error) {
	var candidate *WCSecurityDriver
	defer func() {
		out, err = finishCommand(ctx, ctx, out, err)
		if out == nil && candidate != nil {
			candidate.closed.Store(true)
			candidate.cancel()
			candidate.stopOpening()
		}
	}()
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if ctx == nil || principal == nil || principal.Err() != nil || w == nil || w.effective == nil || w.effective.executor == nil || w.effective.executor.fileBinding == nil || grant == nil {
		return nil, ErrInvalid
	}
	r := w.effective
	if scope.role != "owner" && scope.role != "admin" || scope.membershipRole != scope.role || scope.primary != 1 || scope.primaryProfile != scope.profileID || len(scope.memberBirth) != 32 || len(scope.profileBirth) != 32 || scope.familyID == "" || scope.tokenHash == "" || scope.accountID == "" || scope.profileID == "" || scope.serverID == "" || scope.epoch <= 0 || scope.tokenGeneration <= 0 || scope.memberRevision <= 0 || scope.profileRevision <= 0 {
		return nil, ErrInvalid
	}
	if expected.Writer || expected.Owner != r.owner || expected.Class != governor.Recovery || expected.Key.Domain != uint16(SecurityAccept) || expected.Key.ID != scope.familyID || len(expected.Footprint) != 1 || expected.Footprint[0] != (governor.Scope{Domain: uint16(SecurityAccept)}) || len(expected.IO) != 0 || expected.Need[governor.MemoryBytes] <= 0 {
		return nil, ErrInvalid
	}
	if err = errors.Join(r.authority.VerifyComposition(r.governor, r.wal), r.authority.Check(grant, expected), r.executor.fileBinding.check()); err != nil {
		return nil, err
	}
	needed := uint64(64 << 10)
	for _, s := range []string{scope.accountID, scope.profileID, scope.memberBirth, scope.profileBirth, scope.familyID, scope.serverID, scope.tokenHash, scope.role, scope.membershipRole, scope.memberLibraries, scope.profileLibraries.String, scope.primaryProfile, scope.horizon, scope.expires} {
		if len(s) > maxCanonicalRequestBytes {
			return nil, ErrCap
		}
		needed += uint64(len(s)) * 32
	}
	if needed > uint64(expected.Need[governor.MemoryBytes]) {
		return nil, ErrCap
	}
	if _, e := time.Parse(time.RFC3339Nano, scope.horizon); e != nil {
		return nil, ErrInvalid
	}
	if _, e := time.Parse(time.RFC3339Nano, scope.expires); e != nil {
		return nil, ErrInvalid
	}
	expected.Key.ID = strings.Clone(expected.Key.ID)
	expected.Footprint = slices.Clone(expected.Footprint)
	d := &WCSecurityDriver{writer: w, scope: scope, principal: principal, grant: grant, expected: expected}
	candidate = d
	d.self = d
	d.lifetime, d.cancel = context.WithDeadline(ctx, expected.Deadline)
	d.stopOpening = context.AfterFunc(r.executor.fileBinding.lifetime, d.cancel)
	if err = d.current(); err != nil {
		d.cancel()
		d.stopOpening()
		return nil, err
	}
	return d, nil
}

func (d *WCSecurityDriver) Close(ctx context.Context) error {
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
		d.work.prepared = wcSecurityPrepared{}
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
func (d *WCSecurityDriver) Disclosure() *SecurityDisclosure {
	if d.current() != nil {
		return nil
	}
	return &SecurityDisclosure{driver: d}
}
func checkWCSecurityDisclosure(w *Writer, v *wcSecurityInvocation) error {
	if w == nil || w.effective == nil || v == nil || v.disclosure == nil || v.disclosure.driver == nil {
		return ErrDisclosureDenied
	}
	d := v.disclosure.driver
	if err := d.current(); err != nil {
		return err
	}
	if d.writer != w || v.request.FamilyID == "" || v.request.FamilyID == d.scope.familyID {
		return ErrDisclosureDenied
	}
	if !v.snapshot && !validWCSecurityRequest(v.request) {
		return ErrInvalid
	}
	return nil
}
func securityReference(c *wcSecurityCommand) (SecurityReceiptRequest, error) {
	if c.retired {
		return c.reference, nil
	}
	digest, err := canonicalRequestDigest(SecurityAccept, 1, c.reference.Receipt.Identity, c.prepared.source)
	if err != nil || c.prepared.request != c.reference || digest != c.reference.Receipt.RequestDigest {
		return SecurityReceiptRequest{}, errors.Join(ErrConflict, err)
	}
	return c.reference, nil
}

// SecurityPreparationSnapshot returns the actual native preparation call;
// the observation supplies no authority or synthetic physical ordinal.
func SecurityPreparationSnapshot(input SecurityCommand) (out SecuritySnapshot, err error) {
	c, ok := input.(*wcSecurityCommand)
	if !ok || c == nil || c.driver == nil {
		return out, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.driver.mu.Unlock()
	if c.retired {
		return out, ErrResolveRequired
	}
	return c.preparation, nil
}

func SecurityReference(input SecurityCommand) (SecurityReceiptRequest, error) {
	c, ok := input.(*wcSecurityCommand)
	if !ok || c == nil || c.driver == nil {
		return SecurityReceiptRequest{}, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return SecurityReceiptRequest{}, ErrBusy
	}
	defer c.driver.mu.Unlock()
	return securityReference(c)
}
func (d *WCSecurityDriver) PrepareRevoke(ctx context.Context, family string) (out SecurityCommand, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.work != nil {
		if d.work.reference.FamilyID == family {
			return d.work, nil
		}
		return nil, ErrResolveRequired
	}
	result, _, err := d.writer.runWCSecurity(ctx, &wcSecurityInvocation{disclosure: d.Disclosure(), request: SecurityReceiptRequest{FamilyID: family}, snapshot: true}, false)
	if err != nil {
		return nil, err
	}
	s := result.Snapshot.source
	if s.revoked != 0 || s.last != s.acknowledged || s.last != 0 || s.incarnation == [16]byte{} {
		return nil, ErrConflict
	}
	id := CommandIdentity{Incarnation: s.incarnation, Sequence: s.last + 1}
	if _, err = rand.Read(id.OperationID[:]); err != nil {
		return nil, err
	}
	source := wcSecuritySource{target: s, issuer: wc008IssuerScope{d.scope.accountID, d.scope.profileID, d.scope.familyID, d.scope.memberBirth, d.scope.profileBirth}}
	digest, err := canonicalRequestDigest(SecurityAccept, 1, id, source)
	if err != nil {
		return nil, err
	}
	q := SecurityReceiptRequest{family, ReceiptReference{id, digest}}
	d.work = &wcSecurityCommand{preparation: result.Snapshot, driver: d, prepared: wcSecurityPrepared{q, source}, reference: q}
	return d.work, nil
}
func (w *Writer) AcceptSecurity(ctx context.Context, cap *SecurityDisclosure, input SecurityCommand) (out SecurityResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	c, ok := input.(*wcSecurityCommand)
	if !ok || c == nil || c.driver == nil {
		return out, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.driver.mu.Unlock()
	q, e := securityReference(c)
	if e != nil {
		return out, e
	}
	if cap == nil || cap.driver != c.driver {
		return out, ErrDisclosureDenied
	}
	out, _, err = w.runWCSecurity(ctx, &wcSecurityInvocation{disclosure: cap, request: q, input: c}, false)
	return
}
func (w *Writer) ResolveSecurity(ctx context.Context, cap *SecurityDisclosure, q SecurityReceiptRequest) (out SecurityResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	if !cap.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer cap.driver.mu.Unlock()
	out, _, err = w.runWCSecurity(ctx, &wcSecurityInvocation{disclosure: cap, request: q}, false)
	return
}
func (w *Writer) AckSecurity(ctx context.Context, cap *SecurityDisclosure, q SecurityReceiptRequest) (out AckResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	d := cap.driver
	if !d.mu.TryLock() {
		return out, ErrBusy
	}
	defer d.mu.Unlock()
	_, out, err = w.runWCSecurity(ctx, &wcSecurityInvocation{disclosure: cap, request: q}, true)
	if err == nil && d.work != nil && d.work.reference == q && out.AcknowledgedThrough >= q.Receipt.Identity.Sequence {
		d.work.prepared = wcSecurityPrepared{}
		d.work.retired = true
		d.work = nil
	}
	return
}
func (w *Writer) CurrentSecurity(ctx context.Context, cap *SecurityDisclosure, family string) (out SecuritySnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	if !cap.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer cap.driver.mu.Unlock()
	r, _, err := w.runWCSecurity(ctx, &wcSecurityInvocation{disclosure: cap, request: SecurityReceiptRequest{FamilyID: family}, snapshot: true}, false)
	return r.Snapshot, err
}
func (w *Writer) runWCSecurity(ctx context.Context, v *wcSecurityInvocation, ack bool) (out SecurityResult, floor AckResult, err error) {
	defer func() {
		r, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{security: out}, ack: floor}, err)
		out, floor, err = r.output.security, r.ack, e
	}()
	if w == nil || w.effective == nil {
		return out, floor, ErrNotImplemented
	}
	r, floor, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, nil, v)
	return r.security, floor, err
}
