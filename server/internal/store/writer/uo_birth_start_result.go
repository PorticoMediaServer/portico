package writer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// Match the existing playbackdisc.MaxPreparationLifetime without importing
// the legacy playback store (which registers SQLite functions via dbwork).
const uoBirthStartMaxLifetimeMS int64 = 120_000

// Complete 700 birth ORIGINAL, not a session, worker grant or new public DTO.
// The public branch is the existing variant=wait/retryAfterMs=500. These exact
// preparation/worker/deadline facts let the parent find that original without
// allocating another worker or renewing its wait. MP still proves its current
// original process/source/queue conditions before rendering/continuing it.
type uoBirthStartResult struct {
	root, preparation, device, startKey, startBody, item, asset, replaces                         string
	worker                                                                                        [16]byte
	part, replacesGeneration, createdMS, deadlineMS, tokenExpiresMS, revision, retryMS, appliedMS int64
}

func (uoBirthStartResult) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoBirthStartResult") }

func preflightUOBirthStart(v uoBirthStartResult) error {
	if len(v.root) != 22 || v.worker == [16]byte{} || !usHex(v.startKey, 64) || !usHex(v.startBody, 64) || v.part < 0 || v.replacesGeneration < 0 || v.createdMS <= 0 || v.deadlineMS <= v.createdMS || v.deadlineMS-v.createdMS > uoBirthStartMaxLifetimeMS || v.tokenExpiresMS < v.deadlineMS || v.revision != 1 || v.retryMS != 500 || v.appliedMS < v.createdMS || v.appliedMS >= v.deadlineMS {
		return ErrInvalid
	}
	if (v.replaces == "") != (v.replacesGeneration == 0) {
		return ErrInvalid
	}
	for _, s := range []string{v.preparation, v.device, v.item, v.asset} {
		if len(s) == 0 || len(s) > 256 || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	if len(v.replaces) > 256 || !utf8.ValidString(v.replaces) || strings.IndexByte(v.replaces, 0) >= 0 {
		return ErrInvalid
	}
	return nil
}
func writeUOBirthStart(e *requestEncoder, id CommandIdentity, q RequestDigest, v uoBirthStartResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_start_preparation_begin")
	e.text("root_birth_start_wait")
	e.text(v.root)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.preparation, v.device, v.startKey, v.startBody, v.item, v.asset, v.replaces} {
		e.text(s)
	}
	e.bytes(v.worker[:])
	for _, n := range []int64{v.part, v.replacesGeneration, v.createdMS, v.deadlineMS, v.tokenExpiresMS, v.revision, v.retryMS, v.appliedMS} {
		e.number(uint64(n))
	}
	return e.err
}
func decodeUOBirthStart(id CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (out uoBirthStartResult, err error) {
	if len(p) == 0 || len(p) > uoRootPayloadCap {
		return out, ErrResolveRequired
	}
	// Parse only this closed tail, then verify ALL prefix/identity/root/request
	// bytes by re-encoding through W's sole compiled result dispatcher.
	base := len(canonicalResultCodec) + 2 + 8 + len("mp_start_preparation_begin") + 8 + len("root_birth_start_wait") + 8 + len(root) + 16 + 8 + 16 + 32
	if len(p) < base {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(p[base:])
	out.root = root
	for _, dst := range []*string{&out.preparation, &out.device, &out.startKey, &out.startBody, &out.item, &out.asset, &out.replaces} {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 256 || n > uint64(r.Len()) {
			return uoBirthStartResult{}, ErrResolveRequired
		}
		b := make([]byte, int(n))
		if _, err = io.ReadFull(r, b); err != nil {
			return uoBirthStartResult{}, ErrResolveRequired
		}
		*dst = string(b)
	}
	if _, err = io.ReadFull(r, out.worker[:]); err != nil {
		return uoBirthStartResult{}, ErrResolveRequired
	}
	for _, dst := range []*int64{&out.part, &out.replacesGeneration, &out.createdMS, &out.deadlineMS, &out.tokenExpiresMS, &out.revision, &out.retryMS, &out.appliedMS} {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > math.MaxInt64 {
			return uoBirthStartResult{}, ErrResolveRequired
		}
		*dst = int64(n)
	}
	if r.Len() != 0 {
		return uoBirthStartResult{}, ErrResolveRequired
	}
	encoded, digest, err := encodeUORootCanonical(id, q, out)
	if err != nil || digest != want || !bytes.Equal(encoded, p) {
		return uoBirthStartResult{}, ErrResolveRequired
	}
	return out, nil
}
