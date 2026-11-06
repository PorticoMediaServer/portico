package writer

import "database/sql"

// EffectiveStageBeginInput is a detached native capture for hidden image BEGIN.
// It declares image intent; it is neither source-seal proof nor permission.
// The caller retains the original command identity across resolution/retries.
// No generated image, retirement identity or result belongs in this input.
type EffectiveStageBeginInput struct {
	ProviderOperationID, EvidenceID, PublicationID, ObjectID string
	Operation                                                EffectiveStageBeginOperationInput
	Entity                                                   EffectiveStageBeginEntityInput
	Producer                                                 EffectiveStageBeginProducerInput
	Image                                                    EffectiveStageBeginImageInput
}

// EffectiveStageBeginOperationInput captures the complete operation authority
// and intent read by BEGIN. Query fields remain exact native TEXT/INTEGER.
// CHANGE-005 evidence_build_id belongs to separate evidence-header custody;
// image BEGIN preserves that pin and does not accept it as authority here.
type EffectiveStageBeginOperationInput struct {
	Exists                                                    bool
	CommandIncarnation                                        [16]byte
	LastCommandSequence, AcknowledgedCommandSequence          int64
	EntityID, Generation                                      int64
	Provider, Phase, Status, Lease, LeaseUntil                string
	EntityIncarnation, PolicyIncarnation                      [16]byte
	SourceRevision, IdentityRevision, MetadataRevision        int64
	HierarchyRevision, SelectionRevision, DescriptiveRevision int64
	PolicyRevision, ConsentRevision                           int64
	Algorithm, RefreshMode, InputDigest, SelectedID           string
	Page                                                      int64
	AfterItem                                                 string
	CreditImageID                                             sql.NullInt64
	QueryTitle, QueryArtist, QueryDigest                      string
	QueryYear, Manual, ReviewSearch                           int64
	HintID, HintProblem, Qualification, EpisodeOrder          string
	SetID, SetSubjectKind, SetProviderID                      sql.NullString
}

// EffectiveStageBeginOptionalIncarnation preserves native NULL versus BLOB16.
// An absent value must contain zero bytes; it is never synthesized from TEXT.
type EffectiveStageBeginOptionalIncarnation struct {
	Value   [16]byte
	Present bool
}

// EffectiveStageBeginEntityInput keeps catalog revision, metadata lifetime,
// owner policy and every nullable TV-family witness distinct. FIRST requires
// kind1 movie and all TV fields NULL, rather than replacing missing data by0.
type EffectiveStageBeginEntityInput struct {
	ID, LibraryID, Kind, Retired, Revision             int64
	MetadataIncarnation                                [16]byte
	SourceRevision, IdentityRevision, MetadataRevision int64
	Language                                           string
	OwnerRevision, OwnerLocked                         int64
	CreditImageID                                      sql.NullInt64
	TVIncarnation                                      EffectiveStageBeginOptionalIncarnation
	TVSourceRevision, TVHierarchyRevision              sql.NullInt64
	TVSelectionRevision, TVDescriptiveRevision         sql.NullInt64
}

// EffectiveStageBeginImageInput is declared build intent, not a verified image
// or a reusable delta plan. SourceManifest and ExpectedManifest are not proofs.
type EffectiveStageBeginImageInput struct {
	EntityID, SourceRevision, IdentityRevision, MetadataRevision, OwnerRevision int64
	EntityIncarnation, PolicyIncarnation                                        [16]byte
	PolicyRevision, ConsentRevision                                             int64
	Language                                                                    string
	SourceManifest, DeclaredInput, ExpectedManifest                             [32]byte
	ExpectedCount, ExpectedBytes                                                int64
}

// Producer projections reuse the exact inert native fact shapes. They expose
// no scanner, connection, callback, verified cap or source-proof constructor.
// Asset/Capture.Incarnation is the native asset-token lifetime witness, never
// playback_origin_revision, info_epoch, artwork incarnation or a token hash.
type (
	EffectiveStageBeginProducerInput    = effectiveBeginProducer
	EffectiveStageBeginLibraryInput     = effectiveBeginLibrary
	EffectiveStageBeginPolicyInput      = effectiveBeginPolicy
	EffectiveStageBeginProviderInput    = effectiveBeginProvider
	EffectiveStageBeginSelectionInput   = effectiveBeginSelection
	EffectiveStageBeginEvidenceInput    = effectiveBeginEvidence
	EffectiveStageBeginSubjectInput     = effectiveBeginSubject
	EffectiveStageBeginPublicationInput = effectiveBeginPublication
	EffectiveStageBeginCreditHeadInput  = effectiveBeginCreditHead
	EffectiveStageBeginCaptureInput     = effectiveBeginCapture
	EffectiveStageBeginAssociationInput = effectiveBeginAssociation
	EffectiveStageBeginAssetInput       = effectiveBeginAsset
	EffectiveStageBeginObjectInput      = effectiveBeginObject
	EffectiveStageBeginSourceInput      = effectiveBeginSource
)
