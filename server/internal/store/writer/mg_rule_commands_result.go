package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// The saved scalar result of one accepted rule save/delete. It carries the
// native row identity/revision and a digest of the exact stored configuration,
// never the 384 KiB configuration text: the bounded lifetime-root receipt is the
// replay store, while MG-25 reads the complete native row under current
// authority. It is not a current DTO: later edits never rewrite this value.
type mgRuleResult struct {
	id, owner, source, series, title, channel string
	revision, updated, configBytes            int64
	enabled, deleted                          bool
	configDigest                              [32]byte
}

const (
	mgRuleResultFamily  = "mg_rule"
	mgRuleResultTextMax = 512
	mgRuleConfigPrefix  = "portico-mg-rule-config-sha256-v1\x00"
)

func mgRuleConfigDigest(config string) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(mgRuleConfigPrefix))
	_, _ = h.Write([]byte(config))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func mgRuleResultOf(f mgRuleFacts) mgRuleResult {
	return mgRuleResult{
		id: f.id, owner: f.owner, source: f.source, series: f.series, title: f.title, channel: f.channel,
		revision: f.revision, updated: f.updated, enabled: f.enabled == 1, deleted: f.deleted == 1,
		configBytes: int64(len(f.config)), configDigest: mgRuleConfigDigest(f.config),
	}
}

func preflightMGRuleResult(v mgRuleResult) error {
	if !mgRecordingHex(v.id, 64) || !mgRecordingHex(v.owner, 64) || !mgRecordingHex(v.source, 48) ||
		!mgRecordingText(v.series, false, 512) || !mgRecordingText(v.title, false, 512) ||
		!mgRecordingText(v.channel, false, 64) || (v.series == "" && v.title == "") ||
		v.revision < 1 || v.updated < 0 || v.configBytes < 1 || v.configBytes > mgRuleMaxConfigBytes ||
		v.configDigest == ([32]byte{}) || (v.deleted && v.enabled) {
		return ErrResolveRequired
	}
	return nil
}

func writeMGRuleResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v mgRuleResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgRuleResultFamily)
	e.text("rule")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	for _, s := range []string{v.id, v.owner, v.source, v.series, v.title, v.channel} {
		e.text(s)
	}
	e.number(uint64(v.revision))
	e.number(uint64(v.updated))
	e.number(uint64(v.configBytes))
	e.flag(v.enabled)
	e.flag(v.deleted)
	e.bytes(v.configDigest[:])
	return e.err
}

// The complete payload is the canonical result encoding of the exact original
// command. Both the payload and its digest are bounded before any custody row.
func mgRulePayload(id CommandIdentity, q RequestDigest, v mgRuleResult) ([]byte, ResultDigest, error) {
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

type mgRuleResultReader struct {
	b  []byte
	at int
}

func (r *mgRuleResultReader) take(n int) ([]byte, error) {
	if n < 0 || r.at < 0 || n > len(r.b)-r.at {
		return nil, ErrResolveRequired
	}
	p := r.b[r.at : r.at+n]
	r.at += n
	return p, nil
}
func (r *mgRuleResultReader) number() (int64, error) {
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
func (r *mgRuleResultReader) flag() (bool, error) {
	p, err := r.take(1)
	if err != nil {
		return false, err
	}
	if p[0] != 0 && p[0] != 1 {
		return false, ErrResolveRequired
	}
	return p[0] == 1, nil
}
func (r *mgRuleResultReader) text(max int) (string, error) {
	n, err := r.number()
	if err != nil || n < 0 || n > int64(max) {
		return "", ErrResolveRequired
	}
	p, err := r.take(int(n))
	if err != nil || !utf8.Valid(p) {
		return "", ErrResolveRequired
	}
	return string(p), nil
}

func (r *mgRuleResultReader) prefix(id CommandIdentity, q RequestDigest, family string) error {
	p, err := r.take(len(canonicalResultCodec))
	if err != nil || string(p) != canonicalResultCodec {
		return ErrResolveRequired
	}
	p, err = r.take(2)
	if err != nil || p[0] != 0 || p[1] != 1 {
		return ErrResolveRequired
	}
	name, err := r.text(mgRuleResultTextMax)
	if err != nil || name != mgRuleResultFamily {
		return ErrResolveRequired
	}
	name, err = r.text(mgRuleResultTextMax)
	if err != nil || name != family {
		return ErrResolveRequired
	}
	p, err = r.take(16)
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

func decodeMGRule(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgRuleResult, err error) {
	r := &mgRuleResultReader{b: payload}
	if err = r.prefix(id, q, "rule"); err != nil {
		return out, err
	}
	for _, dst := range []*string{&out.id, &out.owner, &out.source, &out.series, &out.title, &out.channel} {
		if *dst, err = r.text(512); err != nil {
			return out, err
		}
	}
	if out.revision, err = r.number(); err != nil {
		return out, err
	}
	if out.updated, err = r.number(); err != nil {
		return out, err
	}
	if out.configBytes, err = r.number(); err != nil {
		return out, err
	}
	if out.enabled, err = r.flag(); err != nil {
		return out, err
	}
	if out.deleted, err = r.flag(); err != nil {
		return out, err
	}
	p, err := r.take(32)
	if err != nil {
		return out, err
	}
	copy(out.configDigest[:], p)
	if r.at != len(payload) {
		return mgRuleResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgRuleResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGRuleResult(out); err != nil {
		return mgRuleResult{}, err
	}
	again, _, err := mgRulePayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgRuleResult{}, ErrResolveRequired
	}
	return out, nil
}
