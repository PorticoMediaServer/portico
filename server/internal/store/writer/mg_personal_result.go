package writer

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

// The closed MG personal result variants. A channel preference result is the
// exact saved revision and sparse flags; a reminder result is the saved/deleted
// intent row including its copied start and the generated G reference kind.
// Neither is a current DTO: guide replacement or a later update never rewrites
// these saved values.
type mgPersonalPreferenceResult struct {
	source, channel  string
	revision         int64
	favorite, hidden bool
}
type mgPersonalReminderResult struct {
	id, owner, kind, source, channel, generation, programme, start string
	externalGeneration, libraryGeneration                          string
	revision                                                       int64
}

const mgPersonalResultTextMax = 256

// A library preference's public source is "library:"+channel, so a legal
// 256-byte library channel derives a 264-byte source. The request already
// accepts that channel; the saved result is held to the same derived bound
// instead of a 256-byte cap that would reject an input the command validated.
const (
	mgPersonalLibraryPrefix       = "library:"
	mgPersonalPreferenceSourceMax = mgPersonalResultTextMax + len(mgPersonalLibraryPrefix)
)

func mgPersonalResultText(s string) bool {
	return mgPersonalText(s)
}

// A preference source is either a live source/member ID (bounded by the common
// text cap) or the exact "library:"+channel derivation of this same row.
func mgPersonalPreferenceSource(source, channel string) bool {
	if !mgPersonalResultText(channel) {
		return false
	}
	if strings.HasPrefix(source, mgPersonalLibraryPrefix) {
		return source == mgPersonalLibraryPrefix+channel
	}
	return len(source) <= mgPersonalResultTextMax && mgPersonalResultText(source)
}

func preflightMGPersonalPreference(v mgPersonalPreferenceResult) error {
	if !mgPersonalPreferenceSource(v.source, v.channel) || v.revision < 1 {
		return ErrResolveRequired
	}
	return nil
}

func preflightMGPersonalReminder(v mgPersonalReminderResult) error {
	if len(v.id) != 64 || len(v.owner) != 64 || !mgPersonalText(v.id) || !mgPersonalText(v.owner) ||
		!mgPersonalResultText(v.source) || !mgPersonalResultText(v.channel) || !mgPersonalResultText(v.generation) ||
		!mgPersonalResultText(v.programme) || len(v.start) > 64 || v.start == "" || !utf8.ValidString(v.start) ||
		v.revision < 1 {
		return ErrResolveRequired
	}
	switch v.kind {
	case string(mgPersonalLive):
		if v.externalGeneration != v.generation || v.libraryGeneration != "" {
			return ErrResolveRequired
		}
	case string(mgPersonalLibrary):
		if v.libraryGeneration != v.generation || v.externalGeneration != "" {
			return ErrResolveRequired
		}
	default:
		return ErrResolveRequired
	}
	return nil
}

const mgPersonalResultFamily = "mg_personal"

func writeMGPersonalPreference(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgPersonalPreferenceResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgPersonalResultFamily)
	e.text("channel-preference")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	e.text(v.source)
	e.text(v.channel)
	e.number(uint64(v.revision))
	e.flag(v.favorite)
	e.flag(v.hidden)
	return e.err
}

func writeMGPersonalReminder(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgPersonalReminderResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgPersonalResultFamily)
	e.text("reminder")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	e.text(v.id)
	e.text(v.owner)
	e.text(v.kind)
	e.text(v.source)
	e.text(v.channel)
	e.text(v.generation)
	e.text(v.programme)
	e.text(v.start)
	e.text(v.externalGeneration)
	e.text(v.libraryGeneration)
	e.number(uint64(v.revision))
	return e.err
}

// The complete payload is the canonical result encoding of the exact original
// command. Both the payload and its digest are bounded before any custody row.
func mgPersonalPreferencePayload(id CommandIdentity, q RequestDigest, v mgPersonalPreferenceResult) ([]byte, ResultDigest, error) {
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

func mgPersonalReminderPayload(id CommandIdentity, q RequestDigest, v mgPersonalReminderResult) ([]byte, ResultDigest, error) {
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

type mgPersonalResultReader struct {
	b  []byte
	at int
}

func (r *mgPersonalResultReader) take(n int) ([]byte, error) {
	if n < 0 || r.at < 0 || n > len(r.b)-r.at {
		return nil, ErrResolveRequired
	}
	p := r.b[r.at : r.at+n]
	r.at += n
	return p, nil
}
func (r *mgPersonalResultReader) number() (int64, error) {
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
func (r *mgPersonalResultReader) flag() (bool, error) {
	p, err := r.take(1)
	if err != nil {
		return false, err
	}
	if p[0] != 0 && p[0] != 1 {
		return false, ErrResolveRequired
	}
	return p[0] == 1, nil
}
func (r *mgPersonalResultReader) text() (string, error) {
	n, err := r.number()
	if err != nil || n < 0 || n > int64(mgPersonalPreferenceSourceMax) {
		return "", ErrResolveRequired
	}
	p, err := r.take(int(n))
	if err != nil || !utf8.Valid(p) {
		return "", ErrResolveRequired
	}
	return string(p), nil
}

func (r *mgPersonalResultReader) prefix(id CommandIdentity, q RequestDigest, family string) error {
	p, err := r.take(len(canonicalResultCodec))
	if err != nil || string(p) != canonicalResultCodec {
		return ErrResolveRequired
	}
	p, err = r.take(2)
	if err != nil || p[0] != 0 || p[1] != 1 {
		return ErrResolveRequired
	}
	name, err := r.text()
	if err != nil || name != mgPersonalResultFamily {
		return ErrResolveRequired
	}
	name, err = r.text()
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

func decodeMGPersonalPreference(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgPersonalPreferenceResult, err error) {
	r := &mgPersonalResultReader{b: payload}
	if err = r.prefix(id, q, "channel-preference"); err != nil {
		return out, err
	}
	if out.source, err = r.text(); err != nil {
		return out, err
	}
	if out.channel, err = r.text(); err != nil {
		return out, err
	}
	if out.revision, err = r.number(); err != nil {
		return out, err
	}
	if out.favorite, err = r.flag(); err != nil {
		return out, err
	}
	if out.hidden, err = r.flag(); err != nil {
		return out, err
	}
	if r.at != len(payload) {
		return mgPersonalPreferenceResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgPersonalPreferenceResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGPersonalPreference(out); err != nil {
		return mgPersonalPreferenceResult{}, err
	}
	again, _, err := mgPersonalPreferencePayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgPersonalPreferenceResult{}, ErrResolveRequired
	}
	return out, nil
}

func decodeMGPersonalReminder(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgPersonalReminderResult, err error) {
	r := &mgPersonalResultReader{b: payload}
	if err = r.prefix(id, q, "reminder"); err != nil {
		return out, err
	}
	for _, dst := range []*string{&out.id, &out.owner, &out.kind, &out.source, &out.channel, &out.generation, &out.programme, &out.start, &out.externalGeneration, &out.libraryGeneration} {
		if *dst, err = r.text(); err != nil {
			return out, err
		}
	}
	if out.revision, err = r.number(); err != nil {
		return out, err
	}
	if r.at != len(payload) {
		return mgPersonalReminderResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgPersonalReminderResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGPersonalReminder(out); err != nil {
		return mgPersonalReminderResult{}, err
	}
	again, _, err := mgPersonalReminderPayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgPersonalReminderResult{}, ErrResolveRequired
	}
	return out, nil
}
