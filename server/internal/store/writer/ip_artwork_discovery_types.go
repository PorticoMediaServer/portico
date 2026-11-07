package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"strings"
	"unicode/utf8"
)

// Draft native body components only. No outer dispatcher, disclosure authority,
// governor memory/resource admission, Capture receipts, or proof issuer is wired.
// The sole W frame must own rollback and classify ORIGINAL receipts before fresh
// checks. These bodies do that native classification, but do not expose Applied.
// W must establish current disclosure before calling, including ORIGINAL replay.
// 220 begins a stage under the already acquired exact subject-job lease; lease
// acquisition/provider IO and selecting one image per role are separate work.
// A ready discovery stage is complete observation, never source visibility.
const (
	ipArtworkDiscoveryManifest      = "portico-ip-artwork-discovery-sha256-v1"
	ipArtworkDiscoveryChunk         = 16
	ipArtworkDiscoveryMaxCandidates = 64 // complete finite manifest, not counts as proof
	ipArtworkDiscoveryMaxBytes      = 256 << 10
	ipArtworkDiscoveryMaxText       = 16 << 10
)

type ipArtworkDiscoveryOwner struct {
	id          int64
	incarnation [16]byte
	// Exactly one namespace; equal entity/person integers are not interchangeable.
	entityID, personID                                                  int64
	sourceIncarnation                                                   [16]byte
	sourceRevision, revision, leaseOwner, leaseGeneration, leaseUntilMS int64
}

type ipArtworkDiscoverySpec struct {
	provider, language                                       string
	policyRevision, generation, expectedCount, expectedBytes int64
	manifest                                                 [32]byte
}

type ipArtworkDiscoveryCandidate struct {
	ordinal, sourceOrdinal                     int64 // RAW candidate order versus original per-role order
	role, imageID, origin, locale, attribution string
	score                                      float64
}

type ipArtworkDiscoveryRequest struct {
	identity                             CommandIdentity
	owner                                ipArtworkDiscoveryOwner
	spec                                 ipArtworkDiscoverySpec
	stageID, stageRevision, afterOrdinal int64 // next raw ordinal; empty prefix is zero
	candidates                           []ipArtworkDiscoveryCandidate
}

type ipArtworkDiscoveryResult struct {
	stageID, revision, count, afterOrdinal int64
}

type ipArtworkDiscoveryStage struct {
	revision, state, count, bytes, reclaimAfter int64
}

type ipArtworkDiscoveryReclaimResult struct {
	revision, deletedCount, deletedBytes, afterOrdinal int64
	done                                               bool
}

func ipArtworkDiscoveryText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

func ipArtworkDiscoveryCandidateBytes(c ipArtworkDiscoveryCandidate) (int64, error) {
	if c.ordinal < 0 || c.sourceOrdinal < 0 || math.IsNaN(c.score) || math.IsInf(c.score, 0) ||
		c.role == "" || c.role != strings.ToLower(strings.TrimSpace(c.role)) || c.role == "thumb" || c.role == "thumbnail" ||
		!ipArtworkDiscoveryText(c.role, 64) || !ipArtworkDiscoveryText(c.imageID, 4096) || c.imageID == "" ||
		!ipArtworkDiscoveryText(c.origin, 4096) || !ipArtworkDiscoveryText(c.locale, 64) ||
		!ipArtworkDiscoveryText(c.attribution, ipArtworkDiscoveryMaxText) {
		return 0, invalid("ip_artwork_candidate")
	}
	// Exact v1 retained manifest bytes: 3 scalar64 + 5 length64 + actual UTF-8.
	// This is logical retained debt, not an estimate of WAL or decoded RAM.
	return 64 + int64(len(c.role)+len(c.imageID)+len(c.origin)+len(c.locale)+len(c.attribution)), nil
}

