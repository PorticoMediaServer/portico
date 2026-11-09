package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP nonmovie entity provider accept 216 publishes ONE sealed IP-07 evidence
// build (and, for series/season, the complete IP-08 set) as the current accepted
// provider source for one catalogue entity. It is the accepting command for the
// IP domain: it composes current authority, the original receipt, the exact
// sealed source proof, the selected subject head, the typed field/ID/term
// cohort, the search intent and the saved result in one transaction.
//
// It copies the merged EN accept shape (authority first, original receipt before
// fresh source checks, complete native proof before the first write, typed
// replay from the saved receipt only) without widening EN's FIRST movie purpose,
// credit image or proof families. Nonmovie credits stay evidence-pinned: this
// body verifies the complete selected cohort and records its count; it never
// copies credits per child.
const (
	ipEntityAcceptIDMax     = 128
	ipEntityAcceptFieldsMax = 16
	ipEntityAcceptFieldMax  = 4096
	ipEntityAcceptTermMax   = 4096
)

// ipEntityAcceptField is one typed accepted descriptive value. A field is
// present only when the provider observation actually carried it; an empty
// value with Present true is an explicit empty observation, never absence.
type ipEntityAcceptField struct {
	Name, Value string
	Present     bool
}

type ipEntityAcceptExternal struct {
	Provider, SubjectKind, ProviderID string
}

type ipEntityAcceptTerm struct {
	Vocabulary string
	Ordinal    int64
	Value      string
}

type ipEntityAcceptCredit struct {
	Class, Scope, ProviderPersonID, CreditID string
	Name, Role, Department                   string
	Ordinal, Billing, SourceOrdinal          int64
	BillingPresent, SourceOrdinalPresent     bool
}

// ipEntityAcceptRequest is the complete original acceptance input. The owner
// tuple is IP-05's exact claimed operation; the source tuple is IP-07/08's
// sealed evidence; the selection tuple is the IA accepted-subject head; the
// typed cohort is what the provider adapter selected from that observation.
type ipEntityAcceptRequest struct {
	operationID, provider, purpose, selectedID, lease string
	entityID, generation, leaseUntilMS                int64
	evidenceID, buildID, setID, publicationID         string
	buildGeneration, contentRevision                  int64
	subjectID                                         int64
	subjectKind, providerID, parserVersion            string
	language, region, requestShape, observedAt        string
	selectionIncarnation                              [16]byte
	selectionRevision, searchJobID                    int64
	fields                                            []ipEntityAcceptField
	external                                          []ipEntityAcceptExternal
	terms                                             []ipEntityAcceptTerm
	credits                                           []ipEntityAcceptCredit
}

type ipEntityAcceptCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  ipEntityAcceptRequest
	valid    bool
}

type ipEntityAcceptCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEntityAcceptResult
	Digest  ResultDigest
}

type ipEntityAcceptReceipt struct {
	incarnation                []byte
	operation, request, result []byte
	sequence, acceptedRevision int64
	appliedMS, resultCount     int64
	resultBytes, terminal      int64
	subjectID                  int64
	kind, phase                string
	evidence, set              string
	parser, build, publication string
}

type ipEntityAcceptLookup struct {
	owner    ipEntityJobOwner
	receipt  ipEntityAcceptReceipt
	decision receiptDecision
	value    ipEntityAcceptResult
	digest   ResultDigest
}

type ipEntityAcceptEvidenceFacts struct {
	subjectID                                             int64
	sealed                                                int64
	sealSequence                                          int64
	provider, subjectKind, providerID, parser             string
	language, region, requestShape, observedAt, sourceURL string
	digest, sealOwner                                     string
	sealIncarnation                                       []byte
}

type ipEntityAcceptSealFacts struct {
	incarnation                    []byte
	sequence, acceptedRevision     int64
	appliedMS, terminal, subjectID int64
	rows, bytes, pins              int64
	resultDigest, commitment       []byte
	evidence, set, parser, build   string
}

type ipEntityAcceptBuildFacts struct {
	ownerIncarnation                                                 []byte
	ownerGeneration, cursor, expected, materialized, contentRevision int64
	generation, subjectID                                            int64
	evidenceID, ownerOperation, state, phase                         string
	provider, subjectKind, providerID, parser                        string
	language, region, requestShape, observedAt, digest               string
}

type ipEntityAcceptSelectionFacts struct {
	incarnation []byte
	revision    int64
	subjectKind string
	providerID  string
	status      string
	observedAt  string
	evidence    sql.NullString
	set         sql.NullString
	publication sql.NullString
	track       sql.NullString
}

type ipEntityAcceptProjectionFacts struct {
	title, sortTitle, language, overview, originalTitle, tagline string
	releaseDate, contentRating, studio, network, country         string
	year, revision                                               int64
	incarnation                                                  []byte
}

func (c *ipEntityAcceptCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEntityAcceptCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntityAcceptCommand")
}

// ipEntityAcceptSubjectKind is the nonmovie entity namespace. Episodes are
// members of a TV set, not accepted entity sources; movie and person custody
// keep their own merged owners.
func ipEntityAcceptSubjectKind(value string) bool {
	switch value {
	case "series", "season", "artist", "release", "recording":
		return true
	}
	return false
}

func ipEntityAcceptText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func ipEntityAcceptOptionalID(value string) bool {
	return len(value) == 0 || (len(value) <= ipEntityAcceptIDMax && ipEntityAcceptText(value, ipEntityAcceptIDMax))
}

