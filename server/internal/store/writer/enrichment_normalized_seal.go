package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	NormalizedEvidenceKind      = "evidence_seal"
	NormalizedSourceKind        = "credit_source_seal"
	NormalizedEvidenceAlgorithm = "portico.normalized-evidence.sha256.v1"
	NormalizedSourceAlgorithm   = "portico.normalized-credit-source.sha256.v1"
	MaxSealControlStringBytes   = 64 << 10
	maxNormalizedEvidenceBytes  = 6 << 20
	normalizedMovieParser       = "tmdb.movie.header-selected-credits.v1"
	normalizedMovieShape        = "tmdb.movie.detail+credits.header-selected-credits.v1"
)

type NormalizedSealSelection struct {
	Kind, Algorithm string
	Version         int64
}

// NormalizedSealSummary is content only. It supplies no receipt, dependency,
// capability, verified native snapshot or permission to stage/seal anything.
type NormalizedSealSummary struct {
	VerifiedRows, CanonicalBytes int64
	Commitment                   [32]byte
}

// CheckSealControlStrings counts EVERY retained non-content string occurrence,
// including repeated IDs. The verifier supplies its entire prospective owner,
// capture, pin and receipt closure, not merely the smaller normalized stream.
// It never deduplicates, changes a budget or refunds retained bytes.
func CheckSealControlStrings(occurrences []string) (uint64, error) {
	var n uint64
	for _, s := range occurrences {
		if uint64(len(s)) > MaxSealControlStringBytes-n {
			return 0, &PolicyError{Field: "seal_control_strings", Limit: MaxSealControlStringBytes, Cause: ErrCap}
		}
		n += uint64(len(s))
	}
	for _, s := range occurrences {
		if !normalizedText(s) {
			return 0, invalid("seal_control_text")
		}
	}
	return n, nil
}

// NormalizedEvidenceSeal implements the ONE v1 normalized evidence codec.
// Complete native text/size validation precedes hashing or derived allocation.
// Actual excluded-child absence, cursor/buildR, prospective proof-reference
// bytes, Q and same-grant memory custody remain the writer verifier's job.
func NormalizedEvidenceSeal(selection NormalizedSealSelection, in NormalizedEvidenceInput) (NormalizedSealSummary, error) {
	return normalizedEvidenceSeal(selection, in, nil)
}

func VerifyNormalizedEvidenceSeal(selection NormalizedSealSelection, in NormalizedEvidenceInput, expected NormalizedSealSummary) (NormalizedSealSummary, error) {
	return normalizedEvidenceSeal(selection, in, &expected)
}

func normalizedEvidenceSeal(selection NormalizedSealSelection, in NormalizedEvidenceInput, expected *NormalizedSealSummary) (NormalizedSealSummary, error) {
	if err := checkNormalizedSelection(selection, NormalizedEvidenceKind); err != nil {
		return NormalizedSealSummary{}, err
	}
	if in.Credits == nil || len(in.Credits) > MaxCastCredits+MaxCrewCredits {
		return NormalizedSealSummary{}, invalid("seal_evidence_cohort")
	}
	walk := func(e *normalizedEncoder) { encodeNormalizedEvidence(e, selection, in) }
	pre := normalizedEncoder{}
	walk(&pre)
	if pre.err != nil {
		return NormalizedSealSummary{}, pre.err
	}
	if err := CheckEvidenceSealContentBytes(pre.contentBytes, 0); err != nil {
		return NormalizedSealSummary{}, err
	}
	if err := checkNormalizedEvidence(in); err != nil {
		return NormalizedSealSummary{}, err
	}
	return finishNormalizedSeal(walk, pre, int64(1+len(in.Credits)), expected)
}

// CheckEvidenceSealContentBytes includes the actual prospective native proof
// reference bytes inside the SAME 6 MiB content envelope. Those references do
// not recursively enter the normalized stream. Q is accounted separately once.
func CheckEvidenceSealContentBytes(contentBytes, proofReferenceBytes uint64) error {
	if contentBytes > maxNormalizedEvidenceBytes || proofReferenceBytes > maxNormalizedEvidenceBytes-contentBytes {
		return &PolicyError{Field: "seal_evidence_content", Limit: maxNormalizedEvidenceBytes, Cause: ErrCap}
	}
	return nil
}

