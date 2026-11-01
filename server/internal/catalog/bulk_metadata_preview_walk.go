package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ErrBulkMetadataLargeSelection hands a proved large selection to the general
// streaming reducer. It is not a refusal and never returns its cached ID prefix.
var ErrBulkMetadataLargeSelection = errors.New("metadata preview requires the general selection walk")

// ResolvePreparedBulkMetadataSelection preserves the existing small conversion
// lease. The complete cached counts can classify a large selection without
// enumerating its members; the general producer then walks the original query.
func (s *Service) ResolvePreparedBulkMetadataSelection(viewer Viewer, query JobQuery, revision string) (BrowseSelectionConversion, error) {
	value, err := s.resolvePreparedBrowseSelection(viewer, query, revision, false)
	if err != nil {
		return value, err
	}
	if len(value.IDs) > BrowseSelectionConversionLimit {
		value.Release()
		return BrowseSelectionConversion{}, ErrBulkMetadataLargeSelection
	}
	return value, nil
}

// BulkMetadataLocalSelectionBasisTx checks a held local-search query without
// running the legacy MATCH compiler or advancing membership. This repeatable
// source is shared by admission, worker fences and every pending/ready delivery.
func (s *Service) BulkMetadataLocalSelectionBasisTx(ctx context.Context, tx *sql.Tx, viewer Viewer, in BulkMetadataPreviewRequest) (out BulkMetadataSelectionPage, err error) {
	if tx == nil || dbwork.Snapshot(ctx) != tx {
		return out, sql.ErrTxDone
	}
	if err = in.ValidateSelection(); err != nil {
		return out, err
	}
	if in.Selector == nil || in.Selector.Query == nil || in.Selector.Query.SelectionBasis == "" {
		return out, ErrSelectionBatchUnsupported
	}
	q := in.Selector.Query
	read := s.WithContext(ctx)
	if err = read.CheckBrowseSelectionBasisTx(ctx, tx, viewer, q.LibraryID, q.SelectionBasis); err != nil {
		return out, err
	}
	var catalogRevision, metadataRevision, orderRevision, namespace, input, ready int64
	err = tx.QueryRowContext(ctx, `SELECT r.revision+(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),r.metadata_revision,r.order_revision,COALESCE(a.namespace,0),COALESCE(a.input_generation,0),COALESCE(a.ready_generation,0)
 FROM library_revisions r JOIN catalog_libraries l ON l.library_id=r.library_id AND l.retired=0
 LEFT JOIN reading_attribution_state a ON a.library_id=l.id WHERE r.library_id=?`, q.LibraryID).Scan(&catalogRevision, &metadataRevision, &orderRevision, &namespace, &input, &ready)
	if err != nil {
		return out, err
	}
	fence, err := json.Marshal([]any{q.LibraryID, catalogRevision, metadataRevision, orderRevision, namespace, input, ready, q.SelectionBasis})
	if err != nil {
		return out, err
	}
	out.Fence, out.Selection.Revision = string(fence), in.Expected.CatalogRevision
	return out, nil
}

// BulkMetadataSelectionWalk retains only the shared predicate continuation.
// Each step emits a fixed-size match window; no complete membership is retained.
type BulkMetadataSelectionWalk struct {
	service *Service
	state   *BrowseSelectionBatchState
}

func (w *BulkMetadataSelectionWalk) Bytes() int64 {
	if w == nil {
		return 0
	}
	return int64(w.state.Bytes()) + 4096
}

func (w *BulkMetadataSelectionWalk) Complete() bool { return w != nil && w.state.Complete() }

func (w *BulkMetadataSelectionWalk) Basis() string { return w.state.plan.witness }

func (s *Service) NewBulkMetadataSelectionWalkTx(ctx context.Context, tx *sql.Tx, viewer Viewer, in BulkMetadataPreviewRequest) (*BulkMetadataSelectionWalk, error) {
	if _, err := s.BulkMetadataLocalSelectionBasisTx(ctx, tx, viewer, in); err != nil {
		return nil, err
	}
	read := s.WithContext(ctx)
	q := *in.Selector.Query
	// The exact existing descriptor key proves query/policy equality. Its ready
	// counts and <=201 prefix are bounded, and this temporary delivery is released
	// before returning the independent membership walk.
	proof, err := read.resolvePreparedBrowseSelection(viewer, q, in.Expected.CatalogRevision, false)
	if err != nil {
		return nil, err
	}
	defer proof.Release()
	var node *BrowseNode
	if len(q.Filter) > 0 {
		node, err = ParseBrowseQuery(q.Filter, "selector.query.filter")
		if err != nil {
			return nil, err
		}
	}
	bound := BoundBrowseQuery{Library: q.LibraryID, Pivot: q.Pivot, Query: node, Sorts: q.Sort, Revision: in.Expected.CatalogRevision, snapshot: tx}
	state, err := read.NewBrowseSelectionBatch(BrowseRequest{}, viewer, bound, q.SelectionBasis)
	if err != nil {
		return nil, err
	}
	root := &Service{db: s.db, storage: s.storage, state: s.state, Clock: s.Clock}
	return &BulkMetadataSelectionWalk{service: root, state: state}, nil
}

func (w *BulkMetadataSelectionWalk) Step(ctx context.Context, witness SelectionBatchWitness, reserve func(int64) error) (*BulkMetadataSelectionWalk, SelectionBatchWork, error) {
	state, work, err := w.service.StepBrowseSelectionBatch(ctx, w.state, witness, SelectionBatchLimits{Rows: 64, ReserveTransient: reserve})
	if err != nil {
		return nil, work, err
	}
	return &BulkMetadataSelectionWalk{service: w.service, state: state}, work, nil
}
