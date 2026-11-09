package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// errIPRefreshPageBackoff marks the typed held refusal for a page whose
// provider backoff (metadata_providers.next_attempt_ms) is still in the future.
// The refusal is joined with ErrConflict: the page consumed no raw key and
// wrote nothing, so the driver retries the same page after the backoff.
var errIPRefreshPageBackoff = errors.New("ip refresh page held by provider backoff")

// IP refresh page 202 schedules ONE bounded raw-keyset page of provider work.
// The subject order is always the provider/purpose/entity index; due state,
// backoff, policy and leases only decide whether a page runs or a raw key is
// eligible, never the order in which keys are consumed. The sweep owner row
// carries the raw cursor, and its revision/last/acknowledged CAS is the page
// allowance: one transaction consumes exactly one page. People phase walks
// accepted person sources by provider and resets the cursor once, at the
// entity->people edge.
type ipRefreshPageRequest struct {
	provider, purpose                    string
	expectedGeneration, expectedRevision int64
	consentRevision                      int64
}

type ipRefreshPageCommand struct {
	identity CommandIdentity
	request  ipRefreshPageRequest
	digest   RequestDigest
	valid    bool
}

type ipRefreshPageCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipRefreshPageResult
	Digest  ResultDigest
}

type ipRefreshPageLookup struct {
	decision receiptDecision
	owner    receiptOwnerFacts
	value    ipRefreshPageResult
	digest   ResultDigest
}

// ipRefreshPageResult is the complete saved page result: the POST-CAS sweep
// revision/generation (for an already-complete sweep, the unchanged terminal
// revision), the truthful scheduled count, the raw cursor segment and whether
// the just-scheduled window is still in flight. ScheduledCount is zero in the
// person phase because that phase schedules no acquisition job.
type ipRefreshPageResult struct {
	Provider, Purpose, Phase              string
	Generation, AcceptedRevision, EpochMS int64
	AfterID, ScheduledCount, AppliedMS    int64
	ResultCount                           int64
	PageWaiting                           bool
}

type ipRefreshPageOwner struct {
	incarnation                       [16]byte
	generation, revision, epochMS     int64
	afterID, fromID, untilID, waiting int64
	lastSequence, ackSequence         int64
	phase                             string
}

type ipRefreshPageCandidate struct {
	generation, revision int64
	found                bool
}

func (c *ipRefreshPageCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipRefreshPageCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipRefreshPageCommand")
}