// NormalizedSourceSeal encodes a complete source snapshot plus its exact
// historical E context. It never resolves a proof, parses an opaque reference,
// infers people provenance or equates the E origin entity with the S owner.
func NormalizedSourceSeal(selection NormalizedSealSelection, in NormalizedSourceInput) (NormalizedSealSummary, error) {
	return normalizedSourceSeal(selection, in, nil)
}

func VerifyNormalizedSourceSeal(selection NormalizedSealSelection, in NormalizedSourceInput, expected NormalizedSealSummary) (NormalizedSealSummary, error) {
	return normalizedSourceSeal(selection, in, &expected)
}

func normalizedSourceSeal(selection NormalizedSealSelection, in NormalizedSourceInput, expected *NormalizedSealSummary) (NormalizedSealSummary, error) {
	if err := checkNormalizedSelection(selection, NormalizedSourceKind); err != nil {
		return NormalizedSealSummary{}, err
	}
	if in.Members == nil || len(in.Members) > MaxCastCredits+MaxCrewCredits {
		return NormalizedSealSummary{}, invalid("seal_source_cohort")
	}
	walk := func(e *normalizedEncoder) { encodeNormalizedSource(e, selection, in) }
	pre := normalizedEncoder{}
	walk(&pre)
	if pre.err != nil {
		return NormalizedSealSummary{}, pre.err
	}
	// Intent fields are operational, excluded from C, but still validated and
	// bounded before any derived policy allocation. Full closure census remains
	// mandatory at the writer boundary.
	i := in.Intent
	if _, err := CheckSealControlStrings([]string{i.ProviderOperationID, i.SourceEvidenceOwner, i.SourceEvidenceID, i.SourceParserVersion, i.SourcePublicationID, i.Provider, i.SourceAlgorithm}); err != nil {
		return NormalizedSealSummary{}, err
	}
	if err := checkNormalizedSource(in); err != nil {
		return NormalizedSealSummary{}, err
	}
	return finishNormalizedSeal(walk, pre, int64(len(in.Members)), expected)
}

func checkNormalizedSelection(s NormalizedSealSelection, kind string) error {
	algorithm := NormalizedEvidenceAlgorithm
	if kind == NormalizedSourceKind {
		algorithm = NormalizedSourceAlgorithm
	}
	if s.Kind != kind || s.Version != 1 || s.Algorithm != algorithm {
		return invalid("seal_algorithm")
	}
	return nil
}

func finishNormalizedSeal(walk func(*normalizedEncoder), pre normalizedEncoder, rows int64, expected *NormalizedSealSummary) (NormalizedSealSummary, error) {
	if expected != nil && (expected.VerifiedRows != rows || expected.CanonicalBytes != int64(pre.n)) {
		return NormalizedSealSummary{}, invalid("seal_declared_shape")
	}
	h := sha256.New()
	e := normalizedEncoder{h: h}
	walk(&e)
	if e.err != nil {
		return NormalizedSealSummary{}, e.err
	}
	if e.n != pre.n {
		return NormalizedSealSummary{}, invalid("seal_input_changed")
	}
	out := NormalizedSealSummary{VerifiedRows: rows, CanonicalBytes: int64(e.n)}
	copy(out.Commitment[:], h.Sum(nil))
	if expected != nil && *expected != out {
		return NormalizedSealSummary{}, invalid("seal_commitment")
	}
	return out, nil
}

func normalizedText(s string) bool { return utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 }
func nonblank(s string) bool       { return strings.TrimSpace(s) != "" }
func normalizedHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range s {
		if (s[i] < '0' || s[i] > '9') && (s[i] < 'a' || s[i] > 'f') {
			return false
		}
	}
	return true
}

func normalizedNamespace(parser, provider, kind, providerID, shape string) bool {
	id, err := strconv.ParseInt(providerID, 10, 64)
	return parser == normalizedMovieParser && provider == "tmdb" && kind == "movie" && shape == normalizedMovieShape && err == nil && id > 0 && strconv.FormatInt(id, 10) == providerID
}

