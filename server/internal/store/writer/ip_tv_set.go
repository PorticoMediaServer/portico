package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// IP TV child publication set 213/214/215. One sealed IP-07 series/season
// evidence declares the COMPLETE ordered episode evidence list of the provider
// hierarchy. Begin births one immutable set header under the exact IP-05
// owner/lease; page appends one raw bounded page of the next contiguous ordinal
// range and records the provider's exact next-page truth; seal proves every page
// and member against the begin declaration before marking the set complete and
// attaching the one set_seal proof. A scalar header never completes children:
// only the declared member count/digest and every page can. The set never
// accepts a source, rewrites a catalogue identity or clears an undeclared child.
const (
	ipTVSetAlgorithm  = "portico.ip-tv-set-manifest.sha256.v1"
	ipTVSetVersion    = int64(1)
	ipTVSetIDMax      = 128
	ipTVSetPageMax    = 200
	ipTVSetPagesMax   = 100
	ipTVSetMembersMax = 20000
	ipTVSetMemberBase = int64(512)
)

func ipTVSetText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func ipTVSetSubjectKind(value string) bool {
	return value == "series" || value == "season"
}

// The numbering namespace is closed. Aired and absolute coordinates are
// separate provider namespaces; alternative numbering never reinterprets either.
func ipTVSetOrder(value string) bool {
	return value == "aired" || value == "absolute"
}

// TVSetMemberCharge is the exact retained row charge of one set member. It is
// pure data: the member body is the evidence ID plus fixed row overhead.
func TVSetMemberCharge(evidenceID string) (int64, error) {
	if !ipTVSetText(evidenceID, ipTVSetIDMax) || len(evidenceID) == 0 {
		return 0, ErrInvalid
	}
	return ipTVSetMemberBase + 8*int64(len(evidenceID)), nil
}

// TVSetMemberDigest is the complete ordered member identity of one TV
// publication set. Begin declares it; seal recomputes it from the exact
// persisted page/member rows, so a missing, extra or reordered member changes it.
func TVSetMemberDigest(evidenceIDs []string) ([32]byte, error) {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_tv_set_members.v1\x00"))
	e.number(uint64(len(evidenceIDs)))
	for _, id := range evidenceIDs {
		if !ipTVSetText(id, ipTVSetIDMax) || len(id) == 0 {
			return [32]byte{}, ErrInvalid
		}
		e.text(id)
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

type ipTVSetBeginRequest struct {
	operationID, provider, purpose, selectedID, lease    string
	entityID, generation, leaseUntilMS                   int64
	setID, subjectKind, providerID, episodeOrder         string
	evidenceID, buildID                                  string
	buildGeneration, buildRevision                       int64
	sourceRevision, hierarchyRevision, selectionRevision int64
	memberCount                                          int64
	memberDigest                                         [32]byte
}

type ipTVSetPageRequest struct {
	operationID, provider, purpose, selectedID, lease    string
	entityID, generation, leaseUntilMS                   int64
	setID, subjectKind, providerID, episodeOrder         string
	setRevision, memberStart, page                       int64
	sourceRevision, hierarchyRevision, selectionRevision int64
	nextPage                                             *int64
	members                                              []string
}

// ipTVSetSealPage is one exact page of the complete manifest in provider order.
// NextPage nil is the only no-next declaration.
type ipTVSetSealPage struct {
	Page     int64
	NextPage *int64
	Members  []string
}

type ipTVSetSealRequest struct {
	operationID, provider, purpose, selectedID, lease    string
	entityID, generation, leaseUntilMS                   int64
	setID, subjectKind, providerID, episodeOrder         string
	setRevision                                          int64
	evidenceID, buildID                                  string
	buildGeneration, buildRevision                       int64
	sourceRevision, hierarchyRevision, selectionRevision int64
	pages                                                []ipTVSetSealPage
}

type ipTVSetPageManifest struct {
	page, rows, bytes int64
	nextPage          int64
	nextPresent       bool
	digest            [32]byte
	members           []string
}

type ipTVSetManifest struct {
	pages        []ipTVSetPageManifest
	memberCount  int64
	bytes        int64
	memberDigest [32]byte
	pagesDigest  [32]byte
	commitment   [32]byte
}

type ipTVSetCommand struct {
	kind     string
	identity CommandIdentity
	digest   RequestDigest
	begin    ipTVSetBeginRequest
	page     ipTVSetPageRequest
	seal     ipTVSetSealRequest
	manifest ipTVSetManifest
	valid    bool
}

type ipTVSetCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipTVSetResult
	Digest  ResultDigest
}

type ipTVSetHeader struct {
	provider, subjectKind, providerID, episodeOrder      string
	incarnation                                          [16]byte
	revision, generation, entityID                       int64
	complete                                             int64
	proofOwner                                           string
	sourceRevision, hierarchyRevision, selectionRevision int64
	afterItem, coverageComplete                          int64
}

type ipTVSetLastPage struct {
	page, nextPage int64
	nextPresent    bool
	found          bool
}

type ipTVSetReceipt struct {
	sequence, appliedMS, acceptedRevision               int64
	afterFamily, afterOrdinal, resultCount, resultBytes int64
	kind, phase, setID, afterKey                        string
	terminal                                            bool
	operationID                                         [16]byte
	requestDigest, resultDigest                         [32]byte
	evidenceID, parserVersion, buildID                  string
	evidenceSubjectID                                   int64
	algorithm                                           string
	version, rows, bytes                                int64
	commitment                                          [32]byte
}

type ipTVSetLookup struct {
	owner      ipEntityJobOwner
	receipt    ipTVSetReceipt
	hasReceipt bool
	decision   receiptDecision
	value      ipTVSetResult
	digest     ResultDigest
}

type ipTVSetReceiptFields struct {
	evidenceID          any
	evidenceSubjectID   any
	evidenceSubjectKind any
	evidenceProviderID  any
	parserVersion       any
	buildID             any
	afterFamily         int64
	afterOrdinal        int64
	afterKey            string
	terminal            int64
	proofPins           int64
	sealAlgorithm       any
	sealVersion         any
	sealCommitment      any
	sealRows            any
	sealBytes           any
}

func (c *ipTVSetCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipTVSetCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipTVSetCommand")
}

func ipTVSetBeginShape(q ipTVSetBeginRequest) error {
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return ErrInvalid
	}
	if !ipTVSetText(q.setID, ipTVSetIDMax) || len(q.setID) == 0 ||
		!ipTVSetSubjectKind(q.subjectKind) || !ipTVSetOrder(q.episodeOrder) ||
		!ipTVSetText(q.providerID, ipTVSetIDMax) || len(q.providerID) == 0 ||
		!ipTVSetText(q.evidenceID, ipTVSetIDMax) || len(q.evidenceID) == 0 ||
		!ipTVSetText(q.buildID, ipTVSetIDMax) || len(q.buildID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.buildRevision <= 0 || q.buildRevision == math.MaxInt64 ||
		q.sourceRevision <= 0 || q.sourceRevision == math.MaxInt64 ||
		q.hierarchyRevision < 0 || q.hierarchyRevision == math.MaxInt64 ||
		q.selectionRevision < 0 || q.selectionRevision == math.MaxInt64 ||
		q.memberCount <= 0 || q.memberCount > ipTVSetMembersMax ||
		!ipTVSetDigestSet(q.memberDigest) {
		return ErrInvalid
	}
	return nil
}

func ipTVSetPageShape(q ipTVSetPageRequest) (int64, [32]byte, error) {
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return 0, [32]byte{}, ErrInvalid
	}
	if !ipTVSetText(q.setID, ipTVSetIDMax) || len(q.setID) == 0 ||
		!ipTVSetSubjectKind(q.subjectKind) || !ipTVSetOrder(q.episodeOrder) ||
		!ipTVSetText(q.providerID, ipTVSetIDMax) || len(q.providerID) == 0 ||
		q.setRevision <= 0 || q.setRevision == math.MaxInt64 ||
		q.memberStart < 0 || q.memberStart == math.MaxInt64 ||
		q.page < 0 || q.page >= ipTVSetPagesMax ||
		q.sourceRevision <= 0 || q.sourceRevision == math.MaxInt64 ||
		q.hierarchyRevision < 0 || q.hierarchyRevision == math.MaxInt64 ||
		q.selectionRevision < 0 || q.selectionRevision == math.MaxInt64 ||
		len(q.members) == 0 || len(q.members) > ipTVSetPageMax ||
		q.memberStart > ipTVSetMembersMax-int64(len(q.members)) {
		return 0, [32]byte{}, ErrInvalid
	}
	if q.nextPage != nil && (*q.nextPage != q.page+1 || *q.nextPage >= ipTVSetPagesMax) {
		return 0, [32]byte{}, ErrInvalid
	}
	seen := make(map[string]bool, len(q.members))
	var bytes int64
	for _, member := range q.members {
		if !ipTVSetText(member, ipTVSetIDMax) || len(member) == 0 || seen[member] {
			return 0, [32]byte{}, ErrInvalid
		}
		seen[member] = true
		charge, err := TVSetMemberCharge(member)
		if err != nil || bytes > math.MaxInt64-charge {
			return 0, [32]byte{}, ErrInvalid
		}
		bytes += charge
	}
	digest, err := ipTVSetPageDigest(q.page, q.nextPage, q.members)
	if err != nil {
		return 0, [32]byte{}, err
	}
	return bytes, digest, nil
}

