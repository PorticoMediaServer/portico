package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"unicode/utf8"
)

const mpRepeatPayloadCap = 2048

// Complete original scalar result. PhysicalPending means an exact durable stop
// obligation exists; it never asserts physical exit, pin release or byte refund.
type mpQueueRepeatValue struct {
	Queue, Repeat, CanceledToken, Media, StopRequest    string
	Revision, Custody, UpdatedMS, Generation, AppliedMS int64
	Changed, PhysicalPending                            bool
}

type mpWorkerResult struct {
	Outcome ReceiptOutcome
	Value   mpQueueRepeatValue
	Digest  ResultDigest
}

func preflightMPRepeatValue(v mpQueueRepeatValue) error {
	for _, s := range []string{v.Queue, v.Repeat, v.CanceledToken, v.Media, v.StopRequest} {
		if len(s) > 128 || !utf8.ValidString(s) {
			return ErrResolveRequired
		}
	}
	if v.Queue == "" || (v.Repeat != "off" && v.Repeat != "one" && v.Repeat != "all") || v.Revision <= 0 || v.Custody < 0 || v.UpdatedMS < 0 || v.AppliedMS <= 0 || v.Generation < 0 {
		return ErrResolveRequired
	}
	if v.CanceledToken == "" {
		if v.Media != "" || v.StopRequest != "" || v.Generation != 0 || v.PhysicalPending {
			return ErrResolveRequired
		}
	} else if !v.Changed || v.Media == "" || v.StopRequest == "" || v.Generation <= 0 {
		return ErrResolveRequired
	}
	return nil
}

func writeMPRepeatValue(e *requestEncoder, id CommandIdentity, q RequestDigest, v mpQueueRepeatValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_queue_repeat")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.Queue, v.Repeat, v.CanceledToken, v.Media, v.StopRequest} {
		e.text(s)
	}
	for _, n := range []int64{v.Revision, v.Custody, v.UpdatedMS, v.Generation, v.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(v.Changed)
	e.flag(v.PhysicalPending)
	return e.err
}

// Collect the sole W codec's bytes, not a parallel domain serializer. Its fixed
// preflight bounds allocation before Write. Hash methods satisfy W's encoder.
type mpCodecBuffer struct{ bytes.Buffer }

func (b *mpCodecBuffer) Sum(p []byte) []byte {
	h := sha256.Sum256(b.Bytes())
	return append(p, h[:]...)
}
func (*mpCodecBuffer) Size() int      { return sha256.Size }
func (*mpCodecBuffer) BlockSize() int { return sha256.BlockSize }

func (b *mpCodecBuffer) Write(p []byte) (int, error) { return b.Buffer.Write(p) }
func mpRepeatPayload(id CommandIdentity, q RequestDigest, v mpQueueRepeatValue) ([]byte, ResultDigest, error) {
	digest, err := canonicalResultDigest(id, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, id, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() > mpRepeatPayloadCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), digest, nil
}

// Closed decoder for this one result variant. Header, identity and trailing
// bytes are verified by re-encoding through W after reading bounded scalars.
func decodeMPRepeatPayload(id CommandIdentity, q RequestDigest, p []byte, digest ResultDigest) (mpQueueRepeatValue, error) {
	var v mpQueueRepeatValue
	if len(p) == 0 || len(p) > mpRepeatPayloadCap {
		return v, ErrResolveRequired
	}
	r := bytes.NewReader(p)
	prefix := make([]byte, len(canonicalResultCodec)+2)
	if _, e := io.ReadFull(r, prefix); e != nil || !bytes.Equal(prefix, append([]byte(canonicalResultCodec), 0, 1)) {
		return v, ErrResolveRequired
	}
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 128 || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return string(b), e
	}
	name, e := text()
	if e != nil || name != "mp_queue_repeat" {
		return v, ErrResolveRequired
	}
	var inc, op [16]byte
	var seq uint64
	var request [32]byte
	if _, e = io.ReadFull(r, inc[:]); e != nil {
		return v, ErrResolveRequired
	}
	if binary.Read(r, binary.BigEndian, &seq) != nil {
		return v, ErrResolveRequired
	}
	if _, e = io.ReadFull(r, op[:]); e != nil {
		return v, ErrResolveRequired
	}
	if _, e = io.ReadFull(r, request[:]); e != nil {
		return v, ErrResolveRequired
	}
	if inc != id.Incarnation || op != id.OperationID || seq != uint64(id.Sequence) || request != [32]byte(q) {
		return v, ErrResolveRequired
	}
	for _, s := range []*string{&v.Queue, &v.Repeat, &v.CanceledToken, &v.Media, &v.StopRequest} {
		*s, e = text()
		if e != nil {
			return mpQueueRepeatValue{}, ErrResolveRequired
		}
	}
	for _, n := range []*int64{&v.Revision, &v.Custody, &v.UpdatedMS, &v.Generation, &v.AppliedMS} {
		if binary.Read(r, binary.BigEndian, n) != nil {
			return mpQueueRepeatValue{}, ErrResolveRequired
		}
	}
	flag := func() (bool, error) {
		b, e := r.ReadByte()
		if e != nil || b > 1 {
			return false, ErrResolveRequired
		}
		return b == 1, nil
	}
	if v.Changed, e = flag(); e != nil {
		return mpQueueRepeatValue{}, e
	}
	if v.PhysicalPending, e = flag(); e != nil || r.Len() != 0 {
		return mpQueueRepeatValue{}, ErrResolveRequired
	}
	actual, got, e := mpRepeatPayload(id, q, v)
	if e != nil || got != digest || !bytes.Equal(actual, p) {
		return mpQueueRepeatValue{}, ErrResolveRequired
	}
	return v, nil
}
