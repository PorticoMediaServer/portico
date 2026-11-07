package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func ipPersonStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil {
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}
func ipPersonScan(ctx context.Context, t *commandTurn, id stmt.ID, args []any, dest ...any) error {
	s, e := ipPersonStep(t, id)
	if e != nil {
		return e
	}
	row, e := t.queryRow(ctx, s, args...)
	if e != nil {
		return e
	}
	return row.Scan(dest...)
}
func ipPersonOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	s, e := ipPersonStep(t, id)
	if e != nil {
		return e
	}
	r, e := t.exec(ctx, s, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ipPersonFresh(ctx context.Context, t *commandTurn, r ipPersonRequest) error {
	o, n := r.owner, r.owner.namespace
	if o.source.role != "primary" {
		return ErrInvalid
	}
	var one int64
	args := []any{o.id, r.identity.Incarnation[:], o.revision, o.generation, o.personID, o.token, o.personRevision, o.libraryID, n.provider, n.providerID, n.language, n.region, n.shape, n.subjectID, n.incarnation[:], n.revision, o.policyIncarnation[:], o.policyRevision, o.consentRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, o.phase, ipPersonOptionalInt(o.nextAttempt), o.libraryLanguage, o.libraryMetadataRevision}
	if e := ipPersonScan(ctx, t, ipPPFresh, args, &one); e != nil {
		return errors.Join(ErrConflict, e)
	}
	s := o.source
	if e := ipPersonScan(ctx, t, ipPPSource, []any{s.revision, o.personID, n.provider, s.role, s.revision, o.personID, n.provider, s.role, n.providerID, s.incarnation[:], s.revision, ipPersonNullText(s.publication), ipPersonNullText(s.evidence)}, &one); e != nil {
		return errors.Join(ErrConflict, e)
	}
	return nil
}

// Reads the entire saved historical result BEFORE checking current person,
// namespace, source role/head, policy, consent, owner revision or lease. Outer
// CURRENT disclosure authority must already be established even for this path.
func ipPersonLookup(ctx context.Context, t *commandTurn, k CommandID, r ipPersonRequest, q RequestDigest, b *ipPersonDecode) (out ipPersonOutcome, owner receiptOwnerFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(t), struct {
			out   ipPersonOutcome
			owner receiptOwnerFacts
		}{out, owner}, err)
		out, owner, err = v.out, v.owner, e
	}()
	var inc []byte
	if err = ipPersonScan(ctx, t, ipPPOwner, []any{r.owner.id}, &inc, &owner.lastSequence, &owner.acknowledgedSequence); err != nil {
		return
	}
	if len(inc) != 16 {
		err = ErrResolveRequired
		return
	}
	copy(owner.incarnation[:], inc)
	owner.exists = true
	key := []any{r.owner.id, r.identity.Incarnation[:], r.identity.Sequence}
	var n int64
	err = ipPersonScan(ctx, t, ipPPReceiptOctets, key, &n)
	f := receiptFacts{}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if err = b.reserve(n); err != nil {
			return
		}
		var op, req, dig, commit []byte
		var kind int64
		var ei, bi, pi, alg sql.NullString
		var ver, rows, size sql.NullInt64
		v := ipPersonResult{}
		err = ipPersonScan(ctx, t, ipPPReceipt, key, &op, &req, &dig, &kind, &v.revision, &v.after, &v.count, &v.appliedMS, &ei, &bi, &pi, &alg, &ver, &commit, &rows, &size)
		if err != nil {
			return
		}
		if len(op) != 16 || len(req) != 32 || len(dig) != 32 || n != int64(len(ei.String)+len(bi.String)+len(pi.String)+len(alg.String)) {
			err = ErrResolveRequired
			return
		}
		v.evidenceID, v.buildID, v.publicationID, v.sealAlgorithm = ipPersonNullable(ei), ipPersonNullable(bi), ipPersonNullable(pi), ipPersonNullable(alg)
		if alg.Valid {
			if !ver.Valid || !rows.Valid || !size.Valid || len(commit) != 32 {
				err = ErrResolveRequired
				return
			}
			v.sealVersion, v.sealRows, v.sealBytes = ver.Int64, rows.Int64, size.Int64
			copy(v.sealCommitment[:], commit)
		} else if ver.Valid || rows.Valid || size.Valid || commit != nil {
			err = ErrResolveRequired
			return
		}
		f.exists = true
		f.kind = CommandID(kind)
		f.identity.command = r.identity
		copy(f.identity.command.OperationID[:], op)
		copy(f.identity.digest[:], req)
		copy(f.resultDigest[:], dig)
		out.result = v
	}
	if err != nil {
		return
	}
	out.decision, err = classifyReceipt(receiptIdentity{r.identity, q}, k, owner, f)
	if err != nil {
		return
	}
	if out.decision == receiptReplay {
		var d ResultDigest
		d, err = ipPersonResultDigest(k, r.identity, q, out.result)
		if err != nil || !bytes.Equal(d[:], f.resultDigest[:]) {
			err = ErrResolveRequired
		}
	} else {
		out.result = ipPersonResult{}
	}
	return
}
func ipPersonSave(ctx context.Context, t *commandTurn, k CommandID, r ipPersonRequest, q RequestDigest, v ipPersonResult) error {
	d, e := ipPersonResultDigest(k, r.identity, q, v)
	if e != nil {
		return e
	}
	command := ""
	switch k {
	case IPProviderClaim, IPEvidenceBegin:
		command = "begin"
	case IPProviderRenew:
		command = "renew"
	case IPProviderFinish:
		command = "retry"
	case IPEvidenceAppend:
		command = "append"
	case IPEvidenceSeal:
		command = "evidence_seal"
	case IPProviderAccept:
		command = "accept"
	case IPProviderDelta:
		command = "delta"
	default:
		return ErrInvalid
	}
	var alg, ver, commit, rows, size any
	if k == IPEvidenceSeal {
		alg = v.sealAlgorithm
		ver = v.sealVersion
		commit = v.sealCommitment[:]
		rows = v.sealRows
		size = v.sealBytes
	}
	return ipPersonOne(ctx, t, ipPPSave, r.owner.id, r.identity.Incarnation[:], r.identity.Sequence, r.identity.OperationID[:], q[:], v.appliedMS, d[:], command, v.revision, v.after, v.count, ipPersonNullText(v.evidenceID), ipPersonNullText(v.buildID), ipPersonNullText(v.publicationID), alg, ver, commit, rows, size)
}

