package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Issued only by W's trusted composition. Scope is independent of the original
// request's source/lease witnesses; revocation applies to historical disclosure.
type enrichmentScope struct {
	ownerID, libraryKey string
	libraryID, entityID int64
	lifetime            context.Context
	revoked             atomic.Bool
	fixture             *effectiveFixturePermit
}

// No production constructor mints this value. A separately reviewed test-only
// harness may supply canonical fixture INPUT, never execution or seal proof.
// The production source/seal gate in applyEffectiveStageBegin remains unchanged.
type effectiveFixturePermit struct {
	issuer    *effectiveRuntime
	reference ReceiptReference
}

type effectivePlan struct {
	registration stmt.Receipt // required Capture proof; zero fails closed
	recipe       turnRecipe
	// Included in the terminal reserve and whole-command IO/WAL forecast, never
	// an assumed built-in 5ms allowance. Qualification belongs to composition.
	headerSync Work
	steps      []statementStep
	need       governor.Amounts
	io         []governor.DeviceIO
}

// No runtime registration, worker budget or callbacks are accepted. The actual
// private composition must own pool/native idle memory and WAL sampling outside
// command custody. No production profile is inferred from these descriptors.
type effectiveRuntime struct {
	mu            sync.Mutex
	executor      *executor
	governor      governor.Governor
	authority     *governor.Authority
	wal           *governor.WALGate
	path          string
	owner         governor.OwnerID
	plan          effectivePlan
	registry      *stmt.Registry
	fenced        bool
	cleanupPanics []terminalPanic
}

