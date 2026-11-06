package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
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
	CallStarted, CallReturned time.Time
	WallNS                    time.Duration
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

func capture(phase string, purpose writer.CommandID, outcome writer.ReceiptOutcome, result any, err error, start, end time.Time) Observation {
	o := Observation{Phase: phase, CallStarted: start, CallReturned: end, WallNS: end.Sub(start), Evidence: outcome.Evidence, Outcome: &outcome, Result: result}
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

type BrowseInput struct {
	Request governor.Request
	Budget  time.Duration
	Output  read.BrowseOutput
	Input   read.BrowseInput
	// Consume retains output custody through bounded encoding/consumption and
	// retires the output through its owning composition, even on read failure.
	// It executes inside the measured ready interval, after both snapshots.
	Consume func(read.BrowsePage) error
}

// Browse measures preparation/queue, both windows and output consumption against
// the ORIGINAL ready deadline. The protected snapshot ceiling is not a budget.
// The private BrowseCursor is intentionally not treated as a serializable token.
func Browse(reader *read.BrowseReader, prepare func(context.Context, Offer) (BrowseInput, error)) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if reader == nil || prepare == nil {
			return preparationError(emit, errors.New("browse composition unavailable"))
		}
		start := time.Now()
		in, err := prepare(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		if in.Consume == nil {
			return preparationError(emit, errors.New("browse output consumer unavailable"))
		}
		page, evidence, err := reader.Window(ctx, in.Request, in.Budget, in.Output, in.Input)
		err = errors.Join(err, in.Consume(page))
		end := time.Now()
		o := Observation{Phase: "BROWSE", CallStarted: start, CallReturned: end, WallNS: end.Sub(start), Result: struct {
			Items       int
			Exhausted   bool
			Evidence    read.BrowseReadResult
			ReadyBudget time.Duration
			Deadline    time.Time
		}{len(page.Items), page.Exhausted, evidence, in.Budget, in.Request.Deadline}}
		if err != nil {
			o.CallError = err.Error()
		}
		o.RefusalError = evidence.Refusal != nil
		return errors.Join(err, emit(o))
	}
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
		o := Observation{Phase: "PASSIVE", CallStarted: start, CallReturned: end, WallNS: end.Sub(start), Result: result, RefusalError: result.Refusal != nil}
		if err != nil {
			o.CallError = err.Error()
		}
		return errors.Join(err, emit(o))
	}
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
		token := gate.Observation()
		state, err := governor.ReadWAL(path)
		end := time.Now()
		o := Observation{Phase: "WAL", CallStarted: start, CallReturned: end, WallNS: end.Sub(start), Result: struct {
			TokenBefore, TokenAfter uint64
			State                   governor.WALState
			ResetCount              uint64
			LastRestart             governor.WALRestartObservation
		}{token, gate.Observation(), state, gate.ResetCount(), gate.LastRestartObservation()}}
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
