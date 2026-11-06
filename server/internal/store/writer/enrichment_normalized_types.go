package writer

// These are borrowed, synchronous native-value projections. They are neither
// immutable command packets nor provenance: callers must keep them stable for
// the complete call. No input is retained or normalized by this codec.
// NULL tags reuse the existing EvidenceInteger/EvidenceText shapes; inactive
// values must be zero. BLOBs have fixed native widths, never text encodings.

// NormalizedEvidenceHeader contains the explicit native fields for the v1 projection.
type NormalizedEvidenceHeader struct {
	ID                 string          `json:"id"`
	SubjectID          int64           `json:"subject_id"`
	ParserVersion      string          `json:"parser_version"`
	Provider           string          `json:"provider"`
	SubjectKind        string          `json:"subject_kind"`
	ProviderID         string          `json:"provider_id"`
	Language           string          `json:"language"`
	Region             string          `json:"region"`
	RequestShape       string          `json:"request_shape"`
	Digest             string          `json:"digest"`
	ObservedAt         string          `json:"observed_at"`
	SourceURL          string          `json:"source_url"`
	ETag               string          `json:"etag"`
	LastModified       string          `json:"last_modified"`
	Title              string          `json:"title"`
	OriginalTitle      string          `json:"original_title"`
	Overview           string          `json:"overview"`
	Tagline            string          `json:"tagline"`
	ContentRating      string          `json:"content_rating"`
	Studio             string          `json:"studio"`
	Network            string          `json:"network"`
	Country            string          `json:"country"`
	ReleaseDate        string          `json:"release_date"`
	Year               int64           `json:"year"`
	Artist             string          `json:"artist"`
	LengthMillis       EvidenceInteger `json:"length_millis"`
	ArtistType         EvidenceText    `json:"artist_type"`
	BeginYear          EvidenceInteger `json:"begin_year"`
	EndYear            EvidenceInteger `json:"end_year"`
	WikidataID         string          `json:"wikidata_id"`
	Biography          string          `json:"biography"`
	BiographySource    string          `json:"biography_source"`
	BiographySourceURL string          `json:"biography_source_url"`
	Disambiguation     string          `json:"disambiguation"`
	ReleaseGroupID     string          `json:"release_group_id"`
	GroupTitle         string          `json:"group_title"`
	PrimaryType        string          `json:"primary_type"`
	Barcode            string          `json:"barcode"`
}

// NormalizedEvidenceCredit contains the explicit native fields for the v1 projection.
type NormalizedEvidenceCredit struct {
	Provider         string          `json:"provider"`
	EvidenceKind     string          `json:"evidence_kind"`
	CreditClass      string          `json:"credit_class"`
	EvidenceID       string          `json:"evidence_id"`
	Scope            string          `json:"scope"`
	Ordinal          int64           `json:"ordinal"`
	ProviderPersonID string          `json:"provider_person_id"`
	Name             string          `json:"name"`
	ArtistName       string          `json:"artist_name"`
	SortName         string          `json:"sort_name"`
	Disambiguation   string          `json:"disambiguation"`
	JoinPhrase       string          `json:"join_phrase"`
	Role             string          `json:"role"`
	Department       string          `json:"department"`
	CreditID         string          `json:"credit_id"`
	SourceOrdinal    EvidenceInteger `json:"source_ordinal"`
	Billing          EvidenceInteger `json:"billing"`
}

// NormalizedUpstreamEvidence contains the explicit native fields for the v1 projection.
type NormalizedUpstreamEvidence struct {
	ProviderOperationID   string   `json:"provider_operation_id"`
	CommandIncarnation    [16]byte `json:"command_incarnation"`
	CommandSequence       int64    `json:"command_sequence"`
	OperationID           [16]byte `json:"operation_id"`
	RequestDigest         [32]byte `json:"request_digest"`
	CommandKind           string   `json:"command_kind"`
	EntityID              int64    `json:"entity_id"`
	SealVersion           int64    `json:"seal_version"`
	SealEvidenceID        string   `json:"seal_evidence_id"`
	SealSubjectID         int64    `json:"seal_subject_id"`
	SealParserVersion     string   `json:"seal_parser_version"`
	SealProvider          string   `json:"seal_provider"`
	SealSubjectKind       string   `json:"seal_subject_kind"`
	SealProviderID        string   `json:"seal_provider_id"`
	SealLanguage          string   `json:"seal_language"`
	SealRegion            string   `json:"seal_region"`
	SealRequestShape      string   `json:"seal_request_shape"`
	SealObservationDigest string   `json:"seal_observation_digest"`
	SealOwnerGeneration   int64    `json:"seal_owner_generation"`
	SealBuildID           string   `json:"seal_build_id"`
	SealBuildGeneration   int64    `json:"seal_build_generation"`
	SealContentRevision   int64    `json:"seal_content_revision"`
	SealAlgorithm         string   `json:"seal_algorithm"`
	SealCommitment        [32]byte `json:"seal_commitment"`
	SealVerifiedRows      int64    `json:"seal_verified_rows"`
	SealCanonicalBytes    int64    `json:"seal_canonical_bytes"`
}