// ipEntityAcceptFieldAllowed is the finite descriptive API. Projection fields
// update the automatic entity values; the remaining names are provenance-only.
func ipEntityAcceptFieldAllowed(name string) bool {
	switch name {
	case "title", "sortTitle", "language", "overview", "originalTitle", "tagline",
		"releaseDate", "contentRating", "studio", "network", "country", "year",
		"sortName", "status", "disambiguation", "type", "isbn", "pageCount":
		return true
	}
	return false
}

// ipEntityAcceptOwnerKind maps the provider subject namespace to the owner field
// kind. It never changes catalogue identity.
func ipEntityAcceptOwnerKind(subjectKind string) string {
	switch subjectKind {
	case "series":
		return "show"
	case "season":
		return "season"
	case "artist":
		return "artist"
	case "release":
		return "album"
	case "recording":
		return "item"
	}
	return ""
}

func ipEntityAcceptYear(value string) bool {
	if len(value) == 0 || len(value) > 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	year, err := strconv.ParseInt(value, 10, 64)
	return err == nil && year >= 0 && year <= 9999
}

func ipEntityAcceptFieldsShape(fields []ipEntityAcceptField) error {
	if len(fields) == 0 || len(fields) > ipEntityAcceptFieldsMax {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(fields))
	title, language := false, false
	for _, field := range fields {
		if !ipEntityAcceptFieldAllowed(field.Name) || seen[field.Name] ||
			!ipEntityAcceptText(field.Value, ipEntityAcceptFieldMax) {
			return ErrInvalid
		}
		seen[field.Name] = true
		switch field.Name {
		case "title":
			if !field.Present || len(field.Value) == 0 {
				return ErrInvalid
			}
			title = true
		case "language":
			if !field.Present || len(field.Value) == 0 {
				return ErrInvalid
			}
			language = true
		case "year":
			if !field.Present || !ipEntityAcceptYear(field.Value) {
				return ErrInvalid
			}
		}
	}
	if !title || !language {
		return ErrInvalid
	}
	return nil
}

func ipEntityAcceptExternalShape(external []ipEntityAcceptExternal) error {
	if int64(len(external)) > ipEvidenceFamilyBound("external") {
		return ErrInvalid
	}
	seen := make(map[[3]string]bool, len(external))
	for _, row := range external {
		if !ipPolicyProviderCode(row.Provider) || !ipEntityAcceptSubjectKind(row.SubjectKind) ||
			!ipEvidenceText(row.ProviderID, ipEvidenceIDMax) || len(row.ProviderID) == 0 {
			return ErrInvalid
		}
		key := [3]string{row.Provider, row.SubjectKind, row.ProviderID}
		if seen[key] {
			return ErrInvalid
		}
		seen[key] = true
	}
	return nil
}

func ipEntityAcceptTermsShape(terms []ipEntityAcceptTerm) error {
	if int64(len(terms)) > ipEvidenceFamilyBound("term") {
		return ErrInvalid
	}
	seen := make(map[[2]string]bool, len(terms))
	for _, term := range terms {
		// Only the permanent genre vocabulary exists in the embedded catalogue.
		// Reserved provider-specific vocabularies need their own adopted number.
		if term.Vocabulary != "genre" || term.Ordinal < 0 ||
			!ipEntityAcceptText(term.Value, ipEntityAcceptTermMax) || len(term.Value) == 0 {
			return ErrInvalid
		}
		key := [2]string{term.Vocabulary, strconv.FormatInt(term.Ordinal, 10)}
		if seen[key] {
			return ErrInvalid
		}
		seen[key] = true
	}
	return nil
}

// ipEntityAcceptCreditClass is the closed metadata_evidence_credits class set.
// An IP evidence cohort carries every class the provider observation carries:
// screen cast/crew and MusicBrainz artist credits. Contributions stay
// evidence-pinned, so this is a count/shape check, never the retained 100cast/
// 50crew selection image.
func ipEntityAcceptCreditClass(value string) bool {
	switch value {
	case string(Cast), string(Crew), "artist":
		return true
	}
	return false
}

func ipEntityAcceptCreditsShape(credits []ipEntityAcceptCredit) error {
	// IP-06 bounds the complete evidence credit family to 1024 rows across
	// pages; the retained selection image (100 cast + 50 crew) belongs to the
	// merged screen-credit selector, not to this evidence-cohort check.
	if int64(len(credits)) > ipEvidenceFamilyBound("credit") {
		return ErrInvalid
	}
	seen := make(map[[2]string]bool, len(credits))
	for _, credit := range credits {
		if !ipEntityAcceptCreditClass(credit.Class) ||
			credit.Ordinal < 0 || credit.Billing < 0 || credit.SourceOrdinal < 0 ||
			(credit.Class == string(Cast) && !credit.BillingPresent) ||
			!credit.SourceOrdinalPresent ||
			!ipEntityAcceptText(credit.Scope, ipEntityAcceptIDMax) ||
			!ipEntityAcceptText(credit.ProviderPersonID, ipEvidenceIDMax) ||
			!ipEntityAcceptText(credit.CreditID, ipEntityAcceptIDMax) ||
			!ipEntityAcceptText(credit.Name, ipEntityAcceptTermMax) || len(credit.Name) == 0 ||
			!ipEntityAcceptText(credit.Role, ipEntityAcceptTermMax) ||
			!ipEntityAcceptText(credit.Department, ipEntityAcceptTermMax) {
			return ErrInvalid
		}
		// metadata_evidence_credits is keyed by (evidence, scope, ordinal): the
		// request must name each row once or the count check cannot prove the
		// complete cohort.
		key := [2]string{credit.Scope, strconv.FormatInt(credit.Ordinal, 10)}
		if seen[key] {
			return ErrInvalid
		}
		seen[key] = true
	}
	return nil
}

