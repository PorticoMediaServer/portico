package writer

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// The saved scalar of one accepted bounded reconciliation. It carries the exact
// resulting work custody (phase/cursor/job revision/lease attempts), the
// source-generation and rule revision the frame verified, and for a scheduled
// page the ordered intent ids it actually changed. It is never a current DTO, a
// capture claim, an allocation, a lease token for future work or a physical
// byte fact.
type mgReconcileResult struct {
	variant                                              string
	rule, owner, generation, phase, cursor               string
	ruleRevision, workRevision                           int64
	jobOwner, jobGeneration, jobExpires, attempts, dueMS int64
	hasDue, pending                                      bool
	units, ownerRevision                                 int64
	ids                                                  []string
}

const (
	mgReconcileResultFamily  = "mg_reconcile"
	mgReconcileResultVariant = "rule"
	mgScheduledResultVariant = "scheduled"
	mgReconcileResultTextMax = 8192
)

func preflightMGReconcile(v mgReconcileResult) error {
	if v.variant != mgReconcileResultVariant && v.variant != mgScheduledResultVariant {
		return ErrResolveRequired
	}
	if !mgRecordingHex(v.rule, 64) || !mgRecordingHex(v.owner, 64) || !mgRecordingHex(v.generation, 48) ||
		!mgReconcileWorkPhase(v.phase) || !mgRecordingText(v.cursor, false, mgReconcileCursorMax) ||
		v.ruleRevision < 1 || v.workRevision < 1 || v.jobOwner < 0 || v.jobGeneration < 0 || v.jobExpires < 0 ||
		v.attempts < 0 || v.attempts > mgReconcileAttemptsMax || v.dueMS < 0 ||
		v.units < 0 || v.units > mgReconcileWorkLimit || len(v.ids) > mgReconcileWorkLimit {
		return ErrResolveRequired
	}
	if v.phase == "idle" {
		if v.hasDue || v.jobOwner != 0 || v.jobExpires != 0 || v.cursor != "" || v.pending {
			return ErrResolveRequired
		}
	} else if !v.hasDue || v.jobOwner < 1 || v.jobExpires < 1 {
		return ErrResolveRequired
	}
	for _, id := range v.ids {
		if !mgRecordingHex(id, 64) {
			return ErrResolveRequired
		}
	}
	switch v.variant {
	case mgReconcileResultVariant:
		if v.ownerRevision != 0 || len(v.ids) != 0 {
			return ErrResolveRequired
		}
	case mgScheduledResultVariant:
		if v.ownerRevision < 1 || int64(len(v.ids)) != v.units {
			return ErrResolveRequired
		}
		if v.pending {
			if v.phase != "discover" || v.cursor == "" || v.units < 1 {
				return ErrResolveRequired
			}
		} else if v.phase != "idle" || v.cursor != "" {
			return ErrResolveRequired
		}
	}
	return nil
}

func writeMGReconcile(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgReconcileResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgReconcileResultFamily)
	e.text(v.variant)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.rule, v.owner, v.generation, v.phase, v.cursor} {
		e.text(s)
	}
	for _, n := range []int64{v.ruleRevision, v.workRevision, v.jobOwner, v.jobGeneration, v.jobExpires,
		v.attempts, v.dueMS, v.units, v.ownerRevision} {
		e.number(uint64(n))
	}
	e.flag(v.hasDue)
	e.flag(v.pending)
	e.number(uint64(len(v.ids)))
	for _, s := range v.ids {
		e.text(s)
	}
	return e.err
}

func mgReconcilePayload(id CommandIdentity, q RequestDigest, v mgReconcileResult) ([]byte, ResultDigest, error) {
	d, err := canonicalResultDigest(id, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, id, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() < 1 || b.Len() > uoRootPayloadCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), d, nil
}

type mgReconcileReader struct {
	b  []byte
	at int
}

func (r *mgReconcileReader) take(n int) ([]byte, error) {
	if n < 0 || r.at < 0 || n > len(r.b)-r.at {
		return nil, ErrResolveRequired
	}
	p := r.b[r.at : r.at+n]
	r.at += n
	return p, nil
}

func (r *mgReconcileReader) number() (int64, error) {
	p, err := r.take(8)
	if err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint64(p)
	if n > math.MaxInt64 {
		return 0, ErrResolveRequired
	}
	return int64(n), nil
}

func (r *mgReconcileReader) flag() (bool, error) {
	p, err := r.take(1)
	if err != nil {
		return false, err
	}
	if p[0] != 0 && p[0] != 1 {
		return false, ErrResolveRequired
	}
	return p[0] == 1, nil
}

func (r *mgReconcileReader) text() (string, error) {
	n, err := r.number()
	if err != nil || n < 0 || n > mgReconcileResultTextMax {
		return "", ErrResolveRequired
	}
	p, err := r.take(int(n))
	if err != nil || !utf8.Valid(p) {
		return "", ErrResolveRequired
	}
	return string(p), nil
}

func (r *mgReconcileReader) prefix(id CommandIdentity, q RequestDigest) error {
	p, err := r.take(len(canonicalResultCodec))
	if err != nil || string(p) != canonicalResultCodec {
		return ErrResolveRequired
	}
	p, err = r.take(2)
	if err != nil || p[0] != 0 || p[1] != 1 {
		return ErrResolveRequired
	}
	name, err := r.text()
	if err != nil || name != mgReconcileResultFamily {
		return ErrResolveRequired
	}
	return nil
}

func (r *mgReconcileReader) identity(id CommandIdentity, q RequestDigest) error {
	p, err := r.take(16)
	if err != nil || string(p) != string(id.Incarnation[:]) {
		return ErrResolveRequired
	}
	n, err := r.number()
	if err != nil || n != id.Sequence {
		return ErrResolveRequired
	}
	p, err = r.take(16)
	if err != nil || string(p) != string(id.OperationID[:]) {
		return ErrResolveRequired
	}
	p, err = r.take(32)
	if err != nil || string(p) != string(q[:]) {
		return ErrResolveRequired
	}
	return nil
}

func decodeMGReconcile(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgReconcileResult, err error) {
	r := &mgReconcileReader{b: payload}
	if err = r.prefix(id, q); err != nil {
		return out, err
	}
	if out.variant, err = r.text(); err != nil {
		return out, err
	}
	if err = r.identity(id, q); err != nil {
		return out, err
	}
	for _, dst := range []*string{&out.rule, &out.owner, &out.generation, &out.phase, &out.cursor} {
		if *dst, err = r.text(); err != nil {
			return out, err
		}
	}
	for _, dst := range []*int64{&out.ruleRevision, &out.workRevision, &out.jobOwner, &out.jobGeneration,
		&out.jobExpires, &out.attempts, &out.dueMS, &out.units, &out.ownerRevision} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	if out.hasDue, err = r.flag(); err != nil {
		return out, err
	}
	if out.pending, err = r.flag(); err != nil {
		return out, err
	}
	n, err := r.number()
	if err != nil || n < 0 || n > mgReconcileWorkLimit {
		return mgReconcileResult{}, ErrResolveRequired
	}
	for j := int64(0); j < n; j++ {
		id, err := r.text()
		if err != nil {
			return mgReconcileResult{}, err
		}
		out.ids = append(out.ids, id)
	}
	if r.at != len(payload) {
		return mgReconcileResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgReconcileResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGReconcile(out); err != nil {
		return mgReconcileResult{}, err
	}
	again, _, err := mgReconcilePayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgReconcileResult{}, ErrResolveRequired
	}
	return out, nil
}