func checkNormalizedEvidence(in NormalizedEvidenceInput) error {
	h := in.Header
	if !nonblank(h.ID) || h.SubjectID <= 0 || !normalizedNamespace(h.ParserVersion, h.Provider, h.SubjectKind, h.ProviderID, h.RequestShape) || !normalizedHex(h.Digest, 64) || !nonblank(h.Title) || h.SourceURL != "https://api.themoviedb.org/3/movie/"+h.ProviderID {
		return invalid("seal_evidence_header")
	}
	if _, err := time.Parse(time.RFC3339Nano, h.ObservedAt); err != nil {
		return invalid("seal_observed_at")
	}
	if h.ReleaseDate == "" {
		if h.Year != 0 {
			return invalid("seal_year")
		}
	} else {
		date, err := time.Parse("2006-01-02", h.ReleaseDate)
		if err != nil || h.Year != int64(date.Year()) {
			return invalid("seal_year")
		}
	}
	if h.LengthMillis.Present && h.LengthMillis.Value < 0 {
		return invalid("seal_runtime")
	}
	for _, s := range []string{h.ContentRating, h.Studio, h.Network, h.Country, h.Artist, h.WikidataID, h.Biography, h.BiographySource, h.BiographySourceURL, h.Disambiguation, h.ReleaseGroupID, h.GroupTitle, h.PrimaryType, h.Barcode} {
		if s != "" {
			return invalid("seal_excluded_family")
		}
	}
	if h.ArtistType.Present || h.BeginYear.Present || h.EndYear.Present {
		return invalid("seal_excluded_family")
	}
	var candidates [MaxCastCredits + MaxCrewCredits]ScreenCreditCandidate
	for j, c := range in.Credits {
		if c.Provider != h.Provider || c.EvidenceKind != h.SubjectKind || c.EvidenceID != h.ID || c.Ordinal != int64(j) || c.Scope != "" || c.ArtistName != "" || c.SortName != "" || c.Disambiguation != "" || c.JoinPhrase != "" || !nonblank(c.ProviderPersonID) || !nonblank(c.Name) || !c.SourceOrdinal.Present || c.SourceOrdinal.Value < 0 {
			return invalid("seal_credit_shape")
		}
		for _, field := range []struct {
			value string
			limit int
		}{
			{c.ProviderPersonID, 1024}, {c.CreditID, 1024}, {c.Name, 1200}, {c.Role, 2000}, {c.Department, 800},
		} {
			if len(field.value) > field.limit {
				return &PolicyError{Field: "seal_credit_text", Limit: field.limit, Cause: ErrCap}
			}
		}
		if err := normalizedBilling(c.CreditClass, c.Billing); err != nil {
			return err
		}
		candidates[j] = ScreenCreditCandidate{Class: CreditClass(c.CreditClass), Billing: c.Billing.Value, SourceOrdinal: c.SourceOrdinal.Value, CreditID: c.CreditID, Role: c.Role, Department: c.Department}
	}
	return ValidateRetainedCreditShape(candidates[:len(in.Credits)])
}

func normalizedBilling(class string, b EvidenceInteger) error {
	if (class == "cast" && b.Present && b.Value >= 0) || (class == "crew" && !b.Present && b.Value == 0) {
		return nil
	}
	return invalid("seal_credit_billing")
}