func ipEntityAcceptShape(q ipEntityAcceptRequest) error {
	if ipEntityEvidenceShapeOwner(q.operationID, q.provider, q.purpose, q.selectedID, q.lease,
		q.entityID, q.generation, q.leaseUntilMS) != nil {
		return ErrInvalid
	}
	if !ipEvidenceText(q.evidenceID, ipEvidenceIDMax) || len(q.evidenceID) == 0 ||
		!ipEvidenceText(q.buildID, ipEvidenceIDMax) || len(q.buildID) == 0 ||
		!ipEntityAcceptOptionalID(q.setID) ||
		!ipEntityAcceptText(q.publicationID, ipEntityAcceptIDMax) || len(q.publicationID) == 0 ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.contentRevision <= 0 || q.contentRevision == math.MaxInt64 ||
		q.subjectID <= 0 || !ipEntityAcceptSubjectKind(q.subjectKind) ||
		!ipEvidenceText(q.providerID, ipEvidenceIDMax) || len(q.providerID) == 0 ||
		!ipEvidenceText(q.parserVersion, ipEvidenceTextMax) || len(q.parserVersion) == 0 ||
		!ipEvidenceText(q.language, 64) || !ipEvidenceText(q.region, 64) ||
		!ipEntityEvidenceLocaleValid(q.provider, q.subjectKind, q.parserVersion,
			q.requestShape, q.language, q.region) ||
		!ipEvidenceText(q.requestShape, ipEvidenceTextMax) || len(q.requestShape) == 0 ||
		!ipEvidenceText(q.observedAt, 64) || len(q.observedAt) == 0 ||
		q.selectionIncarnation == [16]byte{} ||
		q.selectionRevision <= 0 || q.selectionRevision == math.MaxInt64 ||
		q.searchJobID <= 0 {
		return ErrInvalid
	}
	prefix := "ip." + q.provider + "."
	if !strings.HasPrefix(q.parserVersion, prefix) || !strings.HasPrefix(q.requestShape, prefix) ||
		q.parserVersion == q.requestShape {
		return ErrInvalid
	}
	if _, ok := ipEntityJobTextTime(q.observedAt); !ok {
		return ErrInvalid
	}
	// A TV set is optional for a series/season acceptance, but only a
	// series/season may carry one. Other nonmovie kinds never do.
	if q.setID != "" && q.subjectKind != "series" && q.subjectKind != "season" {
		return ErrInvalid
	}
	if err := ipEntityAcceptFieldsShape(q.fields); err != nil {
		return err
	}
	if err := ipEntityAcceptExternalShape(q.external); err != nil {
		return err
	}
	if err := ipEntityAcceptTermsShape(q.terms); err != nil {
		return err
	}
	return ipEntityAcceptCreditsShape(q.credits)
}

func newIPEntityAcceptCommand(identity CommandIdentity, in ipEntityAcceptRequest) (*ipEntityAcceptCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipEntityAcceptShape(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPEntityAccept, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityAcceptCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipEntityAcceptLookupReceipt resolves the ORIGINAL receipt before any fresh
// source/lease probe. Current authority is checked by the caller first.
func ipEntityAcceptLookupReceipt(ctx context.Context, turn *commandTurn, ref ReceiptReference,
	q ipEntityAcceptRequest) (out ipEntityAcceptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntityAccept {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobOwnerStmt, q.operationID, q.provider)
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
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipEntityAcceptSavedStmt, q.operationID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest []byte
	var set, publication sql.NullString
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest,
		&out.receipt.appliedMS, &resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision,
		&out.receipt.phase, &out.receipt.terminal, &out.receipt.evidence, &out.receipt.subjectID,
		&out.receipt.parser, &out.receipt.build, &set, &publication, &out.receipt.resultCount,
		&out.receipt.resultBytes)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.kind != "accept" || out.receipt.phase != "accepted" ||
			out.receipt.terminal != 1 || out.receipt.acceptedRevision <= 0 ||
			!publication.Valid || len(publication.String) == 0 ||
			out.receipt.resultCount <= 0 || out.receipt.resultBytes < 0 {
			return out, ErrResolveRequired
		}
		out.receipt.incarnation = receiptIncarnation
		out.receipt.operation = operation
		out.receipt.request = requestDigest
		out.receipt.result = resultDigest
		if set.Valid {
			out.receipt.set = set.String
		}
		out.receipt.publication = publication.String
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], receiptIncarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPEntityAccept,
			resultCount: out.receipt.resultCount}
		copy(found.resultDigest[:], resultDigest)
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPEntityAccept,
		receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
			lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.acknowledgedSequence}, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		value, e := ipEntityAcceptSaved(q, ref.Identity, out.receipt)
		if e != nil {
			return out, errors.Join(ErrResolveRequired, e)
		}
		computed, e := ipEntityAcceptResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != found.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