func newEffectiveRuntime(w *executor, path string, g governor.Governor, a *governor.Authority, wal *governor.WALGate, owner governor.OwnerID, plan effectivePlan) (*effectiveRuntime, error) {
	if w == nil || w.db == nil || w.now == nil || path == "" || owner == 0 {
		return nil, ErrInvalid
	}
	if err := a.VerifyComposition(g, wal); err != nil {
		return nil, err
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	registry, err := stmt.New(defs, plan.registration)
	if err != nil {
		return nil, err
	}
	// Each ID occurs at most once except the owner (scope, history, handler) and
	// receipt (history, handler). Bound the WHOLE path, including terminal reserve.
	if len(plan.steps) != len(defs) || plan.need[governor.MemoryBytes] <= 0 || plan.need[governor.CPUMilli] <= 0 ||
		plan.recipe.limit.Rows == 0 || plan.recipe.limit.Rows > math.MaxUint32 || plan.recipe.limit.Pages == 0 || plan.recipe.limit.Pages > math.MaxUint32 ||
		plan.need[governor.WALBytes] != int64(plan.recipe.limit.WALBytes) || plan.recipe.limit.WALBytes > math.MaxInt64 {
		return nil, invalid("effective_plan")
	}
	if _, err := NewQuantum(plan.recipe.limit, plan.recipe.terminal, w.now()); err != nil {
		return nil, err
	}
	if plan.headerSync.Hold <= 0 || plan.headerSync.WALBytes < 32 || !fits(plan.headerSync, plan.recipe.terminal) ||
		plan.need[governor.IOBytes] < 32 {
		return nil, invalid("effective_header_sync_forecast")
	}
	seen := make(map[stmt.ID]bool, len(defs))
	remaining := plan.recipe.limit
	for _, work := range []Work{plan.recipe.setup, plan.recipe.terminal} {
		if work.Hold <= 0 || !fits(work, remaining) {
			return nil, ErrQuantum
		}
		remaining = subtract(remaining, work)
	}
	for _, step := range plan.steps {
		if seen[step.id] || step.work.Rows == 0 || step.work.VMOps == 0 || step.work.Hold <= 0 {
			return nil, invalid("effective_step")
		}
		if _, err := registry.Lookup(step.id); err != nil {
			return nil, err
		}
		seen[step.id] = true
		uses := 1
		if step.id == enrichmentEffectiveOwner {
			uses = 3
		}
		if step.id == enrichmentEffectiveReceipt {
			uses = 2
		}
		for range uses {
			if !fits(step.work, remaining) {
				return nil, ErrQuantum
			}
			remaining = subtract(remaining, step.work)
		}
	}
	for _, def := range defs {
		if !seen[def.ID] {
			return nil, invalid("missing_effective_step")
		}
	}
	plan.steps = append([]statementStep(nil), plan.steps...)
	plan.io = append([]governor.DeviceIO(nil), plan.io...)
	for i := range plan.io {
		plan.io[i].DeviceID = strings.Clone(plan.io[i].DeviceID)
	}
	return &effectiveRuntime{executor: w, governor: g, authority: a, wal: wal, path: strings.Clone(path), owner: owner, plan: plan, registry: registry}, nil
}

func (r *effectiveRuntime) checkDisclosure(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := r.currentDisclosure(w, cap, request); err != nil {
		return err
	}
	return ctx.Err()
}

// Current permission is independent of a caller's cancellation/deadline. In
// particular cancellation cannot mask revocation of an already-read receipt.
func (r *effectiveRuntime) currentDisclosure(w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) error {
	if cap == nil || cap.issuer != w || cap.scope == nil || cap.scope.lifetime == nil || cap.scope.revoked.Load() || cap.scope.lifetime.Err() != nil ||
		request.LibraryID <= 0 || request.EntityID <= 0 || request.Receipt.Identity.Sequence <= 0 || request.ProviderOperationID == "" || !utf8.ValidString(request.ProviderOperationID) ||
		len(request.ProviderOperationID) > maxCanonicalRequestBytes || cap.scope.ownerID != request.ProviderOperationID ||
		cap.scope.libraryID != request.LibraryID || cap.scope.entityID != request.EntityID {
		return ErrDisclosureDenied
	}
	return nil
}

func (r *effectiveRuntime) disclose(ctx context.Context, turn *commandTurn, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) error {
	for _, point := range []struct {
		id       stmt.ID
		args     []any
		expected int64
	}{
		{effectiveDisclosureLibrary, []any{request.LibraryID, cap.scope.libraryKey}, request.LibraryID},
		{effectiveDisclosureSubject, []any{request.EntityID, request.LibraryID}, request.EntityID},
	} {
		row, err := turn.queryRow(ctx, turn.steps[point.id].statementStep, point.args...)
		if err != nil {
			return err
		}
		var actual int64
		if err := row.Scan(&actual); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDisclosureDenied
			}
			return err
		}
		if actual != point.expected {
			return ErrDisclosureDenied
		}
	}
	owner, err := effectiveBeginOwnerFacts(ctx, turn, turn.steps[enrichmentEffectiveOwner].statementStep, request.ProviderOperationID)
	if err != nil {
		return err
	}
	if owner.exists && owner.entityID != request.EntityID {
		return ErrDisclosureDenied
	}
	return nil // missing owner goes to the resolver, never an implicit fresh owner
}

func (r *effectiveRuntime) execute(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand) (EffectiveStageResult, error) {
	result, _, err := r.run(ctx, w, cap, request, input, false)
	return result, err
}

func (r *effectiveRuntime) ack(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) (AckResult, error) {
	_, result, err := r.run(ctx, w, cap, request, nil, true)
	return result, err
}