func ipTVSetSealShape(q ipTVSetSealRequest) (ipTVSetManifest, error) {
	var zero ipTVSetManifest
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return zero, ErrInvalid
	}
	if !ipTVSetText(q.setID, ipTVSetIDMax) || len(q.setID) == 0 ||
		!ipTVSetSubjectKind(q.subjectKind) || !ipTVSetOrder(q.episodeOrder) ||
		!ipTVSetText(q.providerID, ipTVSetIDMax) || len(q.providerID) == 0 ||
		!ipTVSetText(q.evidenceID, ipTVSetIDMax) || len(q.evidenceID) == 0 ||
		!ipTVSetText(q.buildID, ipTVSetIDMax) || len(q.buildID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.buildRevision <= 0 || q.buildRevision == math.MaxInt64 ||
		q.sourceRevision <= 0 || q.sourceRevision == math.MaxInt64 ||
		q.hierarchyRevision < 0 || q.hierarchyRevision == math.MaxInt64 ||
		q.selectionRevision < 0 || q.selectionRevision == math.MaxInt64 ||
		q.setRevision <= 0 || q.setRevision == math.MaxInt64 ||
		len(q.pages) == 0 || len(q.pages) > ipTVSetPagesMax ||
		q.setRevision != int64(len(q.pages))+1 {
		return zero, ErrInvalid
	}
	seen := make(map[string]bool)
	all := make([]string, 0)
	pages := make([]ipTVSetPageManifest, 0, len(q.pages))
	var total, bytes int64
	for i, page := range q.pages {
		if page.Page != int64(i) {
			return zero, ErrInvalid
		}
		if i == len(q.pages)-1 {
			if page.NextPage != nil {
				return zero, ErrInvalid
			}
		} else if page.NextPage == nil || *page.NextPage != page.Page+1 || *page.NextPage >= ipTVSetPagesMax {
			return zero, ErrInvalid
		}
		if len(page.Members) == 0 || len(page.Members) > ipTVSetPageMax ||
			total > ipTVSetMembersMax-int64(len(page.Members)) {
			return zero, ErrInvalid
		}
		var pageBytes int64
		for _, member := range page.Members {
			if !ipTVSetText(member, ipTVSetIDMax) || len(member) == 0 || seen[member] {
				return zero, ErrInvalid
			}
			seen[member] = true
			charge, err := TVSetMemberCharge(member)
			if err != nil || pageBytes > math.MaxInt64-charge || bytes > math.MaxInt64-charge {
				return zero, ErrInvalid
			}
			pageBytes += charge
			bytes += charge
			all = append(all, member)
		}
		total += int64(len(page.Members))
		digest, err := ipTVSetPageDigest(page.Page, page.NextPage, page.Members)
		if err != nil {
			return zero, ErrInvalid
		}
		manifest := ipTVSetPageManifest{page: page.Page, rows: int64(len(page.Members)),
			bytes: pageBytes, digest: digest, members: page.Members}
		if page.NextPage != nil {
			manifest.nextPresent, manifest.nextPage = true, *page.NextPage
		}
		pages = append(pages, manifest)
	}
	if total <= 0 || total > ipTVSetMembersMax || bytes <= 0 {
		return zero, ErrInvalid
	}
	memberDigest, err := TVSetMemberDigest(all)
	if err != nil {
		return zero, ErrInvalid
	}
	pagesDigest := ipTVSetPagesDigest(pages)
	commitment := ipTVSetCommitment(q, memberDigest, pagesDigest, total, bytes)
	return ipTVSetManifest{pages: pages, memberCount: total, bytes: bytes,
		memberDigest: memberDigest, pagesDigest: pagesDigest, commitment: commitment}, nil
}

