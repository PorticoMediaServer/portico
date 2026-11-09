package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP nonmovie entity evidence seal 212 closes ONE complete IP-06 begin/append
// build. It is the only command that marks an IP evidence build ready or an
// evidence body sealed. The exact owner/lease gate, closed family set and the
// cursor/digest/charge chain stay IP-06's; this command replays that chain from
// the complete manifest, proves the exact child counts and writes the one typed
// saved seal proof that later acceptance reads. It never accepts a source,
// publishes anything or writes an EN FIRST seal tuple.
// The seal manifest version is part of the saved proof identity. It advances
// with the corrected family order and pre-staged recording headers, so an
// old-order seal is never replayed or reinterpreted as a current manifest.
const (
	ipSealAlgorithm = "portico.ip-evidence-manifest.sha256.v2"
	ipSealVersion   = int64(2)
)

// ipEntitySealPage is one exact append page as the parser staged it. The page
// family is the closed IP-06 family set; a page carries rows of exactly one
// family and retains the raw after-ordinal chain position.
type ipEntitySealPage struct {
	Family       string
	AfterOrdinal int64
	rows         int64
	aliases      []ipEvidenceAlias
	external     []ipEvidenceExternal
	terms        []ipEvidenceTerm
	ratings      []ipEvidenceRating
	credits      []ipEvidenceCredit
	episodes     []ipEvidenceEpisode
	media        []ipEvidenceMedia
	tracks       []ipEvidenceTrack
}

// counts follows ipEvidenceFamilySpecs exactly: alias, external, term, rating,
// episode, media, track, credit.
func (p ipEntitySealPage) counts() [8]int {
	return [8]int{len(p.aliases), len(p.external), len(p.terms), len(p.ratings),
		len(p.episodes), len(p.media), len(p.tracks), len(p.credits)}
}

// page returns the one selected family page. Any row in a different family is
// refused; the seal never merges families into one page.
func (p ipEntitySealPage) page(family int) (any, int) {
	counts := p.counts()
	for i, count := range counts {
		if count > 0 && i != family {
			return nil, -1
		}
	}
	if family < 0 || family >= len(counts) {
		return nil, -1
	}
	switch family {
	case 0:
		return p.aliases, len(p.aliases)
	case 1:
		return p.external, len(p.external)
	case 2:
		return p.terms, len(p.terms)
	case 3:
		return p.ratings, len(p.ratings)
	case 4:
		return p.episodes, len(p.episodes)
	case 5:
		return p.media, len(p.media)
	case 6:
		return p.tracks, len(p.tracks)
	default:
		return p.credits, len(p.credits)
	}
}

// ipEntitySealRequest carries the complete sealed manifest. The owner tuple is
// IP-05's exact claimed operation; the evidence tuple is the immutable
// observation the build staged; the family declarations and pages are the
// complete normalized content in exact append order.
type ipEntitySealRequest struct {
	operationID, provider, purpose, selectedID, lease string
	entityID, generation, leaseUntilMS                int64
	evidenceID, buildID                               string
	buildGeneration, contentRevision                  int64
	subjectID                                         int64
	subjectKind, providerID, parserVersion            string
	language, region, requestShape, observedAt        string
	observationDigest                                 [32]byte
	families                                          []ipEvidenceFamilyDeclaration
	pages                                             []ipEntitySealPage
}

// ipEntitySealManifest is the derived, request-only manifest. Every field is
// recomputed from the request, so typed replay never reads mutable build or
// evidence state.
type ipEntitySealManifest struct {
	shape      [32]byte
	chain      [32]byte
	commitment [32]byte
	rows       int64
	bytes      int64
}

type ipEntitySealCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	manifest ipEntitySealManifest
	valid    bool
	request  ipEntitySealRequest
}

type ipEntitySealCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEntitySealResult
	Digest  ResultDigest
}

type ipEntitySealBuild struct {
	evidenceID, owner, state, phase, expiresAt         string
	provider, subjectKind, providerID, parser          string
	language, region, requestShape, observedAt, digest string
	generation, ownerGeneration, cursor, expectedRows  int64
	materializedRows, charge, revision, subjectID      int64
	sealed, proofOwnerPresent, ipSealOwnerPresent      int64
	ownerIncarnation                                   [16]byte
	expectedShapeDigest, materializedDigest            [32]byte
}

