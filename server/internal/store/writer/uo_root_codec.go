package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"strings"
	"unicode/utf8"
)

const uoRootPayloadCap = 65536

// The shared lifetime-root operation envelope. It carries a complete closed
// scalar effect plus its original root/identity binding; it is never permission
// or an Applied facade. UO-03 and later purposes add their own compiled result
// type/codec and one closed dispatcher case rather than widening this shape.
type uoOperationResult struct {
	status                                    uint8 // complete, accepted-pending, partial, terminal failure
	revision, cursor, done, failed, appliedMS int64
	target                                    string
}

type uoRootOperationResult struct {
	root   string
	kind   CommandID
	result uoOperationResult
}

// Purpose closure comes from W's registered descriptors and the sole canonical
// dispatcher. The caller's own parent purpose supplies the concrete kind; no
// domain number range, caller bool or foreign root chooses it.
func preflightUORootOperation(v uoRootOperationResult) error {
	if len(v.root) != 22 || !utf8.ValidString(v.root) || v.result.status < 1 || v.result.status > 4 || v.result.revision < 0 || v.result.cursor < 0 || v.result.done < 0 || v.result.failed < 0 || v.result.appliedMS <= 0 || v.result.target == "" || len(v.result.target) > 128 || !utf8.ValidString(v.result.target) || strings.ContainsRune(v.result.target, 0) {
		return ErrInvalid
	}
	d, err := Describe(v.kind)
	if err != nil {
		return err
	}
	// Complete fixed wire plus exact compiled purpose/root text lengths.
	fixed := len(canonicalResultCodec) + 2 + 8 + 8 + len("root_operation") + 8 + 16 + 8 + 16 + 32 + 1 + 40 + 8 + len(v.result.target)
	if len(d.Name) > uoRootPayloadCap-fixed-len(v.root) || !utf8.ValidString(d.Name) {
		return ErrInvalid
	}
	return nil
}
func writeUORootOperation(e *requestEncoder, i CommandIdentity, q RequestDigest, v uoRootOperationResult) error {
	d, err := Describe(v.kind)
	if err != nil {
		return err
	}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(d.Name)
	e.text("root_operation")
	e.text(v.root)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.bytes([]byte{v.result.status})
	for _, n := range []int64{v.result.revision, v.result.cursor, v.result.done, v.result.failed, v.result.appliedMS} {
		e.number(uint64(n))
	}
	e.text(v.result.target)
	return e.err
}

// This sink only retains the bytes emitted by encodeCanonicalResult. It adds
// no type descriptors, custom checksum format, decoder registration or codec.
type uoRootPayloadSink struct {
	payload []byte
	digest  hash.Hash
}

func (s *uoRootPayloadSink) Write(p []byte) (int, error) {
	if len(p) > uoRootPayloadCap-len(s.payload) {
		return 0, ErrInvalid
	}
	s.payload = append(s.payload, p...)
	return s.digest.Write(p)
}
func (s *uoRootPayloadSink) Sum(p []byte) []byte { return s.digest.Sum(p) }
func (s *uoRootPayloadSink) Reset()              { s.payload = nil; s.digest.Reset() }
func (s *uoRootPayloadSink) Size() int           { return s.digest.Size() }
func (s *uoRootPayloadSink) BlockSize() int      { return s.digest.BlockSize() }

func encodeUORootOperation(i CommandIdentity, q RequestDigest, v uoRootOperationResult) ([]byte, ResultDigest, error) {
	return encodeUORootCanonical(i, q, v)
}

// The caller's exact typed value still passes W's closed result dispatcher.
// No runtime codec registration or opaque result reflection is introduced.
func encodeUORootCanonical(i CommandIdentity, q RequestDigest, v any) ([]byte, ResultDigest, error) {
	if i.Incarnation == [16]byte{} || i.OperationID == [16]byte{} || q == [32]byte{} {
		return nil, ResultDigest{}, ErrInvalid
	}
	sink := &uoRootPayloadSink{digest: sha256.New()}
	encoder := requestEncoder{h: sink}
	if err := encodeCanonicalResult(&encoder, i, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	var digest ResultDigest
	copy(digest[:], sink.Sum(nil))
	return sink.payload, digest, nil
}

func decodeUORootOperation(i CommandIdentity, q RequestDigest, root string, kind CommandID, p []byte, want ResultDigest) (uoOperationResult, error) {
	var zero uoOperationResult
	if len(p) == 0 || len(p) > uoRootPayloadCap || ResultDigest(sha256.Sum256(p)) != want {
		return zero, ErrResolveRequired
	}
	desc, err := Describe(kind)
	if err != nil {
		return zero, ErrResolveRequired
	}
	base := len(canonicalResultCodec) + 2 + 8 + len(desc.Name) + 8 + len("root_operation") + 8 + len(root) + 16 + 8 + 16 + 32
	if len(p) < base+49 {
		return zero, ErrResolveRequired
	}
	tail := p[base:]
	r := uoOperationResult{status: tail[0]}
	fields := []*int64{&r.revision, &r.cursor, &r.done, &r.failed, &r.appliedMS}
	for n, dst := range fields {
		value := binary.BigEndian.Uint64(tail[1+n*8 : 9+n*8])
		if value > uint64(^uint64(0)>>1) {
			return zero, ErrResolveRequired
		}
		*dst = int64(value)
	}
	targetBytes := binary.BigEndian.Uint64(tail[41:49])
	if targetBytes < 1 || targetBytes > 128 || targetBytes != uint64(len(tail)-49) {
		return zero, ErrResolveRequired
	}
	r.target = string(tail[49:])
	encoded, digest, err := encodeUORootOperation(i, q, uoRootOperationResult{root: root, kind: kind, result: r})
	if err != nil || digest != want || !bytes.Equal(encoded, p) {
		return zero, ErrResolveRequired
	}
	return r, nil
}
