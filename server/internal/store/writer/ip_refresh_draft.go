package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// IPRefreshRequest201 and IPRefreshPage202 native components, against IP-001
// only. A finish under 202 finishes the ENTITY phase and advances to people;
// it cannot claim the entire sweep complete. Person traversal, lease issuance,
// bootstrap and the outer authority/resource/proof dispatcher are not integrated.
// The provider worker must consume the allowance already reserved at scheduling;
// a later adapter must not debit these same requests a second time.
const ipRefreshRawLimit = 16
const ipRefreshHourCap, ipRefreshDayCap int64 = 240, 2000

type ipRefreshRequest struct {
	provider, purpose                           string
	identity                                    CommandIdentity
	kind                                        ipRefreshKind
	generation, revision, afterID               int64
	leaseOwner, leaseGeneration, leaseExpiresMS int64
	consentRevision, nowMS                      int64
}
type ipRefreshKind uint8

const (
	ipRefreshRequestKind ipRefreshKind = iota + 1
	ipRefreshPageKind
	ipRefreshFinishEntitiesKind
)

func (k ipRefreshKind) command() CommandID {
	if k == ipRefreshRequestKind {
		return IPRefreshRequest
	}
	return IPRefreshPage
}
func (k ipRefreshKind) text() string {
	switch k {
	case ipRefreshRequestKind:
		return "request"
	case ipRefreshPageKind:
		return "page"
	case ipRefreshFinishEntitiesKind:
		return "finish"
	}
	return ""
}
func (r ipRefreshRequest) valid() bool {
	return ipPolicyValidText(r.provider, 128) && (r.purpose == "metadata" || r.purpose == "artwork") && ipPolicyValidIdentity(r.identity) && r.kind.text() != "" && r.generation > 0 && r.revision > 0 && r.afterID >= 0 && r.leaseOwner >= 0 && r.leaseGeneration >= 0 && r.leaseExpiresMS >= 0 && r.consentRevision > 0 && r.nowMS >= 0
}
func (r ipRefreshRequest) digest() (RequestDigest, error) {
	return canonicalRequestDigest(r.kind.command(), 1, r.identity, r)
}

type ipRefreshResult struct {
	retired                                bool // ACK floor has no saved scalar payload to disclose.
	revision, generation, epochMS          int64
	phase                                  string
	afterID, scheduled, waiting, appliedMS int64
}

func (r ipRefreshResult) digest(kind CommandID, i CommandIdentity, q RequestDigest) ([32]byte, error) {
	d, err := canonicalResultDigest(i, q, ipRefreshSavedResult{kind, r})
	return [32]byte(d), err
}

type ipRefreshFacts struct {
	ipRefreshResult
	fromID, untilID, leaseOwner, leaseGeneration, leaseExpiresMS int64
}
type ipRefreshTarget struct{ id, generation, revision int64 }

