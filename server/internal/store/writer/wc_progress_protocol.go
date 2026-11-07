package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	self         *WCProgressDriver
	mu           sync.Mutex
	writer       *Writer
	scope        wcProgressScope
	lifetime     context.Context
	cancel       context.CancelFunc
	stopOpening  func() bool
	closed       atomic.Bool
	original     wcSnapshot
	observed     time.Time
	intervalFrom int64
	work         *wcProgressCommand
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
	Kind               string
	Source             wc008ProgressCommand
	Original           wcSnapshot
	Position, Duration float64
	StartedPercent     int64
	Completed          bool
	State              string
}
type wcProgressCommand struct {
	driver    *WCProgressDriver
	request   wcProgressPrepared
	reference ProgressReceiptRequest
	pending   *wcProgressPending
}

// The retained capability binds one EXACT native obligation, not a general
// user exception. Only the trusted driver can issue it after native observation.
type wcProgressPending struct {
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
	return r.executor.fileBinding.check()
}

// Private issuance requires an actual existing-file owner. Birth is a separate
// trusted startup operation; neither this issuer nor execute bootstraps a claim.
func newWCProgressDriver(ctx context.Context, w *Writer, s wcProgressScope, original wcSnapshot) (out *WCProgressDriver, err error) {
	var candidate *WCProgressDriver
	defer func() {
		out, err = finishCommand(ctx, ctx, out, err)
		if out == nil && candidate != nil {
			candidate.Close()
		}
	}()
	if ctx == nil || w == nil || w.effective == nil || w.effective.executor == nil || w.effective.executor.fileBinding == nil || s.playbackID == "" || len(s.playbackID) > wcMaxPlaybackIDBytes || s.incarnation == [16]byte{} || s.clock == [16]byte{} || s.familyID == "" || s.tokenHash == "" {
		return nil, ErrInvalid
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
	if !wcProgressLibraryAllowed(s) || s.libraryID <= 0 || s.subjectID <= 0 || len(s.memberBirth) != 32 || len(s.profileBirth) != 32 || s.memberRevision <= 0 || s.profileRevision <= 0 || s.epoch <= 0 || s.tokenGeneration <= 0 {
		return nil, ErrInvalid
	}
	for i := range original {
		original[i].Blob = bytes.Clone(original[i].Blob)
		original[i].Text = strings.Clone(original[i].Text)
	}
	d := &WCProgressDriver{writer: w, scope: s, original: original, observed: w.effective.executor.now()}
	candidate = d
	d.self = d
	d.intervalFrom = d.observed.UnixMilli()
	d.lifetime, d.cancel = context.WithCancel(ctx)
	d.stopOpening = context.AfterFunc(b.lifetime, d.cancel)
	if err = d.current(); err != nil {
		d.cancel()
		d.stopOpening()
		return nil, err
	}
	return d, nil
}
func (d *WCProgressDriver) Close() {
	if d != nil && d.self == d {
		d.closed.Store(true)
		d.cancel()
		d.stopOpening()
	}
}
func (d *WCProgressDriver) Disclosure() *ProgressDisclosure {
	if d.current() != nil {
		return nil
	}
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
	if v.retained != nil && (v.input == nil || v.input.driver != d || v.input.pending != v.retained || v.retained.driver != d || v.request.Kind != "drain_piece" || v.snapshot) {
		return ErrDisclosureDenied
	}
	return nil
}

func ProgressReference(input ProgressCommand) (ProgressReceiptRequest, error) {
	c, ok := input.(*wcProgressCommand)
	if !ok || c == nil || c.driver == nil {
		return ProgressReceiptRequest{}, ErrInvalid
	}
	digest, err := canonicalRequestDigest(ProgressAccept, 1, c.request.Source.identity, c.request)
	if err != nil || digest != c.reference.Receipt.RequestDigest || c.reference.PlaybackID != c.request.Source.proof.playbackID || c.reference.Kind != c.request.Kind || c.reference.Receipt.Identity != c.request.Source.identity {
		return ProgressReceiptRequest{}, ErrConflict
	}
	return c.reference, nil
}

func (w *Writer) AcceptProgress(ctx context.Context, cap *ProgressDisclosure, input ProgressCommand) (out ProgressResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	q, err := ProgressReference(input)
	if err != nil {
		return out, err
	}
	c := input.(*wcProgressCommand)
	if cap == nil || c.driver != cap.driver {
		return out, ErrDisclosureDenied
	}
	return w.runWCProgress(ctx, &wcProgressInvocation{disclosure: cap, request: q, input: c, retained: c.pending}, false)
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
func (d *WCProgressDriver) Capture(ctx context.Context, position, duration float64, percent int64, completed bool, state string) (out ProgressCommand, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if !d.mu.TryLock() {
		return nil, ErrBusy
	}
	defer d.mu.Unlock()
	if d.work != nil {
		if d.work.request.Kind == "capture_report" {
			return d.work, nil
		}
		return nil, ErrResolveRequired
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
	source.proof.reportSequence = snap.ReportSequence + 1
	prepared := wcProgressPrepared{Kind: "capture_report", Source: source, Original: d.original, Position: position, Duration: duration, StartedPercent: percent, Completed: completed, State: state}
	d.work, err = d.seal(prepared, nil)
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
	snap, err := d.writer.CurrentProgress(ctx, d.Disclosure())
	if err != nil {
		return nil, err
	}
	if !snap.Pending || snap.Last != snap.Acknowledged {
		return nil, ErrResolveRequired
	}
	source := snap.source(d.scope)
	pending := &wcProgressPending{driver: d, source: source}
	d.work, err = d.seal(wcProgressPrepared{Kind: "drain_piece", Source: source}, pending)
	return d.work, err
}
func (d *WCProgressDriver) seal(q wcProgressPrepared, pending *wcProgressPending) (*wcProgressCommand, error) {
	if q.Source.last == int64(^uint64(0)>>1) {
		return nil, ErrCap
	}
	q.Source.identity = CommandIdentity{Incarnation: d.scope.incarnation, Sequence: q.Source.last + 1}
	if _, err := rand.Read(q.Source.identity.OperationID[:]); err != nil {
		return nil, err
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
func composeWCProgress(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.wcProgressPlan = nil
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	if ctx == nil || r == nil || r.executor == nil {
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
	receipts := []stmt.Receipt{r.plan.registration, frozen.registration}
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
