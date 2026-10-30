package storage

import "context"

// InventoryBlobPurpose distinguishes an analysis sidecar from a bounded
// inventory configuration file. Neither a purpose nor a pool label is a grant.
type InventoryBlobPurpose string

const (
	InventoryBlobAnalysisSidecar InventoryBlobPurpose = "analysis_sidecar"
	InventoryBlobIgnore          InventoryBlobPurpose = "inventory_ignore"
)

// InventoryBlobAdmission carries facts from the actual ingestion invocation.
// The remote producer re-resolves them in its own database snapshot. In
// particular, analysis ObjectID/SourceRevision identify the MEDIA target; the
// candidate NFO/cover/subtitle has a separate path and source observation.
// Ignore reads carry the exact ready listing and member instead, without a
// media target or an analysis/decode permission. There is no inferred cost:
// probe/local_metadata/subtitles are existing basic operations, while ignore
// configuration belongs to inventory rather than the decode engine.
type InventoryBlobAdmission struct {
	Purpose                             InventoryBlobPurpose
	JobID, LibraryID, LibrarySourceID   string
	RootIncarnation, RootIdentity       string
	SourceGeneration, PolicyRevision    int64
	ObjectID, SourceRevision, Operation string
	ListingID                           string
	Member                              Snapshot
}

// RemoteInventoryBlobReader resolves current durable inventory authority and
// candidate observations itself. Storage never turns a pool or read guard into
// that authority, and an unsupported producer cannot fall back to Acquire.
type RemoteInventoryBlobReader interface {
	ReadInventoryBlob(context.Context, string, InventoryRequest, InventoryBlobAdmission, int64) ([]byte, error)
}