// The body is private: completion returns a component outcome, never Applied.
// After it succeeds W must compose change intent, close rows and commit safely.
func ipPersonProviderBody(ctx context.Context, t *commandTurn, k CommandID, r ipPersonRequest, b *ipPersonDecode) (out ipPersonOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != k || !r.valid(k) {
		return out, ErrInvalid
	}
	defer t.transaction.guardPanic()
	q, err := r.digest(k)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	out, owner, err = ipPersonLookup(ctx, t, k, r, q, b)
	if err != nil || out.decision != receiptFresh {
		return out, err
	}
	if err = ipPersonFresh(ctx, t, r); err != nil {
		return out, err
	}
	if r.owner.revision == math.MaxInt64 || r.owner.personRevision == math.MaxInt64 || r.owner.source.revision == math.MaxInt64 || r.nowMS != t.transaction.writer.now().UnixMilli() {
		return out, ErrConflict
	}
	o := r.owner
	if k != IPProviderClaim && o.leaseUntilMS <= r.nowMS {
		return out, ErrConflict
	}
	v := ipPersonResult{revision: o.revision + 1, appliedMS: r.nowMS}
	phase := o.phase
	switch k {
	case IPProviderClaim, IPProviderRenew:
		if k == IPProviderClaim {
			if o.leaseUntilMS > r.nowMS || (o.nextAttempt.Valid && o.nextAttempt.Int64 > r.nowMS) || o.leaseGeneration == math.MaxInt64 || r.nextLeaseGeneration != o.leaseGeneration+1 {
				return out, ErrConflict
			}
		} else if r.nextLeaseOwner != o.leaseOwner || r.nextLeaseGeneration != o.leaseGeneration || r.nextLeaseUntilMS <= o.leaseUntilMS {
			return out, ErrConflict
		}
		err = ipPersonOne(ctx, t, ipPPLease, r.nextLeaseOwner, r.nextLeaseGeneration, r.nextLeaseUntilMS, o.id, r.identity.Incarnation[:], o.revision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS)
	case IPProviderFinish:
		var due any = r.nextAttemptMS
		if r.terminal == "finish" {
			phase = "done"
			due = nil
		}
		err = ipPersonOne(ctx, t, ipPPRetry, due, r.nowMS, o.id, r.identity.Incarnation[:], o.revision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS)
	case IPEvidenceBegin:
		phase = "building"
		v, err = ipPersonBegin(ctx, t, r)
	case IPEvidenceAppend:
		phase = "building"
		v, err = ipPersonAppend(ctx, t, r, b)
	case IPEvidenceSeal:
		phase = "sealed"
		v, err = ipPersonSeal(ctx, t, r, b)
	case IPProviderAccept, IPProviderDelta:
		phase = "accepted"
		v, out.change, err = ipPersonAccept(ctx, t, r, b)
	default:
		return out, ErrInvalid
	}
	if err != nil {
		return out, err
	}
	if err = ipPersonOne(ctx, t, ipPPOwnerAdvance, r.identity.Sequence, phase, o.id, r.identity.Incarnation[:], o.revision, owner.lastSequence, owner.acknowledgedSequence); err != nil {
		return out, err
	}
	if err = ipPersonSave(ctx, t, k, r, q, v); err != nil {
		return out, err
	}
	// Exact person seal receipt is inserted first to satisfy the real D FK. EN's
	// metadata_evidence seal_proof_* columns deliberately remain NULL.
	if k == IPEvidenceSeal {
		if err = ipPersonOne(ctx, t, ipPPBuildSeal, o.id, r.identity.Incarnation[:], r.identity.Sequence, r.evidence.buildID); err != nil {
			return out, err
		}
	}
	out.result = v
	return out, nil
}
