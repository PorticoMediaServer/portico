package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// ProgressCommand is sealed by one live internal driver. No public constructor,
// callback, request digest or caller role can manufacture an executable command.
type ProgressCommand interface{ isProgressCommand() }

func (*wcProgressCommand) isProgressCommand() {}

type ProgressReceiptRequest struct {
	PlaybackID, Kind string
	Receipt          ReceiptReference
}
type ProgressDisclosure struct{ driver *WCProgressDriver }

type wcProgressScope struct {
	primary                                                                       int64
	primaryProfile                                                                string
	membershipRole                                                                string
	horizon, expires                                                              string
	playbackID, profileKey, accountID, profileID, memberBirth, profileBirth       string
	libraryKey, familyID, serverID, tokenHash                                     string
	libraryID, subjectID, epoch, tokenGeneration, memberRevision, profileRevision int64
	role, memberLibraries                                                         string
	profileLibraries                                                              sql.NullString
	subjectPublicID, incarnation, clock                                           [16]byte
}

// WCProgressDriver has no public constructor. Trusted composition binds it to
// an existing native owner and the actual owned opening. A copied handle is dead.
// It is neither session authentication nor a product ingress path.
type WCProgressDriver struct {
	sourceStopped     bool
	policy            wcProgressPolicy
	principalLifetime context.Context
	self              *WCProgressDriver
	mu                sync.Mutex
	writer            *Writer
	scope             wcProgressScope
	lifetime          context.Context
	cancel            context.CancelFunc
	stopOpening       func() bool
	closed            atomic.Bool
	original          wcSnapshot
	observed          time.Time
	intervalFrom      int64
	work              *wcProgressCommand
	pending           atomic.Pointer[wcProgressPending]
	grant             *governor.Grant
	expected          governor.Request
	retired           bool
}

type wcSnapshotCell struct {
	Kind    uint8
	Integer int64
	Text    string
	Blob    []byte
}
type wcSnapshot [41]wcSnapshotCell

// The complete typed source and snapshot participate in the shared request
// codec. No interface-valued snapshot or caller-supplied digest is hashed.
type wcProgressPrepared struct {
	Policy             wcProgressPolicy
	Kind               string
	Source             wc008ProgressCommand
	Original           wcSnapshot
	Position, Duration float64
	StartedPercent     int64
	Completed          bool
	State              string
}
type wcProgressCommand struct {
	preparation ProgressSnapshot
	retired     bool
	driver      *WCProgressDriver
	request     wcProgressPrepared
	reference   ProgressReceiptRequest
	pending     *wcProgressPending
}

// The retained capability binds one EXACT native obligation, not a general
// user exception. Only the trusted driver can issue it after native observation.
type wcProgressPending struct {
	root   *wcProgressPending
	driver *WCProgressDriver
	source wc008ProgressCommand
}
type wcProgressInvocation struct {
	disclosure *ProgressDisclosure
	request    ProgressReceiptRequest
	input      *wcProgressCommand
	snapshot   bool
	birth      *wcProgressBirth
	retained   *wcProgressPending
}

func (d *WCProgressDriver) current() error {
	if d == nil || d.self != d || d.writer == nil || d.writer.effective == nil || d.lifetime == nil || d.closed.Load() {
		return ErrDisclosureDenied
	}
	r := d.writer.effective
	if d.lifetime.Err() != nil || r.executor == nil || r.executor.fileBinding == nil {
		return ErrDisclosureDenied
	}
	return errors.Join(r.executor.fileBinding.check(), r.authority.Check(d.grant, d.expected))
}

