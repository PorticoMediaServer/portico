package read

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

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
type SavedCollectionPage struct {
	Items            []TitleItem
	Total            int64
	Exhausted        bool
	Pending          bool
	Next             *SavedCollectionCursor
	Work             *SavedCollectionWork
	Resources        []SavedCollectionResource
	Entries          []SavedCollectionEntry
	Posters, Squares int64
}

// Directory reads user-saved collections. A caller needing multiple polls
// admits BeginDirectory with a job context, then uses DirectoryPage's Work.
func (r *SavedCollectionReader) Directory(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in BrowseInput) (SavedCollectionPage, BrowseReadResult, error) {
	return r.DirectoryPage(ctx, req, budget, out, SavedCollectionDirectoryInput{View: in})
}

// Members supports bounded one-shot reads and explicitly admitted resumable
// work. The job's source footprint and original authorization clock survive polls.
func (r *SavedCollectionReader) Members(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in SavedCollectionInput) (SavedCollectionPage, BrowseReadResult, error) {
	if in.Resource == "" || in.View.After != nil {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	return r.savedPage(ctx, req, budget, out, in.View, in.Resource, false, "", false, in.After, in.Work)
}
