package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	headerSync          Work
	steps               []statementStep
	need                governor.Amounts
	io                  []governor.DeviceIO
	inventoryMemory     inventoryMemoryBudget // private whole-unit partition; zero is unqualified
	searchImageMemory   searchImageMemoryBudget
	scanLifecycleMemory commandMemoryBudget
	wcProgressMemory    commandMemoryBudget
	wcSecurityMemory    commandMemoryBudget
}

// No runtime registration, worker budget or callbacks are accepted. The actual
// private composition must own pool/native idle memory and WAL sampling outside
// command custody. No production profile is inferred from these descriptors.
type effectiveRuntime struct {
	mu                  sync.Mutex
	executor            *executor
	governor            governor.Governor
	authority           *governor.Authority
	wal                 *governor.WALGate
	path                string
	owner               governor.OwnerID
	plan                effectivePlan
	inventoryPlan       *effectivePlan
	wcProgressPlan      *effectivePlan
	wcProgressBirthPlan *effectivePlan
	wcSecurityPlan      *effectivePlan
	lifecyclePlans      [2]*effectivePlan // private, explicitly composed create/claim history profiles
	issuerPlans         [2]*effectivePlan // complete closed issuer source/snapshot/effect profiles
	registry            *stmt.Registry
	fenced              bool
	cleanupPanics       []terminalPanic

	clockHighWater time.Time // composition custody under mu, then transferred into the frame
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

func (r *effectiveRuntime) disclose(ctx context.Context, turn *commandTurn, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
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

func (r *effectiveRuntime) execute(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand) (released EffectiveStageResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	result, _, err := r.run(ctx, w, cap, request, input, false)
	return result, err
}

func (r *effectiveRuntime) ack(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest) (released AckResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	_, result, err := r.run(ctx, w, cap, request, nil, true)
	return result, err
}

func (r *effectiveRuntime) run(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand, ack bool) (released EffectiveStageResult, releasedAck AckResult, releaseErr error) {
	defer func() {
		v, err := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{effective: released}, ack: releasedAck}, releaseErr)
		released, releasedAck, releaseErr = v.output.effective, v.ack, err
	}()
	out, acknowledged, err := r.runCommand(ctx, w, cap, request, input, ack, nil, nil)
	return out.effective, acknowledged, err
}

type commandCompletion struct {
	output commandOutput
	ack    AckResult
}

type commandOutput struct {
	effective EffectiveStageResult
	inventory InventoryResult
	publisher SearchImageStageResult
	lifecycle scanLifecycleCommandResult
	source    scanSourceFacts
	run       scanRunSnapshot
	progress  ProgressResult
	security  SecurityResult
}

