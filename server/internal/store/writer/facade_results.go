package writer

// InventoryMember is the exact immutable scan result projection in input/child
// PK order. AcceptedAssetRevision is native catalog_assets.info_epoch, never
// playback/prepared/size/entity revision or a search-publication certificate.
type InventoryMember struct {
	InputOrdinal, ChildKind, ChildOrdinal                int64
	ObjectID                                             string
	LibraryID, SubjectID, AssetID, AcceptedAssetRevision int64
}

// InventoryResult is the agreed ordinary-batch projection. AcceptedRunRevision
// is the actual POST-CAS inventory_runs.job_revision. ResultCount equals the
// COMPLETE immutable Members length; no truncated/reconstructed result is valid.
// N=0 is not this result variant. Source echoes/cursor are exact native TEXT.
// Digest uses W's separate closed inventory result codec, not the request or
// EffectiveStage codec. The codec implementation is a separate prerequisite.
type InventoryResult struct {
	Outcome                                                 ReceiptOutcome
	RunID                                                   string
	AcceptedRunRevision, AppliedMS                          int64
	SourceID, SourceIncarnation, SourceRevision, NextCursor string
	ResultCount                                             int64
	Members                                                 []InventoryMember
	Digest                                                  ResultDigest
}

// SearchImageStageResult reflects the immutable image_stage receipt. The
// accepted revision is POST-APPLY publisher JOB revision, not serving/entity
// revision. ImageID is native INSERT RETURNING; FragmentID is required NULL.
// Initial cursors/totals are zero, chain is the portable seed, input is captured,
// published generation is unchanged. No persisted result-digest column is
// invented: search result wire/codec remains a separate W agreement.
type SearchImageStageResult struct {
	Outcome                                        ReceiptOutcome
	JobID, AcceptedJobRevision, AppliedMS, ImageID int64
	FragmentID                                     *int64
	InputGeneration, PublishedGeneration           int64
	AfterField, AfterOrdinal, AfterFragmentID      int64
	StagedFragments, StagedBytes                   int64
	VerifiedFragments, VerifiedBytes               int64
	SealChain                                      [32]byte
	SealAlgorithm                                  string
}

// EffectiveStageResult wraps the already agreed, closed hidden BEGIN result.
// Its image/revision and observed catalogue revision are distinct native facts.
// Existing EffectiveStageBeginResult.Digest binds only its saved scalar result.
type EffectiveStageResult struct {
	Outcome ReceiptOutcome
	Value   EffectiveStageBeginResult
}
