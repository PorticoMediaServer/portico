package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const iaRefreshRowCap = 64 << 10

// A key has a closed family tag. token is the asset token for families1–5 and
// the native row id for6–7; index is meaningful only for streams and chapters.
type iaRefreshKey struct {
	family uint8
	token  string
	index  int64
}
type iaRefreshImage struct {
	present bool
	values  []any
}

func (k iaRefreshKey) valid() bool {
	return k.family >= 1 && k.family <= 7 && len(k.token) > 0 && len(k.token) <= 128 && utf8.ValidString(k.token) && k.index >= 0 && ((k.family == 2 || k.family == 4) || k.index == 0)
}
func (k iaRefreshKey) args() []any {
	if k.family == 2 || k.family == 4 {
		return []any{k.token, k.index}
	}
	return []any{k.token}
}

// A reversible native key cursor, never a digest or an OFFSET. Family walkers
// decode it and bind the original TEXT/index key to their raw keyset query.
func (k iaRefreshKey) cursor() string {
	return hex.EncodeToString([]byte(k.token)) + fmt.Sprintf(":%016x", k.index)
}
func iaRefreshDecodeCursor(f uint8, cursor string) (iaRefreshKey, error) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 || len(parts[1]) != 16 {
		return iaRefreshKey{}, ErrInvalid
	}
	token, err := hex.DecodeString(parts[0])
	if err != nil {
		return iaRefreshKey{}, ErrInvalid
	}
	n, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil {
		return iaRefreshKey{}, ErrInvalid
	}
	k := iaRefreshKey{f, string(token), n}
	if !k.valid() || k.cursor() != cursor {
		return iaRefreshKey{}, ErrInvalid
	}
	return k, nil
}

func iaRefreshAbsent(k iaRefreshKey) iaRefreshImage {
	s := iaRefreshShapes[k.family]
	v := make([]any, len(s.columns))
	copy(v, k.args())
	return iaRefreshImage{values: v}
}
func iaRefreshReplacement(k iaRefreshKey, r iaRefreshRecord) (iaRefreshImage, error) {
	if !k.valid() {
		return iaRefreshImage{}, ErrInvalid
	}
	if r == nil {
		return iaRefreshAbsent(k), nil
	}
	switch r.(type) {
	case iaRefreshStreamFactRow, iaRefreshStreamRow, iaRefreshChapterFactRow, iaRefreshChapterRow, iaRefreshSubtitleFactRow, iaRefreshSidecarRow, iaRefreshSubtitleRow:
	default:
		return iaRefreshImage{}, ErrInvalid
	}
	f, v := r.refreshRecord()
	if f != k.family {
		return iaRefreshImage{}, ErrInvalid
	}
	s := iaRefreshShapes[f]
	if len(v) != len(s.columns) {
		return iaRefreshImage{}, ErrInvalid
	}
	for j, x := range k.args() {
		if v[j] != x {
			return iaRefreshImage{}, ErrInvalid
		}
	}
	out := iaRefreshImage{true, v}
	_, _, err := iaRefreshImageDigest(k, out)
	return out, err
}

