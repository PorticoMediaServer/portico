package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Observation retains raw values. Durations are computed BEFORE serialization
// discards monotonic components. Missing owner evidence stays nil; facade wall
// time NEVER substitutes for HOLD, outcome or native cleanup proof.
type Observation struct {
	Requested                 *writer.ReceiptReference
	Phase                     string
	Stub                      bool
	Notice                    string `json:",omitempty"`
	CallStarted, CallReturned *time.Time
	WallNS                    *time.Duration
	ReadyNS                   *time.Duration // scheduled offer through consumer return; includes driver lag
	HoldNS, CommitNS          *time.Duration
	Evidence                  *writer.ExecutionEvidence
	Outcome                   *writer.ReceiptOutcome
	Result                    any
	CallError                 string `json:",omitempty"`
	// RefusalError is only an error classification, NOT before-custody proof.
	RefusalError     bool
	EvidenceProblems []string `json:",omitempty"`
}

type InventoryInput struct {
	Command writer.InventoryAcceptCommand
	History writer.ScanReceiptRequest
}
type EffectiveInput struct {
	Command writer.EffectiveStageBeginCommand
	History writer.EnrichmentReceiptRequest
}

// Inventory calls ACCEPT/RESOLVE/ACK with the ORIGINAL identity. Each call is
// emitted immediately, outside custody. No retry/advancement follows an error;
// QL must resolve the original identity using W's recovery composition. A zero
// facade result on error is never treated as rollback or pre-admission refusal.
// Prepare supplies fresh sealed commands/current cursors, never driver authority.
func Inventory(w *writer.Writer, disclosure *writer.ScanDisclosure, prepare func(context.Context, Offer) (InventoryInput, error)) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if w == nil || disclosure == nil || prepare == nil {
			return preparationError(emit, errors.New("inventory composition unavailable"))
		}
		in, err := prepare(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		if in.Command == nil {
			return preparationError(emit, errors.New("missing sealed InventoryAccept command"))
		}
		start := time.Now()
		result, err := w.AcceptInventory(ctx, disclosure, in.Command)
		if outputErr := emit(captureRequested(in.History.Receipt, "ACCEPT", writer.InventoryAccept, result.Outcome, result, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if result.Outcome.Reference != in.History.Receipt || result.Outcome.Replayed || result.Outcome.State != writer.Applied {
			return errors.New("inventory fresh result mismatches original request")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		start = time.Now()
		replay, err := w.ResolveScan(ctx, disclosure, in.History)
		if outputErr := emit(captureRequested(in.History.Receipt, "RESOLVE", writer.InventoryAccept, replay.Outcome, replay, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if replay.Outcome.Reference != in.History.Receipt || !replay.Outcome.Replayed || replay.Outcome.State != writer.Applied || replay.Digest != result.Digest {
			return errors.New("inventory saved result mismatch")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		start = time.Now()
		ack, err := w.AckScan(ctx, disclosure, in.History)
		if outputErr := emit(captureAck(in.History.Receipt, writer.InventoryAccept, ack, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err == nil && ack.AcknowledgedThrough != in.History.Receipt.Identity.Sequence {
			return errors.New("inventory ACK floor mismatch")
		}
		return err
	}
}

func Effective(w *writer.Writer, disclosure *writer.EnrichmentDisclosure, prepare func(context.Context, Offer) (EffectiveInput, error)) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if w == nil || disclosure == nil || prepare == nil {
			return preparationError(emit, errors.New("effective composition unavailable"))
		}
		in, err := prepare(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		if in.Command == nil {
			return preparationError(emit, errors.New("missing sealed EffectiveStage command"))
		}
		start := time.Now()
		result, err := w.StageEffective(ctx, disclosure, in.Command)
		if outputErr := emit(captureRequested(in.History.Receipt, "BEGIN", writer.EffectiveStage, result.Outcome, result, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if result.Outcome.Reference != in.History.Receipt || result.Outcome.Replayed || result.Outcome.State != writer.Applied {
			return errors.New("effective fresh result mismatches original request")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		start = time.Now()
		replay, err := w.ResolveEffective(ctx, disclosure, in.History)
		if outputErr := emit(captureRequested(in.History.Receipt, "RESOLVE", writer.EffectiveStage, replay.Outcome, replay, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if replay.Outcome.Reference != in.History.Receipt || !replay.Outcome.Replayed || replay.Outcome.State != writer.Applied || replay.Value != result.Value {
			return errors.New("effective saved result mismatch")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		start = time.Now()
		ack, err := w.AckEffective(ctx, disclosure, in.History)
		if outputErr := emit(captureAck(in.History.Receipt, writer.EffectiveStage, ack, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err == nil && ack.AcknowledgedThrough != in.History.Receipt.Identity.Sequence {
			return errors.New("effective ACK floor mismatch")
		}
		return err
	}
}

func preparationError(emit func(Observation) error, err error) error {
	return errors.Join(err, emit(Observation{Phase: "PREPARE", CallError: err.Error()}))
}

func captureAck(reference writer.ReceiptReference, purpose writer.CommandID, ack writer.AckResult, err error, start, end time.Time) Observation {
	o := captureRequested(reference, "ACK", purpose, writer.ReceiptOutcome{Evidence: ack.Evidence}, ack, err, start, end)
	o.Outcome = nil // persisted floor, never Applied
	return o
}

func captureRequested(reference writer.ReceiptReference, phase string, purpose writer.CommandID, outcome writer.ReceiptOutcome, result any, err error, start, end time.Time) Observation {
	o := capture(phase, purpose, outcome, result, err, start, end)
	o.Requested = &reference
	return o
}

// observedCall records only an interval actually measured by this driver.
// Stub and preparation-only observations leave all three timing fields nil.
func observedCall(phase string, start, end time.Time) Observation {
	wall := end.Sub(start)
	return Observation{Phase: phase, CallStarted: &start, CallReturned: &end, WallNS: &wall}
}

func capture(phase string, purpose writer.CommandID, outcome writer.ReceiptOutcome, result any, err error, start, end time.Time) Observation {
	o := observedCall(phase, start, end)
	o.Evidence, o.Outcome, o.Result = outcome.Evidence, &outcome, result
	if err != nil {
		o.CallError, o.RefusalError = err.Error(), errors.Is(err, writer.ErrRefused)
	}
	e := o.Evidence
	if e == nil {
		o.EvidenceProblems = append(o.EvidenceProblems, "owner execution evidence unavailable")
		return o
	}
	if e.Purpose != purpose {
		o.EvidenceProblems = append(o.EvidenceProblems, "wrong owner purpose")
	}
	if e.Policy == nil || e.Policy.Engine == "" {
		o.EvidenceProblems = append(o.EvidenceProblems, "actual connection policy unavailable")
	}
	if e.HoldStartedAt.IsZero() || e.PhysicalReturnedAt == nil || !e.PhysicalReturnedAt.After(e.HoldStartedAt) || e.HoldStartedAt.Before(start) || e.PhysicalReturnedAt.After(end) {
		o.EvidenceProblems = append(o.EvidenceProblems, "physical HOLD unavailable or outside facade call")
	} else {
		duration := e.PhysicalReturnedAt.Sub(e.HoldStartedAt)
		o.HoldNS = &duration
	}
	if e.CommitAttemptedAt != nil && e.CommitReturnedAt != nil && !e.CommitAttemptedAt.IsZero() && !e.CommitReturnedAt.Before(*e.CommitAttemptedAt) && !e.CommitAttemptedAt.Before(e.HoldStartedAt) && e.PhysicalReturnedAt != nil && !e.CommitReturnedAt.After(*e.PhysicalReturnedAt) {
		duration := e.CommitReturnedAt.Sub(*e.CommitAttemptedAt)
		o.CommitNS = &duration
	} else if e.Commit != writer.CommitNotAttempted {
		o.EvidenceProblems = append(o.EvidenceProblems, "COMMIT interval unavailable or invalid")
	}
	return o
}

// Passive uses only GV's reviewed governed PASSIVE path. Minimum start spacing,
// actual policy and physical cleanup remain authoritative. Admission refusals
// are retained in raw CheckpointResult, never successful checkpoint samples.
func Passive(cp *governor.Checkpointer, prepare func(context.Context, Offer) (governor.Request, error)) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if cp == nil || prepare == nil {
			return preparationError(emit, errors.New("PASSIVE composition unavailable"))
		}
		request, err := prepare(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		start := time.Now()
		result, err := cp.Step(ctx, request)
		end := time.Now()
		return passiveResult(result, err, start, end, emit)
	}
}

// Step can return a typed admission refusal with nil error. Preserve that raw
// result and native error separately, then fail the action after recording it.
func passiveResult(result governor.CheckpointResult, err error, start, end time.Time, emit func(Observation) error) error {
	o := observedCall("PASSIVE", start, end)
	o.Result, o.RefusalError = result, result.Refusal != nil
	if err != nil {
		o.CallError = err.Error()
	}
	if o.RefusalError {
		err = errors.Join(err, errActionRefused)
	}
	return errors.Join(err, emit(o))
}

// WAL uses GV's read-only observer. Only the owning writer/checkpointer may
// Publish admission samples; this driver NEVER publishes. Counters/events are
// snapshots, not automatically attributed per-call or deduplicated/reset proof.
func WAL(path string, gate *governor.WALGate) Action {
	return func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		if gate == nil || path == "" {
			return preparationError(emit, errors.New("WAL observer unavailable"))
		}
		if err := ctx.Err(); err != nil {
			return preparationError(emit, err)
		}
		start := time.Now()
		result, err := observeWAL(path, gate, governor.ReadWAL, time.Now)
		end := time.Now()
		o := observedCall("WAL", start, end)
		o.Result = result
		if err != nil {
			o.CallError = err.Error()
		}
		return errors.Join(err, emit(o))
	}
}

// StubCadence offers only; NO write, FULL COMMIT, or playback sample. Replace
// with WC's typed adapter once it lands. It always prevents schedule completion.
func StubCadence(reason string) Action {
	return func(_ context.Context, _ Offer, emit func(Observation) error) error {
		return emit(Observation{Phase: "STUB", Stub: true, Notice: reason})
	}
}