type ipEntitySealReceipt struct {
	ipEntityJobReceipt
	evidenceID, parser, buildID string
	algorithm                   string
	subjectID                   int64
	version, rows, bytes        int64
	commitment                  [32]byte
}

type ipEntitySealLookup struct {
	owner    ipEntityJobOwner
	receipt  ipEntitySealReceipt
	decision receiptDecision
	value    ipEntitySealResult
	digest   ResultDigest
}

func (c *ipEntitySealCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEntitySealCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntitySealCommand")
}

func ipEntitySealShape(q ipEntitySealRequest) (ipEntitySealManifest, error) {
	var zero ipEntitySealManifest
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return zero, ErrInvalid
	}
	if !ipEvidenceText(q.evidenceID, ipEvidenceIDMax) || len(q.evidenceID) == 0 ||
		!ipEvidenceText(q.buildID, ipEvidenceIDMax) || len(q.buildID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.contentRevision <= 0 || q.contentRevision == math.MaxInt64 ||
		q.subjectID <= 0 || !ipEvidenceSubjectKind(q.subjectKind) ||
		!ipEvidenceText(q.providerID, ipEvidenceIDMax) || len(q.providerID) == 0 ||
		!ipEvidenceText(q.parserVersion, ipEvidenceTextMax) || len(q.parserVersion) == 0 ||
		!ipEvidenceText(q.language, 64) || !ipEvidenceText(q.region, 64) ||
		!ipEntityEvidenceLocaleValid(q.provider, q.subjectKind, q.parserVersion,
			q.requestShape, q.language, q.region) ||
		!ipEvidenceText(q.requestShape, ipEvidenceTextMax) || len(q.requestShape) == 0 ||
		!ipEvidenceText(q.observedAt, 64) || len(q.observedAt) == 0 ||
		!ipSealDigestSet(q.observationDigest) {
		return zero, ErrInvalid
	}
	if !ipEntityEvidencePairCurrent(q.provider, q.subjectKind, q.parserVersion, q.requestShape) {
		return zero, ErrInvalid
	}
	if _, ok := ipEntityJobTextTime(q.observedAt); !ok {
		return zero, ErrInvalid
	}
	if len(q.families) != len(ipEvidenceFamilySpecs) {
		return zero, ErrInvalid
	}
	for i, spec := range ipEvidenceFamilySpecs {
		family := q.families[i]
		if family.Name != spec.name || family.Rows < 0 || family.Rows > int64(spec.maxRows) {
			return zero, ErrInvalid
		}
	}
	rows, ok := ipEntitySealExpectedRows(q.families)
	if !ok {
		return zero, ErrInvalid
	}
	shape, err := ipEntityEvidenceShapeDigest(q.families)
	if err != nil {
		return zero, ErrInvalid
	}
	if len(q.pages) == 0 && rows != 1 {
		return zero, ErrInvalid
	}
	var running [8]int64
	var familyPages [8][]any
	// The manifest pages are in the closed materialization order: a family may
	// not reappear after a later family started, so media precedes track and
	// track precedes credit exactly as the immediate child FKs require.
	lastFamily := -1
	for _, page := range q.pages {
		family := ipEvidenceFamilyIndex(page.Family)
		if family < 0 || page.AfterOrdinal < 0 || family < lastFamily {
			return zero, ErrInvalid
		}
		lastFamily = family
		payload, count := page.page(family)
		if payload == nil || count <= 0 || count > ipEvidenceFamilySpecs[family].maxPage ||
			page.rows != int64(count) || page.AfterOrdinal != running[family] {
			return zero, ErrInvalid
		}
		chargeReq := ipEntityEvidenceAppendRequest{afterOrdinal: page.AfterOrdinal,
			provider: q.provider, subjectKind: q.subjectKind}
		switch family {
		case 0:
			chargeReq.aliases = page.aliases
		case 1:
			chargeReq.external = page.external
		case 2:
			chargeReq.terms = page.terms
		case 3:
			chargeReq.ratings = page.ratings
		case 4:
			chargeReq.episodes = page.episodes
		case 5:
			chargeReq.media = page.media
		case 6:
			chargeReq.tracks = page.tracks
		default:
			chargeReq.credits = page.credits
		}
		if _, _, err := ipEntityEvidencePageCharge(family, chargeReq, int64(count)); err != nil {
			return zero, ErrInvalid
		}
		running[family] += int64(count)
		if running[family] > q.families[family].Rows {
			return zero, ErrInvalid
		}
		familyPages[family] = append(familyPages[family], payload)
	}
	for family := range ipEvidenceFamilySpecs {
		if running[family] != q.families[family].Rows {
			return zero, ErrInvalid
		}
		digest, err := ipEntitySealFamilyDigest(family, familyPages[family])
		if err != nil || digest != q.families[family].Digest {
			return zero, ErrInvalid
		}
	}
	chain, err := ipEntitySealChainDigest(q, shape, rows)
	if err != nil {
		return zero, ErrInvalid
	}
	commitment, bytes, err := ipEntitySealManifestCommitment(q, shape, chain, rows)
	if err != nil {
		return zero, ErrInvalid
	}
	return ipEntitySealManifest{shape: shape, chain: chain, commitment: commitment, rows: rows, bytes: bytes}, nil
}