// ipEntityAcceptSaved rebuilds the COMPLETE saved result from the original
// request and the saved receipt witnesses. It never reads the mutable accepted
// publication, subject slot, entity projection or search job.
func ipEntityAcceptSaved(q ipEntityAcceptRequest, identity CommandIdentity, r ipEntityAcceptReceipt) (ipEntityAcceptResult, error) {
	var zero ipEntityAcceptResult
	if r.kind != "accept" || r.phase != "accepted" || r.terminal != 1 ||
		!ipEntityAcceptEqualBlob(r.operation, identity.OperationID[:]) ||
		r.evidence != q.evidenceID || r.subjectID != q.subjectID || r.parser != q.parserVersion ||
		r.build != q.buildID || r.set != q.setID || r.publication != q.publicationID ||
		r.acceptedRevision <= 0 || r.resultCount != int64(len(q.fields)) ||
		r.resultBytes != ipEntityAcceptFieldBytes(q.fields) {
		return zero, ErrResolveRequired
	}
	out := ipEntityAcceptResult{
		OperationID: q.operationID, PublicationID: q.publicationID, EvidenceID: q.evidenceID,
		SetID: q.setID, Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		ParserVersion: q.parserVersion, ObservedAt: q.observedAt, Lease: q.lease,
		EntityID: q.entityID, OwnerGeneration: q.generation, ContentRevision: q.contentRevision,
		AcceptedRevision: r.acceptedRevision, SelectionRevision: q.selectionRevision,
		SearchJobID: q.searchJobID, FieldCount: int64(len(q.fields)),
		ExternalCount: int64(len(q.external)), TermCount: int64(len(q.terms)),
		CreditCount: int64(len(q.credits)), LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS,
	}
	if err := preflightIPEntityAcceptResult(out); err != nil {
		return zero, err
	}
	return out, nil
}

func ipEntityAcceptEqualBlob(value []byte, want []byte) bool {
	if len(value) != len(want) {
		return false
	}
	for i := range value {
		if value[i] != want[i] {
			return false
		}
	}
	return true
}

func ipEntityAcceptFieldBytes(fields []ipEntityAcceptField) int64 {
	var total int64
	for _, field := range fields {
		total += int64(len(field.Value))
	}
	return total
}

// ipEntityAcceptEvidenceRead reads the immutable observation header and proves
// the exact IP seal tuple the accept publishes.
func ipEntityAcceptEvidenceRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out ipEntityAcceptEvidenceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptEvidenceStmt, q.evidenceID)
	if err != nil {
		return out, err
	}
	var sealOwner sql.NullString
	var sealSequence sql.NullInt64
	err = row.Scan(&out.subjectID, &out.provider, &out.subjectKind, &out.providerID, &out.parser,
		&out.language, &out.region, &out.requestShape, &out.observedAt, &out.sourceURL,
		&out.digest, &out.sealed, &sealOwner, &out.sealIncarnation, &sealSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.subjectID != q.subjectID || out.provider != q.provider || out.subjectKind != q.subjectKind ||
		out.providerID != q.providerID || out.parser != q.parserVersion || out.language != q.language ||
		out.region != q.region || out.requestShape != q.requestShape || out.observedAt != q.observedAt ||
		out.sealed != 1 || len(out.sourceURL) == 0 || len(out.digest) != 64 ||
		!sealOwner.Valid || sealOwner.String != q.operationID || len(out.sealIncarnation) != 16 ||
		!sealSequence.Valid || sealSequence.Int64 <= 0 {
		return out, ErrConflict
	}
	out.sealOwner, out.sealSequence = sealOwner.String, sealSequence.Int64
	return out, nil
}

// ipEntityAcceptEvidenceSealRead proves the exact ORIGINAL IP-07 seal receipt
// that owns the evidence. Its saved result digest and manifest bytes stay the
// source proof; the accept only pins it.
func ipEntityAcceptEvidenceSealRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out ipEntityAcceptSealFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptEvidenceSealStmt, q.operationID, q.evidenceID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.incarnation, &out.sequence, &out.resultDigest, &out.acceptedRevision,
		&out.appliedMS, &out.terminal, &out.subjectID, &out.parser, &out.build, &out.commitment,
		&out.rows, &out.bytes, &out.pins)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(out.incarnation) != 16 || len(out.resultDigest) != 32 || len(out.commitment) != 32 ||
		out.sequence <= 0 || out.appliedMS < 0 || out.terminal != 1 || out.subjectID != q.subjectID ||
		out.parser != q.parserVersion || out.build != q.buildID || out.rows <= 0 ||
		out.bytes <= 0 || out.pins < 1 {
		return out, ErrConflict
	}
	out.evidence = q.evidenceID
	return out, nil
}

func ipEntityAcceptSetSealRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out ipEntityAcceptSealFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptSetSealStmt, q.operationID, q.setID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.incarnation, &out.sequence, &out.resultDigest, &out.acceptedRevision,
		&out.appliedMS, &out.terminal, &out.evidence, &out.subjectID, &out.parser, &out.build,
		&out.commitment, &out.rows, &out.bytes, &out.pins)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(out.incarnation) != 16 || len(out.resultDigest) != 32 || len(out.commitment) != 32 ||
		out.sequence <= 0 || out.appliedMS < 0 || out.terminal != 1 || out.evidence != q.evidenceID ||
		out.subjectID != q.subjectID || out.parser != q.parserVersion || out.build != q.buildID ||
		out.rows <= 0 || out.bytes <= 0 || out.pins < 1 {
		return out, ErrConflict
	}
	out.set = q.setID
	return out, nil
}