func ipRefreshPageRequestShape(in ipRefreshPageRequest) error {
	if !ipPolicyProviderCode(in.provider) || !ipRefreshPurpose(in.purpose) || in.consentRevision <= 0 ||
		in.expectedGeneration <= 0 || in.expectedRevision <= 0 {
		return ErrInvalid
	}
	if in.purpose == "artwork" && in.provider == "musicbrainz" {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation.
func newIPRefreshPageCommand(identity CommandIdentity, in ipRefreshPageRequest) (*ipRefreshPageCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipRefreshPageRequestShape(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPRefreshPage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipRefreshPageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipRefreshPageFacts reads the complete current owner state after current
// authority has been proved. The command incarnation is the sweep operation's
// incarnation: page receipts share it so their owner FK stays exact.
func ipRefreshPageFacts(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest) (out ipRefreshPageOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPageCurrent, q.provider, q.purpose)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var waiting int64
	err = row.Scan(&out.generation, &out.revision, &out.epochMS, &out.phase, &out.afterID,
		&out.fromID, &out.untilID, &waiting, &incarnation, &out.lastSequence, &out.ackSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || out.generation <= 0 || out.revision <= 0 || out.epochMS < 0 ||
		!(out.phase == "entities" || out.phase == "people" || out.phase == "complete") ||
		out.afterID < 0 || out.fromID < 0 || out.untilID < out.fromID ||
		(waiting != 0 && waiting != 1) || out.lastSequence < 0 || out.ackSequence < 0 ||
		out.ackSequence > out.lastSequence {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	out.waiting = waiting
	return out, nil
}

func ipRefreshPageLookupReceipt(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, ref ReceiptReference) (out ipRefreshPageLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPRefreshPage {
		return out, ErrInvalid
	}
	owner, err := ipRefreshOwnerFacts(ctx, turn, ipRefreshRequest{provider: q.provider, purpose: q.purpose})
	if err != nil {
		return out, err
	}
	out.owner = owner.owner
	if !out.owner.exists {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	row, err := wc008DraftQuery(ctx, turn, ipRefreshReceipt, q.provider, q.purpose, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var incarnation, operation, requestDigest, resultDigest []byte
	var sequence, appliedMS, acceptedRevision, generation, epochMS, afterID, scheduledCount, pageWaiting int64
	var kind, phase string
	var found receiptFacts
	err = row.Scan(&incarnation, &sequence, &operation, &requestDigest, &appliedMS, &resultDigest,
		&kind, &acceptedRevision, &generation, &epochMS, &phase, &afterID, &scheduledCount, &pageWaiting)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		waiting, ok := ipPolicyBool(pageWaiting)
		if len(incarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 || len(resultDigest) != 32 ||
			sequence <= 0 || appliedMS < 0 || kind != "page" || acceptedRevision <= 0 || generation <= 0 ||
			epochMS < 0 || !(phase == "entities" || phase == "people" || phase == "complete") ||
			afterID < 0 || scheduledCount < 0 || scheduledCount > ipPageRawLimit || !ok || waiting && phase != "entities" {
			return out, ErrResolveRequired
		}
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], incarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPRefreshPage, resultCount: 1}
		copy(found.resultDigest[:], resultDigest)
		out.digest = found.resultDigest
		out.value = ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: phase,
			Generation: generation, AcceptedRevision: acceptedRevision, EpochMS: epochMS,
			AfterID: afterID, ScheduledCount: scheduledCount, AppliedMS: appliedMS, ResultCount: 1,
			PageWaiting: waiting}
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPRefreshPage, out.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		computed, e := ipRefreshPageResultDigest(ref.Identity, ref.RequestDigest, out.value)
		if e != nil || computed != out.digest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.digest = computed
	}
	return out, nil
}

func ipRefreshPageRawIDs(ctx context.Context, turn *commandTurn, provider, purpose string, after int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := turn.query(ctx, turn.steps[ipPageRaw].statementStep, provider, purpose, after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if id <= after {
			return nil, ErrConflict
		}
		out = append(out, id)
		if len(out) > int(ipPageRawLimit) {
			return nil, ErrConflict
		}
	}
	return out, rows.Err()
}

func ipRefreshPageHasNextEntity(ctx context.Context, turn *commandTurn, provider, purpose string, after int64) (out bool, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPageNext, provider, purpose, after)
	if err != nil {
		return false, err
	}
	var id int64
	err = row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return id > after, nil
}

func ipRefreshPageWindowWaiting(ctx context.Context, turn *commandTurn, f ipRefreshPageOwner, now int64, provider, purpose string) (out bool, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := turn.query(ctx, turn.steps[ipPageWaiting].statementStep, now, provider, purpose, f.fromID, f.untilID)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	count := 0
	for rows.Next() {
		count++
		if count > int(ipPageRawLimit) {
			return false, ErrConflict
		}
		var id, pending int64
		if err = rows.Scan(&id, &pending); err != nil {
			return false, err
		}
		if pending != 0 && pending != 1 {
			return false, ErrResolveRequired
		}
		out = out || pending == 1
	}
	return out, rows.Err()
}

// ipRefreshPageTarget reads one eligibility point. Excluded keys are consumed
// without scheduling, so a page of entirely excluded rows still advances.
func ipRefreshPageTarget(ctx context.Context, turn *commandTurn, provider, purpose string, entity, now int64) (out ipRefreshPageCandidate, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPageCandidate, entity, provider, purpose)
	if err != nil {
		return out, err
	}
	var generation, revision, retiredEntity, retiredLibrary, enabled int64
	var status, selected, lease string
	var leaseUntil, nextAttempt int64
	err = row.Scan(&generation, &revision, &status, &selected, &lease, &leaseUntil, &nextAttempt,
		&retiredEntity, &retiredLibrary, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if generation <= 0 || revision <= 0 || generation == math.MaxInt64 || revision == math.MaxInt64 ||
		(retiredEntity != 0 && retiredEntity != 1) || (retiredLibrary != 0 && retiredLibrary != 1) ||
		(enabled != 0 && enabled != 1) {
		return out, ErrResolveRequired
	}
	if status != "matched" || selected == "" || lease != "" || leaseUntil > now || nextAttempt > now ||
		retiredEntity != 0 || retiredLibrary != 0 || enabled != 1 {
		return out, nil
	}
	return ipRefreshPageCandidate{generation: generation, revision: revision, found: true}, nil
}

func ipRefreshPagePersonIDs(ctx context.Context, turn *commandTurn, provider string, after int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := turn.query(ctx, turn.steps[ipPagePerson].statementStep, provider, after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if id <= after {
			return nil, ErrConflict
		}
		out = append(out, id)
		if len(out) > int(ipPagePersonPerPage) {
			return nil, ErrConflict
		}
	}
	return out, rows.Err()
}

func ipRefreshPageHasNextPerson(ctx context.Context, turn *commandTurn, provider string, after int64) (out bool, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPagePersonNext, provider, after)
	if err != nil {
		return false, err
	}
	var id int64
	err = row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return id > after, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved page with zero new effects.
func applyIPRefreshPage(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipRefreshPageCommand) (out ipRefreshPageCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipRefreshPageCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPRefreshPage {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipRefreshPageRequestShape(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPRefreshPage, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipRefreshPageLookupReceipt(ctx, turn, q, c.reference())
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderEnabled(ctx, turn, q.provider); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderConsent(ctx, turn, q.provider, q.consentRevision); err != nil {
		return zero, err
	}
	facts, err := ipRefreshPageFacts(ctx, turn, q)
	if err != nil {
		return zero, err
	}
	if facts.incarnation != c.identity.Incarnation {
		return zero, ErrConflict
	}
	if facts.generation != q.expectedGeneration || facts.revision != q.expectedRevision || q.expectedRevision == math.MaxInt64 {
		return zero, ErrConflict
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	nowMS := appliedMS
	var value ipRefreshPageResult
	switch facts.phase {
	case "entities":
		value, err = ipRefreshPageEntities(ctx, turn, q, facts, nowMS, appliedMS)
	case "people":
		value, err = ipRefreshPagePeople(ctx, turn, q, facts, appliedMS)
	case "complete":
		// A terminal sweep is a truthful no-next page: consume the command with
		// no cursor or job effects and save the exact terminal result.
		value, err = ipRefreshPageComplete(ctx, turn, q, facts, appliedMS)
	}
	if err != nil {
		return zero, err
	}
	digest, err := ipRefreshPageResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	waiting := int64(0)
	if value.PageWaiting {
		waiting = 1
	}
	written, err := turn.exec(ctx, turn.steps[ipPageRecord].statementStep, q.provider, q.purpose,
		c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:],
		appliedMS, digest[:], value.AcceptedRevision, value.Generation, value.EpochMS, value.Phase,
		value.AfterID, value.ScheduledCount, waiting)
	if err != nil {
		return zero, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	saved, err := ipRefreshPageLookupReceipt(ctx, turn, q, c.reference())
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func ipRefreshPageEntities(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner, nowMS, appliedMS int64) (out ipRefreshPageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	base := ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: "entities",
		Generation: facts.generation, EpochMS: facts.epochMS, AfterID: facts.afterID,
		AppliedMS: appliedMS, ResultCount: 1}
	if facts.waiting == 1 {
		waiting, e := ipRefreshPageWindowWaiting(ctx, turn, facts, nowMS, q.provider, q.purpose)
		if e != nil {
			return out, e
		}
		if waiting {
			// The previous page is still in flight. Keep its window and cursor,
			// consume this allowance, and remain entity-waiting.
			out = base
			out.AcceptedRevision = facts.revision + 1
			out.PageWaiting = true
			if e = ipRefreshPageUpdate(ctx, turn, q, facts, facts.afterID, facts.fromID, facts.untilID, true, facts.revision+1); e != nil {
				return out, e
			}
			return out, nil
		}
	}
	var enabled, backoff int64
	row, e := wc008DraftQuery(ctx, turn, ipPageProvider, q.provider)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&enabled, &backoff); e != nil {
		return out, e
	}
	if enabled != 0 && enabled != 1 || backoff < 0 {
		return out, ErrResolveRequired
	}
	// ipPolicyProviderEnabled already refused a disabled provider. Keep the
	// page from consuming raw keys rather than relying on that earlier check.
	if enabled != 1 {
		return out, ErrResolveRequired
	}
	if backoff > nowMS {
		// A provider-wide backoff holds the whole page: no raw key is consumed
		// and no cursor advances, so the driver can retry this page (same
		// expected revision) once metadata_providers.next_attempt_ms passes.
		return out, errors.Join(ErrConflict, errIPRefreshPageBackoff)
	}
	ids, e := ipRefreshPageRawIDs(ctx, turn, q.provider, q.purpose, facts.afterID)
	if e != nil {
		return out, e
	}
	scheduled := int64(0)
	from, until := facts.afterID, facts.afterID
	for _, id := range ids {
		until = id
		target, e := ipRefreshPageTarget(ctx, turn, q.provider, q.purpose, id, nowMS)
		if e != nil {
			return out, e
		}
		if !target.found {
			continue
		}
		if e = wc008DraftChanged(ctx, turn, ipPageSchedule, nowMS, id, q.provider, q.purpose,
			target.generation, target.revision, nowMS, nowMS); e != nil {
			return out, e
		}
		scheduled++
	}
	next, e := ipRefreshPageHasNextEntity(ctx, turn, q.provider, q.purpose, until)
	if e != nil {
		return out, e
	}
	if !next && scheduled == 0 {
		// True entity end: reset the raw cursor and enter the people phase in
		// the same CAS that consumes this page allowance.
		out = ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: "people",
			Generation: facts.generation, AcceptedRevision: facts.revision + 1, EpochMS: facts.epochMS,
			AfterID: 0, ScheduledCount: 0, AppliedMS: appliedMS, ResultCount: 1}
		if e = ipRefreshPageTransition(ctx, turn, q, facts); e != nil {
			return out, e
		}
		return out, nil
	}
	out = base
	out.AcceptedRevision = facts.revision + 1
	out.AfterID = until
	out.ScheduledCount = scheduled
	out.PageWaiting = scheduled > 0
	if e = ipRefreshPageUpdate(ctx, turn, q, facts, until, from, until, out.PageWaiting, facts.revision+1); e != nil {
		return out, e
	}
	return out, nil
}

// ipRefreshPagePeople walks the accepted person sources whose provider matches
// the sweep provider. It only advances that cursor: no acquisition job is
// scheduled in this phase, so ScheduledCount is always zero here. Whether an
// accepted person source should schedule person work is a domain question
// (reported, not decided in IP-04).
func ipRefreshPagePeople(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner, appliedMS int64) (out ipRefreshPageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	ids, err := ipRefreshPagePersonIDs(ctx, turn, q.provider, facts.afterID)
	if err != nil {
		return out, err
	}
	until := facts.afterID
	for _, id := range ids {
		until = id
	}
	next := false
	if len(ids) > 0 {
		next, err = ipRefreshPageHasNextPerson(ctx, turn, q.provider, until)
		if err != nil {
			return out, err
		}
	}
	if len(ids) == 0 || !next {
		return ipRefreshPageFinishPeople(ctx, turn, q, facts, facts.afterID, until, appliedMS)
	}
	out = ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: "people",
		Generation: facts.generation, AcceptedRevision: facts.revision + 1, EpochMS: facts.epochMS,
		AfterID: until, ScheduledCount: 0, AppliedMS: appliedMS, ResultCount: 1}
	if err = wc008DraftChanged(ctx, turn, ipPagePeopleUpdate, out.AcceptedRevision, until, facts.afterID,
		until, facts.lastSequence+1, q.provider, q.purpose, facts.generation,
		facts.revision, facts.afterID, facts.fromID, facts.untilID, facts.waiting, facts.incarnation[:],
		facts.lastSequence, facts.ackSequence); err != nil {
		return out, err
	}
	return out, nil
}

func ipRefreshPageFinishPeople(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner, from, until, appliedMS int64) (out ipRefreshPageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: "complete",
		Generation: facts.generation, AcceptedRevision: facts.revision + 1, EpochMS: facts.epochMS,
		AfterID: until, ScheduledCount: 0, AppliedMS: appliedMS, ResultCount: 1}
	if err = wc008DraftChanged(ctx, turn, ipPageComplete, out.AcceptedRevision, until, from, until,
		facts.lastSequence+1, q.provider, q.purpose, facts.generation, facts.revision, facts.afterID,
		facts.fromID, facts.untilID, facts.waiting, facts.incarnation[:], facts.lastSequence,
		facts.ackSequence); err != nil {
		return out, err
	}
	return out, nil
}

// ipRefreshPageComplete serves a page command that reached a sweep which is
// ALREADY complete. It is a truthful terminal page, not a second completion:
// ipPageComplete CASes phase='people' and would match no row. The terminal CAS
// consumes only this command's sequence, so revision, phase, cursor, page window
// and job lease stay exactly as the completing page saved them, and the common
// record step still writes the one page receipt that typed replay reads.
func ipRefreshPageComplete(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner, appliedMS int64) (out ipRefreshPageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = ipRefreshPageResult{Provider: q.provider, Purpose: q.purpose, Phase: "complete",
		Generation: facts.generation, AcceptedRevision: facts.revision, EpochMS: facts.epochMS,
		AfterID: facts.afterID, ScheduledCount: 0, AppliedMS: appliedMS, ResultCount: 1}
	if err = wc008DraftChanged(ctx, turn, ipPageTerminal, facts.lastSequence+1, q.provider, q.purpose,
		facts.generation, facts.revision, facts.afterID, facts.fromID, facts.untilID, facts.waiting,
		facts.incarnation[:], facts.lastSequence, facts.ackSequence); err != nil {
		return out, err
	}
	return out, nil
}

func ipRefreshPageUpdate(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner, after, from, until int64, waiting bool, revision int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	w := int64(0)
	if waiting {
		w = 1
	}
	return wc008DraftChanged(ctx, turn, ipPageUpdate, revision, after, from, until, w,
		facts.lastSequence+1, q.provider, q.purpose, facts.generation, facts.revision, facts.afterID,
		facts.fromID, facts.untilID, facts.waiting, facts.incarnation[:], facts.lastSequence,
		facts.ackSequence)
}

func ipRefreshPageTransition(ctx context.Context, turn *commandTurn, q ipRefreshPageRequest, facts ipRefreshPageOwner) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	return wc008DraftChanged(ctx, turn, ipPageTransition, facts.revision+1, facts.lastSequence+1,
		q.provider, q.purpose, facts.generation, facts.revision, facts.afterID, facts.fromID,
		facts.untilID, facts.waiting, facts.incarnation[:], facts.lastSequence, facts.ackSequence)
}

// A page result is the truthful scheduled count and raw cursor. It never claims
// the whole sweep is complete: people phase is a separate cursor segment.
func ipRefreshPageResultDigest(id CommandIdentity, request RequestDigest, v ipRefreshPageResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func preflightIPRefreshPageResult(v ipRefreshPageResult) error {
	if !ipPolicyProviderCode(v.Provider) || !ipRefreshPurpose(v.Purpose) ||
		!(v.Phase == "entities" || v.Phase == "people" || v.Phase == "complete") ||
		v.Generation <= 0 || v.AcceptedRevision <= 0 || v.EpochMS < 0 || v.AfterID < 0 ||
		v.ScheduledCount < 0 || v.ScheduledCount > ipPageRawLimit || v.AppliedMS < 0 || v.ResultCount != 1 {
		return ErrInvalid
	}
	if v.PageWaiting && v.Phase != "entities" {
		return ErrInvalid
	}
	if (v.Phase == "people" || v.Phase == "complete") && (v.ScheduledCount != 0 || v.PageWaiting) {
		return ErrInvalid
	}
	if v.Purpose == "artwork" && v.Provider == "musicbrainz" {
		return ErrInvalid
	}
	return nil
}

func writeIPRefreshPageResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipRefreshPageResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_refresh")
	e.text("page")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.Provider)
	e.text(v.Purpose)
	e.number(uint64(v.Generation))
	e.number(uint64(v.AcceptedRevision))
	e.number(uint64(v.EpochMS))
	e.text(v.Phase)
	e.number(uint64(v.AfterID))
	e.number(uint64(v.ScheduledCount))
	e.flag(v.PageWaiting)
	e.number(uint64(v.AppliedMS))
	e.number(uint64(v.ResultCount))
	return e.err
}
