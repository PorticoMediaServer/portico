package writer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"
)

// The closed MG recording-intent result: exact saved occurrence identity, the
// pinned guide generation, the padded window, the original options JSON, the
// logical state and both the occurrence and owner revisions. It is never a
// current DTO, a capture claim, an allocation or a physical-byte fact.
type mgRecordingIntentResult struct {
	id, owner, source, channel, programme, generation, state, optionsJSON string
	startMS, endMS, priority, keep, manual, revision, ownerRevision       int64
}

const (
	mgRecordingResultFamily  = "mg_recording"
	mgRecordingResultVariant = "intent"
	mgRecordingResultTextMax = 8192
)

func preflightMGRecordingIntent(v mgRecordingIntentResult) error {
	if !mgRecordingHex(v.id, 64) || !mgRecordingHex(v.owner, 64) || !mgRecordingHex(v.source, 48) ||
		!mgRecordingHex(v.channel, 64) || !mgRecordingHex(v.programme, 64) || !mgRecordingHex(v.generation, 48) ||
		!mgRecordingText(v.optionsJSON, true, 4096) || !mgRecordingRevisionValid(v.revision) ||
		v.ownerRevision < 1 || v.startMS < 0 || v.endMS <= v.startMS ||
		v.priority < -1000 || v.priority > 1000 || (v.keep != 0 && v.keep != 1) || (v.manual != 0 && v.manual != 1) {
		return ErrResolveRequired
	}
	switch v.state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
	default:
		return ErrResolveRequired
	}
	var o mgRecordingOptions
	if mgRecordingDecode(v.optionsJSON, &o) != nil || !o.valid() || o.Priority != v.priority {
		return ErrResolveRequired
	}
	canonical, err := json.Marshal(o)
	if err != nil || string(canonical) != v.optionsJSON {
		return ErrResolveRequired
	}
	return nil
}

func writeMGRecordingIntent(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgRecordingIntentResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgRecordingResultFamily)
	e.text(mgRecordingResultVariant)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.id, v.owner, v.source, v.channel, v.programme, v.generation, v.state, v.optionsJSON} {
		e.text(s)
	}
	for _, n := range []int64{v.startMS, v.endMS, v.priority, v.keep, v.manual, v.revision, v.ownerRevision} {
		e.number(uint64(n))
	}
	return e.err
}

// The complete payload is the canonical result encoding of the exact original
// command. Both the payload and its digest are bounded before any custody row.
func mgRecordingIntentPayload(id CommandIdentity, q RequestDigest, v mgRecordingIntentResult) ([]byte, ResultDigest, error) {
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

type mgRecordingResultReader struct {
	b  []byte
	at int
}

func (r *mgRecordingResultReader) take(n int) ([]byte, error) {
	if n < 0 || r.at < 0 || n > len(r.b)-r.at {
		return nil, ErrResolveRequired
	}
	p := r.b[r.at : r.at+n]
	r.at += n
	return p, nil
}

func (r *mgRecordingResultReader) number() (int64, error) {
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

func (r *mgRecordingResultReader) text() (string, error) {
	n, err := r.number()
	if err != nil || n < 0 || n > mgRecordingResultTextMax {
		return "", ErrResolveRequired
	}
	p, err := r.take(int(n))
	if err != nil || !utf8.Valid(p) {
		return "", ErrResolveRequired
	}
	return string(p), nil
}

func (r *mgRecordingResultReader) prefix(id CommandIdentity, q RequestDigest) error {
	p, err := r.take(len(canonicalResultCodec))
	if err != nil || string(p) != canonicalResultCodec {
		return ErrResolveRequired
	}
	p, err = r.take(2)
	if err != nil || p[0] != 0 || p[1] != 1 {
		return ErrResolveRequired
	}
	name, err := r.text()
	if err != nil || name != mgRecordingResultFamily {
		return ErrResolveRequired
	}
	name, err = r.text()
	if err != nil || name != mgRecordingResultVariant {
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

func decodeMGRecordingIntent(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgRecordingIntentResult, err error) {
	r := &mgRecordingResultReader{b: payload}
	if err = r.prefix(id, q); err != nil {
		return out, err
	}
	for _, dst := range []*string{&out.id, &out.owner, &out.source, &out.channel, &out.programme, &out.generation, &out.state, &out.optionsJSON} {
		if *dst, err = r.text(); err != nil {
			return out, err
		}
	}
	for _, dst := range []*int64{&out.startMS, &out.endMS, &out.priority, &out.keep, &out.manual, &out.revision, &out.ownerRevision} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	if r.at != len(payload) {
		return mgRecordingIntentResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgRecordingIntentResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGRecordingIntent(out); err != nil {
		return mgRecordingIntentResult{}, err
	}
	again, _, err := mgRecordingIntentPayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgRecordingIntentResult{}, ErrResolveRequired
	}
	return out, nil
}
