package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"unicode/utf8"
)

// MG's prospective bounded guide effects. These values neither authorize nor
// execute a command: no SQL, grant, clock, receipt publication or Applied result.
// The command frame must load them natively, resolve original saved receipts,
// reauthorize and check source/lease/footprint custody on its SAME writer turn.
// SQL carriers are pending MG-001 adoption; no product path imports this model.
type mgGuideManifest struct {
	source, generation, previousGeneration string
	expectedRevision                       int64
	expected                               [3]int64 // channels, programmes, choices; immutable at birth
	expectedChain                          [32]byte
}

type mgGuideProgress struct {
	manifest                  mgGuideManifest
	staged                    [3]int64
	bytes, revision, sequence int64
	chain                     [32]byte
	sealed                    bool
}

type mgGuideFamily uint8

const (
	mgGuideChannels   mgGuideFamily = 1
	mgGuideProgrammes mgGuideFamily = 2
	mgGuideChoices    mgGuideFamily = 3
)

// A stage's rows/bytes must come from actual newly inserted native RETURNING
// facts and the frozen owned payload, never caller-supplied success counts. An
// existing duplicate is resolved through the original receipt, never counted
// again. A mismatch after any native INSERT requires whole-command rollback.
type mgGuideStageDelta struct {
	family                         mgGuideFamily
	source, generation             string
	sequence, ordinal, rows, bytes int64
	payload                        [32]byte
}

const mgGuideStageRows int64 = 8
const mgGuideStageBytes int64 = 256 << 10

func validMGGuideProgress(p mgGuideProgress) bool {
	m := p.manifest
	if m.source == "" || m.generation == "" || m.generation == m.previousGeneration ||
		!utf8.ValidString(m.source) || !utf8.ValidString(m.generation) || !utf8.ValidString(m.previousGeneration) ||
		m.expected[0] <= 0 || m.expectedRevision < 0 || (m.expectedRevision == 0) != (m.previousGeneration == "") ||
		p.bytes < 0 || p.revision <= 0 || p.sequence <= 0 {
		return false
	}
	for i, count := range p.staged {
		if count < 0 || m.expected[i] < count {
			return false
		}
	}
	if p.staged == ([3]int64{}) && (p.bytes != 0 || p.chain != mgGuideInitialChain(m)) {
		return false
	}
	return true
}

func planMGGuideStage(before mgGuideProgress, delta mgGuideStageDelta) (mgGuideProgress, error) {
	if !validMGGuideProgress(before) || before.sealed || before.revision == math.MaxInt64 || before.sequence == math.MaxInt64 ||
		delta.family < mgGuideChannels || delta.family > mgGuideChoices || delta.source != before.manifest.source || delta.generation != before.manifest.generation ||
		delta.sequence != before.sequence+1 || delta.rows <= 0 || delta.rows > mgGuideStageRows || delta.bytes <= 0 || delta.bytes > mgGuideStageBytes ||
		delta.payload == ([32]byte{}) || delta.bytes > math.MaxInt64-before.bytes {
		return mgGuideProgress{}, ErrConflict
	}
	family := int(delta.family) - 1
	if delta.ordinal != before.staged[family] || delta.rows > before.manifest.expected[family]-before.staged[family] {
		return mgGuideProgress{}, ErrConflict
	}
	after := before
	after.staged[family] += delta.rows
	after.bytes += delta.bytes
	after.sequence++
	after.revision++
	after.chain = mgGuideNextChain(before.chain, delta)
	return after, nil
}

// The initial chain commits the immutable publication header before any rows
// stage. expectedChain is the terminal commitment, excluded to avoid recursion.
func mgGuideInitialChain(m mgGuideManifest) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico-mg-guide-manifest-v1\x00"))
	var n [8]byte
	for _, s := range []string{m.source, m.generation, m.previousGeneration} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, v := range []int64{m.expectedRevision, m.expected[0], m.expected[1], m.expected[2]} {
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// The chain binds the ordered complete immutable stage payload, its actual
// accepted extent and exact source/generation. A seal cannot swap a header or
// substitute a differently partitioned input after staging began. This is a
// content commitment; it never substitutes for current writer authority.
func mgGuideNextChain(previous [32]byte, delta mgGuideStageDelta) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico-mg-guide-stage-v1\x00"))
	h.Write(previous[:])
	h.Write([]byte{byte(delta.family)})
	var n [8]byte
	for _, s := range []string{delta.source, delta.generation} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, v := range []int64{delta.sequence, delta.ordinal, delta.rows, delta.bytes} {
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	h.Write(delta.payload[:])
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func planMGGuideSeal(before mgGuideProgress, sequence int64) (mgGuideProgress, error) {
	if !validMGGuideProgress(before) || before.sealed || before.revision == math.MaxInt64 || before.sequence == math.MaxInt64 ||
		sequence != before.sequence+1 || before.staged != before.manifest.expected || before.chain != before.manifest.expectedChain {
		return mgGuideProgress{}, ErrConflict
	}
	after := before
	after.sequence++
	after.revision++
	after.sealed = true
	return after, nil
}

type mgGuideHead struct {
	source, generation, state string
	revision                  int64
	removed                   bool
}

// Point CAS must compare BOTH config revision and previous published generation:
// ordinary refresh intentionally need not change the config revision. The SQL
// accepting command repeats these exact predicates and verifies its RETURNING
// row before receipt/event publication. No final generation COUNT is required.
func planMGGuidePublication(seal mgGuideProgress, head mgGuideHead, refresh bool) (mgGuideHead, error) {
	m := seal.manifest
	if !validMGGuideProgress(seal) || !seal.sealed || seal.staged != m.expected || seal.chain != m.expectedChain || head.removed ||
		head.source != m.source || head.revision != m.expectedRevision || head.generation != m.previousGeneration ||
		(head.revision == 0 && (head.state != "" || refresh)) || (head.revision > 0 && head.state != "active" && head.state != "disabled") ||
		(!refresh && head.revision == math.MaxInt64) {
		return mgGuideHead{}, ErrConflict
	}
	after := head
	after.generation = m.generation
	if !refresh {
		after.revision++
	}
	if head.revision == 0 {
		after.state = "active"
	}
	return after, nil
}