func ipTVSetManifestEqual(a, b ipTVSetManifest) bool {
	if a.memberCount != b.memberCount || a.bytes != b.bytes ||
		a.memberDigest != b.memberDigest || a.pagesDigest != b.pagesDigest ||
		a.commitment != b.commitment || len(a.pages) != len(b.pages) {
		return false
	}
	for i := range a.pages {
		left, right := a.pages[i], b.pages[i]
		if left.page != right.page || left.rows != right.rows || left.bytes != right.bytes ||
			left.nextPage != right.nextPage || left.nextPresent != right.nextPresent ||
			left.digest != right.digest || len(left.members) != len(right.members) {
			return false
		}
		for j := range left.members {
			if left.members[j] != right.members[j] {
				return false
			}
		}
	}
	return true
}

func ipTVSetPageDigest(page int64, nextPage *int64, members []string) ([32]byte, error) {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_tv_set_page.v1\x00"))
	e.number(uint64(page))
	e.flag(nextPage != nil)
	if nextPage != nil {
		e.number(uint64(*nextPage))
	}
	e.number(uint64(len(members)))
	for _, member := range members {
		e.text(member)
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

func ipTVSetPagesDigest(pages []ipTVSetPageManifest) [32]byte {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_tv_set_pages.v1\x00"))
	e.number(uint64(len(pages)))
	for _, page := range pages {
		e.number(uint64(page.page))
		e.flag(page.nextPresent)
		if page.nextPresent {
			e.number(uint64(page.nextPage))
		}
		e.number(uint64(page.rows))
		e.number(uint64(page.bytes))
		e.bytes(page.digest[:])
	}
	var out [32]byte
	if e.err == nil {
		copy(out[:], e.h.Sum(nil))
	}
	return out
}

func ipTVSetCommitment(q ipTVSetSealRequest, memberDigest, pagesDigest [32]byte, rows, bytes int64) [32]byte {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_tv_set_seal.v1\x00"))
	e.text(q.setID)
	e.text(q.provider)
	e.text(q.subjectKind)
	e.text(q.providerID)
	e.text(q.episodeOrder)
	e.text(q.evidenceID)
	e.text(q.buildID)
	e.number(uint64(q.buildGeneration))
	e.number(uint64(q.buildRevision))
	e.number(uint64(q.sourceRevision))
	e.number(uint64(q.hierarchyRevision))
	e.number(uint64(q.selectionRevision))
	e.number(uint64(rows))
	e.bytes(memberDigest[:])
	e.bytes(pagesDigest[:])
	e.number(uint64(bytes))
	var out [32]byte
	if e.err == nil {
		copy(out[:], e.h.Sum(nil))
	}
	return out
}

func newIPTVSetBeginCommand(identity CommandIdentity, in ipTVSetBeginRequest) (*ipTVSetCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipTVSetBeginShape(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPTVSetBegin, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipTVSetCommand{kind: "begin", identity: identity, begin: in, digest: digest, valid: true}, nil
}

func newIPTVSetPageCommand(identity CommandIdentity, in ipTVSetPageRequest) (*ipTVSetCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if _, _, err := ipTVSetPageShape(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPTVSetPage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipTVSetCommand{kind: "page", identity: identity, page: in, digest: digest, valid: true}, nil
}

func newIPTVSetSealCommand(identity CommandIdentity, in ipTVSetSealRequest) (*ipTVSetCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	manifest, err := ipTVSetSealShape(in)
	if err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPTVSetSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipTVSetCommand{kind: "seal", identity: identity, seal: in, manifest: manifest,
		digest: digest, valid: true}, nil
}

func ipTVSetReceiptKind(kind string) (CommandID, bool) {
	switch kind {
	case "set_begin":
		return IPTVSetBegin, true
	case "set_page":
		return IPTVSetPage, true
	case "set_seal":
		return IPTVSetSeal, true
	}
	return 0, false
}

// ipTVSetLookupReceipt resolves the ORIGINAL receipt before any fresh lease,
// hierarchy or observation probe. Current authority is checked by the caller
// first. Begin/page/seal never mint an owner; the exact IP-05 claim must already
// exist under this provider namespace.
func ipTVSetLookupReceipt(ctx context.Context, turn *commandTurn, purpose CommandID, ref ReceiptReference,
	ownerID, provider string, entityID int64, decode func(ipTVSetReceipt) (ipTVSetResult, error)) (out ipTVSetLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobOwnerStmt, ownerID, provider)
	if err != nil {
		return out, err
	}
	var incarnation, entityIncarnation, policyIncarnation []byte
	err = row.Scan(&incarnation, &out.owner.lastSequence, &out.owner.acknowledgedSequence,
		&out.owner.generation, &out.owner.status, &out.owner.lease, &out.owner.leaseUntil,
		&out.owner.entityID, &entityIncarnation, &out.owner.sourceRevision, &out.owner.identityRevision,
		&out.owner.metadataRevision, &policyIncarnation, &out.owner.policyRevision,
		&out.owner.consentRevision, &out.owner.selectedID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if _, ok := ipEntityJobTextTime(out.owner.leaseUntil); !ok {
		return out, ErrResolveRequired
	}
	if len(incarnation) != 16 || len(entityIncarnation) != 16 || len(policyIncarnation) != 16 ||
		out.owner.entityID != entityID || out.owner.generation <= 0 || out.owner.sourceRevision <= 0 ||
		out.owner.identityRevision <= 0 || out.owner.metadataRevision <= 0 || out.owner.policyRevision <= 0 ||
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 || len(out.owner.selectedID) > ipTVSetIDMax ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipTVSetReceiptStmt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest, commitment []byte
	var terminal int64
	var afterKey, setID, evidenceID, parserVersion, buildID, algorithm sql.NullString
	var evidenceSubject, version, rows, bytes sql.NullInt64
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest,
		&out.receipt.appliedMS, &resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision,
		&out.receipt.phase, &terminal, &out.receipt.afterFamily, &out.receipt.afterOrdinal,
		&afterKey, &out.receipt.resultCount, &out.receipt.resultBytes, &evidenceID,
		&evidenceSubject, &parserVersion, &buildID, &setID, &algorithm, &version, &commitment,
		&rows, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		kind, known := ipTVSetReceiptKind(out.receipt.kind)
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.acceptedRevision <= 0 || !ipEntityJobFlag(terminal) || !known ||
			!setID.Valid || len(setID.String) == 0 || !afterKey.Valid || len(afterKey.String) != 64 ||
			out.receipt.afterFamily < 0 || out.receipt.afterOrdinal < -1 ||
			out.receipt.resultCount < 0 || out.receipt.resultBytes < 0 {
			return out, ErrResolveRequired
		}
		out.receipt.terminal = terminal == 1
		out.receipt.setID = setID.String
		out.receipt.afterKey = afterKey.String
		switch kind {
		case IPTVSetBegin:
			if out.receipt.phase != "building" || out.receipt.terminal ||
				out.receipt.acceptedRevision != 1 || !evidenceID.Valid || !evidenceSubject.Valid ||
				!parserVersion.Valid || !buildID.Valid || evidenceID.String == "" ||
				evidenceSubject.Int64 <= 0 || parserVersion.String == "" || buildID.String == "" ||
				algorithm.Valid || version.Valid || len(commitment) != 0 || rows.Valid || bytes.Valid {
				return out, ErrResolveRequired
			}
		case IPTVSetPage:
			if out.receipt.phase != "building" || out.receipt.terminal ||
				out.receipt.acceptedRevision <= 1 || out.receipt.resultCount <= 0 ||
				out.receipt.resultBytes <= 0 || evidenceID.Valid || evidenceSubject.Valid ||
				parserVersion.Valid || buildID.Valid || algorithm.Valid || version.Valid ||
				len(commitment) != 0 || rows.Valid || bytes.Valid {
				return out, ErrResolveRequired
			}
		case IPTVSetSeal:
			if out.receipt.phase != "sealed" || !out.receipt.terminal ||
				out.receipt.acceptedRevision <= 1 || out.receipt.resultCount <= 0 ||
				out.receipt.resultBytes <= 0 || !evidenceID.Valid || !evidenceSubject.Valid ||
				!parserVersion.Valid || !buildID.Valid || evidenceID.String == "" ||
				evidenceSubject.Int64 <= 0 || parserVersion.String == "" || buildID.String == "" ||
				!algorithm.Valid || algorithm.String == "" || !version.Valid || version.Int64 <= 0 ||
				len(commitment) != 32 || !rows.Valid || rows.Int64 <= 0 ||
				!bytes.Valid || bytes.Int64 <= 0 {
				return out, ErrResolveRequired
			}
		}
		if evidenceID.Valid {
			out.receipt.evidenceID = evidenceID.String
		}
		if evidenceSubject.Valid {
			out.receipt.evidenceSubjectID = evidenceSubject.Int64
		}
		if parserVersion.Valid {
			out.receipt.parserVersion = parserVersion.String
		}
		if buildID.Valid {
			out.receipt.buildID = buildID.String
		}
		if algorithm.Valid {
			out.receipt.algorithm = algorithm.String
		}
		if version.Valid {
			out.receipt.version = version.Int64
		}
		if len(commitment) == 32 {
			copy(out.receipt.commitment[:], commitment)
		}
		if rows.Valid {
			out.receipt.rows = rows.Int64
		}
		if bytes.Valid {
			out.receipt.bytes = bytes.Int64
		}
		copy(out.receipt.operationID[:], operation)
		copy(out.receipt.requestDigest[:], requestDigest)
		copy(out.receipt.resultDigest[:], resultDigest)
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], receiptIncarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: kind, resultCount: 1}
		copy(found.resultDigest[:], resultDigest)
		out.hasReceipt = true
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, purpose,
		receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
			lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.acknowledgedSequence}, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		value, e := decode(out.receipt)
		if e != nil {
			return out, errors.Join(ErrResolveRequired, e)
		}
		computed, e := ipTVSetResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != out.receipt.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

func ipTVSetBeginSaved(q ipTVSetBeginRequest, identity CommandIdentity, r ipTVSetReceipt) (ipTVSetResult, error) {
	var zero ipTVSetResult
	if r.kind != "set_begin" || r.phase != "building" || r.terminal || r.acceptedRevision != 1 ||
		r.operationID != identity.OperationID || r.setID != q.setID ||
		r.afterKey != hex.EncodeToString(q.memberDigest[:]) || r.resultCount != q.memberCount ||
		r.evidenceID != q.evidenceID || r.buildID != q.buildID || r.evidenceSubjectID <= 0 ||
		r.parserVersion == "" {
		return zero, ErrResolveRequired
	}
	return ipTVSetResult{Kind: "begin", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EvidenceID: q.evidenceID, BuildID: q.buildID,
		EntityID: q.entityID, SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision, MemberCount: q.memberCount,
		MemberDigest: q.memberDigest, Revision: 1, OwnerGeneration: q.generation,
		BuildGeneration: q.buildGeneration, LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS}, nil
}

func ipTVSetPageSaved(q ipTVSetPageRequest, identity CommandIdentity, r ipTVSetReceipt,
	bytes int64, digest [32]byte) (ipTVSetResult, error) {
	var zero ipTVSetResult
	rows := int64(len(q.members))
	if r.kind != "set_page" || r.phase != "building" || r.terminal ||
		r.acceptedRevision != q.setRevision+1 || r.operationID != identity.OperationID ||
		r.setID != q.setID || r.afterFamily != q.page ||
		r.afterOrdinal != q.memberStart+rows-1 || r.resultCount != rows ||
		r.resultBytes != bytes || r.afterKey != hex.EncodeToString(digest[:]) {
		return zero, ErrResolveRequired
	}
	return ipTVSetResult{Kind: "page", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EntityID: q.entityID,
		SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision,
		MemberCount:       q.memberStart + rows, Page: q.page, NextPage: nextPageValue(q.nextPage),
		NextPagePresent: q.nextPage != nil, PageRows: rows, PageBytes: bytes, PageDigest: digest,
		Revision: q.setRevision + 1, OwnerGeneration: q.generation,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS}, nil
}

func nextPageValue(next *int64) int64 {
	if next == nil {
		return 0
	}
	return *next
}

func ipTVSetSealSaved(q ipTVSetSealRequest, m ipTVSetManifest, identity CommandIdentity,
	r ipTVSetReceipt) (ipTVSetResult, error) {
	var zero ipTVSetResult
	if r.kind != "set_seal" || r.phase != "sealed" || !r.terminal ||
		r.acceptedRevision != q.setRevision+1 || r.operationID != identity.OperationID ||
		r.setID != q.setID || r.evidenceID != q.evidenceID || r.buildID != q.buildID ||
		r.algorithm != ipTVSetAlgorithm || r.version != ipTVSetVersion ||
		r.rows != m.memberCount || r.bytes != m.bytes || r.commitment != m.commitment ||
		r.resultCount != m.memberCount || r.resultBytes != m.bytes ||
		r.afterKey != hex.EncodeToString(m.pagesDigest[:]) {
		return zero, ErrResolveRequired
	}
	return ipTVSetResult{Kind: "seal", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EvidenceID: q.evidenceID, BuildID: q.buildID,
		EntityID: q.entityID, SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision, MemberCount: m.memberCount,
		MemberDigest: m.memberDigest, Revision: q.setRevision + 1, OwnerGeneration: q.generation,
		BuildGeneration: q.buildGeneration, LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS,
		SealVersion: ipTVSetVersion, SealRows: m.memberCount, SealBytes: m.bytes,
		Commitment: m.commitment}, nil
}

func ipTVSetHeaderRead(ctx context.Context, turn *commandTurn, setID string) (out ipTVSetHeader, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipTVSetHeaderStmt, setID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var proofOwner sql.NullString
	err = row.Scan(&out.provider, &out.subjectKind, &out.providerID, &out.episodeOrder,
		&incarnation, &out.revision, &out.generation, &out.complete, &out.entityID,
		&proofOwner, &out.sourceRevision, &out.hierarchyRevision, &out.selectionRevision,
		&out.afterItem, &out.coverageComplete)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sql.ErrNoRows
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || out.revision <= 0 || out.generation <= 0 ||
		out.entityID <= 0 || !ipPolicyProviderCode(out.provider) ||
		!ipTVSetSubjectKind(out.subjectKind) || !ipTVSetOrder(out.episodeOrder) ||
		len(out.providerID) == 0 || out.sourceRevision <= 0 ||
		out.hierarchyRevision < 0 || out.selectionRevision < 0 ||
		out.afterItem < 0 || !ipEntityJobFlag(out.complete) ||
		!ipEntityJobFlag(out.coverageComplete) {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	if proofOwner.Valid {
		out.proofOwner = proofOwner.String
	}
	return out, nil
}

func ipTVSetLastPageRead(ctx context.Context, turn *commandTurn, setID string) (out ipTVSetLastPage, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipTVSetLastPageStmt, setID)
	if err != nil {
		return out, err
	}
	var next sql.NullInt64
	err = row.Scan(&out.page, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if out.page < 0 || out.page >= ipTVSetPagesMax {
		return out, ErrResolveRequired
	}
	out.found = true
	if next.Valid {
		if next.Int64 != out.page+1 || next.Int64 >= ipTVSetPagesMax {
			return out, ErrResolveRequired
		}
		out.nextPresent, out.nextPage = true, next.Int64
	}
	return out, nil
}

// ipTVSetRevisions proves the captured coverage basis is still current: the
// exact source revision from the owner plus the operation's hierarchy and
// selection revisions. A new observation or hierarchy change is a conflict.
func ipTVSetRevisions(ctx context.Context, turn *commandTurn, operationID, provider string,
	sourceRevision, hierarchyRevision, selectionRevision int64, owner ipEntityJobOwner) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if sourceRevision != owner.sourceRevision || hierarchyRevision < 0 || selectionRevision < 0 {
		return ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, ipTVSetHierarchyStmt, operationID, provider)
	if err != nil {
		return err
	}
	var hierarchy, selection int64
	if err = row.Scan(&hierarchy, &selection); err != nil {
		return ErrConflict
	}
	if hierarchy != hierarchyRevision || selection != selectionRevision {
		return ErrConflict
	}
	return nil
}

func ipTVSetRecordReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	request RequestDigest, value ipTVSetResult, f ipTVSetReceiptFields) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	digest, err := ipTVSetResultDigest(identity, request, value)
	if err != nil {
		return out, err
	}
	// The result kind ("begin"/"page"/"seal") types the result; the receipt
	// stores the command kind ("set_begin"/"set_page"/"set_seal") that the set
	// readers filter on. Writing the result kind here would make every saved
	// receipt unreadable, including the fresh begin acknowledgement.
	kind := "set_" + value.Kind
	if _, ok := ipTVSetReceiptKind(kind); !ok {
		return out, ErrInvalid
	}
	if err = execIPEntityEvidence(ctx, turn, ipTVSetRecordStmt, value.OperationID,
		identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		kind, value.Revision, value.AppliedMS, digest[:], resultPhaseForKind(value.Kind),
		f.afterFamily, f.afterOrdinal, f.afterKey, value.ResultCount(), value.ResultBytes(),
		f.terminal, f.evidenceID, f.evidenceSubjectID, f.parserVersion, f.buildID,
		value.SetID, f.sealAlgorithm, f.sealVersion, f.sealCommitment, f.sealRows, f.sealBytes,
		f.proofPins, value.EntityID, value.Provider, f.evidenceSubjectKind, f.evidenceProviderID); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return digest, nil
}