// ipEntityAcceptSetRead proves the complete TV set header and its exact IP proof
// tuple. An incomplete set is never accepted as a source.
func ipEntityAcceptSetRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptSetStmt, q.setID)
	if err != nil {
		return out, err
	}
	var entityID, complete, coverageComplete, generation, revision int64
	var provider, subjectKind, providerID string
	var incarnation, proofIncarnation []byte
	var proofOwner sql.NullString
	var proofSequence sql.NullInt64
	err = row.Scan(&entityID, &provider, &subjectKind, &providerID, &complete, &coverageComplete,
		&incarnation, &generation, &revision, &proofOwner, &proofIncarnation, &proofSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if entityID != q.entityID || provider != q.provider || subjectKind != q.subjectKind ||
		providerID != q.providerID || complete != 1 || coverageComplete != 1 ||
		len(incarnation) != 16 || generation <= 0 ||
		revision <= 1 || !proofOwner.Valid || proofOwner.String != q.operationID ||
		len(proofIncarnation) != 16 || !proofSequence.Valid || proofSequence.Int64 <= 0 {
		return out, ErrConflict
	}
	return proofSequence.Int64, nil
}

func ipEntityAcceptBuildRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out ipEntityAcceptBuildFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptBuildStmt, q.buildID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.evidenceID, &out.ownerOperation, &out.ownerIncarnation, &out.ownerGeneration,
		&out.state, &out.phase, &out.cursor, &out.expected, &out.materialized,
		&out.contentRevision, &out.generation, &out.subjectID, &out.provider, &out.subjectKind,
		&out.providerID, &out.parser, &out.language, &out.region, &out.requestShape,
		&out.observedAt, &out.digest)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.evidenceID != q.evidenceID || out.ownerOperation != q.operationID ||
		len(out.ownerIncarnation) != 16 || out.ownerGeneration != q.generation ||
		out.state != "ready" || out.phase != "sealed" || out.cursor <= 0 ||
		out.cursor != out.expected || out.cursor != out.materialized ||
		out.contentRevision != q.contentRevision || out.generation != q.buildGeneration ||
		out.subjectID != q.subjectID || out.provider != q.provider ||
		out.subjectKind != q.subjectKind || out.providerID != q.providerID ||
		out.parser != q.parserVersion || out.language != q.language || out.region != q.region ||
		out.requestShape != q.requestShape || out.observedAt != q.observedAt ||
		len(out.digest) != 64 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityAcceptSelectionRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out ipEntityAcceptSelectionFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptSelectionStmt, q.entityID, q.provider)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.incarnation, &out.revision, &out.subjectKind, &out.providerID,
		&out.status, &out.evidence, &out.set, &out.publication, &out.track, &out.observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(out.incarnation) != 16 || !ipEntityAcceptEqualBlob(out.incarnation, q.selectionIncarnation[:]) ||
		out.revision != q.selectionRevision || out.subjectKind != q.subjectKind ||
		out.providerID != q.providerID || out.status != "selected" || out.track.Valid {
		return out, ErrConflict
	}
	if out.evidence.Valid != out.publication.Valid || out.set.Valid && !out.evidence.Valid {
		return out, ErrResolveRequired
	}
	return out, nil
}

func ipEntityAcceptSearchRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptSearchStmt, q.searchJobID, q.entityID)
	if err != nil {
		return err
	}
	var id, entityID, generation int64
	var phase string
	if err = row.Scan(&id, &entityID, &generation, &phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
		}
		return err
	}
	if id != q.searchJobID || entityID != q.entityID || generation <= 0 || len(phase) == 0 {
		return ErrResolveRequired
	}
	return nil
}

// ipEntityAcceptChildren proves the typed cohort is the COMPLETE sealed evidence
// cohort: exact counts and every row. Missing, extra or foreign children fail
// before the first write.
func ipEntityAcceptChildren(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	counts := []struct {
		id   stmt.ID
		want int
	}{
		{ipEntityAcceptExternalCountStmt, len(q.external)},
		{ipEntityAcceptTermCountStmt, len(q.terms)},
		{ipEntityAcceptCreditCountStmt, len(q.credits)},
	}
	for _, count := range counts {
		got, err := wc008DraftInteger(ctx, turn, count.id, q.evidenceID)
		if err != nil {
			return err
		}
		if got != int64(count.want) {
			return ErrConflict
		}
	}
	for _, row := range q.external {
		if err := ipEntityAcceptHasOne(ctx, turn, ipEntityAcceptExternalPointStmt,
			q.evidenceID, row.Provider, row.SubjectKind, row.ProviderID); err != nil {
			return err
		}
	}
	for _, row := range q.terms {
		if err := ipEntityAcceptHasOne(ctx, turn, ipEntityAcceptTermPointStmt,
			q.evidenceID, row.Vocabulary, row.Ordinal, row.Value); err != nil {
			return err
		}
	}
	for _, row := range q.credits {
		var billing, sourceOrdinal any
		if row.BillingPresent {
			billing = row.Billing
		}
		if row.SourceOrdinalPresent {
			sourceOrdinal = row.SourceOrdinal
		}
		if err := ipEntityAcceptHasOne(ctx, turn, ipEntityAcceptCreditPointStmt,
			q.evidenceID, row.Scope, row.Ordinal, row.Class, row.ProviderPersonID,
			row.CreditID, row.Name, row.Role, row.Department, billing, sourceOrdinal); err != nil {
			return err
		}
	}
	return nil
}

func ipEntityAcceptHasOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}
	if one != 1 {
		return ErrConflict
	}
	return nil
}

func ipEntityAcceptProjectionRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest, owner ipEntityJobOwner) (out ipEntityAcceptProjectionFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptEntityStmt, q.entityID, owner.entityIncarnation[:])
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.title, &out.sortTitle, &out.language, &out.overview, &out.originalTitle,
		&out.tagline, &out.releaseDate, &out.contentRating, &out.studio, &out.network,
		&out.country, &out.year, &out.revision, &out.incarnation)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.revision <= 0 || len(out.incarnation) != 16 ||
		!ipEntityAcceptEqualBlob(out.incarnation, owner.entityIncarnation[:]) ||
		!ipEntityAcceptText(out.title, ipEntityAcceptFieldMax) {
		return out, ErrConflict
	}
	return out, nil
}

