package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// DRAFT IP-001 native bodies only. The dispatcher must first prove CURRENT
// disclosure/mutation authority, supply admitted physical decode memory, compose
// finite steps, and own rollback/COMMIT/resolve. These private results do not
// mint Applied or accept an authority bool, callback, digest or writable handle.
// The dispatcher must also validate the live writer clock against the original
// nowMS before fresh effects; caller timestamps cannot extend a lease/window.
// Owner bootstrap, ACK/reclaim, policy-to-refresh intent/event composition and
// Capture proofs remain integration gates. Product code has no path here.
type ipPolicyValues struct {
	enabled, priority, creditEnabled int64
	region, algorithm, refreshMode   string
}

type ipPolicyRequest struct {
	library                          int64
	provider                         string
	identity                         CommandIdentity
	policyIncarnation                [16]byte
	revision, consentRevision, nowMS int64
	values                           ipPolicyValues
}

type ipPolicyResult struct {
	incarnation         [16]byte
	revision, appliedMS int64
	values              ipPolicyValues
	creditAfterItem     sql.NullInt64
	creditAfterProvider sql.NullString
}

// This is only a bounded copy ledger, NOT a governor grant. The outer adapter
// must charge/retain physical memory before providing it. Every native TEXT
// result below is preceded by a point octet_length in the same W transaction.
type ipPolicyDecode struct{ remaining uint64 }

func (b *ipPolicyDecode) reserve(n int64) error {
	if b == nil || n < 0 || n > 64<<10 || uint64(n) > b.remaining {
		return ErrQuantum
	}
	b.remaining -= uint64(n)
	return nil
}

// Versioned, length-delimited, closed-domain digests. They identify original
// requests/results only; neither this codec nor its digest is authority.
type ipPolicyHash struct{ data []byte }

func (h *ipPolicyHash) integer(v int64) { h.data = binary.BigEndian.AppendUint64(h.data, uint64(v)) }
func (h *ipPolicyHash) bytes(v []byte)  { h.integer(int64(len(v))); h.data = append(h.data, v...) }
func (h *ipPolicyHash) text(v string)   { h.bytes([]byte(v)) }
func (h *ipPolicyHash) values(v ipPolicyValues) {
	h.integer(v.enabled)
	h.integer(v.priority)
	h.integer(v.creditEnabled)
	h.text(v.region)
	h.text(v.algorithm)
	h.text(v.refreshMode)
}
func (r ipPolicyRequest) digest() RequestDigest {
	h := ipPolicyHash{}
	h.text("ip.policy.set.v1")
	h.integer(r.library)
	h.text(r.provider)
	h.bytes(r.policyIncarnation[:])
	h.integer(r.revision)
	h.integer(r.consentRevision)
	h.integer(r.nowMS)
	h.values(r.values)
	return RequestDigest(sha256.Sum256(h.data))
}
func (r ipPolicyResult) digest() [32]byte {
	h := ipPolicyHash{}
	h.text("ip.policy.result.v1")
	h.bytes(r.incarnation[:])
	h.integer(r.revision)
	h.integer(r.appliedMS)
	h.values(r.values)
	if r.creditAfterItem.Valid {
		h.integer(1)
		h.integer(r.creditAfterItem.Int64)
		h.text(r.creditAfterProvider.String)
	} else {
		h.integer(0)
	}
	return sha256.Sum256(h.data)
}
func ipPolicyValidText(v string, max int) bool {
	return len(v) > 0 && len(v) <= max && utf8.ValidString(v)
}
func ipPolicyValidIdentity(v CommandIdentity) bool {
	return v.Sequence > 0 && v.Incarnation != [16]byte{} && v.OperationID != [16]byte{}
}
func (r ipPolicyRequest) valid() bool {
	v := r.values
	return r.library > 0 && ipPolicyValidText(r.provider, 64) && ipPolicyValidIdentity(r.identity) && r.revision > 0 && r.nowMS >= 0 && r.consentRevision > 0 && r.policyIncarnation != [16]byte{} &&
		(v.enabled == 0 || v.enabled == 1) && (v.creditEnabled == 0 || v.creditEnabled == 1) && ipPolicyValidText(v.region, 32) && ipPolicyValidText(v.algorithm, 256) && (v.refreshMode == "fill_missing" || v.refreshMode == "replace_unlocked")
}

func ipPolicyStep(t *commandTurn, id stmt.ID) statementStep { return t.steps[id].statementStep }
func ipPolicyScan(ctx context.Context, t *commandTurn, id stmt.ID, args []any, dest ...any) error {
	r, err := t.queryRow(ctx, ipPolicyStep(t, id), args...)
	if err != nil {
		return err
	}
	return r.Scan(dest...)
}
func ipPolicyOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	r, err := t.exec(ctx, ipPolicyStep(t, id), args...)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ipPolicyConsentFresh(ctx context.Context, t *commandTurn, revision int64, enabled bool) error {
	var current, confirmed int64
	if err := ipPolicyScan(ctx, t, ipPolicyConsent, nil, &current, &confirmed); err != nil {
		return err
	}
	if current != revision || (enabled && confirmed != 1) {
		return ErrConflict
	}
	return nil
}

