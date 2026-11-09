package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"unicode/utf8"
)

// The fixed cleanup slot is bounded by the closed native owner schema
// (reclaim_result_payload BETWEEN 1 AND 4096). This codec enforces the same
// ceiling before the row is written, so a cleanup result can never grow past
// its fixed slot and no child receipt is ever created to hold it.
const uoRootReclaimPayloadCap = 4096

// uoRootReclaimResult is the complete original of one bounded cleanup quantum:
// the ACK floor it observed, the one child it removed (if any), the exact bytes
// refunded, the new cursor/debt and whether another candidate remains.
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

// writeUORootReclaim emits the same canonical envelope as W's sole codec. It is
// reached only through result_digest.go's closed dispatcher: encode and decode
// both route every byte through that one case, so dropping the case breaks the
// round trip instead of silently bypassing it.
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
	if i.Sequence <= v.floor {
		return nil, ResultDigest{}, ErrInvalid
	}
	payload, digest, err := encodeUORootCanonical(i, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	if len(payload) < 1 || len(payload) > uoRootReclaimPayloadCap {
		return nil, ResultDigest{}, ErrInvalid
	}
	return payload, digest, nil
}

func decodeUORootReclaim(i CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (uoRootReclaimResult, error) {
	var zero uoRootReclaimResult
	if len(p) < 73 || len(p) > uoRootReclaimPayloadCap || ResultDigest(sha256.Sum256(p)) != want {
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
