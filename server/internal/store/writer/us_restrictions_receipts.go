package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type usRestrictionsReceiptLookup struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	result   usRestrictionsSavedResult
	digest   ResultDigest
}

func (usRestrictionsReceiptLookup) String() string   { return "usRestrictionsReceiptLookup{redacted}" }
func (usRestrictionsReceiptLookup) GoString() string { return "usRestrictionsReceiptLookup{redacted}" }
func (usRestrictionsReceiptLookup) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsReceiptLookup")
}

// Current authority and original fixed binding precede every immutable payload
// read. No current profile, rating table, labels or target revision is consulted.
func lookupUSRestrictions(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usRestrictionsReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	if q.kind != usProfileRestrictionsEdit {
		return out, ErrInvalid
	}
	custody, err := lookupUSSecurityBinding(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.owner, out.decision = custody.owner, custody.decision
	if custody.decision != receiptReplay {
		return out, nil
	}
	count := custody.binding.resultCount
	if count < 0 || count > 64 {
		return out, ErrResolveRequired
	}
	key := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	row, err := usPolicyRow(ctx, turn, usRestrictionsReceiptOctets, key...)
	if err != nil {
		return out, err
	}
	var header, child int64
	if err = row.Scan(&header); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	row, err = usPolicyRow(ctx, turn, usRestrictionsReceiptResultOctets, key...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&child); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if header <= 0 || child <= 0 || header > maxCanonicalResultBytes || child > maxCanonicalResultBytes-header {
		return out, ErrResolveRequired
	}
	total := header + child
	args := append(append([]any(nil), key...), int64(-1), int64(64))
	lengths, err := readUSRestrictionReceiptLabels(ctx, turn, args, count, nil)
	if err != nil {
		return out, err
	}
	for _, n := range lengths {
		if n <= 0 || n > maxCanonicalResultBytes-total {
			return out, ErrResolveRequired
		}
		total += n
	}
	if err = a.requireMemory(ctx, turn, total); err != nil {
		return out, err
	}
	v := usRestrictionsSavedResult{kind: q.kind, version: 1, ownerID: q.ownerID}
	row, err = usPolicyRow(ctx, turn, usRestrictionsReceiptHeader, key...)
	if err != nil {
		return out, err
	}
	var kind, version, actualCount, bytes int64
	var codec string
	var digest []byte
	var keyID sql.NullString
	var nonce, body sql.Null[[]byte]
	if err = row.Scan(&kind, &version, &codec, &v.appliedMS, &digest, &actualCount, &keyID, &nonce, &body, &bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if kind != int64(q.kind) || version != 1 || codec != "profile_restrictions_v1" || actualCount != count || bytes != header || keyID.Valid || nonce.Valid || body.Valid || len(digest) != 32 {
		return out, ErrResolveRequired
	}
	if ResultDigest(digest) != ResultDigest(custody.binding.resultDigest) {
		return out, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usRestrictionsReceiptResult, key...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&v.account, &v.memberIncarnation, &v.profile, &v.profileLifetime, &v.revision, &v.ratingSystem, &v.maximumAgeRating, &v.maximumAge, &v.allowUnrated, &v.allowDownloads, &v.allowLiveTV, &v.allowDVR, &v.allowWatchTogether, &bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if bytes != child {
		return out, ErrResolveRequired
	}
	v.labels = make([]string, 0, int(count))
	if _, err = readUSRestrictionReceiptLabels(ctx, turn, args, count, &v.labels); err != nil {
		return out, err
	}
	for n, label := range v.labels {
		if int64(len(q.ownerID)+16+len(label)) != lengths[n] {
			return out, ErrResolveRequired
		}
	}
	out.digest, err = usRestrictionsResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil || out.digest != ResultDigest(custody.binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	actual, err := usRestrictionsResultVariableBytes(v)
	if err != nil || actual != total {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

// The closed ordinal range is always (-1,64), including count zero, so an
// unexpected legal extra position cannot disappear behind the reported count.
func readUSRestrictionReceiptLabels(ctx context.Context, turn *commandTurn, args []any, count int64, labels *[]string) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	id := usRestrictionsReceiptLabelOctets
	if labels != nil {
		id = usRestrictionsReceiptLabels
	}
	rows, err := usPolicyRows(ctx, turn, id, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]int64, 0, int(count))
	for rows.Next() {
		var ordinal, bytes int64
		var label string
		if labels == nil {
			err = rows.Scan(&ordinal, &bytes)
		} else {
			err = rows.Scan(&ordinal, &label)
		}
		if err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= count || labels == nil && bytes <= 0 {
			return nil, ErrResolveRequired
		}
		out = append(out, bytes)
		if labels != nil {
			*labels = append(*labels, label)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != count {
		return nil, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