func ipArtworkDiscoveryValidate(q ipArtworkDiscoveryRequest, kind CommandID) error {
	o, s := q.owner, q.spec
	if o.id <= 0 || (o.entityID <= 0) == (o.personID <= 0) || o.entityID < 0 || o.personID < 0 ||
		q.identity.Incarnation != o.incarnation || q.identity.Sequence <= 0 || o.sourceRevision <= 0 || o.revision <= 0 ||
		o.revision == math.MaxInt64 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 || o.leaseUntilMS <= 0 ||
		!ipArtworkDiscoveryText(s.provider, 64) || s.provider == "" || s.provider != strings.ToLower(strings.TrimSpace(s.provider)) ||
		!ipArtworkDiscoveryText(s.language, 64) || s.policyRevision <= 0 || s.generation <= 0 ||
		s.expectedCount < 0 || s.expectedCount > ipArtworkDiscoveryMaxCandidates || s.expectedBytes < 0 ||
		s.expectedBytes > ipArtworkDiscoveryMaxBytes || (s.expectedCount == 0) != (s.expectedBytes == 0) ||
		s.expectedBytes < 64*s.expectedCount {
		return invalid("ip_artwork_discovery_request")
	}
	if kind == IPArtworkDiscoveryClaim {
		if q.stageID != 0 || q.stageRevision != 0 || q.afterOrdinal != 0 || len(q.candidates) != 0 {
			return ErrInvalid
		}
	} else if q.stageID <= 0 || q.stageRevision <= 0 || q.stageRevision == math.MaxInt64 || q.afterOrdinal < 0 || q.afterOrdinal > s.expectedCount {
		return ErrInvalid
	}
	if kind != IPArtworkDiscoveryPage && len(q.candidates) != 0 {
		return ErrInvalid
	}
	if kind == IPArtworkDiscoveryPage {
		if len(q.candidates) == 0 || len(q.candidates) > ipArtworkDiscoveryChunk || int64(len(q.candidates)) > s.expectedCount-q.afterOrdinal {
			return ErrInvalid
		}
		var bytes int64
		for n, c := range q.candidates {
			b, err := ipArtworkDiscoveryCandidateBytes(c)
			if err != nil || c.ordinal != q.afterOrdinal+int64(n) {
				return invalid("ip_artwork_discovery_raw_order")
			}
			bytes += b
			for _, previous := range q.candidates[:n] {
				if c.role == previous.role && (c.imageID == previous.imageID || c.sourceOrdinal == previous.sourceOrdinal) {
					return invalid("ip_artwork_discovery_duplicate")
				}
			}
		}
		if bytes > s.expectedBytes {
			return invalid("ip_artwork_discovery_bytes")
		}
	}
	return nil
}

func ipArtworkDiscoveryPut(h hash.Hash, value int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(value))
	_, _ = h.Write(b[:])
}
func ipArtworkDiscoveryPutText(h hash.Hash, value string) {
	ipArtworkDiscoveryPut(h, int64(len(value)))
	_, _ = h.Write([]byte(value))
}

// Preparation commitment only. Finish independently re-reads the entire finite
// native manifest including the extra no-next row; no digest/flag grants proof.
func ipArtworkDiscoveryManifestDigest(o ipArtworkDiscoveryOwner, s ipArtworkDiscoverySpec, rows []ipArtworkDiscoveryCandidate) [32]byte {
	h := sha256.New()
	ipArtworkDiscoveryPutText(h, ipArtworkDiscoveryManifest)
	for _, n := range []int64{o.id, o.entityID, o.personID, o.sourceRevision, s.policyRevision, s.generation, s.expectedCount, s.expectedBytes} {
		ipArtworkDiscoveryPut(h, n)
	}
	_, _ = h.Write(o.incarnation[:])
	_, _ = h.Write(o.sourceIncarnation[:])
	ipArtworkDiscoveryPutText(h, s.provider)
	ipArtworkDiscoveryPutText(h, s.language)
	for _, c := range rows {
		ipArtworkDiscoveryPut(h, c.ordinal)
		ipArtworkDiscoveryPut(h, c.sourceOrdinal)
		for _, v := range []string{c.role, c.imageID, c.origin, c.locale, c.attribution} {
			ipArtworkDiscoveryPutText(h, v)
		}
		// The manifest's float representation follows SQLite's normalization of -0.
		score := c.score
		if score == 0 {
			score = 0
		}
		ipArtworkDiscoveryPut(h, int64(math.Float64bits(score)))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Saved results use W's sole result codec; source manifests remain a separate domain.
type ipArtworkDiscoverySavedResult struct {
	Kind  CommandID
	Value ipArtworkDiscoveryResult
}

func ipArtworkDiscoveryResultDigest(kind CommandID, i CommandIdentity, q RequestDigest, r ipArtworkDiscoveryResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, ipArtworkDiscoverySavedResult{kind, r})
}
