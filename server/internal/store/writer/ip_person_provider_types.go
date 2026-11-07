package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Unadopted IP-001 D body components. W must compose current disclosure and
// mutation permission, owner birth admission, actual governor/physical memory,
// clock, receipt-before-fresh, safe COMMIT/resolve and Capture. No product entry
// point, authority bool, capability, second writer or public Applied exists here.
//
// This first closed person slice admits ONLY header, aliases and external IDs.
// Credits, terms, ratings, episodes, tracks, media, attachments and collections
// are excluded, not claimed complete. Parser/request namespaces never enter EN.
const (
	ipPersonParser      = "portico-person-header-alias-id-v1"
	ipPersonShape       = "person-header-alias-id-v1"
	ipPersonManifest    = "portico-ip-person-source-sha256-v1"
	ipPersonMaxChildren = 32
	ipPersonChunk       = 8
	ipPersonMaxBytes    = 64 << 10
)

type ipPersonNamespace struct {
	subjectID                                     int64
	provider, providerID, language, region, shape string
	incarnation                                   [16]byte
	revision                                      int64
}
type ipPersonSource struct {
	role                  string // explicitly captured; this slice admits primary only
	incarnation           [16]byte
	revision              int64 // zero means a natively proved absent primary head
	publication, evidence string
}
type ipPersonOwner struct {
	id                              string
	personID                        int64
	token                           string
	personRevision, libraryID       int64
	libraryLanguage                 string
	libraryMetadataRevision         int64
	namespace                       ipPersonNamespace
	policyIncarnation               [16]byte
	policyRevision, consentRevision int64
	generation, revision            int64
	phase                           string
	leaseOwner                      string
	leaseGeneration, leaseUntilMS   int64
	nextAttempt                     sql.NullInt64
	source                          ipPersonSource
}
type ipPersonHeader struct {
	name, biography, birthDate, deathDate, birthPlace string
	observedAt, sourceURL                             string
}
type ipPersonAlias struct{ requestedID, canonicalID, alias, observedAt string }
type ipPersonExternalID struct{ provider, kind, id string }
type ipPersonEvidence struct {
	id, buildID string
	header      ipPersonHeader
	aliases     []ipPersonAlias
	externalIDs []ipPersonExternalID // strict tuple(provider,kind,id) ascending
	commitment  [32]byte
	bytes       int64
}

// Full ORIGINAL root, including complete immutable source, CAS, source role,
// lease proposal, terminal input and identity. Never digest a selected subset.
type ipPersonRequest struct {
	identity                                             CommandIdentity
	owner                                                ipPersonOwner
	evidence                                             ipPersonEvidence
	nowMS, after, through                                int64
	buildRevision                                        int64
	publicationID                                        string
	nextLeaseOwner                                       string
	nextLeaseGeneration, nextLeaseUntilMS, nextAttemptMS int64
	terminal                                             string // retry or finish; terminal data is part of the original root
	sealSequence                                         int64
}
type ipPersonResult struct {
	revision, after, count, appliedMS  int64
	evidenceID, buildID, publicationID string
	sealAlgorithm                      string
	sealVersion                        int64
	sealCommitment                     [32]byte
	sealRows, sealBytes                int64
}
type ipPersonSavedResult struct {
	kind  CommandID
	value ipPersonResult
}
type ipPersonOutcome struct {
	decision receiptDecision
	result   ipPersonResult
	change   ipPersonSearchIntent
}

// This is a COMPOSED change intent. It is not a durable PB publication and must
// be consumed by reviewed W/PB in the same accepting transaction before COMMIT.
// Replay emits no intent. No live calls, FTS writes or new publisher here.
type ipPersonSearchIntent struct {
	personID, revision   int64
	token, publicationID string
}

// Local copy ledger only. Outer W must already hold admitted physical bytes;
// neither this counter nor a manifest is authority or a governor capability.
type ipPersonDecode struct{ remaining int64 }