// Private issuance requires an actual existing-file owner. Birth is a separate
// trusted startup operation; neither this issuer nor execute bootstraps a claim.
func newWCProgressDriver(ctx, principal context.Context, w *Writer, s wcProgressScope, original wcSnapshot, policy wcProgressPolicy, expected governor.Request, grant *governor.Grant) (out *WCProgressDriver, err error) {
	var candidate *WCProgressDriver
	defer func() {
		out, err = finishCommand(ctx, ctx, out, err)
		if out == nil && candidate != nil {
			candidate.closed.Store(true)
			candidate.cancel()
			candidate.stopOpening()
		}
	}()
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if ctx == nil || principal == nil || principal.Err() != nil || w == nil || w.effective == nil || w.effective.executor == nil || w.effective.executor.fileBinding == nil || s.playbackID == "" || len(s.playbackID) > wcMaxPlaybackIDBytes || s.incarnation == [16]byte{} || s.clock == [16]byte{} || s.familyID == "" || s.tokenHash == "" {
		return nil, ErrInvalid
	}

	r := w.effective
	if grant == nil || expected.Writer || expected.Owner != r.owner || expected.Class != governor.Foreground || expected.Key.Domain != uint16(ProgressAccept) || expected.Key.ID != s.playbackID || len(expected.Footprint) != 1 || expected.Footprint[0] != (governor.Scope{Library: s.libraryID, Domain: uint16(ProgressAccept)}) || expected.Need[governor.MemoryBytes] <= 0 || len(expected.IO) != 0 {
		return nil, ErrInvalid
	}
	if err = r.authority.Check(grant, expected); err != nil {
		return nil, err
	}
	needed := uint64(64 << 10)
	for _, text := range []string{s.playbackID, s.profileKey, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryKey, s.familyID, s.serverID, s.tokenHash, s.role, s.membershipRole, s.memberLibraries, s.profileLibraries.String, s.horizon, s.expires, s.primaryProfile} {
		if uint64(len(text)) > uint64(expected.Need[governor.MemoryBytes])/32 {
			return nil, &Refusal{Code: RefusalCapacity}
		}
		needed += uint64(len(text)) * 32
	}
	for _, cell := range original {
		if uint64(len(cell.Text)+len(cell.Blob)) > uint64(expected.Need[governor.MemoryBytes])/32 {
			return nil, &Refusal{Code: RefusalCapacity}
		}
		needed += uint64(len(cell.Text)+len(cell.Blob)) * 32
	}
	if needed > uint64(expected.Need[governor.MemoryBytes]) {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	b := w.effective.executor.fileBinding
	if err = b.check(); err != nil {
		return nil, err
	}
	if err = w.effective.authority.VerifyComposition(w.effective.governor, w.effective.wal); err != nil {
		return nil, err
	}
	total := 0
	for _, c := range original {
		if c.Kind > 3 || !utf8.ValidString(c.Text) || len(c.Text) > maxCanonicalRequestBytes-total || len(c.Blob) > maxCanonicalRequestBytes-total-len(c.Text) {
			return nil, ErrCap
		}
		total += len(c.Text) + len(c.Blob)
	}
	if _, e := time.Parse(time.RFC3339Nano, s.horizon); e != nil {
		return nil, ErrInvalid
	}
	if _, e := time.Parse(time.RFC3339Nano, s.expires); e != nil {
		return nil, ErrInvalid
	}
	if s.primary < 0 || s.primary > 1 || s.primaryProfile == "" || s.primary == 1 && s.primaryProfile != s.profileID {
		return nil, ErrInvalid
	}
	if !policy.valid() {
		return nil, ErrInvalid
	}
	if !wcProgressLibraryAllowed(s) || s.libraryID <= 0 || s.subjectID <= 0 || len(s.memberBirth) != 32 || len(s.profileBirth) != 32 || s.memberRevision <= 0 || s.profileRevision <= 0 || s.epoch <= 0 || s.tokenGeneration <= 0 {
		return nil, ErrInvalid
	}
	for i := range original {
		original[i].Blob = bytes.Clone(original[i].Blob)
		original[i].Text = strings.Clone(original[i].Text)
	}
	expected.Key.ID = strings.Clone(expected.Key.ID)
	expected.Footprint = slices.Clone(expected.Footprint)
	d := &WCProgressDriver{writer: w, scope: s, original: original, observed: w.effective.executor.now(), grant: grant, expected: expected, principalLifetime: principal, policy: policy}
	candidate = d
	d.self = d
	d.intervalFrom = d.observed.UnixMilli()
	d.lifetime, d.cancel = context.WithDeadline(ctx, expected.Deadline)
	d.stopOpening = context.AfterFunc(b.lifetime, d.cancel)
	if err = d.current(); err != nil {
		d.cancel()
		d.stopOpening()
		return nil, err
	}
	return d, nil
}

// Close revokes before joining. Busy frames keep the original preparation
// grant and payload. There is no timeout or floor-based refund.
func (d *WCProgressDriver) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil || d.self != d {
		return ErrInvalid
	}
	d.closed.Store(true)
	d.cancel()
	d.writer.effective.governor.Cancel(d.grant)
	r := d.writer.effective
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
		d.work.request = wcProgressPrepared{}
		d.work.pending = nil
	}
	d.work = nil
	d.pending.Store(nil)
	d.original = wcSnapshot{}
	d.scope = wcProgressScope{}
	if err := r.authority.Retired(d.grant); err != nil {
		return err
	}
	d.retired = true
	d.stopOpening()
	return nil
}
func (d *WCProgressDriver) Disclosure() *ProgressDisclosure {
	if d.current() != nil || d.principalLifetime == nil || d.principalLifetime.Err() != nil {
		return nil
	}
	return &ProgressDisclosure{driver: d}
}