// ipEntityAcceptOwnerField translates one accepted descriptive field name to
// the descriptive editor field metadata_owner_fields stores (IP-24/IP-25). The
// catalogue overview is the editor's 'description' and the accepted metadata
// language is 'metadataLanguage'; the remaining names already agree. The
// provenance-only names (status, sortName, disambiguation, type, isbn,
// pageCount) have no owner row at all: the accept bodies only UPDATE rows the
// lock command can create, and the lock command refuses those names, so they are
// passed through unchanged and their automatic observation is never stored in
// metadata_owner_fields.
func ipEntityAcceptOwnerField(name string) string {
	switch name {
	case "overview":
		return string(fieldmeta.Description)
	case "language":
		return string(fieldmeta.MetadataLanguage)
	}
	return name
}

// ipEntityAcceptOwnerFacts is one durable owner row. The catalogue projection
// carries the EFFECTIVE value; under a lock the provider observation is
// retained only here as automatic_value.
type ipEntityAcceptOwnerFacts struct {
	value, automatic                       string
	valuePresent, automaticPresent, locked int64
}

func ipEntityAcceptOwnerFlag(value int64) bool { return value == 0 || value == 1 }

// ipEntityAcceptOwnerFieldsRead reads the durable owner rows for the requested
// descriptive fields, keyed by the ACCEPT field name. A missing row is absence,
// not an error: only the lock command creates one.
func ipEntityAcceptOwnerFieldsRead(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest) (out map[string]ipEntityAcceptOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	kind := ipEntityAcceptOwnerKind(q.subjectKind)
	if kind == "" {
		return nil, ErrInvalid
	}
	out = make(map[string]ipEntityAcceptOwnerFacts, len(q.fields))
	for _, field := range q.fields {
		if _, ok := out[field.Name]; ok {
			continue
		}
		row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptOwnerFieldReadStmt,
			kind, q.entityID, ipEntityAcceptOwnerField(field.Name))
		if err != nil {
			return nil, err
		}
		var facts ipEntityAcceptOwnerFacts
		err = row.Scan(&facts.value, &facts.valuePresent, &facts.automatic,
			&facts.automaticPresent, &facts.locked)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !ipEntityAcceptOwnerFlag(facts.valuePresent) ||
			!ipEntityAcceptOwnerFlag(facts.automaticPresent) ||
			!ipEntityAcceptOwnerFlag(facts.locked) ||
			facts.locked == 1 && facts.valuePresent != 1 {
			return nil, ErrResolveRequired
		}
		out[field.Name] = facts
	}
	return out, nil
}

// ipEntityAcceptProjection applies the typed automatic values to the current
// projection. An absent observation writes nothing. A locked owner field keeps
// its effective catalogue value exactly as the descriptive lock defines it
// (including a locked explicit empty); the provider value is retained only as
// the automatic observation by ipEntityAcceptOwnerFields.
func ipEntityAcceptProjection(q ipEntityAcceptRequest, before ipEntityAcceptProjectionFacts,
	owners map[string]ipEntityAcceptOwnerFacts) (out ipEntityAcceptProjectionFacts, err error) {
	out = before
	for _, field := range q.fields {
		if !field.Present || owners[field.Name].locked == 1 {
			continue
		}
		switch field.Name {
		case "title":
			out.title = field.Value
		case "sortTitle":
			out.sortTitle = field.Value
		case "language":
			out.language = field.Value
		case "overview":
			out.overview = field.Value
		case "originalTitle":
			out.originalTitle = field.Value
		case "tagline":
			out.tagline = field.Value
		case "releaseDate":
			out.releaseDate = field.Value
		case "contentRating":
			out.contentRating = field.Value
		case "studio":
			out.studio = field.Value
		case "network":
			out.network = field.Value
		case "country":
			out.country = field.Value
		case "year":
			year, e := strconv.ParseInt(field.Value, 10, 64)
			if e != nil || year < 0 || year > 9999 {
				return out, ErrInvalid
			}
			out.year = year
		}
	}
	if owners["title"].locked != 1 &&
		(!ipEntityAcceptText(out.title, ipEntityAcceptFieldMax) || len(out.title) == 0) {
		return out, ErrInvalid
	}
	if owners["language"].locked != 1 &&
		(!ipEntityAcceptText(out.language, 64) || len(out.language) == 0) {
		return out, ErrInvalid
	}
	return out, nil
}

func ipEntityAcceptSortKeys(q ipEntityAcceptProjectionFacts) (key, head, keepKey, keepHead string, err error) {
	projection, err := ProjectFilenameMetadata(q.title, q.sortTitle, q.language)
	if err != nil {
		return "", "", "", "", ErrInvalid
	}
	return projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles, nil
}

func ipEntityAcceptTermKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ipEntityAcceptNullable(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func ipEntityAcceptPriorProvider(ctx context.Context, turn *commandTurn, publication string) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityAcceptPriorStmt, publication)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if len(out) == 0 || len(out) > ipEvidenceIDMax {
		return out, ErrResolveRequired
	}
	return out, nil
}