func (b *ipPersonDecode) reserve(n int64) error {
	if b == nil || n < 0 || n > ipPersonMaxBytes || n > b.remaining {
		return ErrQuantum
	}
	b.remaining -= n
	return nil
}
func ipPersonText(s string, max int, required bool) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func ipPersonDate(s string) bool {
	if s == "" {
		return true
	}
	d, e := time.Parse("2006-01-02", s)
	return e == nil && d.Format("2006-01-02") == s
}
func ipPersonExternalLess(a, b ipPersonExternalID) bool {
	if a.provider != b.provider {
		return a.provider < b.provider
	}
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	return a.id < b.id
}
func (v ipPersonEvidence) rows() int64 { return 1 + int64(len(v.aliases)+len(v.externalIDs)) }
func ipPersonValidHeader(h ipPersonHeader) bool {
	return ipPersonText(h.name, 4096, true) && ipPersonText(h.biography, 16384, false) && ipPersonDate(h.birthDate) && ipPersonDate(h.deathDate) && ipPersonText(h.birthPlace, 4096, false) && ipPersonText(h.observedAt, 128, true) && ipPersonText(h.sourceURL, 4096, true)
}
func (v ipPersonEvidence) valid(n ipPersonNamespace) bool {
	if !ipPersonText(v.id, 128, true) || !ipPersonText(v.buildID, 128, true) || !ipPersonValidHeader(v.header) || len(v.aliases)+len(v.externalIDs) > ipPersonMaxChildren {
		return false
	}
	for _, a := range v.aliases {
		if !ipPersonText(a.requestedID, 256, true) || a.canonicalID != n.providerID || !ipPersonText(a.alias, 4096, false) || !ipPersonText(a.observedAt, 128, true) {
			return false
		}
	}
	for i, x := range v.externalIDs {
		if !ipPersonText(x.provider, 128, true) || x.kind != "person" || !ipPersonText(x.id, 256, true) || (i > 0 && !ipPersonExternalLess(v.externalIDs[i-1], x)) {
			return false
		}
	}
	d, b, e := ipPersonManifestDigest(n, v)
	return e == nil && b <= ipPersonMaxBytes && b == v.bytes && d == v.commitment
}
func (r ipPersonRequest) valid(kind CommandID) bool {
	o, n := r.owner, r.owner.namespace
	if !ipPersonText(r.publicationID, 128, false) || !ipPersonText(r.nextLeaseOwner, 128, false) || !ipPersonText(r.terminal, 8, false) || r.after < 0 || r.after > ipPersonMaxChildren+1 || r.through < 0 || r.through > ipPersonMaxChildren+1 || r.buildRevision < 0 || r.nextLeaseGeneration < 0 || r.nextLeaseUntilMS < 0 || r.nextAttemptMS < 0 || r.sealSequence < 0 {
		return false
	}
	if kind == IPProviderClaim || kind == IPProviderRenew || kind == IPProviderFinish {
		v := r.evidence
		if v.id != "" || v.buildID != "" || v.header != (ipPersonHeader{}) || len(v.aliases) != 0 || len(v.externalIDs) != 0 || v.bytes != 0 || v.commitment != [32]byte{} {
			return false
		}
	}
	if !ipPolicyValidIdentity(r.identity) || !ipPersonText(o.id, 128, true) || o.personID <= 0 || !ipPersonText(o.token, 256, true) || o.personRevision <= 0 || o.libraryID <= 0 || !ipPersonText(o.libraryLanguage, 128, false) || o.libraryMetadataRevision <= 0 || n.subjectID <= 0 || !ipPersonText(n.provider, 128, true) || !ipPersonText(n.providerID, 256, true) || !ipPersonText(n.language, 32, true) || !ipPersonText(n.region, 32, false) || n.shape != ipPersonShape || n.incarnation == [16]byte{} || n.revision <= 0 || o.policyIncarnation == [16]byte{} || o.policyRevision <= 0 || o.consentRevision <= 0 || o.generation <= 0 || o.revision <= 0 || (o.phase != "claimed" && o.phase != "building" && o.phase != "sealed" && o.phase != "stale") || !ipPersonText(o.leaseOwner, 128, true) || o.leaseGeneration <= 0 || o.leaseUntilMS < 0 || (!o.nextAttempt.Valid && o.nextAttempt.Int64 != 0) || (o.nextAttempt.Valid && o.nextAttempt.Int64 < 0) || o.source.role != "primary" || o.source.incarnation == [16]byte{} || o.source.revision < 0 || !ipPersonText(o.source.publication, 128, false) || !ipPersonText(o.source.evidence, 128, false) || (o.source.publication == "") != (o.source.evidence == "") || (o.source.revision == 0 && (o.source.publication != "" || o.source.evidence != "")) || r.nowMS < 0 {
		return false
	}
	switch kind {
	case IPProviderClaim, IPProviderRenew:
		return ipPersonText(r.nextLeaseOwner, 128, true) && r.nextLeaseGeneration > 0 && r.nextLeaseUntilMS > r.nowMS
	case IPProviderFinish:
		return (r.terminal == "retry" && r.nextAttemptMS >= r.nowMS) || r.terminal == "finish"
	case IPEvidenceBegin, IPEvidenceAppend, IPEvidenceSeal, IPProviderAccept, IPProviderDelta:
		if kind == IPEvidenceBegin && o.phase != "claimed" {
			return false
		}
		if !r.evidence.valid(n) {
			return false
		}
		if kind == IPEvidenceAppend {
			return r.after >= 1 && r.through > r.after && r.through <= r.evidence.rows() && r.through-r.after <= ipPersonChunk
		}
		if kind == IPProviderDelta && (o.source.revision <= 0 || o.source.publication == "" || o.source.evidence == "") {
			return false
		}
		if kind == IPProviderAccept || kind == IPProviderDelta {
			return ipPersonText(r.publicationID, 128, true) && r.sealSequence > 0
		}
		return true
	default:
		return false
	}
}
func (r ipPersonRequest) digest(k CommandID) (RequestDigest, error) {
	return canonicalRequestDigest(k, 1, r.identity, r)
}
func ipPersonResultDigest(k CommandID, i CommandIdentity, q RequestDigest, r ipPersonResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, ipPersonSavedResult{k, r})
}
func ipPersonNullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func ipPersonNullable(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}
func ipPersonHex(v [32]byte) string { return hex.EncodeToString(v[:]) }