func (d *WCProgressDriver) retainedDisclosure() *ProgressDisclosure {
	return &ProgressDisclosure{driver: d}
}

func checkWCProgressDisclosure(w *Writer, v *wcProgressInvocation) error {
	if v == nil || w == nil || w.effective == nil || v.disclosure == nil {
		return ErrDisclosureDenied
	}
	d := v.disclosure.driver
	if err := d.current(); err != nil {
		return err
	}
	if d.writer != w || v.request.PlaybackID != d.scope.playbackID || v.request.Receipt.Identity.Incarnation != d.scope.incarnation || v.request.Receipt.Identity.Sequence <= 0 || (v.request.Kind != "capture_report" && v.request.Kind != "drain_piece") {
		return ErrDisclosureDenied
	}
	if v.retained == nil && (d.principalLifetime == nil || d.principalLifetime.Err() != nil) {
		return ErrDisclosureDenied
	}
	if v.retained != nil && (v.retained.driver != d || v.retained.root == nil || v.retained.root != d.pending.Load() || v.request.Kind != "drain_piece" || (!v.snapshot && (v.input == nil || v.input.driver != d || v.input.pending != v.retained))) {
		return ErrDisclosureDenied
	}
	return nil
}

func ProgressReference(input ProgressCommand) (ProgressReceiptRequest, error) {
	c, ok := input.(*wcProgressCommand)
	if !ok || c == nil || c.driver == nil {
		return ProgressReceiptRequest{}, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return ProgressReceiptRequest{}, ErrBusy
	}
	defer c.driver.mu.Unlock()
	return progressReference(c)
}
func progressReference(c *wcProgressCommand) (ProgressReceiptRequest, error) {
	if c.retired {
		return c.reference, nil
	}
	digest, err := canonicalRequestDigest(ProgressAccept, 1, c.request.Source.identity, c.request)
	if err != nil || digest != c.reference.Receipt.RequestDigest || c.reference.PlaybackID != c.request.Source.proof.playbackID || c.reference.Kind != c.request.Kind || c.reference.Receipt.Identity != c.request.Source.identity {
		return ProgressReceiptRequest{}, ErrConflict
	}
	return c.reference, nil
}

// ProgressPreparationSnapshot exposes only the actual preparation call's native
// snapshot/evidence. It cannot mint a command, grant, floor or physical ordinal.
func ProgressPreparationSnapshot(input ProgressCommand) (ProgressSnapshot, error) {
	c, ok := input.(*wcProgressCommand)
	if !ok || c == nil || c.driver == nil {
		return ProgressSnapshot{}, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return ProgressSnapshot{}, ErrBusy
	}
	defer c.driver.mu.Unlock()
	if c.retired {
		return ProgressSnapshot{}, ErrResolveRequired
	}
	return c.preparation, nil
}

