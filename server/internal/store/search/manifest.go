package search

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"math"
)

const ManifestAlgorithm = "portico-search-manifest-sha256-chain-v1"

var ErrManifest = errors.New("invalid search manifest")

type ManifestSubject uint8

const (
	ManifestEntity ManifestSubject = 1
	ManifestPerson ManifestSubject = 2
)

// ManifestInput captures immutable native image/bank inputs. Entity coordinates
// are absent (zero) for a person. These values are data, never SQL identifiers.
// Writer authority, operation identity and own-title/People payloads belong in
// the separate accepting-command digest, not this fragment-manifest seed.
type ManifestInput struct {
	Subject                         ManifestSubject
	SubjectID, LibraryID, Kind      int64
	SearchGenerationID              int64
	BankKey, EngineVersion          string
	UnicodeFingerprint, Recipe      string
	InputGeneration, SourceRevision int64
	SourceIncarnation               [16]byte
}

// ManifestRow is the exact stored fragment tuple. Revision is opaque native
// TEXT; do not parse it as the image's INTEGER revision. Physical row IDs and
// PB-only normalized coordinates are excluded from the hash.
type ManifestRow struct {
	Field                                 uint16
	Ordinal                               int64
	Revision, Prefix, Lexical, Normalized string
}

func (f Fragment) ManifestRow(field uint16, ordinal int64, revision string) ManifestRow {
	return ManifestRow{field, ordinal, revision, f.prefix, f.lexical, f.normalized}
}

// ManifestCheckpoint is a portable values-only seal checkpoint. W must persist
// it with the physical fragment cursor and immutable receipt in the same CAS.
// Restoring these values grants no authority and proves no native retirement.
type ManifestCheckpoint struct {
	Chain                [32]byte
	Count, PreparedBytes int64
	AfterField           uint16
	AfterOrdinal         int64
}

// Manifest describes a complete prepared image. It retains no rows. Callers
// reserve preparation memory before constructing their bounded row inputs.
type Manifest struct {
	seed, digest [32]byte
	count, bytes int64
	valid        bool
}

func (m Manifest) Seed() [32]byte   { return m.seed }
func (m Manifest) Digest() [32]byte { return m.digest }
func (m Manifest) Count() int64     { return m.count }
func (m Manifest) Bytes() int64     { return m.bytes }

// RestoreManifest restores immutable expected values read from the image after
// restart, without loading its fragments. It performs format validation only;
// W must independently rederive the seed from captured input and validate the
// image/algorithm/fences before trusting these persisted values.
func RestoreManifest(algorithm string, seed, digest [32]byte, count, bytes int64) (Manifest, error) {
	if algorithm != ManifestAlgorithm || count < 0 || bytes < 0 ||
		count == 0 && (bytes != 0 || digest != seed) {
		return Manifest{}, ErrManifest
	}
	return Manifest{seed, digest, count, bytes, true}, nil
}

func ManifestSeed(in ManifestInput, count, bytes int64) ([32]byte, error) {
	if in.Subject != ManifestEntity && in.Subject != ManifestPerson ||
		in.Subject == ManifestPerson && (in.LibraryID != 0 || in.Kind != 0) ||
		in.SearchGenerationID <= 0 || in.InputGeneration <= 0 || in.SourceRevision <= 0 ||
		count < 0 || bytes < 0 {
		return [32]byte{}, ErrManifest
	}
	h := sha256.New()
	_, _ = io.WriteString(h, "portico.search.manifest.seed.v1\x00")
	manifestString(h, in.Recipe)
	_, _ = h.Write([]byte{byte(in.Subject)})
	manifestInt(h, in.SubjectID)
	if in.Subject == ManifestEntity {
		manifestInt(h, in.LibraryID)
		manifestInt(h, in.Kind)
	}
	manifestInt(h, in.SearchGenerationID)
	manifestString(h, in.BankKey)
	manifestString(h, in.EngineVersion)
	manifestString(h, in.UnicodeFingerprint)
	manifestInt(h, in.InputGeneration)
	_, _ = h.Write(in.SourceIncarnation[:])
	manifestInt(h, in.SourceRevision)
	manifestInt(h, count)
	manifestInt(h, bytes)
	return manifestSum(h), nil
}