// Exact source manifest wire (all unsigned integers BE64; text is BE64 length
// then UTF8): LP domain, LP parser, subjectID, LP provider, LP literal person,
// LP providerID/language/region/requestShape, incarnation16, subjectRevision,
// header row tag=0, LP name/biography/birthDate/deathDate/birthPlace/observedAt/
// sourceURL, aliasCount, each alias tag=1, ordinal, LP requestedID/canonicalID/
// alias/observedAt, externalCount, each external tag=2, LP provider/kind/id.
// No evidence/build ID or expected manifest is hashed into its own commitment.
// W's byte primitives are reused; this new SOURCE domain is not a request/result
// codec. Native verification rebuilds this exact stream from independent rows.
func ipPersonManifestDigest(n ipPersonNamespace, v ipPersonEvidence) ([32]byte, int64, error) {
	e := requestEncoder{h: sha256.New()}
	e.text(ipPersonManifest)
	e.text(ipPersonParser)
	e.number(uint64(n.subjectID))
	e.text(n.provider)
	e.text("person")
	e.text(n.providerID)
	e.text(n.language)
	e.text(n.region)
	e.text(n.shape)
	e.bytes(n.incarnation[:])
	e.number(uint64(n.revision))
	e.number(0)
	for _, s := range []string{v.header.name, v.header.biography, v.header.birthDate, v.header.deathDate, v.header.birthPlace, v.header.observedAt, v.header.sourceURL} {
		e.text(s)
	}
	e.number(uint64(len(v.aliases)))
	for i, a := range v.aliases {
		e.number(1)
		e.number(uint64(i))
		e.text(a.requestedID)
		e.text(a.canonicalID)
		e.text(a.alias)
		e.text(a.observedAt)
	}
	e.number(uint64(len(v.externalIDs)))
	for _, x := range v.externalIDs {
		e.number(2)
		e.text(x.provider)
		e.text(x.kind)
		e.text(x.id)
	}
	var d [32]byte
	copy(d[:], e.h.Sum(nil))
	return d, int64(e.written), e.err
}

// Preparation owns these finite copied child slices. This is normalization,
// not provider IO, writer admission, a seal capability or W authority.
func ipPersonPrepareEvidence(n ipPersonNamespace, id, buildID string, h ipPersonHeader, aliases []ipPersonAlias, external []ipPersonExternalID) (ipPersonEvidence, error) {
	if len(aliases)+len(external) > ipPersonMaxChildren {
		return ipPersonEvidence{}, ErrQuantum
	}
	v := ipPersonEvidence{id: id, buildID: buildID, header: h, aliases: append([]ipPersonAlias(nil), aliases...), externalIDs: append([]ipPersonExternalID(nil), external...)}
	d, b, e := ipPersonManifestDigest(n, v)
	if e != nil {
		return ipPersonEvidence{}, e
	}
	v.commitment = d
	v.bytes = b
	if !v.valid(n) {
		return ipPersonEvidence{}, ErrInvalid
	}
	return v, nil
}

func ipPersonOptionalInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
