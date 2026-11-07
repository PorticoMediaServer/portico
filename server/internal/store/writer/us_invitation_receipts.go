package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type usInvitationReceiptLookup struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	result   usInvitationSavedResult
	digest   ResultDigest
}

// Replay reads only immutable native receipt facts after current authority and
// original owner binding. Numeric preflight precedes every saved payload scan.
func lookupUSInvitation(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usInvitationReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	codec, e := usInvitationCodec(q.kind)
	if e != nil {
		return out, e
	}
	custody, e := lookupUSSecurityBinding(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	count := custody.binding.resultCount
	if count < 0 || count > usInvitationMaxLibraries {
		return out, ErrResolveRequired
	}
	args := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	point := func(id stmt.ID) (int64, error) {
		row, e := usPolicyRow(ctx, turn, id, args...)
		if e != nil {
			return 0, e
		}
		var n int64
		e = row.Scan(&n)
		if e != nil {
			return 0, errors.Join(ErrResolveRequired, e)
		}
		if n <= 0 {
			return 0, ErrResolveRequired
		}
		return n, nil
	}
	header, e := point(usInvitationReceiptOctets)
	if e != nil {
		return out, e
	}
	child, e := point(usInvitationReceiptResultOctets)
	if e != nil {
		return out, e
	}
	total := int64(0)
	add := func(n int64) error {
		if n < 0 || n > maxCanonicalResultBytes-total {
			return &Refusal{Code: RefusalCapacity}
		}
		total += n
		return nil
	}
	if e = add(header); e != nil {
		return out, e
	}
	if e = add(child); e != nil {
		return out, e
	}
	bounded := append(append([]any(nil), args...), int64(-1), int64(256))
	lengths, e := usInvitationReceiptCohort(ctx, turn, bounded, count, nil)
	if e != nil {
		return out, e
	}
	for _, n := range lengths {
		if e = add(n); e != nil {
			return out, e
		}
	}
	if err = a.requireMemory(ctx, turn, total); err != nil {
		return out, err
	}
	v := usInvitationSavedResult{ownerID: q.ownerID, issuerServer: a.issuer.server, kind: q.kind, version: 1, count: int(count)}
	row, e := usPolicyRow(ctx, turn, usInvitationReceiptHeader, args...)
	if e != nil {
		return out, e
	}
	var kind, version, actualCount, n int64
	var actualCodec string
	var digest []byte
	var key sql.NullString
	var nonce, body sql.Null[[]byte]
	if e = row.Scan(&kind, &version, &actualCodec, &v.appliedMS, &digest, &actualCount, &key, &nonce, &body, &n); e != nil {
		return out, errors.Join(ErrResolveRequired, e)
	}
	if kind != int64(q.kind) || version != 1 || actualCodec != codec || actualCount != count || n != header || len(digest) != 32 || ResultDigest(digest) != ResultDigest(custody.binding.resultDigest) {
		return out, ErrResolveRequired
	}
	if q.kind == usInvitationCreate {
		if !key.Valid || !nonce.Valid || !body.Valid || len(nonce.V) != 12 || len(body.V) != 59 {
			return out, ErrResolveRequired
		}
		v.code.keyID, v.code.nonce, v.code.body = key.String, nonce.V, body.V
	} else if key.Valid || nonce.Valid || body.Valid {
		return out, ErrResolveRequired
	}
	row, e = usPolicyRow(ctx, turn, usInvitationReceiptResult, args...)
	if e != nil {
		return out, e
	}
	var remote, tokens int64
	if e = row.Scan(&v.id, &v.serverID, &v.notes, &v.role, &v.state, &v.createdMS, &v.expiresMS, &v.acceptedMS, &v.accountID, &v.revision, &v.boundUsername, &v.access.mode, &v.access.streams, &remote, &tokens, &v.access.rating, &v.codeHash, &n); e != nil {
		return out, errors.Join(ErrResolveRequired, e)
	}
	if n != child || (remote != 0 && remote != 1) || (tokens != 0 && tokens != 1) {
		return out, ErrResolveRequired
	}
	v.access.allowRemote, v.access.accessTokens = remote == 1, tokens == 1
	v.libraries = make([]string, 0, int(count))
	if _, e = usInvitationReceiptCohort(ctx, turn, bounded, count, &v.libraries); e != nil {
		return out, e
	}
	for ordinal, id := range v.libraries {
		if int64(len(q.ownerID)+16+len("invitation")+len(id)) != lengths[ordinal] {
			return out, ErrResolveRequired
		}
	}
	if q.kind == usInvitationCreate {
		v.code.manifest = usInvitationCodeAAD(q.reference.Identity, v.ownerID, v.issuerServer, v.id, v.codeHash, v.code.keyID)
	}
	out.digest, e = usInvitationResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if e != nil || out.digest != ResultDigest(custody.binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	actual, e := usInvitationResultVariableBytes(v)
	if e != nil || actual != total {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

// Both passes require exactly the dense bounded cohort, including for count=0.
func usInvitationReceiptCohort(ctx context.Context, turn *commandTurn, args []any, count int64, ids *[]string) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	id := usInvitationReceiptLibraryOctets
	if ids != nil {
		id = usInvitationReceiptLibraries
	}
	rows, err := usPolicyRows(ctx, turn, id, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]int64, 0, int(count))
	for rows.Next() {
		var ordinal, n int64
		var s string
		if ids == nil {
			err = rows.Scan(&ordinal, &n)
		} else {
			err = rows.Scan(&ordinal, &s)
		}
		if err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= count || ids == nil && n <= 0 {
			return nil, ErrResolveRequired
		}
		out = append(out, n)
		if ids != nil {
			*ids = append(*ids, s)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != count {
		return nil, ErrResolveRequired
	}
	return out, nil
}
