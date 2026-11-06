package writer

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	normalizedEvidenceAlgorithm = NormalizedEvidenceAlgorithm
	normalizedSourceAlgorithm   = NormalizedSourceAlgorithm
)

// W owns the sole normalized implementation and limits. These EN projections
// are inert data, not another encoder, verified snapshot or authority callback.
type normalizedSealCommitment [32]byte // neither RequestDigest nor ResultDigest
type normalizedSealManifest struct {
	algorithm            string
	version, rows, bytes int64
	commitment           normalizedSealCommitment
}

// Exact immutable26-field upstream projection in approved f39cdeae. This is
// inert original native DATA, never a verified-reference capability.
type normalizedEvidenceReceipt struct {
	ownerID                                                                  string
	commandKind                                                              string
	identity                                                                 CommandIdentity
	request                                                                  RequestDigest
	entityID, version                                                        int64
	evidenceID                                                               string
	subjectID                                                                int64
	parser, provider, kind, providerID, language, region, shape, observation string
	ownerGeneration                                                          int64
	buildID                                                                  string
	buildGeneration, revision                                                int64
	manifest                                                                 normalizedSealManifest
}
type normalizedSourceHeader struct {
	id                         string
	entityID                   int64
	provider, record, observed string
	count                      int64
}
type normalizedSourceRow struct {
	id                                                                                                                                string
	entityID                                                                                                                          int64
	provider, tupleDigest, record, creditID, providerPersonID, identityKey, personToken, targetKind, targetID, name, role, department string
	class                                                                                                                             CreditClass
	rawOrdinal                                                                                                                        int64
	billing                                                                                                                           EvidenceInteger
}
type normalizedSourceMember struct {
	publicationID          string
	entityID               int64
	provider               string
	position               int64
	sourceRowID, reference string
	row                    normalizedSourceRow
}
type normalizedSourceContent struct {
	upstream normalizedEvidenceReceipt
	header   normalizedSourceHeader
	members  []normalizedSourceMember
	intent   NormalizedSourceIntent // CURRENT operation facts, never inferred from historical E
}