func (w *Writer) AcceptProgress(ctx context.Context, cap *ProgressDisclosure, input ProgressCommand) (out ProgressResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	c, ok := input.(*wcProgressCommand)
	if !ok || c == nil || c.driver == nil {
		return out, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.driver.mu.Unlock()
	q, err := progressReference(c)
	if err != nil {
		return out, err
	}
	if cap == nil || c.driver != cap.driver || c.pending != nil || q.Kind != "capture_report" {
		return out, ErrDisclosureDenied
	}
	return w.runWCProgress(ctx, &wcProgressInvocation{disclosure: cap, request: q, input: c}, false)
}

// ExecuteDrain is a driver operation, never a revoked user disclosure exception.
// The opaque retained capability must have been issued for this exact pending
// obligation. Public Resolve/ACK still use Current scope independently.
func (d *WCProgressDriver) ExecuteDrain(ctx context.Context, input ProgressCommand) (out ProgressResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	c, ok := input.(*wcProgressCommand)
	if !ok || c == nil || c.driver == nil {
		return out, ErrInvalid
	}
	if !c.driver.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.driver.mu.Unlock()
	q, err := progressReference(c)
	if err != nil {
		return out, err
	}
	if err = d.current(); err != nil {
		return out, err
	}
	if c.driver != d || c.pending == nil || q.Kind != "drain_piece" {
		return out, ErrDisclosureDenied
	}
	return d.writer.runWCProgress(ctx, &wcProgressInvocation{disclosure: d.retainedDisclosure(), request: q, input: c, retained: c.pending}, false)
}

func (w *Writer) ResolveProgress(ctx context.Context, cap *ProgressDisclosure, q ProgressReceiptRequest) (out ProgressResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	return w.runWCProgress(ctx, &wcProgressInvocation{disclosure: cap, request: q}, false)
}
func (w *Writer) AckProgress(ctx context.Context, cap *ProgressDisclosure, q ProgressReceiptRequest) (out AckResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if w == nil || w.effective == nil {
		return out, ErrNotImplemented
	}
	if err = checkWCProgressDisclosure(w, &wcProgressInvocation{disclosure: cap, request: q}); err != nil {
		return out, err
	}
	d := cap.driver
	if !d.mu.TryLock() {
		return out, ErrBusy
	}
	defer d.mu.Unlock()
	_, out, err = w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, true, nil, &wcProgressInvocation{disclosure: cap, request: q})
	if err == nil && d.work != nil && d.work.reference == q && out.AcknowledgedThrough >= q.Receipt.Identity.Sequence {
		if q.Kind == "capture_report" {
			d.intervalFrom = d.work.request.Source.untilMS
		}
		d.work.request = wcProgressPrepared{}
		d.work.pending = nil
		d.work.retired = true
		d.work = nil
	}
	return out, err
}
func (w *Writer) runWCProgress(ctx context.Context, v *wcProgressInvocation, ack bool) (out ProgressResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if w == nil || w.effective == nil {
		return out, ErrNotImplemented
	}
	result, _, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, nil, v)
	return result.progress, err
}

// Scope is rechecked natively in the protected turn. Snapshot is current data,
// never a saved result and never an ACK or replacement pending interval.
func (w *Writer) CurrentProgress(ctx context.Context, cap *ProgressDisclosure) (out ProgressSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if cap == nil || cap.driver == nil {
		return out, ErrDisclosureDenied
	}
	d := cap.driver
	q := ProgressReceiptRequest{PlaybackID: d.scope.playbackID, Kind: "capture_report", Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: d.scope.incarnation, Sequence: 1}}}
	result, err := w.runWCProgress(ctx, &wcProgressInvocation{disclosure: cap, request: q, snapshot: true}, false)
	return result.Snapshot, err
}

