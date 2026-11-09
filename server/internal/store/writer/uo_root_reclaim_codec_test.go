package writer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func uoRootReclaimCodecValue() uoRootReclaimResult {
	return uoRootReclaimResult{root: "abcdefghijklmnopqrstuv", floor: 4, oldCursor: 1, removedSequence: 2, removedCount: 1, removedBytes: 160, nextCursor: 2, remainingRows: 1, remainingBytes: 300, appliedMS: 100}
}

// TestUORootReclaimSoleCanonicalCodec proves the cleanup result is encoded by
// W's closed result_digest.go case, not a private writer: the reclaim encoder,
// the canonical root dispatcher and the plain canonical digest all agree byte
// for byte, and the decoder returns the identical original.
func TestUORootReclaimSoleCanonicalCodec(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 5, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootReclaimCodecValue()
	p, d, err := encodeUORootReclaim(i, q, v)
	if err != nil || !bytes.HasPrefix(p, []byte(canonicalResultCodec)) || len(p) > uoRootReclaimPayloadCap {
		t.Fatal(len(p), err)
	}
	canonical, canonicalDigest, err := encodeUORootCanonical(i, q, v)
	if err != nil || !bytes.Equal(p, canonical) || d != canonicalDigest {
		t.Fatal("reclaim encoder bypassed the closed dispatcher", d, canonicalDigest, err)
	}
	sole, err := canonicalResultDigest(i, q, v)
	if err != nil || sole != d {
		t.Fatal("independent result codec disagrees", sole, d, err)
	}
	if got, err := decodeUORootReclaim(i, q, v.root, p, d); err != nil || got != v {
		t.Fatal(got, v, err)
	}
	// A result that violates the fixed-slot preflight never reaches the writer.
	for name, mutate := range map[string]func(*uoRootReclaimResult){
		"removed-without-count":  func(v *uoRootReclaimResult) { v.removedCount = 0 },
		"negative-applied":       func(v *uoRootReclaimResult) { v.appliedMS = -1 },
		"cursor-past-floor":      func(v *uoRootReclaimResult) { v.nextCursor = v.floor + 1 },
		"two-removed":            func(v *uoRootReclaimResult) { v.removedCount = 2 },
		"removed-at-old-cursor":  func(v *uoRootReclaimResult) { v.removedSequence = v.oldCursor },
		"remaining-without-next": func(v *uoRootReclaimResult) { v.noNext = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := v
			mutate(&bad)
			if _, _, err := encodeUORootReclaim(i, q, bad); err == nil {
				t.Fatal("malformed cleanup result accepted")
			}
		})
	}
	if _, _, err := encodeUORootReclaim(i, q, v); err != nil {
		t.Fatal(err)
	}
	// The canonical identity/sequence rules still apply through the dispatcher.
	var zero CommandIdentity
	if _, _, err := encodeUORootReclaim(zero, q, v); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty original identity accepted", err)
	}
	behind := i
	behind.Sequence = v.floor
	if _, _, err := encodeUORootReclaim(behind, q, v); !errors.Is(err, ErrInvalid) {
		t.Fatal("cleanup sequence not ahead of its captured floor", err)
	}
}

// TestUORootReclaimDecoderNegatives proves foreign shapes are refused before
// any value is trusted: trailing bytes, truncation, a payload over the fixed
// 4096-byte slot cap (checked before allocation), an empty payload and a
// different original binding.
func TestUORootReclaimDecoderNegatives(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 5, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootReclaimCodecValue()
	p, d, err := encodeUORootReclaim(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	trailing := append(append([]byte(nil), p...), 0)
	if _, err := decodeUORootReclaim(i, q, v.root, trailing, ResultDigest(sha256.Sum256(trailing))); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("trailing bytes decoded", err)
	}
	truncated := append([]byte(nil), p[:len(p)-1]...)
	if _, err := decodeUORootReclaim(i, q, v.root, truncated, ResultDigest(sha256.Sum256(truncated))); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("truncated payload decoded", err)
	}
	oversize := make([]byte, uoRootReclaimPayloadCap+1)
	copy(oversize, p)
	if _, err := decodeUORootReclaim(i, q, v.root, oversize, ResultDigest(sha256.Sum256(oversize))); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("over-cap payload decoded", err)
	}
	if _, err := decodeUORootReclaim(i, q, v.root, nil, ResultDigest{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("empty payload decoded", err)
	}
	if _, err := decodeUORootReclaim(i, q, v.root, p, ResultDigest{9}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign digest decoded", err)
	}
	for name, change := range map[string]func(*CommandIdentity, *RequestDigest, *string){
		"sequence":    func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Sequence++ },
		"incarnation": func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Incarnation = [16]byte{9} },
		"operation":   func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.OperationID = [16]byte{9} },
		"request":     func(_ *CommandIdentity, q *RequestDigest, _ *string) { q[0]++ },
		"root":        func(_ *CommandIdentity, _ *RequestDigest, root *string) { *root = "zyxwvutsrqponmlkjihgfe" },
	} {
		t.Run(name, func(t *testing.T) {
			ii, qq, root := i, q, v.root
			change(&ii, &qq, &root)
			if _, err := decodeUORootReclaim(ii, qq, root, p, d); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("foreign original binding decoded", err)
			}
		})
	}
}