func ipRefreshBody(ctx context.Context, t *commandTurn, r ipRefreshRequest, b *ipPolicyDecode) (out ipRefreshResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != r.kind.command() || !r.valid() {
		return out, ErrInvalid
	}
	key := []any{r.provider, r.purpose}
	receiptKey := []any{r.provider, r.purpose, r.identity.Incarnation[:], r.identity.Sequence}
	var owner receiptOwnerFacts
	var ci []byte
	if err = ipPolicyScan(ctx, t, ipRefreshOwner, key, &ci, &owner.lastSequence, &owner.acknowledgedSequence); err != nil {
		return out, err
	}
	if len(ci) != 16 {
		return out, ErrResolveRequired
	}
	copy(owner.incarnation[:], ci)
	owner.exists = true
	var found receiptFacts
	var op, dig, rd []byte
	var command int64
	err = ipPolicyScan(ctx, t, ipRefreshReceiptIdentity, receiptKey, &op, &dig, &rd, &command)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if len(op) != 16 || len(dig) != 32 || len(rd) != 32 {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.identity.command = r.identity
		copy(found.identity.command.OperationID[:], op)
		copy(found.identity.digest[:], dig)
		copy(found.resultDigest[:], rd)
		found.kind = CommandID(command)
	}
	requestDigest, err := r.digest()
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{r.identity, requestDigest}, r.kind.command(), owner, found)
	if err != nil {
		return out, err
	}
	if decision == receiptRetired {
		return ipRefreshResult{retired: true}, nil
	}
	if decision == receiptReplay {
		var n int64
		if err = ipPolicyScan(ctx, t, ipRefreshReceiptOctets, receiptKey, &n); err != nil {
			return out, err
		}
		if err = b.reserve(n); err != nil {
			return out, err
		}
		if err = ipPolicyScan(ctx, t, ipRefreshReceiptResult, receiptKey, &out.revision, &out.generation, &out.epochMS, &out.phase, &out.afterID, &out.scheduled, &out.waiting, &out.appliedMS); err != nil {
			return out, err
		}
		if n != int64(len(out.phase)) || out.revision <= 0 || out.generation <= 0 || out.epochMS < 0 || out.afterID < 0 || out.scheduled < 0 || out.scheduled > ipRefreshRawLimit || (out.waiting != 0 && out.waiting != 1) || out.appliedMS < 0 || (out.phase != "entities" && out.phase != "people" && out.phase != "complete") {
			return out, ErrResolveRequired
		}
		resultDigest, e := out.digest(r.kind.command(), r.identity, requestDigest)
		if e != nil || resultDigest != found.resultDigest {
			return out, ErrResolveRequired
		}
		return out, nil
	}
	if decision != receiptFresh {
		return out, ErrResolveRequired
	}
	// Exact replay returns before current generation, policy, consent or lease CAS.
	if err = ipPolicyConsentFresh(ctx, t, r.consentRevision, true); err != nil {
		return out, err
	}
	var f ipRefreshFacts
	var n int64
	if err = ipPolicyScan(ctx, t, ipRefreshOctets, key, &n); err != nil {
		return out, err
	}
	if err = b.reserve(n); err != nil {
		return out, err
	}
	if err = ipPolicyScan(ctx, t, ipRefreshCurrent, key, &f.generation, &f.revision, &f.epochMS, &f.phase, &f.afterID, &f.fromID, &f.untilID, &f.waiting, &f.leaseOwner, &f.leaseGeneration, &f.leaseExpiresMS); err != nil {
		return out, err
	}
	if n != int64(len(f.phase)) {
		return out, ErrResolveRequired
	}
	if f.generation != r.generation || f.revision != r.revision || f.afterID != r.afterID || f.leaseOwner != r.leaseOwner || f.leaseGeneration != r.leaseGeneration || f.leaseExpiresMS != r.leaseExpiresMS || f.revision == math.MaxInt64 {
		return out, ErrConflict
	}
	out = f.ipRefreshResult
	out.revision++
	out.appliedMS = r.nowMS
	out.scheduled = 0
	cas := []any{r.provider, r.purpose, r.generation, r.revision, r.afterID, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence}
	switch r.kind {
	case ipRefreshRequestKind:
		if f.generation == math.MaxInt64 || f.leaseGeneration == math.MaxInt64 {
			return out, ErrConflict
		}
		out.generation++
		out.epochMS = max(f.epochMS, r.nowMS)
		out.phase = "entities"
		out.afterID = 0
		out.waiting = 0
		args := []any{out.generation, out.revision, out.epochMS, r.nowMS, r.identity.Sequence}
		args = append(args, cas...)
		if err = ipPolicyOne(ctx, t, ipRefreshRequestUpdate, args...); err != nil {
			return out, err
		}
	case ipRefreshPageKind, ipRefreshFinishEntitiesKind:
		if f.phase != "entities" || r.leaseOwner == 0 || r.leaseGeneration == 0 || r.leaseExpiresMS <= r.nowMS {
			return out, ErrConflict
		}
		waiting, err := ipRefreshPreviousWaiting(ctx, t, r, f)
		if err != nil {
			return out, err
		}
		if r.kind == ipRefreshFinishEntitiesKind {
			if waiting {
				return out, ErrConflict
			}
			if exists, err := ipRefreshHasNext(ctx, t, r, f.afterID); err != nil {
				return out, err
			} else if exists {
				return out, ErrConflict
			}
			out.phase = "people"
			out.waiting = 0
			args := []any{out.revision, r.identity.Sequence}
			args = append(args, cas...)
			if err = ipPolicyOne(ctx, t, ipRefreshFinishUpdate, args...); err != nil {
				return out, err
			}
		} else {
			from, until := f.fromID, f.untilID
			if !waiting {
				from = f.afterID
				out, err = ipRefreshSchedulePage(ctx, t, r, out)
				if err != nil {
					return out, err
				}
				until = out.afterID
				out.waiting = 0
				if out.scheduled > 0 {
					out.waiting = 1
				}
			}
			args := []any{out.revision, out.afterID, from, until, out.waiting, r.identity.Sequence}
			args = append(args, cas...)
			if err = ipPolicyOne(ctx, t, ipRefreshPageUpdate, args...); err != nil {
				return out, err
			}
		}
	}
	d := requestDigest
	resultDigest, err := out.digest(r.kind.command(), r.identity, d)
	if err != nil {
		return out, err
	}
	err = ipPolicyOne(ctx, t, ipRefreshReceiptInsert, r.provider, r.purpose, r.identity.Incarnation[:], r.identity.Sequence, r.identity.OperationID[:], d[:], r.nowMS, resultDigest[:], r.kind.text(), out.revision, out.generation, out.epochMS, out.phase, out.afterID, out.scheduled, out.waiting)
	return out, err
}