// Data preparation cannot establish native closure/provenance/current authority.
// W independently verifies those facts before entering a seal effect turn.
func prepareEvidenceSealManifest(ctx context.Context, p EvidenceStagePayload, controls enrichmentControlCapture) (normalizedSealManifest, error) {
	if ctx == nil {
		return normalizedSealManifest{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return normalizedSealManifest{}, err
	}
	if _, err := preflightEnrichmentControls(controls); err != nil {
		return normalizedSealManifest{}, err
	}
	if !p.valid || len(p.credits) > MaxCastCredits+MaxCrewCredits {
		return normalizedSealManifest{}, ErrInvalid
	}
	input := projectNormalizedEvidence(p)
	summary, err := NormalizedEvidenceSeal(NormalizedSealSelection{NormalizedEvidenceKind, NormalizedEvidenceAlgorithm, 1}, input)
	if err != nil {
		return normalizedSealManifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return normalizedSealManifest{}, err
	}
	return normalizedSealManifest{NormalizedEvidenceAlgorithm, 1, summary.VerifiedRows, summary.CanonicalBytes, normalizedSealCommitment(summary.Commitment)}, nil
}

func prepareSourceSealManifest(ctx context.Context, input normalizedSourceContent, controls enrichmentControlCapture) (normalizedSourceContent, normalizedSealManifest, error) {
	var zero normalizedSourceContent
	if ctx == nil {
		return zero, normalizedSealManifest{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if _, err := preflightEnrichmentControls(controls); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if input.members == nil || len(input.members) > MaxCastCredits+MaxCrewCredits {
		return zero, normalizedSealManifest{}, ErrInvalid
	}
	native := projectNormalizedSource(input)
	summary, err := NormalizedSourceSeal(NormalizedSealSelection{NormalizedSourceKind, NormalizedSourceAlgorithm, 1}, native)
	if err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	// W has checked the entire borrowed input before ANY variable-string clone.
	// Owning is preparation custody, not native/provenance proof. The temporary
	// bounded DTO slices above borrow strings and are never retained by W.
	owned := cloneNormalizedSource(input)
	if err := ctx.Err(); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	return owned, normalizedSealManifest{NormalizedSourceAlgorithm, 1, summary.VerifiedRows, summary.CanonicalBytes, normalizedSealCommitment(summary.Commitment)}, nil
}

// Explicit native projection; no field order or JSON/reflection can choose
// normalized bytes here. W's sole codec defines and verifies that byte stream.
func projectNormalizedEvidence(p EvidenceStagePayload) NormalizedEvidenceInput {
	h := p.header.input
	out := NormalizedEvidenceInput{Header: NormalizedEvidenceHeader{
		ID: h.ID, SubjectID: h.SubjectID, ParserVersion: h.ParserVersion, Provider: h.Provider, SubjectKind: h.SubjectKind, ProviderID: h.ProviderID, Language: h.Language, Region: h.Region, RequestShape: h.RequestShape,
		Digest: hex.EncodeToString(h.ObservationDigest[:]), ObservedAt: h.ObservedAt, SourceURL: h.SourceURL, ETag: h.ETag, LastModified: h.LastModified,
		Title: h.Title, OriginalTitle: h.OriginalTitle, Overview: h.Overview, Tagline: h.Tagline, ContentRating: h.ContentRating, Studio: h.Studio, Network: h.Network, Country: h.Country, ReleaseDate: h.ReleaseDate, Year: h.Year, Artist: h.Artist,
		LengthMillis: h.LengthMillis, ArtistType: h.ArtistType, BeginYear: h.BeginYear, EndYear: h.EndYear, WikidataID: h.WikidataID, Biography: h.Biography, BiographySource: h.BiographySource, BiographySourceURL: h.BiographySourceURL,
		Disambiguation: h.Disambiguation, ReleaseGroupID: h.ReleaseGroupID, GroupTitle: h.GroupTitle, PrimaryType: h.PrimaryType, Barcode: h.Barcode,
	}, Credits: make([]NormalizedEvidenceCredit, len(p.credits))}
	for i, r := range p.credits {
		out.Credits[i] = NormalizedEvidenceCredit{Provider: h.Provider, EvidenceKind: h.SubjectKind, CreditClass: string(r.Class), EvidenceID: h.ID, Scope: "", Ordinal: r.Ordinal, ProviderPersonID: r.ProviderPersonID, Name: r.Name,
			ArtistName: "", SortName: "", Disambiguation: "", JoinPhrase: "", Role: r.Role, Department: r.Department, CreditID: r.CreditID, SourceOrdinal: EvidenceInteger{Value: r.SourceOrdinal, Present: true}, Billing: r.Billing}
	}
	return out
}

func projectNormalizedSource(s normalizedSourceContent) NormalizedSourceInput {
	u, h := s.upstream, s.header
	out := NormalizedSourceInput{Upstream: NormalizedUpstreamEvidence{
		ProviderOperationID: u.ownerID, CommandIncarnation: u.identity.Incarnation, CommandSequence: u.identity.Sequence, OperationID: u.identity.OperationID, RequestDigest: [32]byte(u.request), CommandKind: u.commandKind,
		EntityID: u.entityID, SealVersion: u.version, SealEvidenceID: u.evidenceID, SealSubjectID: u.subjectID, SealParserVersion: u.parser, SealProvider: u.provider, SealSubjectKind: u.kind, SealProviderID: u.providerID, SealLanguage: u.language, SealRegion: u.region, SealRequestShape: u.shape, SealObservationDigest: u.observation,
		SealOwnerGeneration: u.ownerGeneration, SealBuildID: u.buildID, SealBuildGeneration: u.buildGeneration, SealContentRevision: u.revision, SealAlgorithm: u.manifest.algorithm, SealCommitment: [32]byte(u.manifest.commitment), SealVerifiedRows: u.manifest.rows, SealCanonicalBytes: u.manifest.bytes,
	}, Header: NormalizedSourceHeader{PublicationID: h.id, EntityID: h.entityID, Provider: h.provider, AcceptedRecordReference: h.record, ObservedAt: h.observed, MemberCount: h.count}, Members: make([]NormalizedSourceEntry, len(s.members)), Intent: s.intent}
	for i, v := range s.members {
		r := v.row
		out.Members[i] = NormalizedSourceEntry{
			Member: NormalizedSourceMember{PublicationID: v.publicationID, EntityID: v.entityID, Provider: v.provider, Position: v.position, SourceRowID: v.sourceRowID, ReferenceStatus: v.reference},
			Row: NormalizedSourceRow{SourceRowID: r.id, EntityID: r.entityID, Provider: r.provider, TupleDigest: r.tupleDigest, OriginalRecordReference: r.record, OriginalCreditID: r.creditID, OriginalProviderPersonID: r.providerPersonID, OriginalPersonIdentityKey: r.identityKey, OriginalPersonToken: r.personToken, OriginalTargetKind: r.targetKind, OriginalTargetID: r.targetID,
				OriginalCreditedName: r.name, OriginalRole: r.role, OriginalDepartment: r.department, OriginalCreditClass: string(r.class), OriginalSourceOrdinal: r.rawOrdinal, OriginalBilling: r.billing}}
	}
	return out
}

func sealNativeText(s string) bool { return utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 }
func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, b := range []byte(s) {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func cloneNormalizedSource(s normalizedSourceContent) normalizedSourceContent {
	h, u := &s.header, &s.upstream
	i := &s.intent
	for _, p := range []*string{&h.id, &h.provider, &h.record, &h.observed, &u.ownerID, &u.commandKind, &u.evidenceID, &u.parser, &u.provider, &u.kind, &u.providerID, &u.language, &u.region, &u.shape, &u.observation, &u.buildID, &u.manifest.algorithm, &i.ProviderOperationID, &i.SourceEvidenceOwner, &i.SourceEvidenceID, &i.SourceParserVersion, &i.SourcePublicationID, &i.Provider, &i.SourceAlgorithm} {
		*p = strings.Clone(*p)
	}
	s.members = slices.Clone(s.members)
	for i := range s.members {
		m := &s.members[i]
		r := &m.row
		for _, p := range []*string{&m.publicationID, &m.provider, &m.sourceRowID, &m.reference, &r.id, &r.provider, &r.tupleDigest, &r.record, &r.creditID, &r.providerPersonID, &r.identityKey, &r.personToken, &r.targetKind, &r.targetID, &r.name, &r.role, &r.department} {
			*p = strings.Clone(*p)
		}
		if r.class == Cast {
			r.class = Cast
		} else {
			r.class = Crew
		}
	}
	return s
}