func ipEntitySealExpectedRows(families []ipEvidenceFamilyDeclaration) (int64, bool) {
	total := int64(1)
	for _, family := range families {
		if family.Rows < 0 || total > math.MaxInt64-family.Rows {
			return 0, false
		}
		total += family.Rows
	}
	return total, true
}

func ipSealDigestSet(value [32]byte) bool {
	var zero [32]byte
	return value != zero
}

// ipEntitySealFamilyDigest defines one declared family digest over the exact
// ordered page payloads. The begin declarations and this seal derivation share
// the closed family name, so a page move, insertion or deletion changes it.
func ipEntitySealFamilyDigest(family int, pages []any) ([32]byte, error) {
	if family < 0 || family >= len(ipEvidenceFamilySpecs) {
		return [32]byte{}, ErrInvalid
	}
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_evidence_family.v2\x00"))
	e.text(ipEvidenceFamilySpecs[family].name)
	e.number(uint64(len(pages)))
	for _, page := range pages {
		e.value(reflect.ValueOf(page), 0)
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// ipEntitySealChainDigest replays IP-06's exact page chain from the begin build
// digest. Only the recorded materialized head can satisfy it.
func ipEntitySealChainDigest(q ipEntitySealRequest, shape [32]byte, rows int64) ([32]byte, error) {
	digest := ipEntityEvidenceBuildDigest(q.evidenceID, q.buildID, q.buildGeneration, rows, shape)
	for _, page := range q.pages {
		family := ipEvidenceFamilyIndex(page.Family)
		if family < 0 {
			return [32]byte{}, ErrInvalid
		}
		payload, _ := page.page(family)
		if payload == nil {
			return [32]byte{}, ErrInvalid
		}
		next, err := ipEntityEvidenceChainDigest(digest, family, page.AfterOrdinal, payload)
		if err != nil {
			return [32]byte{}, err
		}
		digest = next
	}
	return digest, nil
}

func writeIPEntitySealManifest(e *requestEncoder, q ipEntitySealRequest, shape, chain [32]byte, rows int64) {
	e.bytes([]byte("portico.ip_evidence_seal.v2\x00"))
	e.text(q.evidenceID)
	e.text(q.buildID)
	e.number(uint64(q.buildGeneration))
	e.number(uint64(rows))
	e.text(q.provider)
	e.text(q.subjectKind)
	e.text(q.providerID)
	e.text(q.parserVersion)
	e.text(q.language)
	e.text(q.region)
	e.text(q.requestShape)
	e.text(q.observedAt)
	e.number(uint64(q.subjectID))
	e.bytes(q.observationDigest[:])
	e.bytes(shape[:])
	e.bytes(chain[:])
}

// ipEntitySealManifestCommitment is the complete manifest identity and its
// canonical byte measure. It is inert data, never a request or result digest.
func ipEntitySealManifestCommitment(q ipEntitySealRequest, shape, chain [32]byte, rows int64) ([32]byte, int64, error) {
	measure := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	writeIPEntitySealManifest(&measure, q, shape, chain, rows)
	if measure.err != nil || measure.written == 0 || measure.written > math.MaxInt64 {
		return [32]byte{}, 0, ErrInvalid
	}
	h := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	writeIPEntitySealManifest(&h, q, shape, chain, rows)
	if h.err != nil {
		return [32]byte{}, 0, h.err
	}
	var out [32]byte
	copy(out[:], h.h.Sum(nil))
	return out, int64(measure.written), nil
}

func newIPEntitySealCommand(identity CommandIdentity, in ipEntitySealRequest) (*ipEntitySealCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	manifest, err := ipEntitySealShape(in)
	if err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPEntitySeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntitySealCommand{identity: identity, request: in, manifest: manifest, digest: digest, valid: true}, nil
}

// ipEntitySealLookupReceipt resolves the ORIGINAL receipt before any fresh
// build/owner/lease probe. Current authority is checked by the caller first.
func ipEntitySealLookupReceipt(ctx context.Context, turn *commandTurn, ref ReceiptReference,
	q ipEntitySealRequest, manifest ipEntitySealManifest) (out ipEntitySealLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntitySeal {
		return out, ErrInvalid
	}
	ownerID := q.operationID
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobOwnerStmt, ownerID, q.provider)
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
		out.owner.entityID != q.entityID || out.owner.generation <= 0 || out.owner.sourceRevision <= 0 ||
		out.owner.identityRevision <= 0 || out.owner.metadataRevision <= 0 || out.owner.policyRevision <= 0 ||
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 || len(out.owner.selectedID) > ipEvidenceIDMax ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipEntitySealSavedStmt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest, commitment []byte
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest, &out.receipt.appliedMS,
		&resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision, &out.receipt.phase, &out.receipt.terminal,
		&out.receipt.evidenceID, &out.receipt.subjectID, &out.receipt.parser, &out.receipt.buildID,
		&out.receipt.algorithm, &out.receipt.version, &commitment, &out.receipt.rows, &out.receipt.bytes)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || len(commitment) != 32 ||
			out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.acceptedRevision <= 0 || out.receipt.kind != "evidence_seal" ||
			out.receipt.phase != "sealed" || !out.receipt.terminal {
			return out, ErrResolveRequired
		}
		copy(out.receipt.operationID[:], operation)
		copy(out.receipt.requestDigest[:], requestDigest)
		copy(out.receipt.resultDigest[:], resultDigest)
		copy(out.receipt.commitment[:], commitment)
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], receiptIncarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPEntitySeal, resultCount: 0}
		copy(found.resultDigest[:], resultDigest)
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPEntitySeal,
		receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
			lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.acknowledgedSequence}, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		value, e := ipEntitySealSaved(q, manifest, ref.Identity, out.receipt)
		if e != nil {
			return out, errors.Join(ErrResolveRequired, e)
		}
		computed, e := canonicalResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != out.receipt.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

