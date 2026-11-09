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

// Complete 740 prepared-job birth ORIGINAL. The field set mirrors the existing
// preparedmedia.Job in preparedmedia/types.go one for one (IDs, state/phase,
// generation, bytes/error/revision/times/version); it is a W-owned closed result
// and does not import the preparedmedia service package. MP-20 still owns
// recipe/source validation, the row birth and all outside-SQL encode/lease/
// retry/publish work. This is not a session, worker grant or new public DTO.
type uoBirthJobResult struct {
	root                                                                          string
	worker                                                                        [16]byte
	id, itemID, sourceID, profileID, targetID, state, phase, errorCode, versionID string
	generation, bytes, revision, createdMS, updatedMS, appliedMS                  int64
}

func (uoBirthJobResult) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoBirthJobResult") }

func preflightUOBirthJob(v uoBirthJobResult) error {
	if len(v.root) != 22 || !utf8.ValidString(v.root) || v.worker == [16]byte{} || v.generation < 0 || v.bytes < 0 || v.revision < 1 || v.createdMS <= 0 || v.updatedMS < v.createdMS || v.appliedMS < v.updatedMS {
		return ErrInvalid
	}
	// preparedmedia.Job identifiers are short opaque tokens; the item is the
	// 22-character catalogue public id. Bound before any allocation or write.
	for _, s := range []string{v.id, v.sourceID, v.profileID, v.targetID} {
		if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	if len(v.itemID) != 22 || !utf8.ValidString(v.itemID) || strings.IndexByte(v.itemID, 0) >= 0 {
		return ErrInvalid
	}
	// The closed native state set from prepared_media_jobs. Phase, error and
	// version text stay MP's semantics; this only keeps them bounded.
	switch v.state {
	case "queued", "running", "cancelling", "cancelled", "failed", "succeeded":
	default:
		return ErrInvalid
	}
	for _, s := range []string{v.phase, v.errorCode, v.versionID} {
		if len(s) > 128 || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
	}
	if len(v.phase) == 0 && v.state != "queued" {
		return ErrInvalid
	}
	return nil
}

func writeUOBirthJob(e *requestEncoder, id CommandIdentity, q RequestDigest, v uoBirthJobResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_transcode_create")
	e.text("root_birth_job_original")
	e.text(v.root)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.id, v.itemID, v.sourceID, v.profileID, v.targetID, v.state, v.phase, v.errorCode, v.versionID} {
		e.text(s)
	}
	e.bytes(v.worker[:])
	for _, n := range []int64{v.generation, v.bytes, v.revision, v.createdMS, v.updatedMS, v.appliedMS} {
		e.number(uint64(n))
	}
	return e.err
}

func decodeUOBirthJob(id CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (out uoBirthJobResult, err error) {
	if len(p) == 0 || len(p) > uoRootPayloadCap {
		return out, ErrResolveRequired
	}
	// Parse only this closed tail, then verify ALL prefix/identity/root/request
	// bytes by re-encoding through W's sole compiled result dispatcher.
	base := len(canonicalResultCodec) + 2 + 8 + len("mp_transcode_create") + 8 + len("root_birth_job_original") + 8 + len(root) + 16 + 8 + 16 + 32
	if len(p) < base {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(p[base:])
	out.root = root
	for _, dst := range []*string{&out.id, &out.itemID, &out.sourceID, &out.profileID, &out.targetID, &out.state, &out.phase, &out.errorCode, &out.versionID} {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 128 || n > uint64(r.Len()) {
			return uoBirthJobResult{}, ErrResolveRequired
		}
		b := make([]byte, int(n))
		if _, err = io.ReadFull(r, b); err != nil {
			return uoBirthJobResult{}, ErrResolveRequired
		}
		*dst = string(b)
	}
	if _, err = io.ReadFull(r, out.worker[:]); err != nil {
		return uoBirthJobResult{}, ErrResolveRequired
	}
	for _, dst := range []*int64{&out.generation, &out.bytes, &out.revision, &out.createdMS, &out.updatedMS, &out.appliedMS} {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > math.MaxInt64 {
			return uoBirthJobResult{}, ErrResolveRequired
		}
		*dst = int64(n)
	}
	if r.Len() != 0 {
		return uoBirthJobResult{}, ErrResolveRequired
	}
	encoded, digest, err := encodeUORootCanonical(id, q, out)
	if err != nil || digest != want || !bytes.Equal(encoded, p) {
		return uoBirthJobResult{}, ErrResolveRequired
	}
	return out, nil
}
