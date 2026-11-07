package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const searchPublisherHistoryBytes = uint64(64 << 10)

// Physical outcome belongs to the ONE dispatcher, not either domain body.
type searchPublisherCommandResult struct {
	Outcome  ReceiptOutcome
	Fragment searchFragmentStageResult
	Head     searchHeadPublishResult
	Reclaim  searchFragmentReclaimResult
}

func searchPublisherPlanIndex(purpose CommandID) int {
	switch purpose {
	case SearchFragmentStage:
		return 0
	case SearchHeadPublish:
		return 1
	case SearchFragmentReclaim:
		return 2
	default:
		return -1
	}
}

func searchPublisherDisclosureBytes(v *publisherInvocation) uint64 {
	// Scope strings are frozen by the existing issuer; charge their bind custody.
	n := searchPublisherHistoryBytes
	if v != nil && v.purpose == SearchFragmentReclaim && v.disclosure != nil && v.disclosure.scope != nil {
		n += 4 * uint64(len(v.disclosure.scope.libraryKey))
	}
	return n
}

// Trusted startup installation, using the existing executor, Capture receipts,
// plan freezer and registry. No caller can install a recipe through the facade.
func composeSearchPublisherCommands(ctx context.Context, r *effectiveRuntime, fragment, head effectivePlan, reclaim ...effectivePlan) (err error) {
	var locked, executorLocked, installed bool
	var previous *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry, r.publisherPlans = previous, [3]*effectivePlan{}
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if locked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if len(reclaim) > 1 || ctx == nil || r == nil || r.executor == nil || r.authority == nil || r.governor == nil || r.wal == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	locked = true
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.registry == nil || r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.publisherPlans != ([3]*effectivePlan{}) {
		return ErrBusy
	}
	fragment, err = freezeCommandPlan(r.executor, fragment, searchPublisherCommandDefinitions(SearchFragmentStage), SearchFragmentStage)
	if err != nil {
		return err
	}
	head, err = freezeCommandPlan(r.executor, head, searchPublisherCommandDefinitions(SearchHeadPublish), SearchHeadPublish)
	if err != nil {
		return err
	}
	plans := []effectivePlan{fragment, head}
	purposes := []CommandID{SearchFragmentStage, SearchHeadPublish}
	if len(reclaim) == 1 {
		p, e := freezeCommandPlan(r.executor, reclaim[0], searchPublisherCommandDefinitions(SearchFragmentReclaim), SearchFragmentReclaim)
		if e != nil {
			return e
		}
		plans = append(plans, p)
		purposes = append(purposes, SearchFragmentReclaim)
	}
	for _, p := range plans {
		if p.publisherMemory.preparationBytes < searchPublisherHistoryBytes {
			return &Refusal{Code: RefusalCapacity}
		}
	}
	var all []stmt.Definition
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, purpose := range purposes {
		for _, d := range searchPublisherCommandDefinitions(purpose) {
			found := false
			for _, old := range all {
				if old.ID == d.ID {
					if !reflect.DeepEqual(old, d) {
						return invalid("search_publisher_definition")
					}
					found = true
					break
				}
			}
			if !found {
				all = append(all, d)
			}
		}
	}
	receipts := []stmt.Receipt{r.plan.registration}
	for _, p := range plans {
		receipts = append(receipts, p.registration)
	}
	registry, err := stmt.New(all, receipts...)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	previous, r.registry = r.registry, registry
	for i := range plans {
		r.publisherPlans[i] = &plans[i]
	}
	installed = true
	return nil
}

// Private typed entry for trusted composition/harnesses. Product capability
// issuance and routes are unchanged. Mixed purpose/input/reference refuses.
func (r *effectiveRuntime) runSearchPublisher(ctx context.Context, w *Writer, cap *PublisherDisclosure, q PublisherReceiptRequest, purpose CommandID, fragment *searchFragmentStageCommand, head *searchHeadPublishCommand, ack bool) (out searchPublisherCommandResult, acknowledged AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{search: out}, ack: acknowledged}, err)
		out, acknowledged, err = v.output.search, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r {
		return out, acknowledged, ErrInvalid
	}
	invocation, err := prepareSearchPublisherCommand(ctx, w, cap, q, purpose, fragment, head, ack)
	if err != nil {
		return out, acknowledged, err
	}
	v, acknowledged, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, nil, &invocation)
	return v.search, acknowledged, err
}

func prepareSearchPublisherCommand(ctx context.Context, w *Writer, cap *PublisherDisclosure, q PublisherReceiptRequest, purpose CommandID, fragment *searchFragmentStageCommand, head *searchHeadPublishCommand, ack bool) (out publisherInvocation, err error) {
	defer func() {
		err = errors.Join(err, currentPublisherPrincipal(w, cap))
		out, err = finishCommand(ctx, ctx, out, err)
	}()
	if !searchPublisherPurpose(purpose) || ack && (fragment != nil || head != nil) {
		return out, ErrInvalid
	}
	call := publisherResolveCommand
	if ack {
		call = publisherAckCommand
	}
	var expected PublisherReceiptRequest
	if fragment != nil {
		if purpose != SearchFragmentStage || head != nil || !fragment.valid {
			return out, ErrInvalid
		}
		s := fragment.request.snapshot
		expected = PublisherReceiptRequest{s.job.id, s.source.libraryID, s.source.entityID, ReceiptReference{fragment.identity, fragment.digest}}
		call = publisherStageFragment
	}
	if head != nil {
		if purpose != SearchHeadPublish || !head.valid {
			return out, ErrInvalid
		}
		s := head.request.snapshot.stage
		expected = PublisherReceiptRequest{s.job.id, s.source.libraryID, s.source.entityID, ReceiptReference{head.identity, head.digest}}
		call = publisherPublishHead
	}
	if expected != (PublisherReceiptRequest{}) && expected != q {
		return out, ErrInvalid
	}
	if err = checkPublisherDisclosure(w, cap, q); err != nil {
		return out, err
	}
	return publisherInvocation{disclosure: cap, request: q, call: call, purpose: purpose, fragment: fragment, head: head}, nil
}

