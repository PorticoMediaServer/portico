package writer

import (
	"errors"
	"unicode/utf8"
)

// The saved admin result of one DVR policy command. It is the exact observed
// singleton values after the update, never a current DTO: a later update or a
// recording change never rewrites these saved scalars. Both commands share one
// compiled schema so a cross-kind payload cannot be relabelled.
const (
	mgDVRPolicyDefaultsKind = "dvr-policy-defaults"
	mgDVRPolicyStorageKind  = "dvr-policy-storage"
	mgDVRPolicyTemplateMax  = 512
)

type mgDVRPolicyResult struct {
	kind                                                     string
	revision                                                 int64
	beforeSeconds, afterSeconds, retentionDays, episodeLimit int64
	conflict, showTemplate, movieTemplate                    string
	floorBytes, capBytes                                     int64
}

func (s mgDVRDefaultsSnapshot) result() mgDVRPolicyResult {
	return mgDVRPolicyResult{
		kind: mgDVRPolicyDefaultsKind, revision: s.revision,
		beforeSeconds: s.beforeSeconds, afterSeconds: s.afterSeconds,
		retentionDays: s.retentionDays, episodeLimit: s.episodeLimit,
		conflict: s.conflict, showTemplate: s.showTemplate, movieTemplate: s.movieTemplate,
	}
}
func (s mgDVRStorageSnapshot) result() mgDVRPolicyResult {
	return mgDVRPolicyResult{
		kind: mgDVRPolicyStorageKind, revision: s.revision,
		retentionDays: s.policy.retentionDays, episodeLimit: s.policy.episodeLimit,
		floorBytes: s.policy.floorBytes, capBytes: s.policy.capBytes,
	}
}

// A saved result must be exactly one complete variant. The shared defaults and
// storage retention/episode fields are the only overlapping columns; every
// field that belongs to the other variant must be a zero value.
func preflightMGDVRPolicyResult(v mgDVRPolicyResult) error {
	if v.revision < 1 {
		return invalid("mg_dvr_policy_result")
	}
	switch v.kind {
	case mgDVRPolicyDefaultsKind:
		d := mgDVRDefaultsSnapshot{v.revision, v.beforeSeconds, v.afterSeconds, v.retentionDays, v.episodeLimit, v.conflict, v.showTemplate, v.movieTemplate}
		if !d.valid() || v.floorBytes != 0 || v.capBytes != 0 {
			return invalid("mg_dvr_policy_result")
		}
	case mgDVRPolicyStorageKind:
		s := mgDVRStorageSnapshot{v.revision, mgDVRStorageDesired{v.retentionDays, v.episodeLimit, v.floorBytes, v.capBytes}}
		if !s.valid() || v.beforeSeconds != 0 || v.afterSeconds != 0 || v.conflict != "" || v.showTemplate != "" || v.movieTemplate != "" {
			return invalid("mg_dvr_policy_result")
		}
	default:
		return invalid("mg_dvr_policy_result")
	}
	return nil
}

func writeMGDVRPolicyResult(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgDVRPolicyResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgPersonalResultFamily)
	e.text(v.kind)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	e.number(uint64(v.revision))
	e.number(uint64(v.beforeSeconds))
	e.number(uint64(v.afterSeconds))
	e.number(uint64(v.retentionDays))
	e.number(uint64(v.episodeLimit))
	e.text(v.conflict)
	e.text(v.showTemplate)
	e.text(v.movieTemplate)
	e.number(uint64(v.floorBytes))
	e.number(uint64(v.capBytes))
	return e.err
}

// The complete payload is the canonical result encoding of the exact original
// command. Both the payload and its digest are bounded before any custody row.
func mgDVRPolicyResultPayload(id CommandIdentity, q RequestDigest, v mgDVRPolicyResult) ([]byte, ResultDigest, error) {
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

// Recording-folder templates are longer than the personal result texts, so this
// bounded reader is local to the policy variant instead of widening a shared
// limit.
func mgDVRPolicyText(r *mgPersonalResultReader) (string, error) {
	n, err := r.number()
	if err != nil || n < 0 || n > mgDVRPolicyTemplateMax {
		return "", ErrResolveRequired
	}
	p, err := r.take(int(n))
	if err != nil || !utf8.Valid(p) {
		return "", ErrResolveRequired
	}
	return string(p), nil
}

func decodeMGDVRPolicyResult(id CommandIdentity, q RequestDigest, kind string, payload []byte, digest ResultDigest) (out mgDVRPolicyResult, err error) {
	if kind != mgDVRPolicyDefaultsKind && kind != mgDVRPolicyStorageKind {
		return out, ErrResolveRequired
	}
	r := &mgPersonalResultReader{b: payload}
	if err = r.prefix(id, q, kind); err != nil {
		return out, err
	}
	out.kind = kind
	for _, dst := range []*int64{&out.revision, &out.beforeSeconds, &out.afterSeconds, &out.retentionDays, &out.episodeLimit} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	if out.conflict, err = mgDVRPolicyText(r); err != nil {
		return out, err
	}
	if out.showTemplate, err = mgDVRPolicyText(r); err != nil {
		return out, err
	}
	if out.movieTemplate, err = mgDVRPolicyText(r); err != nil {
		return out, err
	}
	for _, dst := range []*int64{&out.floorBytes, &out.capBytes} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	if r.at != len(payload) {
		return mgDVRPolicyResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgDVRPolicyResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGDVRPolicyResult(out); err != nil {
		return mgDVRPolicyResult{}, err
	}
	again, _, err := mgDVRPolicyResultPayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgDVRPolicyResult{}, ErrResolveRequired
	}
	return out, nil
}