// One report interval is measured with the driver's real owned clock. It is
// frozen once; retry uses the SAME returned command/reference. No catch-up loop.
func (d *WCProgressDriver) Capture(ctx context.Context, reportSequence int64, position, duration float64, state string) (out ProgressCommand, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.work != nil {
		if d.work.request.Kind == "capture_report" && d.work.request.Source.proof.reportSequence == reportSequence && d.work.request.Position == position && d.work.request.Duration == duration && d.work.request.State == state {
			return d.work, nil
		}
		return nil, ErrResolveRequired
	}
	if d.sourceStopped {
		return nil, ErrInvalid
	}
	snap, err := d.writer.CurrentProgress(ctx, d.Disclosure())
	if err != nil {
		return nil, err
	}
	if snap.Pending || snap.Last != snap.Acknowledged {
		return nil, ErrResolveRequired
	}
	now := d.writer.effective.executor.now()
	if err = observeTime(&d.observed, now); err != nil {
		return nil, err
	}
	source := snap.source(d.scope)
	source.fromMS, source.cursorMS, source.untilMS = d.intervalFrom, d.intervalFrom, now.UnixMilli()
	if source.untilMS <= source.fromMS {
		return nil, ErrBusy
	}
	if reportSequence < 0 || reportSequence > 9007199254740991 || reportSequence <= snap.LastAcceptedSequence {
		return nil, ErrConflict
	}
	source.proof.reportSequence = reportSequence
	completed, e := d.policy.completion(d.original[9].Integer, position, duration, state)
	if e != nil {
		return nil, e
	}
	prepared := wcProgressPrepared{Policy: d.policy, Kind: "capture_report", Source: source, Original: d.original, Position: position, Duration: duration, StartedPercent: d.policy.StartedPercent, Completed: completed, State: state}
	d.work, err = d.seal(prepared, nil)
	if err == nil {
		d.work.preparation = snap
	}
	if err == nil && state != "playing" {
		d.sourceStopped = true
	}
	return d.work, err
}
func (d *WCProgressDriver) Drain(ctx context.Context) (out ProgressCommand, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.work != nil {
		if d.work.request.Kind == "drain_piece" {
			return d.work, nil
		}
		return nil, ErrResolveRequired
	}
	var snap ProgressSnapshot
	var result ProgressResult
	root := d.pending.Load()
	if root == nil {
		snap, err = d.writer.CurrentProgress(ctx, d.Disclosure())
		if err == nil && snap.Pending {
			root = d.retainPending(snap.source(d.scope))
		}
	} else {
		q := ProgressReceiptRequest{PlaybackID: d.scope.playbackID, Kind: "drain_piece", Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: d.scope.incarnation, Sequence: 1}}}
		result, err = d.writer.runWCProgress(ctx, &wcProgressInvocation{disclosure: d.retainedDisclosure(), request: q, snapshot: true, retained: root}, false)
		snap = result.Snapshot
	}
	if err != nil {
		return nil, err
	}
	if !snap.Pending || snap.Last != snap.Acknowledged {
		return nil, ErrResolveRequired
	}
	source := snap.source(d.scope)
	pending := &wcProgressPending{driver: d, source: source, root: root}
	d.work, err = d.seal(wcProgressPrepared{Kind: "drain_piece", Source: source}, pending)
	if err == nil {
		d.work.preparation = snap
	}
	return d.work, err
}
func (d *WCProgressDriver) retainPending(source wc008ProgressCommand) *wcProgressPending {
	p := &wcProgressPending{driver: d, source: source}
	p.root = p
	d.pending.Store(p)
	return p
}
func wcPendingMatches(p *wcProgressPending, s ProgressSnapshot) bool {
	if p == nil || !s.Pending {
		return false
	}
	q := p.root.source
	return q.proof.clockIncarnation == s.Clock && q.proof.occurrenceGeneration == s.Occurrence && q.proof.reportSequence == s.ReportSequence && q.proof.activityGeneration == s.Activity && q.proof.rememberGeneration == s.Policy && q.startEventID == s.StartEvent.Int64 && s.From.Valid && s.Until.Valid && s.Cursor.Valid && q.fromMS == s.From.Int64 && q.untilMS == s.Until.Int64 && s.Cursor.Int64 >= q.fromMS && s.Cursor.Int64 < q.untilMS
}

// Called ONLY after the shared physical terminal proves the native commit.
func recordWCProgressCompletion(v *wcProgressInvocation, out ProgressResult) {
	if v == nil || v.input == nil || v.request.Kind != "capture_report" || out.Value.CommandKind != "capture_report" {
		return
	}
	source := v.input.request.Source
	source.startEventID = out.Value.ResultStartEventId.Int64
	source.lastAcceptedSequence = out.Value.ResultReportSequence
	v.disclosure.driver.retainPending(source)
}

func (d *WCProgressDriver) seal(q wcProgressPrepared, pending *wcProgressPending) (*wcProgressCommand, error) {
	if q.Source.last == int64(^uint64(0)>>1) {
		return nil, ErrCap
	}
	q.Source.identity = CommandIdentity{Incarnation: d.scope.incarnation, Sequence: q.Source.last + 1}
	if _, err := rand.Read(q.Source.identity.OperationID[:]); err != nil {
		return nil, err
	}
	if pending != nil {
		pending.source = q.Source
	}
	digest, err := canonicalRequestDigest(ProgressAccept, 1, q.Source.identity, q)
	if err != nil {
		return nil, err
	}
	c := &wcProgressCommand{driver: d, request: q, pending: pending, reference: ProgressReceiptRequest{PlaybackID: d.scope.playbackID, Kind: q.Kind, Receipt: ReceiptReference{q.Source.identity, digest}}}
	return c, nil
}

