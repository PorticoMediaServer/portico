package writer

import (
	"context"
	"database/sql"
	"errors"
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
	inventoryPlan *effectivePlan
	registry      *stmt.Registry
	fenced        bool
	cleanupPanics []terminalPanic
}

func newEffectiveRuntime(w *executor, path string, g governor.Governor, a *governor.Authority, wal *governor.WALGate, owner governor.OwnerID, plan effectivePlan, inventoryPlans ...effectivePlan) (*effectiveRuntime, error) {
	if w == nil || w.db == nil || w.now == nil || path == "" || owner == 0 {
		return nil, ErrInvalid
	}
	if err := a.VerifyComposition(g, wal); err != nil {
		return nil, err
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	plan, err := freezeCommandPlan(w, plan, defs, EffectiveStage)
	if err != nil {
		return nil, err
	}
	if len(inventoryPlans) > 1 {
		return nil, invalid("inventory_plan_count")
	}
	var inventoryPlan *effectivePlan
	if len(inventoryPlans) == 1 {
		inventoryDefs := inventoryHistoryDefinitions()
		frozen, err := freezeCommandPlan(w, inventoryPlans[0], inventoryDefs, InventoryAccept)
		if err != nil {
			return nil, err
		}
		inventoryPlan = &frozen
		defs = append(defs, inventoryDefs...)
	}
	registry, err := stmt.New(defs, plan.registration)
	if err != nil {
		return nil, err
	}
	return &effectiveRuntime{executor: w, governor: g, authority: a, wal: wal, path: strings.Clone(path), owner: owner, plan: plan, inventoryPlan: inventoryPlan, registry: registry}, nil
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

func (r *effectiveRuntime) run(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand, ack bool) (EffectiveStageResult, AckResult, error) {
	out, acknowledged, err := r.runCommand(ctx, w, cap, request, input, ack, nil)
	return out.effective, acknowledged, err
}

type commandOutput struct {
	effective EffectiveStageResult
	inventory InventoryResult
}

func (r *effectiveRuntime) runCommand(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand, ack bool, scan *inventoryInvocation) (out commandOutput, acknowledged AckResult, err error) {
	if ctx == nil {
		return out, acknowledged, ErrInvalid
	}
	if err := r.currentRunDisclosure(w, cap, request, scan); err != nil {
		return out, acknowledged, err
	}
	if err := ctx.Err(); err != nil {
		return out, acknowledged, err
	}
	purpose, plan, reference, key, libraryID := EffectiveStage, &r.plan, request.Receipt, request.ProviderOperationID, request.LibraryID
	outcome := &out.effective.Outcome
	if scan != nil {
		if input != nil || cap != nil || request != (EnrichmentReceiptRequest{}) {
			return out, acknowledged, ErrInvalid
		}
		if r.inventoryPlan == nil {
			return out, acknowledged, ErrNotImplemented
		}
		purpose, plan, reference, key, libraryID = InventoryAccept, r.inventoryPlan, scan.request.Receipt, scan.request.RunID, scan.request.LibraryID
		outcome = &out.inventory.Outcome
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
				outcome.State = ResolveRequired
				outcome.Reference = reference
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
	deadline := now.Add(plan.recipe.limit.Hold)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	class, err := commandClass(purpose)
	if err != nil {
		return out, acknowledged, err
	}
	expected := governor.Request{RestartRisk: risk, Key: governor.JobKey{Domain: uint16(purpose), ID: key}, Owner: r.owner,
		Class: class, Writer: true, Need: plan.need, IO: plan.io, MaxHold: plan.recipe.limit.Hold, Deadline: deadline,
		Budget:    governor.UnitBudget{RawRows: uint32(plan.recipe.limit.Rows), VMOps: plan.recipe.limit.VMOps, Pages: uint32(plan.recipe.limit.Pages)},
		Footprint: []governor.Scope{{Library: libraryID, Domain: uint16(purpose)}}}
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
	custody, err = bindGovernedTurn(purpose, plan.recipe.limit, r.governor, r.authority, r.wal, decision.Grant, expected)
	if err != nil {
		r.fenced = true
		return out, acknowledged, err
	}
	tx, err := r.executor.begin(ctx, purpose, plan.recipe, custody)
	if !errors.Is(err, ErrBusy) {
		transferred = true
	}
	if err != nil {
		if r.executor.fenced || r.executor.ambiguous {
			r.fenced = true
		}
		if !ack && errors.Is(err, ErrResolveRequired) {
			outcome.State = ResolveRequired
			outcome.Reference = reference
		}
		return out, acknowledged, err
	}
	defer tx.rollback(nil) // preserves the PRIMARY panic through frame cleanup
	turn, err := newCommandTurn(tx, r.registry, plan.steps)
	if err == nil {
		if scan != nil {
			_, err = inventoryOwnerFacts(ctx, turn, scan.request.RunID)
			if err == nil {
				err = checkInventoryNativeScope(ctx, turn, scan.request)
			}
		} else {
			err = r.disclose(ctx, turn, cap, request)
		}
	}
	if err == nil {
		err = errors.Join(r.currentRunDisclosure(w, cap, request, scan), ctx.Err())
	}
	var historical bool
	if err == nil && scan != nil {
		var lookup inventoryReceiptLookup
		lookup, err = lookupInventoryAccept(ctx, turn, scan.request)
		if err == nil {
			if ack {
				acknowledged.AcknowledgedThrough, err = acknowledgeInventory(ctx, turn, scan.request, lookup)
			} else {
				out.inventory = inventoryHistoricalResult(scan.request, lookup)
				historical = lookup.decision != receiptFresh
				if lookup.decision == receiptFresh && scan.accept {
					// SC's complete fresh effect body and qualified recipe have
					// not landed. No subset may become Applied.
					err = &Refusal{Code: RefusalUnqualified}
				}
			}
		}
	}
	if err == nil && scan == nil {
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
				outcome.Reference = reference
				switch disposition {
				case receiptRetired:
					outcome.State = ResultRetired
					historical = true
				case receiptReplay:
					outcome.State = Applied
					outcome.Replayed = true
					out.effective.Value = saved
					historical = true
				case receiptFresh:
					outcome.State = Pending
					if input != nil {
						permit := cap.scope.fixture
						if permit == nil || permit.issuer != r || permit.reference != request.Receipt {
							err = &Refusal{Code: RefusalUnqualified}
						} else {
							out.effective.Value, err = applyEffectiveStageBegin(ctx, turn, input)
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
	permissionErr := r.currentRunDisclosure(w, cap, request, scan)
	if errors.Is(permissionErr, ErrDisclosureDenied) {
		out = commandOutput{}
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
	if scan == nil && !ack && !historical && input != nil {
		if physical.state == commitUnknown {
			out.effective.Value = EffectiveStageBeginResult{}
			outcome.State = ResolveRequired
		} else if physical.state == committed {
			if out.effective.Value.ImageID > 0 {
				outcome.State = Applied
			}
		} else {
			out.effective.Value = EffectiveStageBeginResult{}
		}
	}
	if !ack && !historical && !errors.Is(err, ErrDisclosureDenied) && (physical.state == commitUnknown || errors.Is(err, ErrResolveRequired)) {
		outcome.State = ResolveRequired
		outcome.Reference = reference
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
	// Revocation during COMMIT/physical return/WAL sampling must also redact
	// historical data. It never changes the actual physical evidence.
	if permissionErr := r.currentRunDisclosure(w, cap, request, scan); permissionErr != nil {
		out = commandOutput{}
		acknowledged = AckResult{}
		err = errors.Join(err, permissionErr)
	}
	if ack {
		acknowledged.Evidence = evidence
	} else {
		outcome.Evidence = evidence
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
