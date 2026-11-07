package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Prospective command metadata only. The parent aggregates these descriptors;
// none of these IDs is an authorized/executable command in the shared dispatcher.
const (
	MGLCBegin            CommandID = 860
	MGLCStageCandidates  CommandID = 861
	MGLCFinishCandidates CommandID = 862
	MGLCStageEntries     CommandID = 863
	MGLCPublish          CommandID = 864
	MGLCVerifyEntries    CommandID = 865
	MGLCSeal             CommandID = 866
	MGLCAcknowledge      CommandID = 867
)

func mgLCDescriptors() []Descriptor {
	return []Descriptor{{MGLCBegin, "mg.lc.begin", Normal}, {MGLCStageCandidates, "mg.lc.stage-candidates", Normal}, {MGLCFinishCandidates, "mg.lc.finish-candidates", Normal}, {MGLCStageEntries, "mg.lc.stage-entries", Normal}, {MGLCPublish, "mg.lc.publish", Normal}, {MGLCVerifyEntries, "mg.lc.verify", Normal}, {MGLCSeal, "mg.lc.seal", Normal}, {MGLCAcknowledge, "mg.lc.ack", Normal}}
}

const (
	mgLCRows              = 8
	mgLCBytes             = 256 << 10
	mgLCMaxDuration int64 = 7 * 24 * 60 * 60 * 1000
	// A seven-civil-day window may exceed 168 hours across a DST fallback.
	mgLCMaxWindow       int64 = 8 * 24 * 60 * 60 * 1000
	mgLCCandidatesPhase       = "mg-lc-candidates"
	mgLCSchedulePhase         = "mg-lc-scheduling"
	mgLCCompletePhase         = "mg-lc-verifying"
	mgLCVerifiedPhase         = "mg-lc-verified"
	mgLCSealedPhase           = "mg-lc-sealed"
)

type mgLCLibraryWitness struct {
	id                                           string
	revision, order, metadata, origin, catalogue int64
	incarnation                                  [16]byte
}

// Frozen values supplied by the parent's compiled RB/RS adapter. Its candidate
// read port is the existing browse/detail candidate selection: keyset pages,
// resolved integer entity/show IDs, one whole playable source, duration and
// observed source binding, at ONE catalogue + play-history cutoff. It must own
// and byte-admit the complete immutable page before entering a writer hold.
// No catalogue SQL callback, catalogue import or new scheduler is introduced.
// Libraries are sorted and exactly cover the normalized config's selection.
// These values are NOT current authorization or evidence of physical exit.
type mgLCBuild struct {
	channel, generation, base, config, seed, lease string
	revision, start, end, leaseUntil, history      int64
	libraries                                      []mgLCLibraryWitness
	incarnation, create                            [16]byte
	birth                                          RequestDigest
}

type mgLCCandidate struct {
	rule                            string
	item, ordinal                   int64
	library, asset, source, title   string
	duration, show, season, episode int64
	weight                          float64
	sort                            string
}

// These are native owner facts, not an Applied result or receipt codec.
type mgLCProgress struct {
	phase, after, status, lease, reclaimFamily, reclaimKey                 string
	cursor, candidates, unresolved, last, ack, revision, bytes, entries    int64
	verified, verifyAfter, verifyEnd, leaseUntil, published, reclaimSubkey int64
	entryDigest, verifyDigest                                              [32]byte
	sealed                                                                 bool
}

type mgLCCommand struct {
	identity CommandIdentity
	request  RequestDigest
}

func (c mgLCCommand) valid(b mgLCBuild) bool {
	return c.identity.Incarnation == b.incarnation && c.identity.Sequence > 0 && c.identity.OperationID != ([16]byte{}) && c.request != (RequestDigest{})
}

// A slot is a bounded immutable selection result from the read planner, not an
// instruction to read the catalogue inside the writer. The native staged
// candidate supplies ALL copied media fields. This first slice supports cuts
// with zero source offset; copy/continuation, random/bag/rule-state selection
// and block resolution remain the read planner's later integration work.
// item=0 means the explicit no-eligible-media slate (at most 30 minutes).
type mgLCSlot struct {
	occurrence, rule, block string
	item, start, end        int64
}

func mgLCText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, '\x00') }
func (b mgLCBuild) fence() string {
	var parts []string
	for _, w := range b.libraries {
		parts = append(parts, w.id+":"+strconv.FormatInt(w.catalogue, 10)+":"+strconv.FormatInt(w.revision, 10)+":"+strconv.FormatInt(w.order, 10)+":"+strconv.FormatInt(w.metadata, 10)+":"+strconv.FormatInt(w.origin, 10)+":"+hex.EncodeToString(w.incarnation[:]))
	}
	sum := sha256.Sum256([]byte("mg-lc-catalogue-v1\x00" + strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}
func (b mgLCBuild) valid() bool {
	n := 0
	for _, s := range []string{b.channel, b.generation, b.base, b.config, b.seed, b.lease} {
		if len(s) > mgLCBytes-n || !mgLCText(s) {
			return false
		}
		n += len(s)
	}
	if b.channel == "" || b.generation == "" || b.generation == b.base || b.config == "" || b.seed == "" || b.lease == "" || b.incarnation == ([16]byte{}) || b.create == ([16]byte{}) || b.birth == (RequestDigest{}) || b.revision <= 0 || b.start < 0 || b.end <= b.start || b.end-b.start > mgLCMaxWindow || b.leaseUntil <= 0 || b.history < 0 || len(b.libraries) == 0 || len(b.libraries) > 8 {
		return false
	}
	previous := ""
	for _, w := range b.libraries {
		if w.id <= previous || !mgLCText(w.id) || strings.ContainsAny(w.id, ":,") || w.revision <= 0 || w.order <= 0 || w.metadata <= 0 || w.origin <= 0 || w.catalogue <= 0 {
			return false
		}
		previous = w.id
		n += len(w.id) + 128
	}
	return n <= mgLCBytes
}
func (c mgLCCandidate) valid(b mgLCBuild) bool {
	if c.rule == "" || c.item <= 0 || c.ordinal < 0 || c.asset == "" || c.source == "" || c.duration <= 0 || c.duration > mgLCMaxDuration || c.show < 0 || c.season < 0 || c.episode < 0 || c.weight <= 0 || math.IsNaN(c.weight) || math.IsInf(c.weight, 0) {
		return false
	}
	for _, s := range []string{c.rule, c.library, c.asset, c.source, c.title, c.sort} {
		if !mgLCText(s) {
			return false
		}
	}
	for _, w := range b.libraries {
		if c.library == w.id {
			return true
		}
	}
	return false
}

// Draft protocol: internal 38b4c8d910948b3433fa58e612b3cf3c5a3b8162,
// candidate SHA256 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// No expected counts or duplicate time/candidate counters are introduced.
// staged_bytes counts canonical bytes of newly inserted candidate/entry rows,
// excluding receipt/header/SQLite allocation costs (the parent's whole recipe
// must separately admit those). entry_digest and verified_entry_digest chain
// the SAME canonical actual-row encoding from an immutable birth seed.
// Candidate EOF still comes from the compiled RB/RS read producer. Exact
// config/library selection, source eligibility and retained history coverage
// are that adapter's responsibility, never a caller-selected SQL callback.
// Eight libraries and zero-offset cut/slate slots are explicit UNQUALIFIED
// component limits, not public caps or full scheduler behavior. No lifecycle
// helper here authorizes physical retirement or generation/pin reclamation.
