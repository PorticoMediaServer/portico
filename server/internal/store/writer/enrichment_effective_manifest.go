package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"math"
	"unicode/utf8"
)

const minEffectiveCreditTupleBytes int64 = 5*8 + 5*8 + 5

// These are materialized-credit manifest bytes, not request/result/evidence
// digests. All values and nullable provenance remain native. image_id is bound
// separately by the closed command's same-entity FK/CAS and immutable receipt.
// Encoding resolves no reference and grants no source/owner/precedence authority.
type effectiveCreditTuple struct {
	entityID, ordinal                                  int64
	personID                                           sql.NullInt64
	roleID, departmentID                               int64
	creditedName, provider, creditID, providerPersonID string
	sourceOrdinal                                      int64
	sourceRowID, ownerEntryID                          sql.NullString
	ownerIncarnation                                   effectiveBeginOptionalIncarnation
	ownerRevision                                      sql.NullInt64
	sourceStatus                                       string
}

type effectiveCreditManifestContext struct {
	entityID                     int64
	currentInput, sourceManifest [32]byte
	language                     string
	count, canonicalBytes        int64
}

// Portable build data only. A checkpoint is NOT a verified image/delta plan.
// The sole writer must establish the native image/revision/input-vector CAS and
// prior bounded verification custody before resuming one from persisted fields.
type effectiveCreditManifestCheckpoint struct {
	seed, chain                 [32]byte
	nextOrdinal, canonicalBytes int64
}

// byteBudget is the trusted caller's encoding resource bound, not a product
// limit, caller-selected SQL budget, quota, or admission grant. Preflight all
// sizes before UTF-8 scans, copies, allocation or hashing. No text normalization.
func effectiveCreditTupleBytes(row effectiveCreditTuple, byteBudget int64) ([]byte, error) {
	if row.entityID <= 0 || row.ordinal < 0 ||
		(!row.personID.Valid && row.personID.Int64 != 0) ||
		(!row.sourceRowID.Valid && row.sourceRowID.String != "") ||
		(!row.ownerEntryID.Valid && row.ownerEntryID.String != "") ||
		(!row.ownerIncarnation.present && row.ownerIncarnation.value != [16]byte{}) ||
		(!row.ownerRevision.Valid && row.ownerRevision.Int64 != 0) ||
		row.ownerEntryID.Valid != row.ownerIncarnation.present || row.ownerEntryID.Valid != row.ownerRevision.Valid ||
		(row.ownerRevision.Valid && row.ownerRevision.Int64 <= 0) {
		return nil, invalid("credit_manifest_tuple_shape")
	}
	switch row.sourceStatus {
	case "current", "missing", "replaced":
	default:
		return nil, invalid("credit_manifest_source_status")
	}
	// Fixed I64s, five LP lengths and five nullable presence tags. Present
	// nullable values add their own payloads/LP length. Native sourceOrdinal is
	// intentionally signed, including domain-admitted negative sentinel values.
	size := minEffectiveCreditTupleBytes
	if row.personID.Valid {
		size += 8
	}
	if row.sourceRowID.Valid {
		size += 8
	}
	if row.ownerEntryID.Valid {
		size += 8
	}
	if row.ownerIncarnation.present {
		size += 16
	}
	if row.ownerRevision.Valid {
		size += 8
	}
	texts := []string{row.creditedName, row.provider, row.creditID, row.providerPersonID, row.sourceStatus}
	if row.sourceRowID.Valid {
		texts = append(texts, row.sourceRowID.String)
	}
	if row.ownerEntryID.Valid {
		texts = append(texts, row.ownerEntryID.String)
	}
	for _, s := range texts {
		if int64(len(s)) > math.MaxInt64-size {
			return nil, invalid("credit_manifest_tuple_overflow")
		}
		size += int64(len(s))
	}
	if byteBudget <= 0 || size > byteBudget || uint64(size) > uint64(^uint(0)>>1) {
		return nil, invalid("credit_manifest_tuple_budget")
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return nil, invalid("credit_manifest_tuple_text")
		}
	}
	out := make([]byte, 0, int(size))
	number := func(v int64) { out = binary.BigEndian.AppendUint64(out, uint64(v)) }
	text := func(s string) { out = binary.BigEndian.AppendUint64(out, uint64(len(s))); out = append(out, s...) }
	nullableNumber := func(v sql.NullInt64) {
		if !v.Valid {
			out = append(out, 0)
		} else {
			out = append(out, 1)
			number(v.Int64)
		}
	}
	nullableText := func(v sql.NullString) {
		if !v.Valid {
			out = append(out, 0)
		} else {
			out = append(out, 1)
			text(v.String)
		}
	}
	number(row.entityID)
	number(row.ordinal)
	nullableNumber(row.personID)
	number(row.roleID)
	number(row.departmentID)
	text(row.creditedName)
	text(row.provider)
	text(row.creditID)
	text(row.providerPersonID)
	number(row.sourceOrdinal)
	nullableText(row.sourceRowID)
	nullableText(row.ownerEntryID)
	if row.ownerIncarnation.present {
		out = append(out, 1)
		out = append(out, row.ownerIncarnation.value[:]...)
	} else {
		out = append(out, 0)
	}
	nullableNumber(row.ownerRevision)
	text(row.sourceStatus)
	return out, nil
}

