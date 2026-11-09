package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// IP artwork discovery 220-222 records ONE immutable, bounded provider
// candidate page for an already claimed artwork subject job. Claim declares the
// complete page size/byte debt and its original manifest; page appends RAW
// ordered candidates; finish re-reads the whole finite page through a point
// octet preflight and refuses any prefix, gap, duplicate or byte mismatch. A
// ready stage is a complete observation, never file-ready material, a selected
// image or a source row: IP-19/IP-21 own installation and selection.
const (
	ipArtworkDiscoveryManifest      = "portico-ip-artwork-discovery-sha256-v1"
	ipArtworkDiscoveryPage          = 16
	ipArtworkDiscoveryMaxCandidates = 160
	ipArtworkDiscoveryMaxBytes      = 4 << 20
	ipArtworkDiscoveryMaxText       = 16 << 10
)

// Exactly one namespace (entity or person); equal integers are not
// interchangeable. The job lease facts are CAS witnesses supplied by the
// caller, never authority: current owner authority is proved separately.
type ipArtworkDiscoveryOwner struct {
	id          int64
	incarnation [16]byte
	entityID    int64
	personID    int64
	// sourceIncarnation/sourceRevision fence the accepted artwork source; the
	// exact subject job must still be in its providers phase.
	sourceIncarnation                                     [16]byte
	sourceRevision, revision, leaseOwner, leaseGeneration int64
	leaseUntilMS                                          int64
}

// The complete declared page. effectiveLanguage is the caller's resolved
// artwork language ("" is textless); the writer re-derives it from current
// settings/source and refuses a mismatch. The manifest is the original
// observation commitment, not authorization.
type ipArtworkDiscoverySpec struct {
	provider, language                          string
	policyRevision, consentRevision, generation int64
	expectedCount, expectedBytes                int64
	manifest                                    [32]byte
}

type ipArtworkDiscoveryCandidate struct {
	ordinal, sourceOrdinal                     int64 // RAW page order versus original per-role order
	role, imageID, origin, locale, attribution string
	score                                      float64
}

type ipArtworkDiscoveryRequest struct {
	owner                                ipArtworkDiscoveryOwner
	spec                                 ipArtworkDiscoverySpec
	stageID, stageRevision, afterOrdinal int64 // afterOrdinal is the next RAW ordinal
	candidates                           []ipArtworkDiscoveryCandidate
}

// The saved result is the POST-CAS stage head only. It carries no selected
// candidate, file manifest, digest of image bytes or source visibility.
type ipArtworkDiscoveryResult struct {
	StageID, Revision, Count, AfterOrdinal int64
}

type ipArtworkDiscoveryStage struct {
	revision, count, bytes, reclaimAfter int64
	state                                string
}

type ipArtworkDiscoveryCommand struct {
	identity CommandIdentity
	request  ipArtworkDiscoveryRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkDiscoveryCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkDiscoveryResult
	Digest  ResultDigest
}

type ipArtworkDiscoveryOutcome struct {
	decision receiptDecision
	result   ipArtworkDiscoveryResult
	digest   ResultDigest
}

// Saved results use W's sole result codec; the source manifest stays a separate
// domain and is never relabeled as a result or proof.
type ipArtworkDiscoverySavedResult struct {
	Kind  CommandID
	Value ipArtworkDiscoveryResult
}

func (c *ipArtworkDiscoveryCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkDiscoveryKind(kind CommandID) bool {
	return kind == IPArtworkDiscoveryClaim || kind == IPArtworkDiscoveryPage || kind == IPArtworkDiscoveryFinish
}

func ipArtworkDiscoveryRole(role string) bool {
	switch role {
	case "poster", "backdrop", "still", "logo", "square", "portrait", "banner", "cover":
		return true
	}
	return false
}

func ipArtworkDiscoveryProvider(provider string) bool {
	// Artwork never borrows the MusicBrainz metadata eligibility; only the two
	// screen-image providers carry an image page under screen consent.
	return provider == "tmdb" || provider == "tvdb"
}

func ipArtworkDiscoveryText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

// A candidate's retained manifest bytes are 3 scalar64 + 5 length64 fields plus
// the actual UTF-8 payload. This is logical retained debt, not WAL or decoded
// image memory.
func ipArtworkDiscoveryCandidateBytes(c ipArtworkDiscoveryCandidate) (int64, error) {
	if !ipArtworkDiscoveryRole(c.role) || c.role != strings.ToLower(strings.TrimSpace(c.role)) ||
		c.ordinal < 0 || c.sourceOrdinal < 0 || math.IsNaN(c.score) || math.IsInf(c.score, 0) ||
		!ipArtworkDiscoveryText(c.role, 64) ||
		!ipArtworkDiscoveryText(c.imageID, 4096) || c.imageID == "" ||
		!ipArtworkDiscoveryText(c.origin, 4096) || c.origin == "" ||
		!ipArtworkDiscoveryText(c.locale, 64) ||
		!ipArtworkDiscoveryText(c.attribution, ipArtworkDiscoveryMaxText) {
		return 0, invalid("ip_artwork_candidate")
	}
	if c.locale != "" {
		if _, err := language.Parse(c.locale); err != nil {
			return 0, invalid("ip_artwork_locale")
		}
	}
	return 64 + int64(len(c.role)+len(c.imageID)+len(c.origin)+len(c.locale)+len(c.attribution)), nil
}

func ipArtworkDiscoveryRequestShape(kind CommandID, identity CommandIdentity, q ipArtworkDiscoveryRequest) error {
	if !ipArtworkDiscoveryKind(kind) || !ipPolicyCommandIdentityValid(identity) {
		return ErrInvalid
	}
	o, s := q.owner, q.spec
	if o.id <= 0 || o.incarnation != identity.Incarnation || (o.entityID > 0) == (o.personID > 0) ||
		o.entityID < 0 || o.personID < 0 || o.sourceIncarnation == [16]byte{} || o.sourceRevision <= 0 ||
		o.revision <= 0 || o.revision == math.MaxInt64 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 ||
		o.leaseUntilMS <= 0 {
		return ErrInvalid
	}
	if !ipArtworkDiscoveryProvider(s.provider) || !ipArtworkDiscoveryText(s.language, 64) ||
		s.policyRevision <= 0 || s.consentRevision <= 0 || s.generation <= 0 ||
		s.expectedCount < 0 || s.expectedCount > ipArtworkDiscoveryMaxCandidates ||
		s.expectedBytes < 0 || s.expectedBytes > ipArtworkDiscoveryMaxBytes ||
		(s.expectedCount == 0) != (s.expectedBytes == 0) || s.expectedBytes < 64*s.expectedCount {
		return ErrInvalid
	}
	switch kind {
	case IPArtworkDiscoveryClaim:
		if q.stageID != 0 || q.stageRevision != 0 || q.afterOrdinal != 0 || len(q.candidates) != 0 {
			return ErrInvalid
		}
	case IPArtworkDiscoveryPage:
		if q.stageID <= 0 || q.stageRevision <= 0 || q.stageRevision == math.MaxInt64 ||
			q.afterOrdinal < 0 || q.afterOrdinal > s.expectedCount ||
			len(q.candidates) == 0 || len(q.candidates) > ipArtworkDiscoveryPage ||
			int64(len(q.candidates)) > s.expectedCount-q.afterOrdinal {
			return ErrInvalid
		}
	case IPArtworkDiscoveryFinish:
		if q.stageID <= 0 || q.stageRevision <= 0 || q.stageRevision == math.MaxInt64 ||
			q.afterOrdinal < 0 || q.afterOrdinal != s.expectedCount || len(q.candidates) != 0 {
			return ErrInvalid
		}
	}
	if kind == IPArtworkDiscoveryPage {
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

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkDiscoveryCommand(kind CommandID, identity CommandIdentity, in ipArtworkDiscoveryRequest) (*ipArtworkDiscoveryCommand, error) {
	if err := ipArtworkDiscoveryRequestShape(kind, identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkDiscoveryCommand{identity: identity, request: in, digest: digest, valid: true}, nil
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

// Original observation commitment only. Finish re-reads the complete native
// page and recomputes this; no stored digest or caller flag becomes proof.
func ipArtworkDiscoveryManifestDigest(o ipArtworkDiscoveryOwner, s ipArtworkDiscoverySpec, rows []ipArtworkDiscoveryCandidate) [32]byte {
	h := sha256.New()
	ipArtworkDiscoveryPutText(h, ipArtworkDiscoveryManifest)
	for _, n := range []int64{o.id, o.entityID, o.personID, o.sourceRevision, s.policyRevision,
		s.consentRevision, s.generation, s.expectedCount, s.expectedBytes} {
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
		score := c.score
		if score == 0 {
			score = 0 // SQLite normalizes -0 before readback
		}
		ipArtworkDiscoveryPut(h, int64(math.Float64bits(score)))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func ipArtworkDiscoveryResultDigest(kind CommandID, identity CommandIdentity, request RequestDigest, value ipArtworkDiscoveryResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkDiscoverySavedResult{kind, value})
}