func ManifestLeaf(row ManifestRow) ([32]byte, error) {
	if row.Field == 0 || row.Field > 64 || row.Ordinal < 0 {
		return [32]byte{}, ErrManifest
	}
	h := sha256.New()
	_, _ = io.WriteString(h, "portico.search.manifest.leaf.v1\x00")
	var field [2]byte
	binary.BigEndian.PutUint16(field[:], row.Field)
	_, _ = h.Write(field[:])
	manifestInt(h, row.Ordinal)
	for _, value := range []string{row.Revision, row.Prefix, row.Lexical, row.Normalized} {
		manifestString(h, value)
	}
	return manifestSum(h), nil
}

func ManifestStep(previous, leaf [32]byte) [32]byte {
	h := sha256.New()
	_, _ = io.WriteString(h, "portico.search.manifest.step.v1\x00")
	_, _ = h.Write(previous[:])
	_, _ = h.Write(leaf[:])
	return manifestSum(h)
}

// AdvanceManifest checks canonical field/ordinal order and overflow before
// returning a new checkpoint. Error leaves the supplied checkpoint unchanged.
// W separately validates expected input, lease, physical cursor and budgets.
func AdvanceManifest(cp ManifestCheckpoint, row ManifestRow) (ManifestCheckpoint, error) {
	if !validCheckpoint(cp) || cp.Count == math.MaxInt64 {
		return ManifestCheckpoint{}, ErrManifest
	}
	if cp.Count == 0 || row.Field > cp.AfterField {
		if row.Ordinal != 0 {
			return ManifestCheckpoint{}, ErrManifest
		}
	} else if row.Field != cp.AfterField || cp.AfterOrdinal == math.MaxInt64 || row.Ordinal != cp.AfterOrdinal+1 {
		return ManifestCheckpoint{}, ErrManifest
	}
	leaf, err := ManifestLeaf(row)
	if err != nil {
		return ManifestCheckpoint{}, err
	}
	n, err := manifestRowBytes(row)
	if err != nil || n > math.MaxInt64-cp.PreparedBytes {
		return ManifestCheckpoint{}, ErrManifest
	}
	return ManifestCheckpoint{ManifestStep(cp.Chain, leaf), cp.Count + 1, cp.PreparedBytes + n, row.Field, row.Ordinal}, nil
}

// PrepareManifest replaces the former field-only FragmentPlan.Digest. Hashing
// happens outside the writer, one bounded row at a time, with no retained copy.
func PrepareManifest(in ManifestInput, rows []ManifestRow) (Manifest, error) {
	var bytes int64
	for _, row := range rows {
		n, err := manifestRowBytes(row)
		if err != nil || n > math.MaxInt64-bytes {
			return Manifest{}, ErrManifest
		}
		bytes += n
	}
	seed, err := ManifestSeed(in, int64(len(rows)), bytes)
	if err != nil {
		return Manifest{}, err
	}
	cp := ManifestCheckpoint{Chain: seed}
	for _, row := range rows {
		cp, err = AdvanceManifest(cp, row)
		if err != nil {
			return Manifest{}, err
		}
	}
	return Manifest{seed, cp.Chain, cp.Count, cp.PreparedBytes, true}, nil
}

// Matches is only the digest/count/bytes comparison. A successful SQL seal also
// requires unchanged input, maintained stage totals, an indexed no-next-row
// proof and atomic checkpoint/receipt CAS. This function accepts no SQL state.
func (m Manifest) Matches(cp ManifestCheckpoint) bool {
	return m.valid && validCheckpoint(cp) && cp.Count == m.count && cp.PreparedBytes == m.bytes && cp.Chain == m.digest
}

func validCheckpoint(cp ManifestCheckpoint) bool {
	if cp.Count < 0 || cp.PreparedBytes < 0 {
		return false
	}
	if cp.Count == 0 {
		return cp.PreparedBytes == 0 && cp.AfterField == 0 && cp.AfterOrdinal == 0
	}
	return cp.AfterField > 0 && cp.AfterField <= 64 && cp.AfterOrdinal >= 0 && cp.AfterOrdinal < cp.Count
}

func manifestRowBytes(row ManifestRow) (int64, error) {
	var total int64
	for _, value := range []string{row.Prefix, row.Lexical, row.Normalized} {
		if int64(len(value)) > math.MaxInt64-total {
			return 0, ErrManifest
		}
		total += int64(len(value))
	}
	return total, nil
}

func manifestString(h hash.Hash, value string) {
	manifestInt(h, int64(len(value)))
	_, _ = io.WriteString(h, value)
}

func manifestInt(h hash.Hash, value int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(value))
	_, _ = h.Write(b[:])
}

func manifestSum(h hash.Hash) (sum [32]byte) {
	copy(sum[:], h.Sum(sum[:0]))
	return sum
}