// ipEntitySealSaved rebuilds the COMPLETE saved result from the exact request
// manifest and the saved receipt witnesses. It never reads current evidence,
// build, lease or provider state.
func ipEntitySealSaved(q ipEntitySealRequest, m ipEntitySealManifest, identity CommandIdentity, r ipEntitySealReceipt) (ipEntitySealResult, error) {
	var zero ipEntitySealResult
	if r.kind != "evidence_seal" || r.phase != "sealed" || !r.terminal ||
		r.operationID != identity.OperationID || r.acceptedRevision != q.contentRevision+1 ||
		r.evidenceID != q.evidenceID || r.subjectID != q.subjectID || r.parser != q.parserVersion ||
		r.buildID != q.buildID || r.algorithm != ipSealAlgorithm || r.version != ipSealVersion ||
		r.rows != m.rows || r.bytes != m.bytes || r.commitment != m.commitment {
		return zero, ErrResolveRequired
	}
	return ipEntitySealResult{OperationID: q.operationID, EvidenceID: q.evidenceID, BuildID: q.buildID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		ParserVersion: q.parserVersion, Language: q.language, Region: q.region,
		RequestShape: q.requestShape, ObservedAt: q.observedAt, ObservationDigest: q.observationDigest,
		Lease: q.lease, SubjectID: q.subjectID, BuildGeneration: q.buildGeneration,
		OwnerGeneration: q.generation, ContentRevision: q.contentRevision + 1,
		SealVersion: ipSealVersion, SealRows: m.rows, SealBytes: m.bytes, Commitment: m.commitment,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS}, nil
}