func effectiveCreditManifestSeed(context effectiveCreditManifestContext, byteBudget int64) ([32]byte, error) {
	const fixedSeedBytes int64 = int64(len(effectiveCreditManifestAlgorithm)) + 1 + 5 + 8 + 32 + 32 + 8 + 8 + 8
	if context.entityID <= 0 || context.count < 0 || context.canonicalBytes < 0 || byteBudget < fixedSeedBytes || int64(len(context.language)) > byteBudget-fixedSeedBytes {
		return [32]byte{}, invalid("credit_manifest_context")
	}
	if !utf8.ValidString(context.language) {
		return [32]byte{}, invalid("credit_manifest_language")
	}
	// Reuse the existing adopted seed implementation used by hidden BEGIN.
	// Other header fields have independent native CAS meaning and are not seed
	// bytes; filling them with aliases would invent a different algorithm.
	return effectiveBeginBuildSeed(effectiveBeginHeader{entityID: context.entityID, declaredInput: context.currentInput, sourceManifest: context.sourceManifest, language: context.language, expectedCount: context.count, expectedBytes: context.canonicalBytes}), nil
}

func newEffectiveCreditManifestCheckpoint(context effectiveCreditManifestContext, byteBudget int64) (effectiveCreditManifestCheckpoint, error) {
	seed, err := effectiveCreditManifestSeed(context, byteBudget)
	if err != nil {
		return effectiveCreditManifestCheckpoint{}, err
	}
	return effectiveCreditManifestCheckpoint{seed: seed, chain: seed}, nil
}

func effectiveCreditManifestLeaf(encoded []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(effectiveCreditManifestAlgorithm + "\x00leaf\x00"))
	_, _ = h.Write(encoded)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func effectiveCreditManifestStep(previous, leaf [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(effectiveCreditManifestAlgorithm + "\x00step\x00"))
	_, _ = h.Write(previous[:])
	_, _ = h.Write(leaf[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Advance covers one actual ordered tuple under the declared complete count/
// byte context. It changes no state on error. Count/chain equality still needs
// indexed no-next-row proof, complete native source/owner/label/person validation
// and W custody before any seal; EN cannot supply a trusted checkpoint/flag.
func advanceEffectiveCreditManifest(context effectiveCreditManifestContext, previous effectiveCreditManifestCheckpoint, row effectiveCreditTuple, byteBudget int64) (effectiveCreditManifestCheckpoint, error) {
	seed, err := effectiveCreditManifestSeed(context, byteBudget)
	if err != nil {
		return previous, err
	}
	if previous.seed != seed || previous.nextOrdinal < 0 || previous.nextOrdinal >= context.count || previous.canonicalBytes < 0 || previous.canonicalBytes > context.canonicalBytes ||
		previous.nextOrdinal > previous.canonicalBytes/minEffectiveCreditTupleBytes || row.entityID != context.entityID || row.ordinal != previous.nextOrdinal ||
		(previous.nextOrdinal == 0 && (previous.canonicalBytes != 0 || previous.chain != seed)) {
		return previous, invalid("credit_manifest_checkpoint")
	}
	// A corrupt/overlong next row must also fit the declared remainder before
	// any tuple allocation or hash work, not merely before incrementing counters.
	remaining := context.canonicalBytes - previous.canonicalBytes
	encoded, err := effectiveCreditTupleBytes(row, min(byteBudget, remaining))
	if err != nil {
		return previous, err
	}
	if int64(len(encoded)) > remaining {
		return previous, invalid("credit_manifest_declared_bytes")
	}
	next := previous
	next.chain = effectiveCreditManifestStep(previous.chain, effectiveCreditManifestLeaf(encoded))
	next.nextOrdinal++
	next.canonicalBytes += int64(len(encoded))
	return next, nil
}