func (r *effectiveRuntime) run(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand, ack bool) (out EffectiveStageResult, acknowledged AckResult, err error) {
	if err := r.checkDisclosure(ctx, w, cap, request); err != nil {
		return out, acknowledged, err
	}
	if !r.mu.TryLock() {
		return out, acknowledged, ErrBusy
	}
	defer r.mu.Unlock()
	defer func() {
		if primary := recover(); primary != nil {
			r.fenced = true
			panic(primary)
		}
	}()
	if r.fenced {
		if r.executor.ambiguous {
			if !ack {
				out.Outcome.State = ResolveRequired
				out.Outcome.Reference = request.Receipt
			}
			return out, acknowledged, ErrResolveRequired
		}
		return out, acknowledged, ErrClosed
	}
	before, err := observeWriterWAL(r.path, r.wal, r.executor.now)
	if err != nil {
		return out, acknowledged, errors.Join(&Refusal{Code: RefusalWAL}, err)
	}
	now := r.executor.now()
	risk, err := r.wal.RestartRisk(now)
	if err != nil {
		return out, acknowledged, errors.Join(&Refusal{Code: RefusalWAL}, err)
	}
	deadline := now.Add(r.plan.recipe.limit.Hold)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	class, err := commandClass(EffectiveStage)
	if err != nil {
		return out, acknowledged, err
	}
	expected := governor.Request{RestartRisk: risk, Key: governor.JobKey{Domain: uint16(EffectiveStage), ID: request.ProviderOperationID}, Owner: r.owner,
		Class: class, Writer: true, Need: r.plan.need, IO: r.plan.io, MaxHold: r.plan.recipe.limit.Hold, Deadline: deadline,
		Budget:    governor.UnitBudget{RawRows: uint32(r.plan.recipe.limit.Rows), VMOps: r.plan.recipe.limit.VMOps, Pages: uint32(r.plan.recipe.limit.Pages)},
		Footprint: []governor.Scope{{Library: request.LibraryID, Domain: uint16(EffectiveStage)}}}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		return out, acknowledged, effectiveAdmissionRefusal(decision.Refusal)
	}
	// Own the acquired grant BEFORE binding (which checks a fallible clock).
	// begin consumes custody except ErrBusy; its panic cleanup uses idempotent
	// governed retirement, so this fallback cannot refund it a second time.
	var custody *governedTurn
	transferred := false
	defer func() {
		if !transferred {
			primary := recover()
			if primary != nil {
				r.fenced = true
			}
			retireErr := r.retireUnused(decision.Grant, custody)
			err = errors.Join(err, retireErr)
			if primary != nil {
				panic(primary)
			}
		}
	}()
	custody, err = bindGovernedTurn(EffectiveStage, r.plan.recipe.limit, r.governor, r.authority, r.wal, decision.Grant, expected)
	if err != nil {
		r.fenced = true
		return out, acknowledged, err
	}
	tx, err := r.executor.begin(ctx, EffectiveStage, r.plan.recipe, custody)
	if !errors.Is(err, ErrBusy) {
		transferred = true
	}
	if err != nil {
		if r.executor.fenced || r.executor.ambiguous {
			r.fenced = true
		}
		if !ack && errors.Is(err, ErrResolveRequired) {
			out.Outcome.State = ResolveRequired
			out.Outcome.Reference = request.Receipt
		}
		return out, acknowledged, err
	}
	defer tx.rollback(nil) // preserves the PRIMARY panic through frame cleanup
	turn, err := newCommandTurn(tx, r.registry, r.plan.steps)
	if err == nil {
		err = r.disclose(ctx, turn, cap, request)
	}
	if err == nil {
		err = r.checkDisclosure(ctx, w, cap, request)
	}
	var historical bool
	if err == nil {
		expectedReceipt := receiptIdentity{request.Receipt.Identity, request.Receipt.RequestDigest}
		var saved EffectiveStageBeginResult
		var owner effectiveBeginOwner
		var disposition receiptDecision
		saved, owner, disposition, err = lookupEffectiveStageBegin(ctx, turn, request.ProviderOperationID, expectedReceipt)
		if disposition == receiptRetired && errors.Is(err, errEffectiveBeginResultRetired) {
			err = nil
		}
		if err == nil {
			if ack {
				acknowledged.AcknowledgedThrough, err = acknowledgeEffective(ctx, turn, request, owner, disposition)
			} else {
				out.Outcome.Reference = request.Receipt
				switch disposition {
				case receiptRetired:
					out.Outcome.State = ResultRetired
					historical = true
				case receiptReplay:
					out.Outcome.State = Applied
					out.Outcome.Replayed = true
					out.Value = saved
					historical = true
				case receiptFresh:
					out.Outcome.State = Pending
					if input != nil {
						permit := cap.scope.fixture
						if permit == nil || permit.issuer != r || permit.reference != request.Receipt {
							err = &Refusal{Code: RefusalUnqualified}
						} else {
							out.Value, err = applyEffectiveStageBegin(ctx, turn, input)
						}
					}
				default:
					err = ErrResolveRequired
				}
			}
		}
	}
	if turn != nil {
		err = errors.Join(err, turn.closeRows())
	}
	// A lifetime revoked while reading cannot disclose the saved result or ACK
	// its floor. Caller cancellation alone does not erase already proven history.
	permissionErr := r.currentDisclosure(w, cap, request)
	if errors.Is(permissionErr, ErrDisclosureDenied) {
		out = EffectiveStageResult{}
		acknowledged = AckResult{}
		historical = false
	}
	err = errors.Join(err, permissionErr, ctx.Err())
	var physical turnResult
	if err == nil {
		physical, err = tx.commit()
	} else {
		physical, err = tx.rollback(err)
	}
	if !ack && !historical && input != nil {
		if physical.state == commitUnknown {
			out.Value = EffectiveStageBeginResult{}
			out.Outcome.State = ResolveRequired
		} else if physical.state == committed {
			if out.Value.ImageID > 0 {
				out.Outcome.State = Applied
			}
		} else {
			out.Value = EffectiveStageBeginResult{}
		}
	}
	if !ack && !historical && !errors.Is(err, ErrDisclosureDenied) && (physical.state == commitUnknown || errors.Is(err, ErrResolveRequired)) {
		out.Outcome.State = ResolveRequired
		out.Outcome.Reference = request.Receipt
	}
	if ack && physical.state != committed {
		acknowledged.AcknowledgedThrough = 0
	}
	evidence := frameEvidence(tx, custody)
	if evidence != nil {
		evidence.WALBefore = before
		if tx.connectionReturned && tx.retirementComplete {
			evidence.WALAfter, err = observeAfterReturn(r, err)
		}
		evidence.ReturnedAt = r.executor.now()
	}
	if r.executor.fenced || r.executor.ambiguous {
		r.fenced = true
	}
	if ack {
		acknowledged.Evidence = evidence
	} else {
		out.Outcome.Evidence = evidence
	}
	return out, acknowledged, err
}

