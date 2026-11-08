package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const MPQueueRepeat CommandID = 754

// No public constructor. Trusted composition binds current ordinary native
// authentication to this exact writer and device-owned queue. Copies are dead.
// Historical UO root references are never disclosure or worker-sequence proof.
type mpWorkerDisclosure struct {
	self          *mpWorkerDisclosure
	writer        *Writer
	lifetime      context.Context
	revoked       atomic.Bool
	scope         wcProgressScope
	queue, device string
	incarnation   [16]byte
}

type mpQueueRepeatRequest struct {
	Queue, Account, Profile, Device   string
	ExpectedRevision, ExpectedCustody int64
	Repeat, StopRequest               string
}

type mpRepeatCommand struct {
	input     mpQueueRepeatRequest
	reference ReceiptReference
}
type mpRepeatInvocation struct {
	disclosure *mpWorkerDisclosure
	queue      string
	reference  ReceiptReference
	input      *mpRepeatCommand
}

func (*mpRepeatInvocation) isWCCommandInvocation() {}

func newMPRepeatCommand(id CommandIdentity, q mpQueueRepeatRequest) (*mpRepeatCommand, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	if id.Incarnation == [16]byte{} || id.OperationID == [16]byte{} || id.Sequence <= 0 {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(MPQueueRepeat, 1, id, q)
	if err != nil {
		return nil, err
	}
	q.Queue, q.Account, q.Profile, q.Device, q.Repeat, q.StopRequest = strings.Clone(q.Queue), strings.Clone(q.Account), strings.Clone(q.Profile), strings.Clone(q.Device), strings.Clone(q.Repeat), strings.Clone(q.StopRequest)
	return &mpRepeatCommand{q, ReceiptReference{id, digest}}, nil
}
func (q mpQueueRepeatRequest) validate() error {
	for _, s := range []string{q.Queue, q.Account, q.Profile, q.Device, q.StopRequest} {
		if !mpWorkerID(s) {
			return ErrInvalid
		}
	}
	if q.ExpectedRevision <= 0 || q.ExpectedRevision == 9223372036854775807 || q.ExpectedCustody < 0 || (q.Repeat != "off" && q.Repeat != "one" && q.Repeat != "all") {
		return ErrInvalid
	}
	return nil
}
func mpWorkerID(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// Scope/bind copies, bounded scalar workspace and payload all share the ONE
// admitted partition. Lengths are measured, never columns times maximum width.
func mpWorkerWorkingBytes(v *mpRepeatInvocation, payload uint64) uint64 {
	s := v.disclosure.scope
	n := uint64(64<<10) + payload*32
	for _, x := range []string{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.memberLibraries, s.profileLibraries.String, s.familyID, s.serverID, s.tokenHash, s.horizon, s.expires, v.queue, v.disclosure.device} {
		n += uint64(len(x)) * 32
	}
	if v.input != nil {
		q := v.input.input
		for _, x := range []string{q.Queue, q.Account, q.Profile, q.Device, q.Repeat, q.StopRequest} {
			n += uint64(len(x)) * 32
		}
	}
	return n
}

func checkMPWorkerDisclosure(w *Writer, v *mpRepeatInvocation) error {
	if v == nil || v.disclosure == nil {
		return ErrDisclosureDenied
	}
	c := v.disclosure
	if c.self != c || c.writer != w || w == nil || w.effective == nil || c.lifetime == nil || c.lifetime.Err() != nil || c.revoked.Load() || c.queue != v.queue || c.incarnation != v.reference.Identity.Incarnation {
		return ErrDisclosureDenied
	}
	if !mpWorkerID(c.queue) || !mpWorkerID(c.device) || c.scope.accountID == "" || c.scope.profileID == "" || v.reference.Identity.Sequence <= 0 {
		return ErrDisclosureDenied
	}
	if v.input != nil {
		q := v.input.input
		if q.Queue != c.queue || q.Account != c.scope.accountID || q.Profile != c.scope.profileID || q.Device != c.device || v.input.reference != v.reference {
			return ErrDisclosureDenied
		}
	}
	return nil
}

func (w *Writer) runMPRepeat(ctx context.Context, c *mpWorkerDisclosure, queue string, reference ReceiptReference, input *mpRepeatCommand) (out mpWorkerResult, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if w == nil || w.effective == nil {
		return out, ErrInvalid
	}
	v := &mpRepeatInvocation{c, queue, reference, input}
	if input != nil {
		rebuilt, e := newMPRepeatCommand(reference.Identity, input.input)
		if e != nil || rebuilt.reference != reference {
			return out, errors.Join(ErrConflict, e)
		}
	}
	result, _, err := w.effective.dispatchCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, nil, v)
	return result.mpRepeat, err
}

// One-time trusted profile installation into the existing registry and private
// executor. Complete same-schema Capture covers the existing union as well as
// this purpose. No default budget, second executor, callbacks or generic API.
func composeMPRepeat(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil || r.registry == nil || r.governor == nil || r.authority == nil || r.wal == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	defer r.mu.Unlock()
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	defer r.executor.mu.Unlock()
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.mpRepeatPlan != nil {
		return ErrBusy
	}
	defs := mpWorkerDefinitions()
	frozen, err := freezeCommandPlan(r.executor, p, defs, MPQueueRepeat)
	if err != nil {
		return err
	}
	if frozen.mpWorkerMemory == (commandMemoryBudget{}) || !frozen.mpWorkerLimits.valid() {
		return &Refusal{Code: RefusalUnqualified}
	}
	all := []stmt.Definition{}
	for _, e := range r.registry.Manifest() {
		all = append(all, e.Definition)
	}
	for _, d := range defs {
		found := false
		for _, old := range all {
			if old.ID == d.ID {
				if !reflect.DeepEqual(old, d) {
					return ErrInvalid
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	registry, err := stmt.New(all, p.registration)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	old := r.registry
	r.registry = registry
	r.mpRepeatPlan = &frozen
	if _, err = finishCommand(ctx, ctx, struct{}{}, nil); err != nil {
		r.registry = old
		r.mpRepeatPlan = nil
	}
	return err
}