func checkNormalizedSource(in NormalizedSourceInput) error {
	u, h, i := in.Upstream, in.Header, in.Intent
	if u.CommandKind != NormalizedEvidenceKind || u.SealVersion != 1 || u.SealAlgorithm != NormalizedEvidenceAlgorithm || !normalizedNamespace(u.SealParserVersion, u.SealProvider, u.SealSubjectKind, u.SealProviderID, u.SealRequestShape) || !normalizedHex(u.SealObservationDigest, 64) || !nonblank(u.ProviderOperationID) || !nonblank(u.SealEvidenceID) || !nonblank(u.SealBuildID) || u.CommandSequence <= 0 || u.EntityID <= 0 || u.SealSubjectID <= 0 || u.SealOwnerGeneration <= 0 || u.SealBuildGeneration <= 0 || u.SealContentRevision <= 0 || u.SealCanonicalBytes <= 0 || u.SealCanonicalBytes > maxCanonicalRequestBytes || u.SealVerifiedRows != int64(1+len(in.Members)) {
		return invalid("seal_upstream_evidence")
	}
	if !nonblank(i.ProviderOperationID) || i.SourceOwnerGeneration <= 0 || i.SourceContentRevision <= 0 || i.SourceEvidenceOwner != u.ProviderOperationID || i.SourceEvidenceIncarnation != u.CommandIncarnation || i.SourceEvidenceSequence != u.CommandSequence || i.SourceEvidenceID != u.SealEvidenceID || i.SourceSubjectID != u.SealSubjectID || i.SourceParserVersion != u.SealParserVersion {
		return invalid("seal_upstream_reference")
	}
	if i.SourceAlgorithm != NormalizedSourceAlgorithm || i.SourceVersion != 1 || i.SourceNextPosition != int64(len(in.Members)) || i.SourceExpectedMembers != int64(len(in.Members)) || h.MemberCount != int64(len(in.Members)) || i.SourcePublicationID != h.PublicationID || i.EntityID != h.EntityID || i.Provider != h.Provider || h.Provider != u.SealProvider || h.EntityID <= 0 || !normalizedHex(h.PublicationID, 32) || !nonblank(h.AcceptedRecordReference) {
		return invalid("seal_source_coverage")
	}
	if _, err := time.Parse(time.RFC3339Nano, h.ObservedAt); err != nil {
		return invalid("seal_source_observed_at")
	}
	var candidates [MaxCastCredits + MaxCrewCredits]ScreenCreditCandidate
	for j, entry := range in.Members {
		m, r := entry.Member, entry.Row
		if m.PublicationID != h.PublicationID || m.EntityID != h.EntityID || m.Provider != h.Provider || m.Position != int64(j) || m.SourceRowID != r.SourceRowID || r.EntityID != h.EntityID || r.Provider != h.Provider || !normalizedHex(r.SourceRowID, 32) || !normalizedHex(r.TupleDigest, 64) || (m.ReferenceStatus != "new" && m.ReferenceStatus != "exact" && m.ReferenceStatus != "ambiguous") {
			return invalid("seal_source_member")
		}
		for k := 0; k < j; k++ {
			if in.Members[k].Row.SourceRowID == r.SourceRowID {
				return invalid("seal_source_duplicate")
			}
		}
		for _, p := range []struct {
			s   string
			max int
		}{
			{r.OriginalRecordReference, 4096}, {r.OriginalCreditID, 1024}, {r.OriginalProviderPersonID, 1024}, {r.OriginalPersonIdentityKey, 512}, {r.OriginalPersonToken, 256}, {r.OriginalTargetKind, 64}, {r.OriginalTargetID, 256}, {r.OriginalCreditedName, 1200}, {r.OriginalRole, 2000}, {r.OriginalDepartment, 800},
		} {
			if len(p.s) > p.max {
				return &PolicyError{Field: "seal_source_tuple", Limit: p.max, Cause: ErrCap}
			}
		}
		if !nonblank(r.OriginalRecordReference) || !nonblank(r.OriginalProviderPersonID) || !nonblank(r.OriginalPersonIdentityKey) || !nonblank(r.OriginalPersonToken) || r.OriginalTargetKind != "person" || !nonblank(r.OriginalTargetID) || !nonblank(r.OriginalCreditedName) || r.OriginalSourceOrdinal < 0 {
			return invalid("seal_source_tuple")
		}
		if err := normalizedBilling(r.OriginalCreditClass, r.OriginalBilling); err != nil {
			return err
		}
		candidates[j] = ScreenCreditCandidate{Class: CreditClass(r.OriginalCreditClass), Billing: r.OriginalBilling.Value, SourceOrdinal: r.OriginalSourceOrdinal, CreditID: r.OriginalCreditID, Role: r.OriginalRole, Department: r.OriginalDepartment}
	}
	return ValidateRetainedCreditShape(candidates[:len(in.Members)])
}

// A nil sink is the complete bounded validation/measurement pass. The same
// explicit field walker writes C; there is no second production byte codec,
// reflection, generic tree, retained canonical buffer or JSON conversion.
type normalizedEncoder struct {
	h               io.Writer
	n, contentBytes uint64
	err             error
}

