package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"unicode/utf8"
)

type uoRootReclaimResult struct {
	root                                                                                                                string
	floor, oldCursor, removedSequence, removedCount, removedBytes, nextCursor, remainingRows, remainingBytes, appliedMS int64
	noNext                                                                                                              bool
}

func preflightUORootReclaim(v uoRootReclaimResult) error {
	if len(v.root) != 22 || !utf8.ValidString(v.root) || v.floor < 0 || v.oldCursor < 0 || v.oldCursor > v.floor || v.removedSequence < 0 || v.removedBytes < 0 || v.nextCursor < v.oldCursor || v.nextCursor > v.floor || v.remainingRows < 0 || v.remainingBytes < 0 || (v.remainingRows == 0) != (v.remainingBytes == 0) || v.noNext != (v.remainingRows == 0) || v.appliedMS < 0 {
		return ErrResolveRequired
	}
	switch v.removedCount {
	case 0:
		if v.removedSequence != 0 || v.removedBytes != 0 || v.nextCursor != v.oldCursor || !v.noNext {
			return ErrResolveRequired
		}
	case 1:
		if v.removedSequence <= v.oldCursor || v.removedSequence > v.floor || v.removedBytes <= 0 || v.nextCursor != v.removedSequence {
			return ErrResolveRequired
		}
	default:
		return ErrResolveRequired
	}
	return nil
}
func writeUORootReclaim(e *requestEncoder, i CommandIdentity, q RequestDigest, v uoRootReclaimResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("uo_root_receipt_reclaim")
	e.text("root_reclaim")
	e.text(v.root)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	for _, n := range []int64{v.floor, v.oldCursor, v.removedSequence, v.removedCount, v.removedBytes, v.nextCursor, v.remainingRows, v.remainingBytes, v.appliedMS} {
		e.number(uint64(n))
	}
	e.flag(v.noNext)
	return e.err
}
func encodeUORootReclaim(i CommandIdentity, q RequestDigest, v uoRootReclaimResult) ([]byte, ResultDigest, error) {
	if i.Incarnation == [16]byte{} || i.OperationID == [16]byte{} || q == [32]byte{} || i.Sequence <= v.floor {
		return nil, ResultDigest{}, ErrInvalid
	}
	sink := &uoRootPayloadSink{digest: sha256.New()}
	e := requestEncoder{h: sink}
	if err := encodeCanonicalResult(&e, i, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if len(sink.payload) > 4096 {
		return nil, ResultDigest{}, ErrInvalid
	}
	var digest ResultDigest
	copy(digest[:], sink.Sum(nil))
	return sink.payload, digest, nil
}
func decodeUORootReclaim(i CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (uoRootReclaimResult, error) {
	var zero uoRootReclaimResult
	if len(p) < 73 || len(p) > 4096 || ResultDigest(sha256.Sum256(p)) != want {
		return zero, ErrResolveRequired
	}
	tail := p[len(p)-73:]
	v := uoRootReclaimResult{root: root}
	for n, dst := range []*int64{&v.floor, &v.oldCursor, &v.removedSequence, &v.removedCount, &v.removedBytes, &v.nextCursor, &v.remainingRows, &v.remainingBytes, &v.appliedMS} {
		value := binary.BigEndian.Uint64(tail[n*8 : n*8+8])
		if value > uint64(^uint64(0)>>1) {
			return zero, ErrResolveRequired
		}
		*dst = int64(value)
	}
	if tail[72] > 1 {
		return zero, ErrResolveRequired
	}
	v.noNext = tail[72] == 1
	encoded, digest, err := encodeUORootReclaim(i, q, v)
	if err != nil || digest != want || !bytes.Equal(p, encoded) {
		return zero, ErrResolveRequired
	}
	return v, nil
}