func (r *effectiveRuntime) retireUnused(grant *governor.Grant, c *governedTurn) (err error) {
	defer func() {
		if primary := recover(); primary != nil {
			r.fenced = true
			r.cleanupPanics = append(r.cleanupPanics, terminalPanic{"unused grant retirement", primary})
			err = errTerminalCleanup
			if panicErr, ok := primary.(error); ok {
				err = errors.Join(err, panicErr)
			}
		}
	}()
	if c != nil {
		err = c.retired()
	} else {
		err = r.authority.Retired(grant)
	}
	if err != nil {
		r.fenced = true
	}
	return err
}

func acknowledgeEffective(ctx context.Context, turn *commandTurn, request EnrichmentReceiptRequest, owner effectiveBeginOwner, disposition receiptDecision) (int64, error) {
	if disposition == receiptRetired {
		return owner.acknowledgedSequence, nil
	}
	if disposition != receiptReplay || request.Receipt.Identity.Sequence != owner.lastSequence {
		return 0, ErrResolveRequired
	}
	result, err := turn.exec(ctx, turn.steps[effectiveReceiptAck].statementStep, request.Receipt.Identity.Sequence, request.ProviderOperationID,
		request.Receipt.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, ErrConflict
	}
	return request.Receipt.Identity.Sequence, nil
}

func effectiveAdmissionRefusal(refusal *governor.Refusal) error {
	if refusal == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	code := RefusalCapacity
	switch refusal.Code {
	case governor.Deadline:
		code = RefusalDeadline
	case governor.Unqualified, governor.Invalid:
		code = RefusalUnqualified
	case governor.FailSafe:
		code = RefusalWAL
	}
	out := &Refusal{Code: code}
	if !refusal.RetryAt.IsZero() {
		at := refusal.RetryAt
		out.RetryAt = &at
	}
	return out
}

func observeAfterReturn(r *effectiveRuntime, prior error) (*WALObservation, error) {
	observation, err := observeWriterWAL(r.path, r.wal, r.executor.now)
	return observation, errors.Join(prior, err)
}