// Keep scalar native NULL state explicit; an absent pending cursor is not zero.
type ProgressSnapshot struct {
	Evidence                                                                                                *ExecutionEvidence
	LastEvent                                                                                               sql.NullInt64
	Last, Acknowledged, AcceptedOrdinal, LastAcceptedSequence, ReportSequence, Occurrence, Activity, Policy int64
	StartEvent                                                                                              sql.NullInt64
	Pending                                                                                                 bool
	From, Until, Cursor                                                                                     sql.NullInt64
	Clock                                                                                                   [16]byte
	ReceiptRows, ReceiptBytes, AfterSequence, ControlBytes                                                  int64
	Due                                                                                                     sql.NullInt64
	Viewer, Observation, History, Personal, PlayedMS                                                        int64
}

func (s ProgressSnapshot) source(scope wcProgressScope) wc008ProgressCommand {
	return wc008ProgressCommand{last: s.Last, acknowledged: s.Acknowledged, resultVersion: 1, oldControlBytes: s.ControlBytes, lastAcceptedSequence: s.LastAcceptedSequence,
		authority: "local", acceptedOrdinal: s.AcceptedOrdinal, receiptRows: s.ReceiptRows, receiptVariableBytes: s.ReceiptBytes,
		profileKey: scope.profileKey, accountKey: scope.accountID, profileID: scope.profileID, libraryKey: scope.libraryKey, memberIncarnation: scope.memberBirth, profileLifetime: scope.profileBirth,
		libraryID: scope.libraryID, subjectID: scope.subjectID, subjectPublicID: scope.subjectPublicID,
		proof: wcPlayingProof{scope.playbackID, s.Occurrence, s.ReportSequence, s.Activity, s.Policy, scope.clock}, fromMS: s.From.Int64, untilMS: s.Until.Int64, cursorMS: s.Cursor.Int64, startEventID: s.StartEvent.Int64}
}

// Compose only a complete, explicitly forecast, captured finite definition set.
// No default profile is installed and no product constructor calls this helper.
func composeWCProgress(ctx context.Context, r *effectiveRuntime, p effectivePlan, birthProfiles ...effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.wcProgressPlan = nil
			r.wcProgressBirthPlan = nil
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil || len(birthProfiles) > 1 {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	runtimeLocked = true
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.wcProgressPlan != nil {
		return ErrBusy
	}
	defs := wcProgressCommandDefinitions()
	frozen, err := freezeCommandPlan(r.executor, p, defs, ProgressAccept)
	if err != nil {
		return err
	}
	if frozen.wcProgressMemory == (commandMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	all := []stmt.Definition{}
	for _, e := range r.registry.Manifest() {
		all = append(all, e.Definition)
	}
	all = append(all, defs...)
	var birth *effectivePlan
	if len(birthProfiles) == 1 {
		frozenBirth, e := freezeCommandPlan(r.executor, birthProfiles[0], wcProgressBirthDefinitions(), ProgressAccept)
		if e != nil {
			return e
		}
		if frozenBirth.wcProgressMemory == (commandMemoryBudget{}) {
			return &Refusal{Code: RefusalUnqualified}
		}
		birth = &frozenBirth
		all = append(all, wcProgressBirthDefinitions()...)
	}
	receipts := []stmt.Receipt{r.plan.registration, frozen.registration}
	if birth != nil {
		receipts = append(receipts, birth.registration)
	}
	if r.inventoryPlan != nil {
		receipts = append(receipts, r.inventoryPlan.registration)
	}
	for _, p := range r.lifecyclePlans {
		if p != nil {
			receipts = append(receipts, p.registration)
		}
	}
	registry, err := stmt.New(all, receipts...)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry = r.registry
	r.registry, r.wcProgressPlan = registry, &frozen
	r.wcProgressBirthPlan = birth
	installed = true
	return nil
}

func wcProgressStatementMaxUses(id stmt.ID) int {
	// Report count rollups and a drain's three paired buckets share definitions.
	switch id {
	case wcAPIEventInsert, wcAPIEventOldest, wcAPIEventDelete:
		return 3
	case wc008ProgressItemBucketRead, wc008ProgressItemBucketUpdate, wc008ProgressItemBucket, wc008ProgressProfileBucketRead, wc008ProgressProfileBucketUpdate, wc008ProgressProfileBucket:
		return 3
	case wc008HistoryAppend:
		return 2
	default:
		return 1
	}
}
