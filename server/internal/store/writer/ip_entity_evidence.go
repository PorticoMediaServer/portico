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
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP nonmovie entity evidence begin/append 210/211 stages one bounded,
// normalized, immutable provider observation under an already admitted IP-05
// acquisition owner. Begin materializes the common header row and an open
// metadata_evidence_builds cursor. Append materializes one bounded page of one
// declared child family and advances the build's raw cursor/digest/charge
// chain. Neither command marks the build ready, seals the evidence or accepts a
// source; IP-07 owns the manifest seal.
const (
	ipEvidenceTextMax    = 4096
	ipEvidenceURLMax     = 2048
	ipEvidenceIDMax      = 128
	ipEvidencePageMax    = 6 << 20
	ipEvidenceHeaderCost = int64(512)
)

type ipEvidenceFamilySpec struct {
	name    string
	maxPage int
	maxRows int
	insert  stmt.ID
}

// The declared family set is closed and ordered. The order is part of the
// begin manifest digest and the family index carried by append, so a later seal
// can account for exactly these normalized child families and no aliases of
// EN's FIRST movie purpose.
var ipEvidenceFamilySpecs = [...]ipEvidenceFamilySpec{
	{"alias", 64, 1024, ipEntityEvidenceAliasStmt},
	{"external", 64, 2048, ipEntityEvidenceExternalStmt},
	{"term", 64, 4096, ipEntityEvidenceTermStmt},
	{"rating", 16, 256, ipEntityEvidenceRatingStmt},
	{"credit", 64, 1024, ipEntityEvidenceCreditStmt},
	{"episode", 32, 1024, ipEntityEvidenceEpisodeStmt},
	{"media", 16, 256, ipEntityEvidenceMediaStmt},
	{"track", 32, 1024, ipEntityEvidenceTrackStmt},
}

func ipEvidenceFamilyIndex(name string) int {
	for i := range ipEvidenceFamilySpecs {
		if ipEvidenceFamilySpecs[i].name == name {
			return i
		}
	}
	return -1
}

// Nonmovie entity evidence only. Movie FIRST evidence and person provider
// custody stay with their own merged commands.
func ipEvidenceSubjectKind(value string) bool {
	switch value {
	case "series", "season", "episode", "artist", "release", "recording":
		return true
	}
	return false
}

func ipEvidenceText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

type ipEvidenceFamilyDeclaration struct {
	Name   string
	Rows   int64
	Digest [32]byte
}

type ipEvidenceAlias struct {
	Ordinal                        int64
	RequestedID, CanonicalID, Name string
}

type ipEvidenceExternal struct {
	Provider, SubjectKind, ProviderID string
}

type ipEvidenceTerm struct {
	Ordinal           int64
	Vocabulary, Value string
}

type ipEvidenceRating struct {
	Role         string
	Value, Scale float64
	Votes        int64
	SourceURL    string
}

type ipEvidenceCredit struct {
	Ordinal                                          int64
	Class, Scope, TrackID, ProviderPersonID, Name    string
	ArtistName, SortName, Disambiguation, JoinPhrase string
	Role, Department, CreditID                       string
	SourceOrdinal                                    int64
	SourceOrdinalPresent                             bool
	Billing                                          int64
	BillingPresent                                   bool
}

type ipEvidenceEpisode struct {
	Ordinal                                     int64
	EpisodeEvidenceID, EpisodeOrder             string
	SeasonNumber, EpisodeNumber, AbsoluteNumber *int64
}

type ipEvidenceMedia struct {
	Position      int64
	Format, Title string
}

type ipEvidenceTrack struct {
	TrackID                  string
	MediumPosition, Position int64
	Number, Title, Artist    string
	LengthMillis             *int64
	RecordingEvidenceID      string
}

type ipEntityEvidenceBeginRequest struct {
	operationID, provider, purpose, selectedID, lease   string
	entityID, generation, leaseUntilMS, buildGeneration int64
	buildID, expiresAt                                  string
	header                                              EvidenceHeaderInput
	families                                            []ipEvidenceFamilyDeclaration
}

func (q ipEntityEvidenceBeginRequest) expectedRows() (int64, bool) {
	total := int64(1)
	for _, f := range q.families {
		if f.Rows < 0 || total > math.MaxInt64-f.Rows {
			return 0, false
		}
		total += f.Rows
	}
	return total, true
}

func ipEntityEvidenceHeaderCharge(header EvidenceHeader) int64 {
	return ipEvidenceHeaderCost + 8*(int64(header.TextBytes())+64)
}

