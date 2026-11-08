package read

import (
	"context"
	"hash"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// User-saved COLLECTION member reads are an inert harness component until the
// phase 4 adapter composes them. UO owns saved writes; RB owns saved VIEWS.
// Directory and pin ordering are a separate family (RsSavedDirectory).
const (
	rsSavedCompute readPolicyID = 46
	rsSavedDeliver readPolicyID = 47
)

func init() {
	for _, id := range []readPolicyID{rsSavedCompute, rsSavedDeliver} {
		registerReadPolicy(readPolicyRecipe{id: id, domain: "rs-saved-collection-members", kinds: []int64{1}, stateful: true, scope: readPrincipalScope, validate: rsSavedEligibility})
	}
}

type SavedCollectionReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
	mobi      bool
}

func NewSavedCollectionReader(s *governor.Snapshots) (*SavedCollectionReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &SavedCollectionReader{snapshots: s, pool: s}, nil
}

type SavedCollectionInput struct {
	View     BrowseInput
	Resource string
	After    *SavedCollectionCursor
	Work     *SavedCollectionWork
}

// SavedCollectionPage is one bounded member page of a user-saved COLLECTION.
// Resources carries the single collection descriptor; Entries carries the
// selected cards. Total is the complete viewer-visible member count, computed
// from the whole membership stream even when only one page of cards is
// hydrated. Pending never carries partial entries, counts or a cursor.
type SavedCollectionPage struct {
	Items     []TitleItem
	Total     int64
	Exhausted bool
	Pending   bool
	Next      *SavedCollectionCursor
	Work      *SavedCollectionWork
	Resources []SavedCollectionResource
	Entries   []SavedCollectionEntry
}

// SavedCollectionCursor binds a continuation to the exact viewer, resource,
// query, source generation and complete visible member set. It is internal
// values-only state, not a wire token.
type SavedCollectionCursor struct {
	identity, proof [32]byte
	fence           browseFence
	generation      int64
	key, id         string
}

type SavedCollectionEntry struct {
	EntryID, OrderKey, Library string
	Item                       TitleItem
	State                      UserState
	Reading                    *ReadingFacts
	Artwork                    ArtworkFact
	ArtworkPending             bool
}

type SavedCollectionResource struct {
	ID, Name, Summary, CreatedAt, Visibility, Role, Shape string
	Revision, EntryCount                                  int64
}

type rsSavedCurrent struct {
	innerBytes          int64
	resource            SavedCollectionResource
	entryID, key        string
	item, library, kind int64
	libraryName         string
	memberStarted       bool
	view                BrowseInput
	card                SavedCollectionEntry
	inner               *CollectionPreparation
}

// SavedCollectionWork is caller-owned typed ephemeral work under RB's F-04
// lease. It has no worker, queue, SQL handle, durable projection or renewal.
// Advance/Delivery/Close serialize on this job's mutex so a lease expiry,
// cancel or concurrent Close cannot retire the lease twice or clear retained
// storage mid-enumeration. The caller keeps the job context alive between
// polls; an HTTP poll context must not own the job lifetime.
type SavedCollectionWork struct {
	membersHash             hash.Hash
	mu                      sync.Mutex
	reader                  *SavedCollectionReader
	authority               *governor.Authority
	lease                   *governor.PreparationLease
	job                     context.Context
	owner                   governor.OwnerID
	started                 time.Time
	view                    BrowseInput
	resource                string
	after                   *SavedCollectionCursor
	identity, proof         [32]byte
	stage                   int
	current                 rsSavedCurrent
	records                 []SavedCollectionResource
	entries                 []SavedCollectionEntry
	total                   int64
	more                    bool
	used                    int64
	fence                   *browseFence
	source                  []any
	generation              int64
	captured, ready, closed bool
}

func (*SavedCollectionWork) readPolicyState() {}

// Members supports bounded one-shot reads and explicitly admitted resumable
// work. The job's source footprint, original authorization clock and complete
// visible set survive polls; a resumed page re-prepares and then proves the
// cursor's exact generation and proof.
func (r *SavedCollectionReader) Members(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in SavedCollectionInput) (SavedCollectionPage, BrowseReadResult, error) {
	if in.Resource == "" || in.View.After != nil {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	return r.savedPage(ctx, req, budget, out, in.View, in.Resource, in.After, in.Work)
}

// BeginMembers explicitly admits resumable member work for one saved
// COLLECTION. The returned work borrows no second grant: a nested automatic
// collection reducer uses this same lease.
func (r *SavedCollectionReader) BeginMembers(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, in SavedCollectionInput) (*SavedCollectionWork, *governor.Refusal, error) {
	if in.Resource == "" || len(in.Resource) > 256 {
		return nil, nil, ErrInput
	}
	return r.beginSaved(ctx, a, owner, deadline, bytes, in.View, in.Resource, in.After)
}
