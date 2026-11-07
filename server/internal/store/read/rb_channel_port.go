package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// SharedChannelReadPort is the RB-owned draft seam consumed by MG's immutable
// schedule builder. It is a values-only read contract, not a SQL capability or
// permission to write catalogue facts. A native implementation is still pending.
type SharedChannelReadPort interface {
	SharedChannelWindow(context.Context, governor.Request, time.Duration, BrowseOutput, SharedChannelInput) (SharedChannelBatch, BrowseReadResult, error)
}

// SharedChannelFilter is a bounded structured predicate, never interpolated SQL.
// Field/operator applicability and missing-value semantics are the public browse
// contract. A native implementation must reject unsupported combinations before
// opening a snapshot. Shared recipes exclude personal/profile/history fields.
// The reader owns validation, canonicalization and copying of the frozen input.
type SharedChannelFilter struct {
	All, Any []SharedChannelFilter
	Not      *SharedChannelFilter
	Field    string
	Operator string
	Values   []SharedChannelValue
}
type SharedChannelValue struct {
	Text    *string
	Integer *int64
	Real    *float64
	Boolean *bool
}
type SharedChannelInput struct {
	// View is current trusted authorization, never a fabricated service viewer.
	// Initial native support requires a current local owner and unrestricted
	// profile. A background service-principal path needs its own closed proof.
	View   BrowseInput
	Filter SharedChannelFilter
	After  *SharedChannelCursor
}

// SharedChannelFence is comparable provenance for one immutable batch. The
// private full fence includes viewer/birth/policy witnesses. It confers no
// permission, cannot be fabricated by MG, and must match every continuation.
type SharedChannelFence struct {
	Database                                      string
	Library                                       int64
	DataRevision, OrderRevision, MetadataRevision int64
	AdmissionRevision                             int64
	Query                                         [32]byte
	fence                                         browseFence
}
type SharedChannelCursor struct {
	fence             SharedChannelFence
	item, asset, part int64
}
type SharedChannelPlayable struct {
	Item                                                BrowseItem
	Kind, Library                                       int64
	ItemIncarnation                                     [16]byte
	ItemSourceRevision, ItemPlaybackRevision            int64
	Asset                                               int64
	AssetIncarnation, LinkIncarnation                   [16]byte
	AssetRevision, LinkRevision, PreparedSourceRevision int64
	Token, Path, Container                              string
	Size, ModifiedNS                                    int64
	// Whole-file identity only: linked start=0/end=NULL. Multipart/sliced or
	// ambiguous binding is refused or explicitly skipped as consumed raw work;
	// it can never be presented as a whole playable item.
	DurationSeconds                    float64
	Show, Season                       int64
	SeasonNumber, EpisodeNumber        *int64
	Numbering, AiredAt                 string
	TitleOrder, TitleOrderKeepArticles string
	AddedAt                            string
	Year                               int64
	TrackNumber, DiscNumber            *int64
}
type SharedChannelBatch struct {
	Fence SharedChannelFence
	Items []SharedChannelPlayable
	After *SharedChannelCursor
	// RawEOF proves the entire underlying remaining candidate stream ended.
	// Empty non-EOF means consumed rejected work, never an empty channel pool.
	RawEOF                  bool
	RawFetched, RawConsumed int
}