// The preimage is version + family + full typed key + presence + every column
// in frozen C019 order, with explicit NULL and type tags. Text is exact UTF-8,
// REAL is finite IEEE754 with SQLite's normalized zero. No JSON/opaque digest.
// Debt is96 fixed custody bytes plus these exact encoded preimage bytes; the
// fixed written revision/digest are included in that custody allowance.
func iaRefreshImageDigest(k iaRefreshKey, v iaRefreshImage) ([32]byte, int64, error) {
	e := requestEncoder{h: sha256.New()}
	e.text("portico-ia-refresh-row-v1")
	e.number(uint64(k.family))
	e.text(k.token)
	e.number(uint64(k.index))
	e.flag(v.present)
	if !k.valid() || len(v.values) != len(iaRefreshShapes[k.family].columns) {
		return [32]byte{}, 0, ErrInvalid
	}
	var raw int
	for _, x := range v.values {
		if t, ok := x.(string); ok {
			if len(t) > iaRefreshRowCap-raw {
				return [32]byte{}, 0, &Refusal{Code: RefusalCapacity}
			}
			raw += len(t)
		}
	}
	for j, c := range iaRefreshShapes[k.family].columns {
		x := v.values[j]
		if x == nil {
			if v.present && !c.nullable {
				return [32]byte{}, 0, ErrInvalid
			}
			e.bytes([]byte{0})
			continue
		}
		switch c.kind {
		case "TEXT":
			t, ok := x.(string)
			if !ok || !utf8.ValidString(t) {
				return [32]byte{}, 0, ErrInvalid
			}
			e.bytes([]byte{1})
			e.text(t)
		case "INTEGER":
			n, ok := x.(int64)
			if !ok {
				return [32]byte{}, 0, ErrInvalid
			}
			e.bytes([]byte{2})
			e.number(uint64(n))
		case "REAL":
			n, ok := x.(float64)
			if !ok || math.IsInf(n, 0) || math.IsNaN(n) {
				return [32]byte{}, 0, ErrInvalid
			}
			if n == 0 {
				n = 0
			}
			e.bytes([]byte{3})
			e.number(math.Float64bits(n))
		}
	}
	if e.err != nil {
		return [32]byte{}, 0, e.err
	}
	if e.written > iaRefreshRowCap {
		return [32]byte{}, 0, &Refusal{Code: RefusalCapacity}
	}
	return [32]byte(e.h.Sum(nil)), 96 + int64(e.written), nil
}
func iaRefreshRequestDigest(q iaRefreshRequest) (RequestDigest, error) {
	var image iaRefreshImage
	if q.purpose == IARefreshStep {
		var err error
		image, err = iaRefreshReplacement(q.key, q.row)
		if err != nil {
			return RequestDigest{}, err
		}
	}
	return iaRefreshRequestDigestWithImage(q, image)
}
func iaRefreshRequestDigestWithImage(q iaRefreshRequest, image iaRefreshImage) (RequestDigest, error) {
	var err error
	var digest [32]byte
	if q.purpose == IARefreshStep {
		digest, _, err = iaRefreshImageDigest(q.key, image)
		if err != nil {
			return RequestDigest{}, err
		}
	}
	return canonicalRequestDigest(q.purpose, 1, q.reference.Identity, struct {
		Scope                       iaRefreshScope
		RefreshID, ExpectedRevision int64
		Key                         iaRefreshKey
		After                       [32]byte
	}{q.scope, q.refreshID, q.expectedRevision, q.key, digest})
}

type iaRefreshResult struct {
	refreshID, revision, appliedMS int64
	state                          string
	family                         int64
	key                            string
	rows, bytes                    int64
}

func iaRefreshResultDigest(q iaRefreshRequest, v iaRefreshResult) (ResultDigest, error) {
	if v.refreshID <= 0 || v.revision <= 0 || v.appliedMS < 0 || v.family < 0 || v.family > 7 || len(v.key) > 273 || v.rows < 0 || v.bytes < 0 {
		return ResultDigest{}, ErrResolveRequired
	}
	if (q.purpose == IARefreshBegin || q.purpose == IARefreshStep) && v.state != "copying" || q.purpose == IARefreshRollback && v.state != "restoring" {
		return ResultDigest{}, ErrResolveRequired
	}
	e := requestEncoder{h: sha256.New()}
	e.text("portico-ia-refresh-result-v1")
	e.number(uint64(q.purpose))
	e.bytes(q.reference.Identity.Incarnation[:])
	e.number(uint64(q.reference.Identity.Sequence))
	e.bytes(q.reference.Identity.OperationID[:])
	e.bytes(q.reference.RequestDigest[:])
	e.number(uint64(v.refreshID))
	e.number(uint64(v.revision))
	e.number(uint64(v.appliedMS))
	e.text(v.state)
	e.number(uint64(v.family))
	e.text(v.key)
	e.number(uint64(v.rows))
	e.number(uint64(v.bytes))
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	return ResultDigest(e.h.Sum(nil)), nil
}
func (iaRefreshEntry) Format(s fmt.State, _ rune)   { formatUSPreparation(s, "iaRefreshEntry") }
func (iaRefreshOutcome) Format(s fmt.State, _ rune) { formatUSPreparation(s, "iaRefreshOutcome") }

// Fingerprint for refresh slot1 inputs created by the future IA producer. FIRST
// slot0's existing fingerprint/codec is untouched. The complete original source
// identity includes object revision and policy; leases, current stat and journal
// progress are excluded. The writer recomputes it and compares every native
// witness column, so a recycled change rowid is never enough to recover custody.
func iaRefreshInputFingerprint(s iaRefreshScope) ([32]byte, error) {
	e := requestEncoder{h: sha256.New()}
	e.text("portico-ia-refresh-input-v1")
	for _, n := range []int64{s.changeID, s.assetID, s.libraryID, s.epoch, s.inputRevision, s.sourceGeneration, s.policyRevision, s.size, s.modifiedNS} {
		e.number(uint64(n))
	}
	for _, t := range []string{s.token, s.runID, s.libraryKey, s.sourceID, s.rootIncarnation, s.objectID, s.objectRevision} {
		e.text(t)
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	return [32]byte(e.h.Sum(nil)), nil
}
