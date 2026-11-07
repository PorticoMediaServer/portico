package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const wcReclaimChildPresent stmt.ID = "wc.reclaim.child_present"

func wcReclaimCommandDefinitions() []stmt.Definition {
	defs := wcReclaimProtocolDefinitions()
	for _, d := range wcProgressProtocolDefinitions() {
		if d.ID == wcProgressNativeEvent {
			defs = append(defs, d)
		}
	}
	defs = append(defs, stmt.Definition{ID: wcReclaimChildPresent, Owner: "store/writer WC reclaim snapshot", CallSites: []string{"store/writer/wc_reclaim_dispatch.go"}, Kind: stmt.Read,
		SQL:     `SELECT variable_bytes FROM playback_claim_command_receipts WHERE playback_id=? AND command_incarnation=? AND command_sequence=?`,
		Params:  []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "exact_removed_child", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_claim_command_receipts"}}})
	return defs
}

func checkWCReclaimNativeDisclosure(ctx context.Context, t *commandTurn, v *wcReclaimInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = checkWCReclaimDisclosure(v.disclosure.driver.writer, v); err != nil {
		return err
	}
	n := uint64(64 << 10)
	s := v.disclosure.driver.scope
	for _, x := range []string{s.playbackID, s.profileKey, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryKey} {
		n += uint64(len(x)) * 32
	}
	if err = t.requireCommandMemory(ctx, ProgressReceiptReclaim, n, "wc_reclaim_memory"); err != nil {
		return err
	}
	// Exact retained source key including actual birth witnesses, before any
	// result lookup or cleanup effect. Missing source is never initialized here.
	_, err = readWCReclaimOwner(ctx, t, s)
	return err
}

func readWCReclaimCurrent(ctx context.Context, t *commandTurn, scope wcProgressScope) (out ReclaimSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s, err := readWCReclaimOwner(ctx, t, scope)
	if err != nil {
		return out, err
	}
	// Current state can contain the just-committed unACKed scalar. Only
	// replacement preparation requires that OLD slot sequence be <= ACK.
	row, err := wc008DraftQuery(ctx, t, wcReclaimCurrentSlot, wcReclaimScopeArgs(scope)...)
	if err != nil {
		return out, err
	}
	raw, err := scanWCReclaimSlot(row)
	if err != nil {
		return out, err
	}
	s.slot, s.resultVersion, err = decodeWCReclaimSlot(raw, s.owner)
	if err != nil {
		return out, err
	}
	o := s.owner
	out = ReclaimSnapshot{Last: o.lastSequence, Acknowledged: o.acknowledgedSequence, AfterSequence: o.afterSequence, ReceiptRows: o.rows, ReceiptBytes: o.variableBytes, ControlBytes: s.controlBytes, SlotPresent: s.slot.present, source: s}
	if o.due {
		out.Due = wcPresent(o.dueMS)
	}
	if s.slot.present {
		p := s.slot
		out.Slot = ReclaimValue{CommandKind: wcReclaimKind, ResultVersion: s.resultVersion, CapturedAcknowledgedSequence: p.floor, OldAfterSequence: p.oldCursor, RemovedCount: p.removedCount, RemovedVariableBytes: p.removedBytes, ResultAfterSequence: p.nextCursor, ResultNoNext: p.noNext, AppliedMS: p.appliedMS, ResultCount: 1}
		if p.removedCount == 1 {
			out.Slot.RemovedSequence = wcPresent(p.removedSequence)
			row, e := wc008DraftQuery(ctx, t, wcReclaimChildPresent, scope.playbackID, scope.incarnation[:], p.removedSequence)
			if e != nil {
				return out, e
			}
			var nativeBytes int64
			e = row.Scan(&nativeBytes)
			if errors.Is(e, sql.ErrNoRows) {
				e = nil
			} else if e == nil {
				out.LastRemovedChildPresent = true
			}
			if e != nil {
				return out, e
			}
		}
	}
	row, e := wc008DraftQuery(ctx, t, wcProgressNativeEvent, apievents.ProfileAudience("local", scope.accountID, scope.profileID), int64(1))
	if e != nil {
		return out, e
	}
	var id int64
	e = row.Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	} else if e == nil {
		out.LastEvent = wcPresent(id)
	}
	return out, e
}

type wcReclaimCompletion struct {
	out        ReclaimResult
	ack        AckResult
	historical bool
}

func executeWCReclaimInvocation(ctx context.Context, t *commandTurn, v *wcReclaimInvocation, ack bool) (out ReclaimResult, floor AckResult, historical bool, err error) {
	defer func() {
		r, e := finishCommand(ctx, commandLifetime(t), wcReclaimCompletion{out, floor, historical}, err)
		out, floor, historical, err = r.out, r.ack, r.historical, e
	}()
	if v.snapshot {
		if ack || v.input != nil {
			return out, floor, false, ErrInvalid
		}
		out.Snapshot, err = readWCReclaimCurrent(ctx, t, v.disclosure.driver.scope)
		return out, floor, true, err
	}
	lookup, err := lookupWCReclaim(ctx, t, ProgressReceiptReclaim, v.disclosure.driver.scope, v.request)
	if err != nil {
		return out, floor, false, err
	}
	if ack {
		floor.AcknowledgedThrough, err = acknowledgeWCReclaim(ctx, t, ProgressReceiptReclaim, v.request, lookup)
		return out, floor, true, err
	}
	out.Outcome.Reference = v.request.Receipt
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if v.input == nil {
		return out, floor, false, nil
	}
	q, e := reclaimReference(v.input)
	if e != nil || q != v.request || v.input.driver != v.disclosure.driver {
		return out, floor, false, errors.Join(ErrConflict, e)
	}
	now, e := t.commandTimeMS(ctx, ProgressReceiptReclaim, 0)
	if e != nil {
		return out, floor, false, e
	}
	if now < lookup.snapshot.owner.dueMS {
		return out, floor, false, ErrBusy
	}
	applied, e := applyWCReclaim(ctx, t, ProgressReceiptReclaim, v.input.request, v.request, lookup, now)
	if e != nil {
		return out, floor, false, e
	}
	out.Value, out.Digest = applied.value, applied.digest
	return out, floor, false, nil
}

func wcReclaimStatementMaxUses(id stmt.ID) int {
	switch id {
	case wc008ReclaimFirst, wcProgressNativeOwner:
		return 2
	default:
		return 1
	}
}

func composeWCReclaim(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry, r.wcReclaimPlan = oldRegistry, nil
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
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
	if r.wcReclaimPlan != nil {
		return ErrBusy
	}
	defs := wcReclaimCommandDefinitions()
	p, err = freezeCommandPlan(r.executor, p, defs, ProgressReceiptReclaim)
	if err != nil {
		return err
	}
	if p.wcReclaimMemory == (commandMemoryBudget{}) {
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
					return ErrConflict
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	receipts := []stmt.Receipt{r.plan.registration, p.registration}
	for _, p := range []*effectivePlan{r.inventoryPlan, r.wcProgressPlan, r.wcProgressBirthPlan, r.wcSecurityPlan, r.lifecyclePlans[0], r.lifecyclePlans[1], r.issuerPlans[0], r.issuerPlans[1]} {
		if p != nil {
			receipts = append(receipts, p.registration)
		}
	}
	registry, e := stmt.New(all, receipts...)
	if e != nil {
		return e
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry = r.registry
	r.registry, r.wcReclaimPlan = registry, &p
	installed = true
	return nil
}