// NormalizedSourceHeader contains the explicit native fields for the v1 projection.
type NormalizedSourceHeader struct {
	PublicationID           string `json:"publication_id"`
	EntityID                int64  `json:"entity_id"`
	Provider                string `json:"provider"`
	AcceptedRecordReference string `json:"accepted_record_reference"`
	ObservedAt              string `json:"observed_at"`
	MemberCount             int64  `json:"member_count"`
}

// NormalizedSourceMember contains the explicit native fields for the v1 projection.
type NormalizedSourceMember struct {
	PublicationID   string `json:"publication_id"`
	EntityID        int64  `json:"entity_id"`
	Provider        string `json:"provider"`
	Position        int64  `json:"position"`
	SourceRowID     string `json:"source_row_id"`
	ReferenceStatus string `json:"reference_status"`
}

// NormalizedSourceRow contains the explicit native fields for the v1 projection.
type NormalizedSourceRow struct {
	SourceRowID               string          `json:"source_row_id"`
	EntityID                  int64           `json:"entity_id"`
	Provider                  string          `json:"provider"`
	TupleDigest               string          `json:"tuple_digest"`
	OriginalRecordReference   string          `json:"original_record_reference"`
	OriginalCreditID          string          `json:"original_credit_id"`
	OriginalProviderPersonID  string          `json:"original_provider_person_id"`
	OriginalPersonIdentityKey string          `json:"original_person_identity_key"`
	OriginalPersonToken       string          `json:"original_person_token"`
	OriginalTargetKind        string          `json:"original_target_kind"`
	OriginalTargetID          string          `json:"original_target_id"`
	OriginalCreditedName      string          `json:"original_credited_name"`
	OriginalRole              string          `json:"original_role"`
	OriginalDepartment        string          `json:"original_department"`
	OriginalCreditClass       string          `json:"original_credit_class"`
	OriginalSourceOrdinal     int64           `json:"original_source_ordinal"`
	OriginalBilling           EvidenceInteger `json:"original_billing"`
}

// NormalizedSourceIntent contains the explicit native fields for the v1 projection.
type NormalizedSourceIntent struct {
	ProviderOperationID       string   `json:"provider_operation_id"`
	SourceOwnerGeneration     int64    `json:"source_owner_generation"`
	SourceContentRevision     int64    `json:"source_content_revision"`
	SourceEvidenceOwner       string   `json:"source_evidence_owner"`
	SourceEvidenceIncarnation [16]byte `json:"source_evidence_incarnation"`
	SourceEvidenceSequence    int64    `json:"source_evidence_sequence"`
	SourceEvidenceID          string   `json:"source_evidence_id"`
	SourceSubjectID           int64    `json:"source_subject_id"`
	SourceParserVersion       string   `json:"source_parser_version"`
	SourceNextPosition        int64    `json:"source_next_position"`
	SourceExpectedMembers     int64    `json:"source_expected_members"`
	SourcePublicationID       string   `json:"source_publication_id"`
	EntityID                  int64    `json:"entity_id"`
	Provider                  string   `json:"provider"`
	SourceAlgorithm           string   `json:"source_algorithm"`
	SourceVersion             int64    `json:"source_version"`
}

// NormalizedEvidenceInput requires an explicit complete credit slice, including
// nonnil empty for N=0. Indexed no-next and excluded-family absence are separate
// verifier obligations, not assertions this inert value can establish.
type NormalizedEvidenceInput struct {
	Header  NormalizedEvidenceHeader   `json:"header"`
	Credits []NormalizedEvidenceCredit `json:"credits"`
}

type NormalizedSourceEntry struct {
	Member NormalizedSourceMember `json:"member"`
	Row    NormalizedSourceRow    `json:"row"`
}

// The source intent is checked against the immutable upstream context and
// current publication coverage. It is excluded from the normalized stream.
// Expected bytes/commitment are checked by VerifyNormalizedSourceSeal.
type NormalizedSourceInput struct {
	Upstream NormalizedUpstreamEvidence `json:"upstream"`
	Header   NormalizedSourceHeader     `json:"header"`
	Members  []NormalizedSourceEntry    `json:"members"`
	Intent   NormalizedSourceIntent     `json:"intent"`
}