func ipRefreshHasNext(ctx context.Context, t *commandTurn, r ipRefreshRequest, after int64) (bool, error) {
	var id int64
	err := ipPolicyScan(ctx, t, ipRefreshNext, []any{r.provider, r.purpose, after}, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func ipRefreshPreviousWaiting(ctx context.Context, t *commandTurn, r ipRefreshRequest, f ipRefreshFacts) (waiting bool, err error) {
	if f.waiting == 0 {
		return false, nil
	}
	rows, err := t.query(ctx, ipPolicyStep(t, ipRefreshWaiting), r.nowMS, r.provider, r.purpose, f.fromID, f.untilID)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	count := 0
	for rows.Next() {
		count++
		if count > ipRefreshRawLimit {
			return false, ErrConflict
		}
		var id, pending int64
		if err = rows.Scan(&id, &pending); err != nil {
			return false, err
		}
		waiting = waiting || pending == 1
	}
	return waiting, rows.Err()
}

type ipRefreshWindow struct {
	span                                  string
	oldStart, oldSpent, start, spent, cap int64
}

func ipRefreshWindowRead(ctx context.Context, t *commandTurn, r ipRefreshRequest, span string, width, cap int64) (w ipRefreshWindow, err error) {
	w.span = span
	w.cap = cap
	now := r.nowMS / 1000
	err = ipPolicyScan(ctx, t, ipRefreshBudget, []any{r.provider, span}, &w.oldStart, &w.oldSpent)
	if errors.Is(err, sql.ErrNoRows) {
		w.start = now
		return w, nil
	}
	if err != nil {
		return w, err
	}
	if w.oldStart < 0 || w.oldSpent < 0 {
		return w, ErrConflict
	}
	w.start, w.spent = w.oldStart, w.oldSpent
	if now < w.start || now-w.start >= width {
		w.start = now
		w.spent = 0
	}
	return w, nil
}

func ipRefreshSchedulePage(ctx context.Context, t *commandTurn, r ipRefreshRequest, out ipRefreshResult) (result ipRefreshResult, err error) {
	result = out
	hour, err := ipRefreshWindowRead(ctx, t, r, "hour", 3600, ipRefreshHourCap)
	if err != nil {
		return result, err
	}
	day, err := ipRefreshWindowRead(ctx, t, r, "day", 86400, ipRefreshDayCap)
	if err != nil {
		return result, err
	}
	remaining := min(max(int64(0), hour.cap-hour.spent), max(int64(0), day.cap-day.spent))
	var enabled, backoff int64
	if err = ipPolicyScan(ctx, t, ipRefreshProvider, []any{r.provider}, &enabled, &backoff); err != nil {
		return result, err
	}
	rows, err := t.query(ctx, ipPolicyStep(t, ipRefreshRaw), r.provider, r.purpose, out.afterID)
	if err != nil {
		return result, err
	}
	var raw [ipRefreshRawLimit]ipRefreshTarget
	count := 0
	for rows.Next() {
		if count == len(raw) {
			err = ErrConflict
			break
		}
		v := &raw[count]
		if err = rows.Scan(&v.id, &v.generation, &v.revision); err != nil {
			break
		}
		count++
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return result, err
	}
	for _, v := range raw[:count] {
		// Provider ready/backoff changes eligibility, never the RAW walk.
		if enabled != 1 || backoff > r.nowMS {
			result.afterID = v.id
			continue
		}
		var publicID, sourceInc, artInc, policyInc []byte
		var entityRevision, sourceRevision, identityRevision, artRevision, policyRevision int64
		err = ipPolicyScan(ctx, t, ipRefreshCandidate, []any{r.provider, r.purpose, v.id, v.generation, v.revision, r.nowMS, r.nowMS}, &publicID, &entityRevision, &sourceInc, &sourceRevision, &identityRevision, &artInc, &artRevision, &policyInc, &policyRevision)
		if errors.Is(err, sql.ErrNoRows) {
			result.afterID = v.id
			continue
		}
		if err != nil {
			return result, err
		}
		if len(publicID) != 16 || len(sourceInc) != 16 || len(artInc) != 16 || len(policyInc) != 16 || v.generation <= 0 || v.revision <= 0 || v.generation == math.MaxInt64 || v.revision == math.MaxInt64 {
			return result, ErrConflict
		}
		// An eligible target with no allowance remains ahead of the cursor.
		if remaining == 0 {
			break
		}
		var sourceOK int64
		if err = ipPolicyScan(ctx, t, ipRefreshSource, []any{r.provider, v.id, publicID, entityRevision, sourceInc, sourceRevision, identityRevision, artInc, artRevision, policyInc, policyRevision}, &sourceOK); err != nil {
			return result, err
		}
		if err = ipPolicyOne(ctx, t, ipRefreshSchedule, r.nowMS, v.id, r.provider, r.purpose, v.generation, v.revision, r.nowMS, r.nowMS); err != nil {
			return result, err
		}
		result.scheduled++
		remaining--
		result.afterID = v.id
	}
	if result.scheduled > 0 {
		// Both allowance windows, source scheduling, raw cursor and immutable
		// result commit (or roll back) together on this sole W transaction.
		for _, w := range [2]ipRefreshWindow{hour, day} {
			if w.spent > math.MaxInt64-result.scheduled {
				return result, ErrConflict
			}
			if err = ipPolicyOne(ctx, t, ipRefreshBudgetDebit, r.provider, w.span, w.start, w.spent+result.scheduled, w.oldStart, w.oldSpent); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
