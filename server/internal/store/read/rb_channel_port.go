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
	OpenSharedChannelScope(context.Context, governor.Request, time.Duration, BrowseOutput, SharedChannelScopeInput) (*SharedChannelScope, SharedChannelScopeInfo, BrowseReadResult, error)
	RevalidateSharedChannelScope(context.Context, governor.Request, time.Duration, BrowseOutput, *SharedChannelScope) (BrowseReadResult, error)
	SharedChannelWindow(context.Context, governor.Request, time.Duration, BrowseOutput, SharedChannelInput) (SharedChannelBatch, BrowseReadResult, error)
}

// SharedChannelScopeInput names actual stored facts, not a caller's filter or
// viewer. The generation's created_ms supplies the immutable query anchor.
type SharedChannelScopeInput struct {
	Channel, Generation, Rule, Library string
	ExpectedRevision                   int64
}

// SharedChannelScope has a private issuer and immutable source proof. Copying
// its public provenance never grants access to a different channel or library.
type SharedChannelScope struct {
	issuer  *SharedChannelReader
	input   SharedChannelScopeInput
	stamp   rbChannelStamp
	library rbChannelLibrary
	info    SharedChannelScopeInfo
}
type SharedChannelScopeInfo struct {
	Database, Channel, Generation, Rule, Library string
	ChannelRevision, CatalogueLibrary, CreatedMS int64
	ConfigHash, QueryHash                        [32]byte
	// Every named library must be authorized before showing the shared guide.
	RequiredLibraries    [1024]string
	RequiredLibraryCount int
}
type SharedChannelInput struct {
	Scope *SharedChannelScope
	Kind  int64
	After *SharedChannelCursor
}
type SharedChannelFence struct {
	Database, Channel, Generation, Rule, Library                     string
	ChannelRevision, CatalogueLibrary                                int64
	DataRevision, OrderRevision, MetadataRevision, AdmissionRevision int64
	ConfigHash, Query                                                [32]byte
	source                                                           rbChannelStamp
	library                                                          rbChannelLibrary
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
