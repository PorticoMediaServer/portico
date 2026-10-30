package catalog

import (
	"context"
	"database/sql"
)

// CheckQueueBrowseRevisionTx checks a held whole-view selection in the capture
// transaction, using a freshly authorized Browse-domain viewer fence. The queue
// viewer's independent fence must never be compared with a Browse page token.
func (s *Service) CheckQueueBrowseRevisionTx(ctx context.Context, tx *sql.Tx, viewer Viewer, query *QueueQuery) error {
	if tx == nil || query == nil || query.SelectionRevision == "" || query.LibraryID == AcrossLibraries || !viewer.AllowsLibrary(query.LibraryID) {
		return ErrStaleContinuation
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	s = &clone
	if err := s.checkBrowseSelectionBasis(viewer, query.LibraryID, query.SelectionBasis); err != nil {
		return err
	}
	if err := s.prepareViewer(viewer); err != nil {
		return err
	}
	library, err := s.library(query.LibraryID)
	if err != nil {
		return err
	}
	pivotID := query.Pivot
	if pivotID == "" {
		pivotID = defaultPivot(library.Kind)
	}
	pivot, ok := pivotForKind(library.Kind, pivotID)
	if !ok || !selectionLeafPivot(pivot) || !selectionAggregateLibraries[library.Kind] {
		return ErrStaleContinuation
	}
	sorts, err := resolveBrowseSorts(pivot, query.Sort)
	if err != nil {
		return err
	}
	if !browseQueryReplayable(query.Query, sorts) {
		return ErrStaleContinuation
	}
	revision, err := s.browseRevision(BrowseRequest{Library: query.LibraryID, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.EffectiveRestrictions(), Query: query.Query}, pivot, sorts)
	if err != nil {
		return err
	}
	if revision != query.SelectionRevision {
		return ErrStaleContinuation
	}
	return nil
}
