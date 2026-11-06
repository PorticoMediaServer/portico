package writer

// BasicPurpose labels three distinct closed request/receipt/result variants.
// A label is data, not a CommandID, admission or execution authority. Zero and
// unknown values are not variants; the eventual codecs and dispatcher must
// validate the exact variant independently of the BasicAccept descriptor.
type BasicPurpose string

const (
	BasicPurposeStage  BasicPurpose = "basic_stage"
	BasicPurposeSeal   BasicPurpose = "basic_seal"
	BasicPurposeAccept BasicPurpose = "basic_accept"
)

// BasicReceiptRequest preserves the complete ORIGINAL scan coordinates. It
// never reconstructs a request digest or a saved result from current rows.
type BasicReceiptRequest struct {
	Scan    ScanReceiptRequest
	Purpose BasicPurpose
}

// BasicContinuation projects only actual operation-specific native post-state.
// Input-row staging has the explicit zero continuation; rows_staged is not a
// copy cursor. NextCursor never advances an Inventory directory for FIRST Basic.
type BasicContinuation struct {
	NextCursor                                         string
	NextFamily, NextCopyPhase, NextCopyID, NextCopyAux int64
	NextCopyKey, NextCopyKey2, NextCopyKey3            string
	AfterEntity                                        int64
}

// BasicResult is the native saved projection, not a permission or proof of
// readiness. Domain handlers return ZERO Outcome; the one writer dispatcher
// owns final publication. These declarations provide no codec or entry point.
//
// AcceptedRunRevision is POST-CAS inventory_runs.job_revision. SourceRevision
// remains the exact native observation TEXT, independently of source generation
// or change/input revisions. ChangeID/ExpectedEpoch are the actual owned change
// and its pre-activation expectation; they do not prove the new asset epoch.
//
// Stage/seal have ResultCount0 and an explicitly empty cohort on success.
// Accept returns the COMPLETE activated native member cohort, whose
// AcceptedAssetRevision is POST-activation catalog_assets.info_epoch. Reusing
// InventoryMember shares only that existing row projection, never its codec.
// Every failed/cancelled operation must publish the full zero result.
type BasicResult struct {
	Outcome                                     ReceiptOutcome
	Purpose                                     BasicPurpose
	RunID                                       string
	AcceptedRunRevision, AppliedMS              int64
	SourceID, SourceIncarnation, SourceRevision string
	ChangeID, ExpectedEpoch                     int64
	Continuation                                BasicContinuation
	ResultCount                                 int64
	Members                                     []InventoryMember
	Digest                                      ResultDigest
}