func execIPEntityAccept(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, err := sealStep(turn, id)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, args...)
	if err != nil {
		return fmt.Errorf("stmt %s: %w", id, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ipEntityAcceptFresh is the ONE write path. Every native proof is complete
// before the first write; any later failure rolls back the whole acceptance.
func ipEntityAcceptFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipEntityAcceptRequest,
	request RequestDigest, owner ipEntityJobOwner, appliedMS int64) (out ipEntityAcceptResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	evidence, err := ipEntityAcceptEvidenceRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	seal, err := ipEntityAcceptEvidenceSealRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	retained := seal
	if evidence.sealSequence != seal.sequence || evidence.sealOwner != q.operationID ||
		!ipEntityAcceptEqualBlob(evidence.sealIncarnation, seal.incarnation) {
		return out, ErrConflict
	}
	if q.setID != "" {
		proofSequence, err := ipEntityAcceptSetRead(ctx, turn, q)
		if err != nil {
			return out, err
		}
		setSeal, err := ipEntityAcceptSetSealRead(ctx, turn, q)
		if err != nil {
			return out, err
		}
		if !ipEntityAcceptEqualBlob(setSeal.incarnation, seal.incarnation) ||
			setSeal.evidence != q.evidenceID || setSeal.sequence != proofSequence {
			return out, ErrConflict
		}
		retained = setSeal
	}
	build, err := ipEntityAcceptBuildRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if !ipEntityAcceptEqualBlob(build.ownerIncarnation, seal.incarnation) ||
		build.ownerGeneration != q.generation {
		return out, ErrConflict
	}
	selection, err := ipEntityAcceptSelectionRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if err = ipEntityAcceptSearchRead(ctx, turn, q); err != nil {
		return out, err
	}
	if err = ipEntityAcceptChildren(ctx, turn, q); err != nil {
		return out, err
	}
	before, err := ipEntityAcceptProjectionRead(ctx, turn, q, owner)
	if err != nil {
		return out, err
	}
	owners, err := ipEntityAcceptOwnerFieldsRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	after, err := ipEntityAcceptProjection(q, before, owners)
	if err != nil {
		return out, err
	}
	sortKey, sortHead, keepKey, keepHead, err := ipEntityAcceptSortKeys(after)
	if err != nil {
		return out, err
	}
	previousProvider := ""
	if selection.publication.Valid {
		if previousProvider, err = ipEntityAcceptPriorProvider(ctx, turn, selection.publication.String); err != nil {
			return out, err
		}
	}
	var previousID any
	if selection.publication.Valid {
		previousID = selection.publication.String
	}
	if err = execIPEntityAccept(ctx, turn, ipEntityAcceptPublicationStmt,
		ipEntityAcceptNullable(sql.NullString{String: q.provider, Valid: selection.publication.Valid}),
		ipEntityAcceptNullable(sql.NullString{String: q.subjectKind, Valid: selection.publication.Valid}),
		ipEntityAcceptNullable(sql.NullString{String: previousProvider, Valid: selection.publication.Valid}),
		q.publicationID, q.operationID, q.entityID, q.provider, q.subjectKind, q.providerID,
		q.evidenceID, ipEntityAcceptNullable(sql.NullString{String: q.setID, Valid: q.setID != ""}),
		previousID, q.selectionRevision, q.generation, q.observedAt,
		q.operationID, seal.incarnation, seal.sequence); err != nil {
		return out, err
	}
	if _, err = ipEntityAcceptSelectionAdvance(ctx, turn, q, selection); err != nil {
		return out, err
	}
	for _, row := range q.external {
		if err = ipEntityExternalRegister(ctx, turn, q.entityID, row); err != nil {
			return out, err
		}
	}
	for _, row := range q.terms {
		if err = ipEntityAcceptWriteTerm(ctx, turn, q, row); err != nil {
			return out, err
		}
	}
	revision, err := ipEntityAcceptProjectionWrite(ctx, turn, q, before, after, sortKey, sortHead, keepKey, keepHead)
	if err != nil {
		return out, err
	}
	if revision <= before.revision {
		return out, ErrConflict
	}
	if err = ipEntityAcceptOwnerFields(ctx, turn, q, owners); err != nil {
		return out, err
	}
	if err = ipEntityAcceptOrigins(ctx, turn, q, evidence); err != nil {
		return out, err
	}
	kind := "evidence_seal"
	if q.setID != "" {
		kind = "set_seal"
	}
	if err = execIPEntityAccept(ctx, turn, ipEntityAcceptRetainProofStmt,
		q.operationID, retained.incarnation, retained.sequence, kind, q.evidenceID); err != nil {
		return out, err
	}
	value := ipEntityAcceptResult{
		OperationID: q.operationID, PublicationID: q.publicationID, EvidenceID: q.evidenceID,
		SetID: q.setID, Provider: q.provider, SubjectKind: q.subjectKind, ProviderID: q.providerID,
		ParserVersion: q.parserVersion, ObservedAt: q.observedAt, Lease: q.lease,
		EntityID: q.entityID, OwnerGeneration: q.generation, ContentRevision: q.contentRevision,
		AcceptedRevision: revision, SelectionRevision: q.selectionRevision,
		SearchJobID: q.searchJobID, FieldCount: int64(len(q.fields)),
		ExternalCount: int64(len(q.external)), TermCount: int64(len(q.terms)),
		CreditCount: int64(len(q.credits)), LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS,
	}
	digest, err := ipEntityAcceptResultDigest(identity, request, value)
	if err != nil {
		return out, err
	}
	if err = execIPEntityAccept(ctx, turn, ipEntityAcceptRecordStmt,
		q.operationID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		revision, appliedMS, digest[:], int64(len(q.fields)), ipEntityAcceptFieldBytes(q.fields),
		q.evidenceID, q.subjectID, q.parserVersion, q.buildID,
		ipEntityAcceptNullable(sql.NullString{String: q.setID, Valid: q.setID != ""}),
		q.publicationID, q.entityID, q.provider, q.subjectKind, q.providerID); err != nil {
		return out, err
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, q.operationID, q.provider, q.lease,
		q.generation, q.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

func ipEntityAcceptSelectionAdvance(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest,
	selection ipEntityAcceptSelectionFacts) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := turn.queryRow(ctx, turn.steps[ipEntityAcceptSelectionCASStmt].statementStep,
		q.evidenceID, ipEntityAcceptNullable(sql.NullString{String: q.setID, Valid: q.setID != ""}),
		q.publicationID, q.observedAt, q.entityID, q.provider, q.selectionIncarnation[:],
		q.selectionRevision, q.subjectKind, q.providerID, ipEntityAcceptNullable(selection.evidence),
		ipEntityAcceptNullable(selection.set), ipEntityAcceptNullable(selection.publication))
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = row.Scan(&out); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out <= 0 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityAcceptWriteTerm(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest, row ipEntityAcceptTerm) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	key := ipEntityAcceptTermKey(row.Value)
	if len(key) == 0 {
		return ErrInvalid
	}
	term, err := turn.queryRow(ctx, turn.steps[ipEntityAcceptTermStmt].statementStep, int64(1), key, row.Value)
	if err != nil {
		return err
	}
	var termID int64
	if err = term.Scan(&termID); err != nil || termID <= 0 {
		return errors.Join(ErrResolveRequired, err)
	}
	step, err := sealStep(turn, ipEntityAcceptEntityTermStmt)
	if err != nil {
		return err
	}
	if _, err = turn.exec(ctx, step, q.entityID, termID); err != nil {
		return err
	}
	source := q.provider + ":" + row.Vocabulary + ":" + key
	if !ipEntityAcceptText(source, 1024) {
		return ErrInvalid
	}
	step, err = sealStep(turn, ipEntityAcceptTermSourceStmt)
	if err != nil {
		return err
	}
	_, err = turn.exec(ctx, step, q.entityID, termID, q.provider, source, row.Ordinal)
	return err
}

func ipEntityAcceptProjectionWrite(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest,
	before, after ipEntityAcceptProjectionFacts, sortKey, sortHead, keepKey, keepHead string) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := turn.queryRow(ctx, turn.steps[ipEntityAcceptEntityWriteStmt].statementStep,
		after.title, after.sortTitle, after.language, sortKey, sortHead, keepKey, keepHead,
		after.overview, after.originalTitle, after.tagline, after.releaseDate, after.contentRating,
		after.studio, after.network, after.country, after.year,
		q.entityID, before.revision, before.incarnation,
		before.title, before.sortTitle, before.language, before.overview, before.originalTitle,
		before.tagline, before.releaseDate, before.contentRating, before.studio, before.network,
		before.country, before.year)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = row.Scan(&out); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out != before.revision+1 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityAcceptOwnerFields(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest,
	owners map[string]ipEntityAcceptOwnerFacts) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	kind := ipEntityAcceptOwnerKind(q.subjectKind)
	if kind == "" {
		return ErrInvalid
	}
	for _, field := range q.fields {
		if !field.Present {
			continue
		}
		// A locked row keeps its effective value and lock; only a row read as
		// unlocked follows the source. A missing row is absence, so either
		// statement is a no-op and the catalogue projection is the only record.
		id := ipEntityAcceptOwnerFieldStmt
		args := []any{field.Value, int64(1), kind, q.entityID, ipEntityAcceptOwnerField(field.Name)}
		if facts, ok := owners[field.Name]; ok && facts.locked == 0 {
			id = ipEntityAcceptOwnerUnlockedStmt
			args = []any{field.Value, int64(1), field.Value, int64(1), kind, q.entityID,
				ipEntityAcceptOwnerField(field.Name)}
		}
		step, err := sealStep(turn, id)
		if err != nil {
			return err
		}
		if _, err = turn.exec(ctx, step, args...); err != nil {
			return err
		}
	}
	return nil
}

func ipEntityAcceptOrigins(ctx context.Context, turn *commandTurn, q ipEntityAcceptRequest, evidence ipEntityAcceptEvidenceFacts) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	for _, field := range q.fields {
		if !field.Present {
			continue
		}
		step, err := sealStep(turn, ipEntityAcceptOriginStmt)
		if err != nil {
			return err
		}
		if _, err = turn.exec(ctx, step, q.subjectKind, q.providerID, q.entityID, field.Name,
			q.provider, "provider", evidence.sourceURL, 1.0, q.observedAt, q.language, q.region,
			q.selectionRevision, q.publicationID, q.evidenceID); err != nil {
			return err
		}
	}
	return nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved acceptance with zero new effects, before any fresh probe.
func applyIPEntityAccept(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityAcceptCommand) (out ipEntityAcceptCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityAcceptCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPEntityAccept {
		return zero, ErrInvalid
	}
	q := c.request
	if ipEntityAcceptShape(q) != nil {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPEntityAccept, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEntityAcceptLookupReceipt(ctx, turn, c.reference(), q)
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
	value, err := ipEntityAcceptFresh(ctx, turn, c.identity, q, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityAcceptLookupReceipt(ctx, turn, c.reference(), q)
	if err != nil || saved.decision != receiptReplay || saved.value != value || saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