func (e *normalizedEncoder) raw(p []byte) {
	if e.err != nil {
		return
	}
	if uint64(len(p)) > maxCanonicalRequestBytes-e.n {
		e.err = invalid("seal_request_bytes")
		return
	}
	e.n += uint64(len(p))
	if e.h != nil {
		n, err := e.h.Write(p)
		e.err = err
		if err == nil && n != len(p) {
			e.err = io.ErrShortWrite
		}
	}
}
func (e *normalizedEncoder) number(n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	e.raw(b[:])
}
func (e *normalizedEncoder) unsigned(n uint64) {
	if n > math.MaxInt64 {
		e.err = invalid("seal_unsigned")
		return
	}
	e.number(n)
}
func (e *normalizedEncoder) integer(n int64) { e.number(uint64(n)) }
func (e *normalizedEncoder) text(s string) {
	if e.err != nil {
		return
	}
	if uint64(len(s))+8 > maxCanonicalRequestBytes-e.n {
		e.err = invalid("seal_request_bytes")
		return
	}
	if !normalizedText(s) {
		e.err = invalid("seal_text")
		return
	}
	e.unsigned(uint64(len(s)))
	e.contentBytes += uint64(len(s))
	if e.h == nil {
		e.n += uint64(len(s))
		return
	}
	if h, ok := e.h.(hash.Hash); ok {
		_, e.err = io.WriteString(h, s)
		e.n += uint64(len(s))
		return
	}
	n, err := io.WriteString(e.h, s)
	e.err = err
	e.n += uint64(n)
	if err == nil && n != len(s) {
		e.err = io.ErrShortWrite
	}
}
func (e *normalizedEncoder) nullableInteger(v EvidenceInteger) {
	if !v.Present {
		if v.Value != 0 {
			e.err = invalid("seal_inactive_null")
		}
		e.raw([]byte{0})
		return
	}
	e.raw([]byte{1})
	e.integer(v.Value)
}
func (e *normalizedEncoder) nullableText(v EvidenceText) {
	if !v.Present {
		if v.Value != "" {
			e.err = invalid("seal_inactive_null")
		}
		e.raw([]byte{0})
		return
	}
	e.raw([]byte{1})
	e.text(v.Value)
}
func (e *normalizedEncoder) blob16(v [16]byte) { e.raw(v[:]) }
func (e *normalizedEncoder) blob32(v [32]byte) { e.raw(v[:]) }
func (e *normalizedEncoder) record(tag string, fields uint64, body func(*normalizedEncoder)) {
	if e.err != nil {
		return
	}
	measure := normalizedEncoder{}
	body(&measure)
	if measure.err != nil {
		e.err = measure.err
		return
	}
	// Record tags aren't persisted native content; only payload strings are.
	before := e.contentBytes
	e.text(tag)
	e.contentBytes = before
	e.unsigned(fields)
	e.unsigned(measure.n)
	body(e)
}
func normalizedPrefix(e *normalizedEncoder, s NormalizedSealSelection, n uint64) {
	e.text("portico.normalized-seal")
	e.unsigned(1)
	e.text(s.Kind)
	e.text(s.Algorithm)
	e.unsigned(n)
	e.contentBytes = 0
}
func encodeNormalizedEvidence(e *normalizedEncoder, s NormalizedSealSelection, in NormalizedEvidenceInput) {
	normalizedPrefix(e, s, uint64(1+len(in.Credits)))
	e.record("evidence.header.v1", 38, func(e *normalizedEncoder) { encodeNormalizedEvidenceHeader(e, in.Header) })
	for _, c := range in.Credits {
		e.record("evidence.credit.v1", 17, func(e *normalizedEncoder) { encodeNormalizedEvidenceCredit(e, c) })
	}
}
func encodeNormalizedSource(e *normalizedEncoder, s NormalizedSealSelection, in NormalizedSourceInput) {
	normalizedPrefix(e, s, uint64(2+len(in.Members)))
	e.record("source.upstream-evidence.v1", 26, func(e *normalizedEncoder) { encodeNormalizedUpstreamEvidence(e, in.Upstream) })
	e.record("source.header.v1", 6, func(e *normalizedEncoder) { encodeNormalizedSourceHeader(e, in.Header) })
	for _, v := range in.Members {
		e.record("source.member.v1", 23, func(e *normalizedEncoder) {
			encodeNormalizedSourceMember(e, v.Member)
			encodeNormalizedSourceRow(e, v.Row)
		})
	}
}