func ipEntitySealBuildRead(ctx context.Context, turn *commandTurn, buildID string) (out ipEntitySealBuild, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntitySealBuildStmt, buildID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var shape, materialized string
	var proofOwner, ipOwner sql.NullString
	err = row.Scan(&out.evidenceID, &out.generation, &out.owner, &incarnation, &out.ownerGeneration,
		&out.state, &out.phase, &out.cursor, &out.expectedRows, &out.materializedRows, &out.charge,
		&shape, &materialized, &out.expiresAt, &out.revision, &out.subjectID, &out.provider,
		&out.subjectKind, &out.providerID, &out.parser, &out.language, &out.region, &out.requestShape,
		&out.observedAt, &out.digest, &out.sealed, &proofOwner, &ipOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sql.ErrNoRows
	}
	if err != nil {
		return out, err
	}
	shapeDigest, okShape := ipEvidenceDigestBytes(shape)
	materializedDigest, okMaterialized := ipEvidenceDigestBytes(materialized)
	if len(incarnation) != 16 || !okShape || !okMaterialized || out.generation <= 0 ||
		out.ownerGeneration <= 0 || out.cursor < 0 || out.expectedRows <= 0 ||
		out.materializedRows < 0 || out.materializedRows > out.cursor ||
		out.charge < ipEvidenceHeaderCost || out.revision <= 0 || out.subjectID <= 0 ||
		(out.sealed != 0 && out.sealed != 1) || (out.state != "building" && out.state != "ready" &&
		out.state != "aborting" && out.state != "retiring" && out.state != "done") ||
		len(out.phase) == 0 || !ipPolicyProviderCode(out.provider) || !ipEvidenceSubjectKind(out.subjectKind) ||
		len(out.evidenceID) == 0 || len(out.owner) == 0 || len(out.providerID) == 0 ||
		len(out.parser) == 0 ||
		!ipEntityEvidenceLocaleValid(out.provider, out.subjectKind, out.parser,
			out.requestShape, out.language, out.region) ||
		len(out.requestShape) == 0 || len(out.observedAt) == 0 || len(out.digest) != 64 {
		return out, ErrResolveRequired
	}
	if proofOwner.Valid && proofOwner.String != "" {
		out.proofOwnerPresent = 1
	}
	if ipOwner.Valid && ipOwner.String != "" {
		out.ipSealOwnerPresent = 1
	}
	copy(out.ownerIncarnation[:], incarnation)
	out.expectedShapeDigest, out.materializedDigest = shapeDigest, materializedDigest
	return out, nil
}

// ipEntitySealCounts proves the exact persisted child counts for every closed
// family. A missing, extra or duplicated child page cannot match the declared
// count.
func ipEntitySealCounts(ctx context.Context, turn *commandTurn, evidenceID string) (out [8]int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	// The order is ipEvidenceFamilySpecs exactly: alias, external, term, rating,
	// episode, media, track, credit.
	ids := [...]stmt.ID{ipEntitySealAliasCountStmt, ipEntitySealExternalCountStmt,
		ipEntitySealTermCountStmt, ipEntitySealRatingCountStmt, ipEntitySealEpisodeCountStmt,
		ipEntitySealMediaCountStmt, ipEntitySealTrackCountStmt, ipEntitySealCreditCountStmt}
	for i, id := range ids {
		count, err := wc008DraftInteger(ctx, turn, id, evidenceID)
		if err != nil {
			return out, err
		}
		if count < 0 {
			return out, ErrConflict
		}
		out[i] = count
	}
	return out, nil
}

func ipEntitySealFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipEntitySealRequest,
	manifest ipEntitySealManifest, request RequestDigest, owner ipEntityJobOwner, appliedMS int64) (out ipEntitySealResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	build, err := ipEntitySealBuildRead(ctx, turn, q.buildID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	expires, ok := ipEntityJobTextTime(build.expiresAt)
	if !ok || expires <= appliedMS {
		return out, ErrConflict
	}
	if build.evidenceID != q.evidenceID || build.owner != q.operationID ||
		build.ownerIncarnation != identity.Incarnation || build.ownerGeneration != q.generation ||
		build.generation != q.buildGeneration || build.revision != q.contentRevision ||
		build.subjectID != q.subjectID || build.provider != q.provider ||
		build.subjectKind != q.subjectKind || build.providerID != q.providerID ||
		build.parser != q.parserVersion || build.language != q.language || build.region != q.region ||
		build.requestShape != q.requestShape || build.observedAt != q.observedAt ||
		build.digest != hex.EncodeToString(q.observationDigest[:]) ||
		build.state != "building" || build.phase != "children" || build.sealed != 0 ||
		build.proofOwnerPresent != 0 || build.ipSealOwnerPresent != 0 ||
		build.cursor != manifest.rows || build.expectedRows != manifest.rows ||
		build.materializedRows != manifest.rows ||
		build.expectedShapeDigest != manifest.shape || build.materializedDigest != manifest.chain {
		return out, ErrConflict
	}
	counts, err := ipEntitySealCounts(ctx, turn, q.evidenceID)
	if err != nil {
		return out, err
	}
	for family := range ipEvidenceFamilySpecs {
		if counts[family] != q.families[family].Rows {
			return out, ErrConflict
		}
	}
	reverse, err := wc008DraftInteger(ctx, turn, ipEntitySealReceiptReverseStmt, q.evidenceID)
	if err != nil {
		return out, ErrConflict
	}
	if reverse != 0 {
		return out, ErrConflict
	}
	delta := 8 * (int64(len(q.operationID)) + 16)
	if build.charge > math.MaxInt64-delta {
		return out, ErrCap
	}
	revision := q.contentRevision + 1
	if revision <= 0 {
		return out, ErrInvalid
	}
	row, err := turn.queryRow(ctx, turn.steps[ipEntitySealFinalizeStmt].statementStep,
		hex.EncodeToString(manifest.commitment[:]), delta, q.buildID, q.evidenceID, q.buildGeneration,
		q.contentRevision, manifest.rows, manifest.rows, manifest.rows,
		hex.EncodeToString(manifest.chain[:]), build.charge, q.operationID,
		identity.Incarnation[:], q.generation)
	if err != nil {
		return out, ErrConflict
	}
	var finalRevision, finalCursor, finalCharge int64
	if err = row.Scan(&finalRevision, &finalCursor, &finalCharge); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if finalRevision != revision || finalCursor != manifest.rows || finalCharge != build.charge+delta {
		return out, ErrConflict
	}
	value := ipEntitySealResult{OperationID: q.operationID, EvidenceID: q.evidenceID, BuildID: q.buildID,
		Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		ParserVersion: q.parserVersion, Language: q.language, Region: q.region,
		RequestShape: q.requestShape, ObservedAt: q.observedAt, ObservationDigest: q.observationDigest,
		Lease: q.lease, SubjectID: q.subjectID, BuildGeneration: q.buildGeneration,
		OwnerGeneration: q.generation, ContentRevision: revision, SealVersion: ipSealVersion,
		SealRows: manifest.rows, SealBytes: manifest.bytes, Commitment: manifest.commitment,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS}
	digest, err := ipEntitySealResultDigest(identity, request, value)
	if err != nil {
		return out, err
	}
	if err := execIPEntityEvidence(ctx, turn, ipEntitySealRecordStmt, q.operationID, identity.Incarnation[:],
		identity.Sequence, identity.OperationID[:], request[:],
		revision, appliedMS, digest[:], manifest.bytes, q.evidenceID, q.subjectID, q.parserVersion,
		q.buildID, ipSealAlgorithm, ipSealVersion, manifest.commitment[:], manifest.rows,
		manifest.bytes); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err := execIPEntityEvidence(ctx, turn, ipEntitySealAttachStmt, q.operationID, identity.Incarnation[:],
		identity.Sequence, q.evidenceID, q.subjectID, q.parserVersion, q.provider, q.subjectKind,
		q.providerID, q.language, q.region, q.requestShape, hex.EncodeToString(q.observationDigest[:]),
		q.observedAt); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err := ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved seal with zero new effects, before any fresh lease probe.
func applyIPEntitySeal(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntitySealCommand) (out ipEntitySealCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntitySealCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPEntitySeal {
		return zero, ErrInvalid
	}
	q := c.request
	manifest, err := ipEntitySealShape(q)
	if err != nil || manifest != c.manifest {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPEntitySeal, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEntitySealLookupReceipt(ctx, turn, c.reference(), q, manifest)
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
	value, err := ipEntitySealFresh(ctx, turn, c.identity, q, manifest, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipEntitySealLookupReceipt(ctx, turn, c.reference(), q, manifest)
	if err != nil || saved.decision != receiptReplay || saved.value != value || saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