func validateSearchPublisherInvocation(ctx context.Context, w *Writer, input *publisherInvocation) (out publisherInvocation, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if input != nil && input.purpose == SearchFragmentReclaim {
		return validateSearchReclaimerInvocation(ctx, w, input)
	}
	if input == nil || input.input != nil || input.reclaim != nil || input.reclaimRequest != (searchReclaimerReceiptRequest{}) {
		return out, ErrInvalid
	}
	out, err = prepareSearchPublisherCommand(ctx, w, input.disclosure, input.request, input.purpose, input.fragment, input.head, input.call == publisherAckCommand)
	if err == nil && out != *input {
		return publisherInvocation{}, ErrInvalid
	}
	return out, err
}

func discloseSearchPublisher(ctx context.Context, turn *commandTurn, invocation *publisherInvocation) (owner receiptOwnerFacts, err error) {
	defer func() { owner, err = finishCommand(ctx, commandLifetime(turn), owner, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || invocation == nil || !searchPublisherResourcePurpose(invocation.purpose) || turn.transaction.descriptor.ID != invocation.purpose {
		return owner, ErrInvalid
	}
	if invocation.purpose == SearchFragmentReclaim {
		return discloseSearchReclaimer(ctx, turn, invocation)
	}
	q := invocation.request
	step, err := searchPublisherStep(turn, searchPublisherOwnerID)
	if err != nil {
		return owner, err
	}
	row, err := turn.queryRow(ctx, step, q.JobID)
	if err != nil {
		return owner, err
	}
	var entity, person sql.NullInt64
	var incarnation []byte
	if err = row.Scan(&entity, &person, &incarnation, &owner.lastSequence, &owner.acknowledgedSequence); err != nil {
		return owner, errors.Join(ErrResolveRequired, err)
	}
	if !entity.Valid || person.Valid || entity.Int64 != q.EntityID || len(incarnation) != 16 {
		return owner, ErrDisclosureDenied
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)
	for _, p := range []struct {
		id   stmt.ID
		args []any
		want int64
	}{
		{effectiveDisclosureLibrary, []any{q.LibraryID, invocation.disclosure.scope.libraryKey}, q.LibraryID},
		{effectiveDisclosureSubject, []any{q.EntityID, q.LibraryID}, q.EntityID},
	} {
		step, e := searchPublisherStep(turn, p.id)
		if e != nil {
			return owner, e
		}
		row, e := turn.queryRow(ctx, step, p.args...)
		if e != nil {
			return owner, e
		}
		var actual int64
		if e = row.Scan(&actual); e != nil || actual != p.want {
			return owner, errors.Join(ErrDisclosureDenied, e)
		}
	}
	return owner, nil
}

func executeSearchPublisherBody(ctx context.Context, turn *commandTurn, invocation *publisherInvocation, owner receiptOwnerFacts) (out searchPublisherCommandResult, acknowledged int64, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Out        searchPublisherCommandResult
			Ack        int64
			Historical bool
		}{out, acknowledged, historical}, err)
		out, acknowledged, historical, err = v.Out, v.Ack, v.Historical, e
	}()
	if ctx == nil || turn == nil || turn.transaction == nil || invocation == nil || !searchPublisherResourcePurpose(invocation.purpose) || turn.transaction.descriptor.ID != invocation.purpose {
		return out, 0, false, ErrInvalid
	}
	if invocation.purpose == SearchFragmentReclaim {
		return executeSearchReclaimerBody(ctx, turn, invocation, owner)
	}
	q := invocation.request
	decision := receiptDecision(0)
	savedJob := int64(0)
	if invocation.purpose == SearchFragmentStage {
		out.Fragment, decision, err = lookupSearchFragmentStage(ctx, turn, q)
		savedJob = out.Fragment.JobID
	} else {
		out.Head, decision, err = lookupSearchHeadPublish(ctx, turn, q)
		savedJob = out.Head.JobID
	}
	if err != nil {
		return out, 0, false, err
	}
	if invocation.call == publisherAckCommand {
		acknowledged, err = acknowledgeSearchPublisherFacts(ctx, turn, q, owner, decision, savedJob)
		return out, acknowledged, true, err
	}
	out.Outcome.Reference = q.Receipt
	switch decision {
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		return out, 0, true, nil
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, 0, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, 0, false, ErrResolveRequired
	}
	if invocation.fragment != nil {
		out.Fragment, err = applySearchFragmentStage(ctx, turn, invocation.fragment)
	} else if invocation.head != nil {
		out.Head, err = applySearchHeadPublish(ctx, turn, invocation.head)
	}
	return out, 0, false, err
}

func searchPublisherJobKey(q PublisherReceiptRequest) string {
	return "search/" + strconv.FormatInt(q.JobID, 10)
}