func resultPhaseForKind(kind string) string {
	if kind == "seal" {
		return "sealed"
	}
	return "building"
}

func (v ipTVSetResult) ResultCount() int64 {
	if v.Kind == "page" {
		return v.PageRows
	}
	return v.MemberCount
}

func (v ipTVSetResult) ResultBytes() int64 {
	if v.Kind == "page" {
		return v.PageBytes
	}
	if v.Kind == "seal" {
		return v.SealBytes
	}
	return 0
}

func ipTVSetBeginFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipTVSetBeginRequest,
	request RequestDigest, owner ipEntityJobOwner, appliedMS int64) (out ipTVSetResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	exists, err := wc008DraftInteger(ctx, turn, ipTVSetExistsStmt, q.setID)
	if err != nil {
		return out, err
	}
	if exists != 0 {
		return out, ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, ipTVSetSourceStmt, q.buildID, q.evidenceID)
	if err != nil {
		return out, err
	}
	var subjectID, buildGeneration, buildRevision, receiptSubject, receiptTerminal int64
	var parser, provider, subjectKind, providerID, buildState, buildPhase string
	var receiptKind, receiptEvidence, receiptParser string
	err = row.Scan(&subjectID, &parser, &provider, &subjectKind, &providerID, &buildState,
		&buildPhase, &buildRevision, &buildGeneration, &receiptKind, &receiptTerminal,
		&receiptEvidence, &receiptSubject, &receiptParser)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if provider != q.provider || subjectKind != q.subjectKind || providerID != q.providerID ||
		buildState != "ready" || buildPhase != "sealed" || buildRevision != q.buildRevision ||
		buildGeneration != q.buildGeneration || receiptKind != "evidence_seal" ||
		receiptTerminal != 1 || receiptEvidence != q.evidenceID ||
		receiptSubject != subjectID || receiptParser != parser || subjectID <= 0 ||
		parser == "" {
		return out, ErrConflict
	}
	if err = ipTVSetRevisions(ctx, turn, q.operationID, q.provider, q.sourceRevision,
		q.hierarchyRevision, q.selectionRevision, owner); err != nil {
		return out, err
	}
	if err = execIPEntityEvidence(ctx, turn, ipTVSetInsertStmt, q.setID, q.entityID, q.provider,
		identity.Incarnation[:], q.generation, q.subjectKind, q.providerID, q.episodeOrder,
		owner.consentRevision, owner.policyIncarnation[:], owner.policyRevision, q.sourceRevision,
		q.hierarchyRevision, q.selectionRevision, q.operationID, identity.Incarnation[:]); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	value := ipTVSetResult{Kind: "begin", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EvidenceID: q.evidenceID, BuildID: q.buildID,
		EntityID: q.entityID, SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision, MemberCount: q.memberCount,
		MemberDigest: q.memberDigest, Revision: 1, OwnerGeneration: q.generation,
		BuildGeneration: q.buildGeneration, LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS}
	if _, err = ipTVSetRecordReceipt(ctx, turn, identity, request, value,
		ipTVSetReceiptFields{evidenceID: q.evidenceID, evidenceSubjectID: subjectID,
			evidenceSubjectKind: q.subjectKind, evidenceProviderID: q.providerID,
			parserVersion: parser, buildID: q.buildID, afterFamily: 0, afterOrdinal: -1,
			afterKey: hex.EncodeToString(q.memberDigest[:]), terminal: 0}); err != nil {
		return out, err
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

// ipTVSetPageFresh appends one raw page under the exact live owner and set
// revision. It never marks the set complete or clears an undeclared child.
func ipTVSetPageFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipTVSetPageRequest,
	request RequestDigest, owner ipEntityJobOwner, appliedMS int64) (out ipTVSetResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	header, err := ipTVSetHeaderRead(ctx, turn, q.setID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if header.provider != q.provider || header.subjectKind != q.subjectKind ||
		header.providerID != q.providerID || header.episodeOrder != q.episodeOrder ||
		header.incarnation != identity.Incarnation || header.generation != q.generation ||
		header.entityID != q.entityID || header.revision != q.setRevision ||
		header.complete != 0 || header.proofOwner != "" ||
		header.sourceRevision != q.sourceRevision || header.hierarchyRevision != q.hierarchyRevision ||
		header.selectionRevision != q.selectionRevision {
		return out, ErrConflict
	}
	if err = ipTVSetRevisions(ctx, turn, q.operationID, q.provider, q.sourceRevision,
		q.hierarchyRevision, q.selectionRevision, owner); err != nil {
		return out, err
	}
	last, err := ipTVSetLastPageRead(ctx, turn, q.setID)
	if err != nil {
		return out, err
	}
	if !last.found {
		if q.page != 0 {
			return out, ErrConflict
		}
	} else if last.page != q.page-1 || !last.nextPresent || last.nextPage != q.page {
		return out, ErrConflict
	}
	count, err := wc008DraftInteger(ctx, turn, ipTVSetMemberCountStmt, q.setID)
	if err != nil {
		return out, err
	}
	if count != q.memberStart {
		return out, ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, ipTVSetBeginDeclStmt, q.setID)
	if err != nil {
		return out, err
	}
	var declaredCount int64
	var declaredDigest, declaredEvidence, declaredBuild string
	if err = row.Scan(&declaredCount, &declaredDigest, &declaredEvidence, &declaredBuild); err != nil {
		return out, ErrConflict
	}
	if declaredCount <= 0 || q.memberStart+int64(len(q.members)) > declaredCount {
		return out, ErrConflict
	}
	bytes, digest, err := ipTVSetPageShape(q)
	if err != nil {
		return out, err
	}
	for _, member := range q.members {
		sealed, err := wc008DraftInteger(ctx, turn, ipTVSetMemberStmt, member, q.provider)
		if err != nil {
			return out, err
		}
		if sealed != 1 {
			return out, ErrConflict
		}
	}
	revision := q.setRevision + 1
	var next any
	if q.nextPage != nil {
		next = *q.nextPage
	}
	if err = execIPEntityEvidence(ctx, turn, ipTVSetInsertPageStmt, q.setID, q.page, next,
		int64(len(q.members)), bytes, hex.EncodeToString(digest[:]), revision, revision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	for i, member := range q.members {
		if err = execIPEntityEvidence(ctx, turn, ipTVSetInsertMemberStmt, q.provider, "episode",
			q.setID, q.page, q.memberStart+int64(i), member); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
	}
	if err = wc008DraftChanged(ctx, turn, ipTVSetAdvanceSetStmt, q.setID, q.provider,
		identity.Incarnation[:], q.generation, q.setRevision); err != nil {
		return out, err
	}
	value := ipTVSetResult{Kind: "page", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EntityID: q.entityID,
		SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision,
		MemberCount:       q.memberStart + int64(len(q.members)), Page: q.page,
		NextPage: nextPageValue(q.nextPage), NextPagePresent: q.nextPage != nil,
		PageRows: int64(len(q.members)), PageBytes: bytes, PageDigest: digest, Revision: revision,
		OwnerGeneration: q.generation, LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS}
	if _, err := ipTVSetRecordReceipt(ctx, turn, identity, request, value,
		ipTVSetReceiptFields{afterFamily: q.page, afterOrdinal: q.memberStart + int64(len(q.members)) - 1,
			afterKey: hex.EncodeToString(digest[:]), terminal: 0}); err != nil {
		return out, err
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

func ipTVSetSealFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipTVSetSealRequest,
	manifest ipTVSetManifest, request RequestDigest, owner ipEntityJobOwner, appliedMS int64) (out ipTVSetResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	header, err := ipTVSetHeaderRead(ctx, turn, q.setID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if header.provider != q.provider || header.subjectKind != q.subjectKind ||
		header.providerID != q.providerID || header.episodeOrder != q.episodeOrder ||
		header.incarnation != identity.Incarnation || header.generation != q.generation ||
		header.entityID != q.entityID || header.revision != q.setRevision ||
		header.complete != 0 || header.proofOwner != "" ||
		header.sourceRevision != q.sourceRevision || header.hierarchyRevision != q.hierarchyRevision ||
		header.selectionRevision != q.selectionRevision {
		return out, ErrConflict
	}
	if err = ipTVSetRevisions(ctx, turn, q.operationID, q.provider, q.sourceRevision,
		q.hierarchyRevision, q.selectionRevision, owner); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, ipTVSetSourceStmt, q.buildID, q.evidenceID)
	if err != nil {
		return out, err
	}
	var subjectID, buildGeneration, buildRevision, receiptSubject, receiptTerminal int64
	var parser, provider, subjectKind, providerID, buildState, buildPhase string
	var receiptKind, receiptEvidence, receiptParser string
	err = row.Scan(&subjectID, &parser, &provider, &subjectKind, &providerID, &buildState,
		&buildPhase, &buildRevision, &buildGeneration, &receiptKind, &receiptTerminal,
		&receiptEvidence, &receiptSubject, &receiptParser)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if provider != q.provider || subjectKind != q.subjectKind || providerID != q.providerID ||
		buildState != "ready" || buildPhase != "sealed" || buildRevision != q.buildRevision ||
		buildGeneration != q.buildGeneration || receiptKind != "evidence_seal" ||
		receiptTerminal != 1 || receiptEvidence != q.evidenceID ||
		receiptSubject != subjectID || receiptParser != parser || subjectID <= 0 || parser == "" {
		return out, ErrConflict
	}
	beginCount, err := wc008DraftInteger(ctx, turn, ipTVSetBeginCountStmt, q.setID)
	if err != nil {
		return out, err
	}
	if beginCount != 1 {
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, ipTVSetBeginDeclStmt, q.setID)
	if err != nil {
		return out, err
	}
	var declaredCount int64
	var declaredDigest, declaredEvidence, declaredBuild string
	if err = row.Scan(&declaredCount, &declaredDigest, &declaredEvidence, &declaredBuild); err != nil {
		return out, ErrConflict
	}
	if declaredCount != manifest.memberCount ||
		declaredDigest != hex.EncodeToString(manifest.memberDigest[:]) ||
		declaredEvidence != q.evidenceID || declaredBuild != q.buildID {
		return out, ErrConflict
	}
	if err = ipTVSetSealPagesCheck(ctx, turn, q.setID, manifest); err != nil {
		return out, err
	}
	unsealed, err := wc008DraftInteger(ctx, turn, ipTVSetUnsealedStmt, q.setID)
	if err != nil {
		return out, err
	}
	if unsealed != 0 {
		return out, ErrConflict
	}
	reverse, err := wc008DraftInteger(ctx, turn, ipTVSetSealReverseStmt, q.setID)
	if err != nil {
		return out, err
	}
	if reverse != 0 {
		return out, ErrConflict
	}
	revision := q.setRevision + 1
	value := ipTVSetResult{Kind: "seal", OperationID: q.operationID, SetID: q.setID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		EpisodeOrder: q.episodeOrder, Lease: q.lease, EvidenceID: q.evidenceID, BuildID: q.buildID,
		EntityID: q.entityID, SourceRevision: q.sourceRevision, HierarchyRevision: q.hierarchyRevision,
		SelectionRevision: q.selectionRevision, MemberCount: manifest.memberCount,
		MemberDigest: manifest.memberDigest, Revision: revision, OwnerGeneration: q.generation,
		BuildGeneration: q.buildGeneration, LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS,
		SealVersion: ipTVSetVersion, SealRows: manifest.memberCount, SealBytes: manifest.bytes,
		Commitment: manifest.commitment}
	sealDigest := hex.EncodeToString(manifest.pagesDigest[:])
	if _, err = ipTVSetRecordReceipt(ctx, turn, identity, request, value,
		ipTVSetReceiptFields{evidenceID: q.evidenceID, evidenceSubjectID: subjectID,
			evidenceSubjectKind: q.subjectKind, evidenceProviderID: q.providerID,
			parserVersion: parser, buildID: q.buildID, afterFamily: int64(len(manifest.pages)),
			afterOrdinal: manifest.memberCount - 1, afterKey: sealDigest, terminal: 1, proofPins: 1,
			sealAlgorithm: ipTVSetAlgorithm, sealVersion: ipTVSetVersion,
			sealCommitment: manifest.commitment[:], sealRows: manifest.memberCount,
			sealBytes: manifest.bytes}); err != nil {
		return out, err
	}
	// The set proof pins the receipt just recorded; ordering the receipt first
	// keeps both canonical foreign keys satisfied without a deferred frame.
	if err = wc008DraftChanged(ctx, turn, ipTVSetSealSetStmt, q.sourceRevision, q.hierarchyRevision,
		q.selectionRevision, manifest.memberCount, q.operationID, identity.Incarnation[:],
		identity.Sequence, q.setID, q.provider, identity.Incarnation[:], q.generation, q.setRevision); err != nil {
		return out, err
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

// ipTVSetSealPagesCheck proves the persisted page headers and the complete
// ordered member rows are exactly the request manifest. The last persisted page
// must be the only no-next page, so a missing page or a set that still promises
// another page can never seal.
func ipTVSetSealPagesCheck(ctx context.Context, turn *commandTurn, setID string, manifest ipTVSetManifest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := turn.query(ctx, turn.steps[ipTVSetPagesStmt].statementStep, setID)
	if err != nil {
		return err
	}
	page := 0
	for rows.Next() {
		if page >= len(manifest.pages) {
			return ErrConflict
		}
		var number, rowCount, byteCount int64
		var next sql.NullInt64
		var digest string
		if err = rows.Scan(&number, &next, &rowCount, &byteCount, &digest); err != nil {
			return err
		}
		want := manifest.pages[page]
		if number != want.page || rowCount != want.rows || byteCount != want.bytes ||
			digest != hex.EncodeToString(want.digest[:]) || next.Valid != want.nextPresent ||
			(next.Valid && next.Int64 != want.nextPage) {
			return ErrConflict
		}
		page++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if page != len(manifest.pages) {
		return ErrConflict
	}
	members, err := turn.query(ctx, turn.steps[ipTVSetMembersStmt].statementStep, setID)
	if err != nil {
		return err
	}
	position := 0
	page = 0
	pageRow := 0
	ids := make([]string, 0, int(manifest.memberCount))
	for members.Next() {
		if page >= len(manifest.pages) {
			return ErrConflict
		}
		var number, ordinal int64
		var evidence string
		if err = members.Scan(&number, &ordinal, &evidence); err != nil {
			return err
		}
		if number != int64(page) || ordinal != int64(position) ||
			pageRow >= len(manifest.pages[page].members) {
			return ErrConflict
		}
		if evidence != manifest.pages[page].members[pageRow] {
			return ErrConflict
		}
		ids = append(ids, evidence)
		position++
		pageRow++
		if pageRow == len(manifest.pages[page].members) {
			page++
			pageRow = 0
		}
	}
	if err = members.Err(); err != nil {
		return err
	}
	if position != int(manifest.memberCount) || page != len(manifest.pages) {
		return ErrConflict
	}
	digest, err := TVSetMemberDigest(ids)
	if err != nil || digest != manifest.memberDigest {
		return ErrConflict
	}
	return nil
}

func applyIPTVSetBegin(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipTVSetCommand) (out ipTVSetCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipTVSetCommandResult
	if c == nil || !c.valid || c.kind != "begin" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPTVSetBegin {
		return zero, ErrInvalid
	}
	q := c.begin
	if err = ipTVSetBeginShape(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPTVSetBegin, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipTVSetReceipt) (ipTVSetResult, error) {
		return ipTVSetBeginSaved(q, c.identity, r)
	}
	lookup, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetBegin, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipEntityJobOwnerFresh(ctx, turn, q.operationID, lookup.owner, q.provider, q.selectedID,
		q.lease, q.generation, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipTVSetBeginFresh(ctx, turn, c.identity, q, actual, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetBegin, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func applyIPTVSetPage(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipTVSetCommand) (out ipTVSetCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipTVSetCommandResult
	if c == nil || !c.valid || c.kind != "page" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPTVSetPage {
		return zero, ErrInvalid
	}
	q := c.page
	bytes, pageDigest, err := ipTVSetPageShape(q)
	if err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPTVSetPage, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipTVSetReceipt) (ipTVSetResult, error) {
		return ipTVSetPageSaved(q, c.identity, r, bytes, pageDigest)
	}
	lookup, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetPage, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipEntityJobOwnerFresh(ctx, turn, q.operationID, lookup.owner, q.provider, q.selectedID,
		q.lease, q.generation, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipTVSetPageFresh(ctx, turn, c.identity, q, actual, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetPage, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func applyIPTVSetSeal(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipTVSetCommand) (out ipTVSetCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipTVSetCommandResult
	if c == nil || !c.valid || c.kind != "seal" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPTVSetSeal {
		return zero, ErrInvalid
	}
	q := c.seal
	manifest, err := ipTVSetSealShape(q)
	if err != nil || !ipTVSetManifestEqual(manifest, c.manifest) {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPTVSetSeal, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipTVSetReceipt) (ipTVSetResult, error) {
		return ipTVSetSealSaved(q, manifest, c.identity, r)
	}
	lookup, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetSeal, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipEntityJobOwnerFresh(ctx, turn, q.operationID, lookup.owner, q.provider, q.selectedID,
		q.lease, q.generation, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipTVSetSealFresh(ctx, turn, c.identity, q, manifest, actual, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipTVSetLookupReceipt(ctx, turn, IPTVSetSeal, c.reference(), q.operationID,
		q.provider, q.entityID, decode)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