func ipPolicySetBody(ctx context.Context, t *commandTurn, r ipPolicyRequest, b *ipPolicyDecode) (out ipPolicyResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPPolicySet || !r.valid() {
		return out, ErrInvalid
	}
	key := []any{r.library, r.provider}
	var owner receiptOwnerFacts
	var ci []byte
	if err = ipPolicyScan(ctx, t, ipPolicyOwner, key, &ci, &owner.lastSequence, &owner.acknowledgedSequence); err != nil {
		return out, err
	}
	if len(ci) != 16 {
		return out, ErrResolveRequired
	}
	copy(owner.incarnation[:], ci)
	owner.exists = true
	receiptKey := append(append([]any{}, key...), r.identity.Incarnation[:], r.identity.Sequence)
	var found receiptFacts
	var op, dig, savedDigest []byte
	err = ipPolicyScan(ctx, t, ipPolicyReceiptIdentity, receiptKey, &op, &dig, &savedDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if len(op) != 16 || len(dig) != 32 || len(savedDigest) != 32 {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = IPPolicySet
		found.identity.command = r.identity
		copy(found.identity.command.OperationID[:], op)
		copy(found.identity.digest[:], dig)
		copy(found.resultDigest[:], savedDigest)
	}
	decision, err := classifyReceipt(receiptIdentity{r.identity, r.digest()}, IPPolicySet, owner, found)
	if err != nil {
		return out, err
	}
	if decision == receiptRetired {
		return out, ErrResolveRequired
	}
	if decision == receiptReplay {
		var n int64
		if err = ipPolicyScan(ctx, t, ipPolicyReceiptOctets, receiptKey, &n); err != nil {
			return out, err
		}
		if err = b.reserve(n); err != nil {
			return out, err
		}
		var inc []byte
		if err = ipPolicyScan(ctx, t, ipPolicyReceiptResult, receiptKey, &inc, &out.revision, &out.appliedMS, &out.values.enabled, &out.values.priority, &out.values.region, &out.values.algorithm, &out.values.refreshMode, &out.values.creditEnabled, &out.creditAfterItem, &out.creditAfterProvider); err != nil {
			return out, err
		}
		actual := int64(len(out.values.region) + len(out.values.algorithm) + len(out.values.refreshMode) + len(out.creditAfterProvider.String))
		if n != actual || len(inc) != 16 || out.revision <= 0 || out.appliedMS < 0 || out.creditAfterItem.Valid != out.creditAfterProvider.Valid {
			return out, ErrResolveRequired
		}
		copy(out.incarnation[:], inc)
		if out.digest() != found.resultDigest {
			return out, ErrResolveRequired
		}
		return out, nil
	}
	if decision != receiptFresh {
		return out, ErrResolveRequired
	}
	// Original receipt classification deliberately precedes all fresh CAS.
	if err = ipPolicyConsentFresh(ctx, t, r.consentRevision, r.values.enabled == 1); err != nil {
		return out, err
	}
	var n int64
	if err = ipPolicyScan(ctx, t, ipPolicyOctets, key, &n); err != nil {
		return out, err
	}
	if err = b.reserve(n); err != nil {
		return out, err
	}
	var inc []byte
	if err = ipPolicyScan(ctx, t, ipPolicyCurrent, key, &inc, &out.revision, &out.values.enabled, &out.values.priority, &out.values.region, &out.values.algorithm, &out.values.refreshMode, &out.values.creditEnabled, &out.creditAfterItem, &out.creditAfterProvider); err != nil {
		return out, err
	}
	if n != int64(len(out.values.region)+len(out.values.algorithm)+len(out.values.refreshMode)+len(out.creditAfterProvider.String)) || len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], inc)
	if out.incarnation != r.policyIncarnation || out.revision != r.revision {
		return out, ErrConflict
	}
	if out.values != r.values {
		if out.revision == math.MaxInt64 {
			return out, ErrConflict
		}
		out.revision++
	}
	out.values = r.values
	out.appliedMS = r.nowMS
	if err = ipPolicyOne(ctx, t, ipPolicyUpdate, out.revision, r.values.enabled, r.values.priority, r.values.region, r.values.algorithm, r.values.refreshMode, r.values.creditEnabled, r.identity.Sequence, r.library, r.provider, r.policyIncarnation[:], r.revision, r.identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence); err != nil {
		return out, err
	}
	d := r.digest()
	rd := out.digest()
	err = ipPolicyOne(ctx, t, ipPolicyReceiptInsert, r.library, r.provider, r.identity.Incarnation[:], r.identity.Sequence, r.identity.OperationID[:], d[:], r.nowMS, rd[:], out.incarnation[:], out.revision, out.values.enabled, out.values.priority, out.values.region, out.values.algorithm, out.values.refreshMode, out.values.creditEnabled, ipPolicyNullableInt(out.creditAfterItem), ipPolicyNullableText(out.creditAfterProvider))
	return out, err
}
func ipPolicyNullableInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func ipPolicyNullableText(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}