func (r *effectiveRuntime) runCommand(ctx context.Context, w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, input *effectiveStageBeginCommand, ack bool, scan *inventoryInvocation, invocation wcCommandInvocation, publishers ...*publisherInvocation) (out commandOutput, acknowledged AckResult, err error) {
	var wc *wcProgressInvocation
	var security *wcSecurityInvocation
	invalidWC := false
	switch v := invocation.(type) {
	case nil:
	case *wcProgressInvocation:
		wc = v
	case *wcSecurityInvocation:
		security = v
	default:
		invalidWC = true
	}
	var publisher *publisherInvocation
	if len(publishers) == 1 {
		publisher = publishers[0]
	}
	// Installed before EVERY return, including admission/binding failures. It
	// executes after rollback/retirement defers and the final WAL sample.
	lifetime := ctx
	var stopLifetime context.CancelFunc
	var tx *transaction
	defer func() {
		if stopLifetime != nil {
			defer stopLifetime()
		}
		err = errors.Join(err, r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation))
		if tx != nil {
			err = errors.Join(err, tx.lifetimeErr)
		}
		result, finalErr := finishCommand(ctx, lifetime, commandCompletion{out, acknowledged}, err)
		out, acknowledged, err = result.output, result.ack, finalErr
	}()
	if invalidWC || ctx == nil || len(publishers) > 1 || len(publishers) == 1 && publisher == nil {
		return out, acknowledged, ErrInvalid
	}
	if err := r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation); err != nil {
		return out, acknowledged, err
	}
	if err := ctx.Err(); err != nil {
		return out, acknowledged, err
	}
	if publisher != nil {
		if input != nil || cap != nil || request != (EnrichmentReceiptRequest{}) || scan != nil || invocation != nil || ack != (publisher.call == publisherAckImage) {
			return out, acknowledged, ErrInvalid
		}
		if _, err := validatePublisherInvocation(ctx, w, publisher); err != nil {
			return out, acknowledged, err
		}
		// PB's finite native receipt/effect adapters and complete captured
		// profile are not on main yet. The SAME dispatcher owns this closed
		// entry, but no grant/BEGIN or substitute handler is manufactured.
		return out, acknowledged, ErrNotImplemented
	}
	purpose, plan, reference, key, libraryID := EffectiveStage, &r.plan, request.Receipt, request.ProviderOperationID, request.LibraryID
	outcome := &out.effective.Outcome
	if scan != nil {
		if input != nil || cap != nil || request != (EnrichmentReceiptRequest{}) {
			return out, acknowledged, ErrInvalid
		}
		if scan.lifecycle == 0 && r.inventoryPlan == nil {
			return out, acknowledged, ErrNotImplemented
		}
		if scan.input != nil {
			original, err := scan.input.receiptRequest()
			if err != nil || original != scan.request {
				return out, acknowledged, ErrInvalid
			}
		}
		purpose, plan, reference, key, libraryID = InventoryAccept, r.inventoryPlan, scan.request.Receipt, scan.request.RunID, scan.request.LibraryID
		outcome = &out.inventory.Outcome
		if scan.lifecycle != 0 {
			if scan.input != nil || (scan.lifecycle != InventoryCreate && scan.lifecycle != InventoryClaim) {
				return out, acknowledged, ErrInvalid
			}
			purpose = scan.lifecycle
			plan = nil // read the one-time installed profile only under runtime custody
			outcome = &out.lifecycle.Outcome
		}
	}
	if wc != nil {
		if scan != nil || cap != nil || input != nil || request != (EnrichmentReceiptRequest{}) || publisher != nil {
			return out, acknowledged, ErrInvalid
		}
		purpose, plan, reference, key, libraryID = ProgressAccept, nil, wc.request.Receipt, wc.request.PlaybackID, wc.disclosure.driver.scope.libraryID
		outcome = &out.progress.Outcome
	}
	if security != nil {
		if scan != nil || cap != nil || input != nil || request != (EnrichmentReceiptRequest{}) || publisher != nil {
			return out, acknowledged, ErrInvalid
		}
		purpose, plan, reference, key, libraryID = SecurityAccept, nil, security.request.Receipt, security.request.FamilyID, 0
		outcome = &out.security.Outcome
	}
	if !r.mu.TryLock() {
		return out, acknowledged, ErrBusy
	}
	defer r.mu.Unlock()
	if wc != nil {
		plan = r.wcProgressPlan
		if wc.birth != nil {
			plan = r.wcProgressBirthPlan
		}
		if plan == nil {
			return out, acknowledged, ErrNotImplemented
		}
	}
	if security != nil {
		plan = r.wcSecurityPlan
		if plan == nil {
			return out, acknowledged, ErrNotImplemented
		}
	}
	if scan != nil && scan.lifecycle != 0 {
		plan = r.lifecyclePlans[int(purpose-InventoryCreate)]
		if scan.issuer != nil {
			if err = validateScanIssuerInvocation(r, w, scan); err != nil {
				return out, acknowledged, err
			}
			plan = r.issuerPlans[int(purpose-InventoryCreate)]
		}
		if plan == nil {
			return out, acknowledged, ErrNotImplemented
		}
	}
	defer func() {
		if primary := recover(); primary != nil {
			r.fenced = true
			// A panic can precede Try, while the before-WAL sample is valid.
			// Invalidate without another fallible clock or changing the panic.
			_ = r.wal.Publish(r.wal.Observation(), governor.WALState{}, time.Time{})
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
	before, err := observeWriterWALAt(r.path, r.wal, r.executor.now, &r.clockHighWater)
	if err != nil {
		return out, acknowledged, errors.Join(&Refusal{Code: RefusalWAL}, err)
	}
	now := r.executor.now()
	if err := observeTime(&r.clockHighWater, now); err != nil {
		return out, acknowledged, errors.Join(err, r.wal.Publish(r.wal.Observation(), governor.WALState{}, time.Time{}))
	}
	risk, err := r.wal.RestartRisk(now)
	if err != nil {
		return out, acknowledged, errors.Join(&Refusal{Code: RefusalWAL}, err)
	}
	if scan != nil && scan.issuer != nil && (scan.issuer.action == scanIssuerSource || scan.issuer.action == scanIssuerSnapshot || scan.issuer.action == scanIssuerRecover) {
		scan.issuer.deadline = now.Add(scan.issuer.driver.readyPlan.ready)
		if stop, ok := ctx.Deadline(); ok && stop.Before(scan.issuer.deadline) {
			scan.issuer.deadline = stop
		}
	}
	deadline := now.Add(plan.recipe.limit.Hold)
	if scan != nil && scan.issuer != nil && !scan.issuer.deadline.IsZero() && scan.issuer.deadline.Before(deadline) {
		deadline = scan.issuer.deadline
	}
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
	if scan != nil && scan.issuer != nil && scan.issuer.work != nil {
		if err = scan.issuer.work.submitting(); err != nil {
			return out, acknowledged, err
		}
	}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		if scan != nil && scan.issuer != nil && scan.issuer.work != nil {
			err = scan.issuer.work.refused(decision)
		}
		return out, acknowledged, errors.Join(err, effectiveAdmissionRefusal(decision.Refusal))
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
			if retireErr == nil && scan != nil && scan.issuer != nil && scan.issuer.work != nil {
				scan.issuer.work.admissionReturned(custody)
			}
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
	custody.inventoryMemory = plan.inventoryMemory // immutable W profile, never a worker override
	custody.searchImageMemory = plan.searchImageMemory
	custody.scanLifecycleMemory = plan.scanLifecycleMemory
	custody.wcProgressMemory = plan.wcProgressMemory
	custody.wcSecurityMemory = plan.wcSecurityMemory
	custody.clockHighWater = &r.clockHighWater
	tx, err = r.executor.begin(ctx, purpose, plan.recipe, custody)
	if !errors.Is(err, ErrBusy) {
		transferred = true
	}
	if err != nil {
		if r.executor.fenced || r.executor.ambiguous {
			r.fenced = true
		}
		if scan != nil && scan.issuer != nil && scan.issuer.work != nil {
			scan.issuer.work.admissionReturned(custody)
		}
		if !ack && errors.Is(err, ErrResolveRequired) {
			outcome.State = ResolveRequired
			outcome.Reference = reference
		}
		return out, acknowledged, err
	}
	defer tx.rollback(nil) // preserves the PRIMARY panic through frame cleanup
	if scan != nil && scan.issuer != nil {
		scan.issuer.frame = tx
		if p := scan.issuer.retained; p != nil {
			p.mu.Lock()
			p.frame = tx
			p.mu.Unlock()
		}
		if scan.issuer.work != nil {
			if err = scan.issuer.work.bindFrame(tx); err != nil {
				return out, acknowledged, err
			}
		}
	}
	// The frame cancels its SQL context during retirement. Keep the EXACT same
	// deadline alive through physical return/WAL sampling, without mistaking
	// internal cleanup cancellation for caller/protected-lifetime cancellation.
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return out, acknowledged, ErrInvalid
	}
	lifetime, stopLifetime = commandDeadline(ctx, deadline)
	turn, err := newCommandTurn(tx, r.registry, plan.steps)
	if err == nil {
		if security != nil {
			err = checkWCSecurityNativeDisclosure(ctx, turn, security)
		} else if wc != nil {
			err = checkWCProgressNativeDisclosure(ctx, turn, wc)
		} else if scan != nil {
			if scan.lifecycle == 0 {
				_, err = inventoryOwnerFacts(ctx, turn, scan.request.RunID)
			}
			if err == nil {
				if scan.issuer != nil && (scan.issuer.action == scanIssuerSource || scan.issuer.action == scanIssuerCreate) {
					err = checkScanIssuerLibrary(ctx, turn, scan.request)
				} else {
					err = checkInventoryNativeScope(ctx, turn, scan.request)
				}
			}
		} else {
			err = r.disclose(ctx, turn, cap, request)
		}
	}
	if err == nil {
		err = errors.Join(r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation), turn.checkLifetimes(ctx))
	}
	var historical bool
	if err == nil && security != nil {
		out.security, acknowledged, historical, err = executeWCSecurityInvocation(ctx, turn, security, ack)
	}
	if err == nil && wc != nil {
		out.progress, acknowledged, historical, err = executeWCProgressInvocation(ctx, turn, wc, ack)
	}
	if err == nil && scan != nil && scan.issuer != nil {
		out, historical, err = runScanIssuerTurn(ctx, turn, scan, ack)
		if err == nil && ack {
			acknowledged.AcknowledgedThrough, err = acknowledgeScanRunLifecycle(ctx, turn, scan.request, scan.lifecycle)
		}
	}
	if err == nil && scan != nil && scan.lifecycle != 0 && scan.issuer == nil {
		if err = turn.requireScanLifecycleMemory(ctx, scanLifecycleHistoryBytes); err == nil {
			if ack {
				acknowledged.AcknowledgedThrough, err = acknowledgeScanRunLifecycle(ctx, turn, scan.request, scan.lifecycle)
			} else {
				var lookup scanLifecycleReceiptLookup
				lookup, err = lookupScanRunLifecycle(ctx, turn, scan.request, scan.lifecycle)
				if err == nil {
					out.lifecycle = scanLifecycleHistoricalResult(scan.request, lookup)
					historical = lookup.decision != receiptFresh
				}
			}
		}
	}
	if err == nil && scan != nil && scan.lifecycle == 0 {
		var lookup inventoryReceiptLookup
		lookup, err = lookupInventoryAccept(ctx, turn, scan.request)
		// Recheck permission even when lookup reports cancellation/error;
		// neither a saved result nor a fresh decision escapes revocation.
		err = errors.Join(err, r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation), turn.checkLifetimes(ctx))
		if err == nil {
			if ack {
				acknowledged.AcknowledgedThrough, err = acknowledgeInventory(ctx, turn, scan.request, lookup)
			} else {
				out.inventory = inventoryHistoricalResult(scan.request, lookup)
				historical = lookup.decision != receiptFresh
				if lookup.decision == receiptFresh && scan.input != nil {
					// N1's trusted preparation and complete qualified native
					// composition are not installed here. No subset becomes Applied.
					err = checkInventoryFreshLibrary(ctx, turn, scan.request)
					if err == nil {
						err = &Refusal{Code: RefusalUnqualified}
					}
				}
			}
		}
	}
	if err == nil && scan == nil && invocation == nil {
		expectedReceipt := receiptIdentity{request.Receipt.Identity, request.Receipt.RequestDigest}
		var saved EffectiveStageBeginResult
		var owner effectiveBeginOwner
		var disposition receiptDecision
		saved, owner, disposition, err = lookupEffectiveStageBegin(ctx, turn, request.ProviderOperationID, expectedReceipt)
		err = errors.Join(err, r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation))
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
						if err = turn.checkLifetimes(ctx); err != nil {
							break
						}
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
	if err == nil && scan != nil && scan.input != nil && !historical {
		err = checkInventoryTerminal(ctx, turn, scan.input)
	}
	if err == nil && scan != nil && scan.issuer != nil && scan.issuer.action == scanIssuerClaim && !historical {
		_, err = turn.scanClaimTimeMS(ctx, scan.issuer.claimMode, scan.issuer.claim.request.Run.leaseExpiresMS, scan.issuer.expiresMS)
	}
	// No partial result survives ANY preterminal failure. The same helper
	// runs again at final publication after physical return and all cleanup.
	checked, checkedErr := finishCommand(ctx, tx.ctx, commandCompletion{out, acknowledged}, errors.Join(err, r.currentCommandDisclosure(w, cap, request, scan, publisher, invocation)))
	out, acknowledged, err = checked.output, checked.ack, checkedErr
	var physical turnResult
	if err == nil {
		physical, err = tx.commit()
	} else {
		physical, err = tx.rollback(err)
	}
	if scan != nil && scan.issuer != nil && !ack && !historical && physical.state == committed &&
		(scan.issuer.action == scanIssuerCreate || scan.issuer.action == scanIssuerClaim) {
		outcome.State = Applied
		outcome.Reference = reference
	}
	if scan == nil && invocation == nil && !ack && !historical && input != nil {
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
	if wc != nil && !ack && !historical && wc.input != nil && physical.state == committed {
		out.progress.Outcome.State = Applied
		recordWCProgressCompletion(wc, out.progress)
	}
	if security != nil && !ack && !historical && security.input != nil && physical.state == committed {
		out.security.Outcome.State = Applied
	}
	if ack && physical.state != committed {
		acknowledged.AcknowledgedThrough = 0
	}
	evidence := frameEvidence(tx, custody)
	if evidence != nil {
		evidence.WALBefore = before
		if tx.connectionReturned && tx.retirementComplete {
			evidence.WALAfter, err = observeAfterReturn(r, tx, err)
		}
		evidence.ReturnedAt = r.executor.now()
	}
	if r.executor.fenced || r.executor.ambiguous {
		r.fenced = true
	}
	if security != nil && security.snapshot {
		out.security.Snapshot.Evidence = evidence
	}
	if wc != nil && (wc.snapshot || wc.retainedAck) {
		out.progress.Snapshot.Evidence = evidence
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

func acknowledgeEffective(ctx context.Context, turn *commandTurn, request EnrichmentReceiptRequest, owner effectiveBeginOwner, disposition receiptDecision) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
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

func observeAfterReturn(r *effectiveRuntime, tx *transaction, prior error) (*WALObservation, error) {
	observation, err := observeWriterWALAt(r.path, r.wal, r.executor.now, &r.clockHighWater, tx.clockHighWater, tx.commitReturnedAt, tx.physicalReturnedAt)
	return observation, errors.Join(prior, err)
}