type ipEntityEvidenceAppendRequest struct {
	operationID, provider, purpose, selectedID, lease string
	entityID, generation, leaseUntilMS                int64
	evidenceID                                        string
	subjectID                                         int64
	subjectKind, providerID, parserVersion            string
	observedAt                                        string
	buildID                                           string
	buildGeneration, buildRevision                    int64
	family                                            string
	familyIndex, familyStart, familyRows              int64
	afterOrdinal, materializedBefore                  int64
	aliases                                           []ipEvidenceAlias
	external                                          []ipEvidenceExternal
	terms                                             []ipEvidenceTerm
	ratings                                           []ipEvidenceRating
	credits                                           []ipEvidenceCredit
	episodes                                          []ipEvidenceEpisode
	media                                             []ipEvidenceMedia
	tracks                                            []ipEvidenceTrack
}

func (q ipEntityEvidenceAppendRequest) counts() [8]int {
	return [8]int{len(q.aliases), len(q.external), len(q.terms), len(q.ratings),
		len(q.credits), len(q.episodes), len(q.media), len(q.tracks)}
}

// page returns the one selected family page and its row count. Any row in a
// different family, or a negative index, is refused.
func (q ipEntityEvidenceAppendRequest) page(family int) (any, int) {
	counts := q.counts()
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
		return q.aliases, len(q.aliases)
	case 1:
		return q.external, len(q.external)
	case 2:
		return q.terms, len(q.terms)
	case 3:
		return q.ratings, len(q.ratings)
	case 4:
		return q.credits, len(q.credits)
	case 5:
		return q.episodes, len(q.episodes)
	case 6:
		return q.media, len(q.media)
	default:
		return q.tracks, len(q.tracks)
	}
}

type ipEntityEvidenceCommand struct {
	kind     string
	identity CommandIdentity
	digest   RequestDigest
	valid    bool
	begin    ipEntityEvidenceBeginRequest
	append   ipEntityEvidenceAppendRequest
	header   EvidenceHeader
}

type ipEntityEvidenceCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEntityEvidenceResult
	Digest  ResultDigest
}

type ipEntityEvidenceBuild struct {
	evidenceID, owner, state, phase, digest, expiresAt    string
	provider, subjectKind, providerID, parser, observedAt string
	generation, ownerGeneration, cursor, expectedRows     int64
	materializedRows, charge, revision, subjectID         int64
	ownerIncarnation                                      [16]byte
	digestBytes                                           [32]byte
	sealed                                                int64
}

type ipEntityEvidenceLookup struct {
	owner      ipEntityJobOwner
	receipt    ipEntityJobReceipt
	hasReceipt bool
	decision   receiptDecision
	value      ipEntityEvidenceResult
	digest     ResultDigest
}

func (c *ipEntityEvidenceCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEntityEvidenceCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntityEvidenceCommand")
}

func ipEntityEvidenceShapeOwner(operationID, provider, purpose, selectedID, lease string, entityID, generation, leaseUntilMS int64) error {
	if len(operationID) != 32 || !ipPolicyProviderCode(provider) || purpose != "metadata" ||
		!ipEvidenceText(selectedID, ipEvidenceIDMax) || len(selectedID) == 0 ||
		!ipEvidenceText(lease, ipEvidenceIDMax) || len(lease) == 0 ||
		entityID <= 0 || generation <= 0 || generation == math.MaxInt64 || leaseUntilMS <= 0 {
		return ErrInvalid
	}
	return nil
}

// Both the parser version and the request shape use a provider-specific IP
// namespace. EN's FIRST movie purpose is explicitly outside it, so a nonmovie
// begin cannot reuse or alias the merged EN parser/proof family.
func ipEntityEvidenceHeaderAllowed(h EvidenceHeaderInput) bool {
	if !ipEvidenceSubjectKind(h.SubjectKind) || !ipPolicyProviderCode(h.Provider) {
		return false
	}
	prefix := "ip." + h.Provider + "."
	if !strings.HasPrefix(h.ParserVersion, prefix) || !strings.HasPrefix(h.RequestShape, prefix) ||
		h.ParserVersion == h.RequestShape || !ipEvidenceText(h.ParserVersion, ipEvidenceTextMax) ||
		!ipEvidenceText(h.RequestShape, ipEvidenceTextMax) || !ipEvidenceText(h.ID, ipEvidenceIDMax) ||
		!ipEvidenceText(h.ProviderID, ipEvidenceIDMax) || h.SubjectID <= 0 {
		return false
	}
	return true
}

func ipEntityEvidenceShapeBegin(q ipEntityEvidenceBeginRequest) (EvidenceHeader, error) {
	var zero EvidenceHeader
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return zero, ErrInvalid
	}
	if !ipEvidenceText(q.buildID, ipEvidenceIDMax) || len(q.buildID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 {
		return zero, ErrInvalid
	}
	if expires, ok := ipEntityJobTextTime(q.expiresAt); !ok || expires <= 0 {
		return zero, ErrInvalid
	}
	if q.header.Provider != q.provider || q.header.ProviderID != q.selectedID ||
		!ipEntityEvidenceHeaderAllowed(q.header) {
		return zero, ErrInvalid
	}
	header, err := NewEvidenceHeader(q.header)
	if err != nil {
		return zero, err
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
	if _, ok := q.expectedRows(); !ok {
		return zero, ErrInvalid
	}
	return header, nil
}

func ipEntityEvidenceShapeAppend(q ipEntityEvidenceAppendRequest) error {
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return ErrInvalid
	}
	if !ipEvidenceText(q.evidenceID, ipEvidenceIDMax) || len(q.evidenceID) == 0 ||
		q.subjectID <= 0 || !ipEvidenceSubjectKind(q.subjectKind) ||
		!ipEvidenceText(q.providerID, ipEvidenceIDMax) || len(q.providerID) == 0 ||
		!ipEvidenceText(q.parserVersion, ipEvidenceTextMax) || len(q.parserVersion) == 0 ||
		!ipEvidenceText(q.observedAt, 64) || len(q.observedAt) == 0 ||
		!ipEvidenceText(q.buildID, ipEvidenceIDMax) || len(q.buildID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.buildRevision <= 0 || q.buildRevision == math.MaxInt64 ||
		q.familyIndex < 0 || q.familyIndex >= int64(len(ipEvidenceFamilySpecs)) ||
		q.family != ipEvidenceFamilySpecs[q.familyIndex].name ||
		q.familyStart < 1 || q.familyRows < 0 || q.afterOrdinal < 0 || q.materializedBefore < 1 {
		return ErrInvalid
	}
	if _, ok := ipEntityJobTextTime(q.observedAt); !ok {
		return ErrInvalid
	}
	if q.familyStart > math.MaxInt64-q.afterOrdinal || q.familyStart+q.afterOrdinal != q.materializedBefore ||
		q.afterOrdinal > math.MaxInt64-q.familyRows {
		return ErrInvalid
	}
	_, rows := q.page(int(q.familyIndex))
	if rows <= 0 || rows > ipEvidenceFamilySpecs[q.familyIndex].maxPage ||
		q.afterOrdinal+int64(rows) > q.familyRows {
		return ErrInvalid
	}
	if _, _, err := ipEntityEvidencePageCharge(int(q.familyIndex), q, int64(rows)); err != nil {
		return err
	}
	return nil
}

func newIPEntityEvidenceBeginCommand(identity CommandIdentity, in ipEntityEvidenceBeginRequest) (*ipEntityEvidenceCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	header, err := ipEntityEvidenceShapeBegin(in)
	if err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPEntityEvidenceBegin, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityEvidenceCommand{kind: "begin", identity: identity, begin: in, header: header, digest: digest, valid: true}, nil
}

func newIPEntityEvidenceAppendCommand(identity CommandIdentity, in ipEntityEvidenceAppendRequest) (*ipEntityEvidenceCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipEntityEvidenceShapeAppend(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPEntityEvidenceAppend, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityEvidenceCommand{kind: "append", identity: identity, append: in, digest: digest, valid: true}, nil
}

func ipEntityEvidenceReceiptKind(kind string) (CommandID, bool) {
	switch kind {
	case "begin":
		return IPEntityEvidenceBegin, true
	case "append":
		return IPEntityEvidenceAppend, true
	}
	return 0, false
}

// ipEntityEvidenceLookupReceipt resolves the ORIGINAL receipt before any fresh
// lease, observation, policy or consent probe. Current authority is checked by
// the caller first. Begin/append never mint an owner; the exact IP-05 claim must
// already exist under this provider namespace.
func ipEntityEvidenceLookupReceipt(ctx context.Context, turn *commandTurn, purpose CommandID, ref ReceiptReference,
	ownerID, provider string, entityID int64, decode func(ipEntityJobReceipt) (ipEntityEvidenceResult, error)) (out ipEntityEvidenceLookup, err error) {
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
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 || len(out.owner.selectedID) > ipEvidenceIDMax ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipEntityJobReceiptStmt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest []byte
	var terminal int64
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest, &out.receipt.appliedMS,
		&resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision, &out.receipt.phase, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		term := terminal == 1
		kind, known := ipEntityEvidenceReceiptKind(out.receipt.kind)
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.acceptedRevision <= 0 || !ipEntityJobFlag(terminal) || !known ||
			out.receipt.phase != "building" || term {
			return out, ErrResolveRequired
		}
		out.receipt.terminal = term
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
		computed, e := canonicalResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != out.receipt.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

func ipEntityEvidenceBeginSaved(q ipEntityEvidenceBeginRequest, header EvidenceHeader, identity CommandIdentity, r ipEntityJobReceipt) (ipEntityEvidenceResult, error) {
	var zero ipEntityEvidenceResult
	if r.kind != "begin" || r.phase != "building" || r.terminal || r.acceptedRevision != 1 ||
		r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipEntityEvidenceResult{Kind: "begin", OperationID: q.operationID,
		EvidenceID: header.ID(), BuildID: q.buildID, Provider: q.provider,
		SubjectKind: header.input.SubjectKind, ProviderID: header.input.ProviderID,
		ParserVersion: header.input.ParserVersion, Lease: q.lease, SubjectID: header.input.SubjectID,
		BuildGeneration: q.buildGeneration, Revision: 1, MaterializedRows: 1, ResultCount: 1,
		ResultBytes: ipEntityEvidenceHeaderCharge(header), LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS}, nil
}

func ipEntityEvidenceAppendSaved(q ipEntityEvidenceAppendRequest, identity CommandIdentity, r ipEntityJobReceipt, rows int64) (ipEntityEvidenceResult, error) {
	var zero ipEntityEvidenceResult
	if r.kind != "append" || r.phase != "building" || r.terminal || r.acceptedRevision != q.buildRevision+1 ||
		r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	charge, _, err := ipEntityEvidencePageCharge(int(q.familyIndex), q, rows)
	if err != nil {
		return zero, ErrResolveRequired
	}
	return ipEntityEvidenceResult{Kind: "append", OperationID: q.operationID,
		EvidenceID: q.evidenceID, BuildID: q.buildID, Provider: q.provider, SubjectKind: q.subjectKind,
		ProviderID: q.providerID, ParserVersion: q.parserVersion, Family: q.family, Lease: q.lease,
		SubjectID: q.subjectID, BuildGeneration: q.buildGeneration, Revision: q.buildRevision + 1,
		MaterializedRows: q.materializedBefore + rows, ResultCount: rows, ResultBytes: charge,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS}, nil
}

// ipEntityEvidencePageCharge validates one family page and returns its total
// persisted row charge and row count. It never trims, reorders or normalizes
// text: an oversized or out-of-family row is refused.
func ipEntityEvidencePageCharge(family int, q ipEntityEvidenceAppendRequest, rows int64) (int64, int64, error) {
	page, count := q.page(family)
	if page == nil || count < 0 || int64(count) != rows {
		return 0, 0, ErrInvalid
	}
	var total int64
	row := func(chunks ...string) error {
		var content int64
		for _, chunk := range chunks {
			if !ipEvidenceText(chunk, ipEvidenceTextMax) {
				return ErrInvalid
			}
			content += int64(len(chunk))
		}
		if content > ipEvidencePageMax || total > math.MaxInt64-512-8*content {
			return ErrCap
		}
		total += 512 + 8*content
		return nil
	}
	switch family {
	case 0:
		for i, value := range q.aliases {
			if value.Ordinal != q.afterOrdinal+int64(i) || len(value.RequestedID) == 0 || len(value.CanonicalID) == 0 {
				return 0, 0, ErrInvalid
			}
			if err := row(value.RequestedID, value.CanonicalID, value.Name); err != nil {
				return 0, 0, err
			}
		}
	case 1:
		for _, value := range q.external {
			if !ipEvidenceText(value.Provider, 64) || len(value.Provider) == 0 ||
				!ipEvidenceSubjectKind(value.SubjectKind) ||
				!ipEvidenceText(value.ProviderID, ipEvidenceTextMax) || len(value.ProviderID) == 0 {
				return 0, 0, ErrInvalid
			}
			if err := row(value.Provider, value.SubjectKind, value.ProviderID); err != nil {
				return 0, 0, err
			}
		}
	case 2:
		for i, value := range q.terms {
			if value.Ordinal != q.afterOrdinal+int64(i) || len(value.Vocabulary) == 0 || len(value.Value) == 0 {
				return 0, 0, ErrInvalid
			}
			if err := row(value.Vocabulary, value.Value); err != nil {
				return 0, 0, err
			}
		}
	case 3:
		for _, value := range q.ratings {
			if !ipEvidenceText(value.Role, 64) || len(value.Role) == 0 || value.Scale <= 0 ||
				math.IsNaN(value.Scale) || math.IsInf(value.Scale, 0) ||
				math.IsNaN(value.Value) || math.IsInf(value.Value, 0) || value.Votes < 0 ||
				!ipEvidenceText(value.SourceURL, ipEvidenceURLMax) || len(value.SourceURL) == 0 {
				return 0, 0, ErrInvalid
			}
			if err := row(value.Role, value.SourceURL); err != nil {
				return 0, 0, err
			}
		}
	case 4:
		for i, value := range q.credits {
			if value.Ordinal != q.afterOrdinal+int64(i) ||
				(value.Class != "cast" && value.Class != "crew" && value.Class != "artist") ||
				len(value.ProviderPersonID) == 0 || len(value.Name) == 0 ||
				(value.Scope != "" && q.subjectKind != "release") ||
				(value.Class == "cast" && (!value.BillingPresent || value.Billing < 0)) ||
				(value.Class != "cast" && (value.BillingPresent || value.Billing != 0)) {
				return 0, 0, ErrInvalid
			}
			if err := row(value.Class, value.Scope, value.TrackID, value.ProviderPersonID, value.Name,
				value.ArtistName, value.SortName, value.Disambiguation, value.JoinPhrase, value.Role,
				value.Department, value.CreditID); err != nil {
				return 0, 0, err
			}
		}
	case 5:
		for i, value := range q.episodes {
			if value.Ordinal != q.afterOrdinal+int64(i) || len(value.EpisodeEvidenceID) == 0 ||
				len(value.EpisodeOrder) == 0 || !ipEvidenceOptionalNumber(value.SeasonNumber) ||
				!ipEvidenceOptionalNumber(value.EpisodeNumber) || !ipEvidenceOptionalNumber(value.AbsoluteNumber) {
				return 0, 0, ErrInvalid
			}
			if err := row(value.EpisodeEvidenceID, value.EpisodeOrder); err != nil {
				return 0, 0, err
			}
		}
	case 6:
		for i, value := range q.media {
			if value.Position != q.afterOrdinal+int64(i)+1 || len(value.Format) == 0 || len(value.Title) == 0 {
				return 0, 0, ErrInvalid
			}
			if err := row(value.Format, value.Title); err != nil {
				return 0, 0, err
			}
		}
	case 7:
		for _, value := range q.tracks {
			if len(value.TrackID) == 0 || value.MediumPosition <= 0 || value.Position <= 0 ||
				len(value.Number) == 0 || len(value.Title) == 0 || len(value.RecordingEvidenceID) == 0 ||
				!ipEvidenceOptionalNumber(value.LengthMillis) {
				return 0, 0, ErrInvalid
			}
			if err := row(value.TrackID, value.Number, value.Title, value.Artist, value.RecordingEvidenceID); err != nil {
				return 0, 0, err
			}
		}
	default:
		return 0, 0, ErrInvalid
	}
	if total <= 0 {
		return 0, 0, ErrCap
	}
	return total, int64(count), nil
}

func ipEvidenceOptionalNumber(value *int64) bool {
	return value == nil || (*value >= 0 && *value <= 1_000_000)
}

func ipEntityEvidenceBuildRead(ctx context.Context, turn *commandTurn, buildID string) (out ipEntityEvidenceBuild, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityEvidenceBuildStmt, buildID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	err = row.Scan(&out.evidenceID, &out.generation, &out.owner, &incarnation, &out.ownerGeneration,
		&out.state, &out.phase, &out.cursor, &out.expectedRows, &out.materializedRows, &out.charge,
		&out.digest, &out.expiresAt, &out.revision, &out.subjectID, &out.provider, &out.subjectKind,
		&out.providerID, &out.parser, &out.sealed, &out.observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sql.ErrNoRows
	}
	if err != nil {
		return out, err
	}
	digest, ok := ipEvidenceDigestBytes(out.digest)
	if len(incarnation) != 16 || !ok || out.generation <= 0 || out.ownerGeneration <= 0 ||
		out.cursor < 0 || out.expectedRows <= 0 || out.materializedRows < 0 || out.materializedRows > out.cursor ||
		out.charge < ipEvidenceHeaderCost || out.revision <= 0 || out.subjectID <= 0 ||
		out.sealed != 0 || (out.state != "building" && out.state != "ready" && out.state != "aborting" &&
		out.state != "retiring" && out.state != "done") || len(out.phase) == 0 ||
		!ipPolicyProviderCode(out.provider) || !ipEvidenceSubjectKind(out.subjectKind) ||
		len(out.evidenceID) == 0 || len(out.owner) == 0 || len(out.providerID) == 0 || len(out.parser) == 0 {
		return out, ErrResolveRequired
	}
	copy(out.ownerIncarnation[:], incarnation)
	out.digestBytes = digest
	return out, nil
}

func ipEvidenceDigestBytes(value string) ([32]byte, bool) {
	var out [32]byte
	if len(value) != 64 {
		return out, false
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

func ipEntityEvidenceShapeDigest(families []ipEvidenceFamilyDeclaration) ([32]byte, error) {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_evidence_shape.v1\x00"))
	e.number(uint64(len(families)))
	for _, family := range families {
		e.text(family.Name)
		e.number(uint64(family.Rows))
		e.bytes(family.Digest[:])
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

func ipEntityEvidenceBuildDigest(evidenceID, buildID string, generation, expectedRows int64, shape [32]byte) [32]byte {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_evidence_build.v1\x00"))
	e.text(evidenceID)
	e.text(buildID)
	e.number(uint64(generation))
	e.number(uint64(expectedRows))
	e.bytes(shape[:])
	var out [32]byte
	if e.err == nil {
		copy(out[:], e.h.Sum(nil))
	}
	return out
}

func ipEntityEvidenceChainDigest(previous [32]byte, family int, after int64, page any) ([32]byte, error) {
	if family < 0 || family >= len(ipEvidenceFamilySpecs) || after < 0 || page == nil {
		return [32]byte{}, ErrInvalid
	}
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte("portico.ip_evidence_chain.v1\x00"))
	e.bytes(previous[:])
	e.number(uint64(family))
	e.number(uint64(after))
	e.value(reflect.ValueOf(page), 0)
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

func execIPEntityEvidence(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, ok := turn.steps[id]
	if !ok {
		return ErrInvalid
	}
	written, err := turn.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func ipEntityEvidenceAdvanceOwner(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	ownerID, provider, lease string, generation, leaseUntilMS int64, owner ipEntityJobOwner) (err error) {
	if identity.Sequence <= owner.lastSequence {
		return ErrConflict
	}
	return wc008DraftChanged(ctx, turn, ipEntityEvidenceAdvanceOwnerStmt, identity.Sequence,
		ownerID, provider, identity.Incarnation[:], generation,
		lease, ipEntityJobTimeText(leaseUntilMS), owner.lastSequence, owner.acknowledgedSequence)
}

func ipEntityEvidenceInsertPage(ctx context.Context, turn *commandTurn, family int, q ipEntityEvidenceAppendRequest) error {
	switch family {
	case 0:
		for _, value := range q.aliases {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceAliasStmt, q.evidenceID, value.Ordinal,
				value.RequestedID, value.CanonicalID, value.Name, q.observedAt); err != nil {
				return err
			}
		}
	case 1:
		for _, value := range q.external {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceExternalStmt, q.evidenceID,
				value.Provider, value.SubjectKind, value.ProviderID); err != nil {
				return err
			}
		}
	case 2:
		for _, value := range q.terms {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceTermStmt, q.evidenceID,
				value.Vocabulary, value.Ordinal, value.Value); err != nil {
				return err
			}
		}
	case 3:
		for _, value := range q.ratings {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceRatingStmt, q.evidenceID,
				q.provider, value.Role, value.Value, value.Scale, value.Votes, value.SourceURL); err != nil {
				return err
			}
		}
	case 4:
		for _, value := range q.credits {
			source, billing := any(nil), any(nil)
			if value.SourceOrdinalPresent {
				source = value.SourceOrdinal
			}
			if value.BillingPresent {
				billing = value.Billing
			}
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceCreditStmt, q.provider, q.subjectKind,
				value.Class, q.evidenceID, value.Scope, value.Ordinal, value.ProviderPersonID, value.Name,
				value.ArtistName, value.SortName, value.Disambiguation, value.JoinPhrase, value.Role,
				value.Department, value.CreditID, source, billing); err != nil {
				return err
			}
		}
	case 5:
		for _, value := range q.episodes {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceEpisodeStmt, q.provider, q.subjectKind,
				q.evidenceID, value.Ordinal, value.EpisodeEvidenceID, value.EpisodeOrder,
				ipEvidenceOptionalValue(value.SeasonNumber), ipEvidenceOptionalValue(value.EpisodeNumber),
				ipEvidenceOptionalValue(value.AbsoluteNumber)); err != nil {
				return err
			}
		}
	case 6:
		for _, value := range q.media {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceMediaStmt, q.provider, q.subjectKind,
				q.evidenceID, value.Position, value.Format, value.Title); err != nil {
				return err
			}
		}
	case 7:
		for _, value := range q.tracks {
			if err := execIPEntityEvidence(ctx, turn, ipEntityEvidenceTrackStmt, q.provider, q.subjectKind,
				q.evidenceID, value.TrackID, value.MediumPosition, value.Position, value.Number,
				value.Title, value.Artist, ipEvidenceOptionalValue(value.LengthMillis),
				value.RecordingEvidenceID); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

func ipEvidenceOptionalValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func ipEntityEvidenceRecordReceipt(ctx context.Context, turn *commandTurn, id CommandIdentity, ownerID string,
	request RequestDigest, value ipEntityEvidenceResult, familyIndex, afterOrdinal int64, afterKey string) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	digest, err := ipEntityEvidenceResultDigest(id, request, value)
	if err != nil {
		return out, err
	}
	step, ok := turn.steps[ipEntityEvidenceRecordStmt]
	if !ok {
		return out, ErrInvalid
	}
	written, err := turn.exec(ctx, step.statementStep, ownerID,
		id.Incarnation[:], id.Sequence, id.OperationID[:], request[:], value.Kind, value.Revision,
		value.AppliedMS, digest[:], familyIndex, afterOrdinal, afterKey, value.ResultCount,
		value.ResultBytes, value.EvidenceID, value.SubjectID, value.ParserVersion, value.BuildID)
	if err != nil {
		return out, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return digest, nil
}

func ipEntityEvidenceBeginFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipEntityEvidenceBeginRequest, header EvidenceHeader, owner ipEntityJobOwner, appliedMS int64) (out ipEntityEvidenceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err = ipEntityEvidenceBuildRead(ctx, turn, q.buildID); err == nil {
		return out, ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityEvidenceReceiptReverseStmt, header.ID())
	if err != nil {
		return out, err
	}
	var existing int64
	if err = row.Scan(&existing); err != nil || existing != 0 {
		return out, errors.Join(ErrConflict, err)
	}
	if err = execIPEntityEvidence(ctx, turn, ipEntityEvidenceInsertEvidenceStmt,
		append(evidenceHeaderValues(header.input), int64(0))...); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	expected, ok := q.expectedRows()
	if !ok {
		return out, ErrInvalid
	}
	shape, err := ipEntityEvidenceShapeDigest(q.families)
	if err != nil {
		return out, err
	}
	digest := ipEntityEvidenceBuildDigest(header.ID(), q.buildID, q.buildGeneration, expected, shape)
	charge := ipEntityEvidenceHeaderCharge(header)
	if err = execIPEntityEvidence(ctx, turn, ipEntityEvidenceInsertBuildStmt, q.buildID, header.ID(),
		q.buildGeneration, q.operationID, identity.Incarnation[:], q.generation, expected,
		hex.EncodeToString(shape[:]), hex.EncodeToString(digest[:]), charge, q.expiresAt); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return ipEntityEvidenceResult{Kind: "begin", OperationID: q.operationID, EvidenceID: header.ID(),
		BuildID: q.buildID, Provider: q.provider, SubjectKind: header.input.SubjectKind,
		ProviderID: header.input.ProviderID, ParserVersion: header.input.ParserVersion, Lease: q.lease,
		SubjectID: header.input.SubjectID, BuildGeneration: q.buildGeneration, Revision: 1,
		MaterializedRows: 1, ResultCount: 1, ResultBytes: charge, LeaseUntilMS: q.leaseUntilMS,
		AppliedMS: appliedMS}, nil
}

func ipEntityEvidenceAppendFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipEntityEvidenceAppendRequest, owner ipEntityJobOwner, appliedMS int64) (out ipEntityEvidenceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	build, err := ipEntityEvidenceBuildRead(ctx, turn, q.buildID)
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
	_, rows := q.page(int(q.familyIndex))
	if rows <= 0 || build.evidenceID != q.evidenceID || build.owner != q.operationID ||
		build.ownerIncarnation != identity.Incarnation || build.ownerGeneration != q.generation ||
		build.generation != q.buildGeneration || build.subjectID != q.subjectID ||
		build.provider != q.provider || build.subjectKind != q.subjectKind ||
		build.providerID != q.providerID || build.parser != q.parserVersion ||
		build.observedAt != q.observedAt || build.state != "building" || build.phase != "children" ||
		build.sealed != 0 || build.cursor != q.materializedBefore || build.revision != q.buildRevision {
		return out, ErrConflict
	}
	if q.familyStart > build.cursor || build.cursor-q.familyStart != q.afterOrdinal ||
		build.cursor > math.MaxInt64-int64(rows) || build.cursor+int64(rows) > build.expectedRows {
		return out, ErrConflict
	}
	page, _ := q.page(int(q.familyIndex))
	digest, err := ipEntityEvidenceChainDigest(build.digestBytes, int(q.familyIndex), q.afterOrdinal, page)
	if err != nil {
		return out, err
	}
	charge, _, err := ipEntityEvidencePageCharge(int(q.familyIndex), q, int64(rows))
	if err != nil {
		return out, err
	}
	if build.charge > math.MaxInt64-charge {
		return out, ErrCap
	}
	if err = ipEntityEvidenceInsertPage(ctx, turn, int(q.familyIndex), q); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := turn.queryRow(ctx, turn.steps[ipEntityEvidenceAppendStmt].statementStep,
		int64(rows), int64(rows), charge, hex.EncodeToString(digest[:]), q.buildID, q.evidenceID,
		q.buildGeneration, q.operationID, identity.Incarnation[:], q.generation, build.cursor,
		build.materializedRows, build.charge, build.digest, q.buildRevision, int64(rows))
	if err != nil {
		return out, ErrConflict
	}
	var cursor, materialized, total, revision int64
	if err = row.Scan(&cursor, &materialized, &total, &revision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if cursor != build.cursor+int64(rows) || materialized != build.materializedRows+int64(rows) ||
		total != build.charge+charge || revision != q.buildRevision+1 {
		return out, ErrConflict
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return ipEntityEvidenceResult{Kind: "append", OperationID: q.operationID, EvidenceID: q.evidenceID,
		BuildID: q.buildID, Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		ParserVersion: q.parserVersion, Family: q.family, Lease: q.lease, SubjectID: q.subjectID,
		BuildGeneration: q.buildGeneration, Revision: revision, MaterializedRows: cursor,
		ResultCount: int64(rows), ResultBytes: charge, LeaseUntilMS: q.leaseUntilMS,
		AppliedMS: appliedMS}, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved begin with zero new effects, before any fresh lease probe.
func applyIPEntityEvidenceBegin(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityEvidenceCommand) (out ipEntityEvidenceCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityEvidenceCommandResult
	if c == nil || !c.valid || c.kind != "begin" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPEntityEvidenceBegin {
		return zero, ErrInvalid
	}
	q := c.begin
	header, err := ipEntityEvidenceShapeBegin(q)
	if err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPEntityEvidenceBegin, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	ownerID := q.operationID
	decode := func(r ipEntityJobReceipt) (ipEntityEvidenceResult, error) {
		return ipEntityEvidenceBeginSaved(q, header, c.identity, r)
	}
	lookup, err := ipEntityEvidenceLookupReceipt(ctx, turn, IPEntityEvidenceBegin, c.reference(), ownerID, q.provider, q.entityID, decode)
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
	value, err := ipEntityEvidenceBeginFresh(ctx, turn, c.identity, q, header, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipEntityEvidenceRecordReceipt(ctx, turn, c.identity, ownerID, c.digest, value, 0, 0, "")
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityEvidenceLookupReceipt(ctx, turn, IPEntityEvidenceBegin, c.reference(), ownerID,
		q.provider, q.entityID, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

// Append advances only the exact live owner's open build. The original receipt
// replays before the build, lease, observation, policy or consent witnesses.
func applyIPEntityEvidenceAppend(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityEvidenceCommand) (out ipEntityEvidenceCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityEvidenceCommandResult
	if c == nil || !c.valid || c.kind != "append" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPEntityEvidenceAppend {
		return zero, ErrInvalid
	}
	q := c.append
	if err = ipEntityEvidenceShapeAppend(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPEntityEvidenceAppend, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	_, rows := q.page(int(q.familyIndex))
	decode := func(r ipEntityJobReceipt) (ipEntityEvidenceResult, error) {
		return ipEntityEvidenceAppendSaved(q, c.identity, r, int64(rows))
	}
	lookup, err := ipEntityEvidenceLookupReceipt(ctx, turn, IPEntityEvidenceAppend, c.reference(),
		q.operationID, q.provider, q.entityID, decode)
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
	value, err := ipEntityEvidenceAppendFresh(ctx, turn, c.identity, q, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipEntityEvidenceRecordReceipt(ctx, turn, c.identity, q.operationID, c.digest, value,
		q.familyIndex, q.afterOrdinal+value.ResultCount, q.evidenceID)
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityEvidenceLookupReceipt(ctx, turn, IPEntityEvidenceAppend, c.reference(),
		q.operationID, q.provider, q.entityID, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
