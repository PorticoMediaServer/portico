package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// BulkMetadataTarget names the displayed entity and its real editor namespace.
// ContentKind prevents two different playable kinds from masquerading as one item schema.
type BulkMetadataTarget struct {
	Kind        string `json:"kind"`
	ContentKind string `json:"contentKind"`
	ID          string `json:"id"`
	LibraryID   string `json:"libraryId"`
}
type BulkMetadataPreviewRequest struct {
	Targets  []BulkMetadataTarget `json:"targets,omitempty"`
	Selector *JobSelector         `json:"selector,omitempty"`
	Expected *JobExpected         `json:"expected,omitempty"`
}

// BulkMetadataSelection is transaction-local SQL, never a client-provided fragment.
// Query selections name displayed entities, not their playable descendants.
type BulkMetadataSelection struct {
	SQL      string
	Args     []any
	Targets  []BulkMetadataTarget
	Revision string
}

const BulkMetadataPreviewTargetLimit = 2048

// ValidateSelection checks only request shape, before a service or database is
// required. Authority and live target/revision checks remain in the transaction.
func (in BulkMetadataPreviewRequest) ValidateSelection() error {
	if len(in.Targets) > 0 {
		if in.Selector != nil || in.Expected != nil || len(in.Targets) > BulkMetadataPreviewTargetLimit {
			return jobInvalid("preview needs bounded targets or one query selector")
		}
		seen := map[string]bool{}
		for _, target := range in.Targets {
			kind, err := compactcatalog.ParseKind(target.ContentKind)
			if err != nil || compactcatalog.OwnerNamespace(kind) != target.Kind || target.ID == "" || len(target.ID) > 256 || target.LibraryID == "" || len(target.LibraryID) > 256 || seen[target.ID] {
				return jobInvalid("invalid metadata target")
			}
			seen[target.ID] = true
		}
		return nil
	}
	if in.Selector == nil || in.Selector.Query == nil || in.Selector.Items != nil || in.Selector.Container != nil || in.Expected == nil || in.Expected.CatalogRevision == "" {
		return jobInvalid("preview query requires its exact revision")
	}
	return nil
}

func (s *Service) BulkMetadataSelectionTx(ctx context.Context, tx *sql.Tx, v Viewer, in BulkMetadataPreviewRequest) (BulkMetadataSelection, error) {
	return s.bulkMetadataSelectionTx(ctx, tx, v, in, nil)
}

func (s *Service) bulkMetadataSelectionTx(ctx context.Context, tx *sql.Tx, v Viewer, in BulkMetadataPreviewRequest, candidates []int64) (BulkMetadataSelection, error) {
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	s = &clone
	out := BulkMetadataSelection{Targets: in.Targets}
	if err := in.ValidateSelection(); err != nil {
		return out, err
	}
	if len(in.Targets) > 0 {
		raw, err := json.Marshal(in.Targets)
		if err != nil {
			return out, err
		}
		visible, args := v.entityVisibilitySQL("i.id", "l.library_id")
		// Playable entities can be visible even before a browse row is published.
		leaf, leafArgs := v.itemVisibilitySQL("i.id")
		out.SQL = `SELECT i.id FROM json_each(?) target JOIN catalog_entities i ON i.public_id=pid_blob(json_extract(target.value,'$.id')) JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind WHERE i.retired=0 AND l.retired=0 AND l.library_id=json_extract(target.value,'$.libraryId') AND k.name=json_extract(target.value,'$.contentKind') AND ((k.playable=1 AND ` + leaf + `) OR (k.playable=0 AND ` + visible + `))` + recordingsClause("i.id", v.EffectiveRestrictions())
		out.Args = append([]any{string(raw)}, leafArgs...)
		out.Args = append(out.Args, args...)
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+out.SQL+`)`, out.Args...).Scan(&count); err != nil {
			return out, err
		}
		if count != len(in.Targets) {
			return out, sql.ErrNoRows
		}
		return out, nil
	}
	q := in.Selector.Query
	if err := s.checkBrowseSelectionBasis(v, q.LibraryID, q.SelectionBasis); err != nil {
		return out, err
	}
	if !v.AllowsLibrary(q.LibraryID) {
		return out, sql.ErrNoRows
	}
	lib, err := s.library(q.LibraryID)
	if err != nil {
		return out, err
	}
	pivot, ok := pivotForKind(lib.Kind, q.Pivot)
	if !ok || !pivot.Browsable {
		return out, ErrBrowsePivot
	}
	if err = s.browseReady(q.LibraryID); err != nil {
		return out, err
	}
	sorts, err := resolveBrowseSorts(pivot, q.Sort)
	if err != nil {
		return out, err
	}
	var node *BrowseNode
	if len(q.Filter) > 0 {
		if node, err = ParseBrowseQuery(q.Filter, "filter"); err != nil {
			return out, jobInvalid("invalid preview filter")
		}
	}
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: v.Profile, restrictions: v.EffectiveRestrictions(), count: s.countRows, ids: s.entityIDs, itemPivot: playablePivot(pivot)}
	if candidates != nil {
		// The page already names a bounded candidate set. Global selectivity probes
		// and materialized member-ID sets would undo that bound for sparse filters.
		compiler.count, compiler.ids = nil, nil
		compiler.bounded = true
		compiler.groupSearch = true
	}
	for _, name := range entityKindMembers(pivot) {
		if kind, e := compactcatalog.ParseKind(name); e == nil {
			compiler.kinds = append(compiler.kinds, int(kind))
		}
	}
	readingEligible, err := s.bindReadingBrowse(q.LibraryID, compiler)
	if err != nil {
		return out, err
	}
	filter, err := compiler.compile(node)
	if err != nil {
		return out, err
	}
	where, args := browseScopeSQL(q.LibraryID, pivot)
	where += " AND " + readingEligible
	visible, bound := v.entityVisibilitySQL("e.entity_id", "l.library_id")
	out.SQL = `SELECT e.entity_id AS id FROM catalog_browse_rows e JOIN catalog_libraries l ON l.id=e.library_id WHERE ` + where + ` AND ` + filter + ` AND ` + visible
	out.Args = append(args, compiler.args...)
	out.Args = append(out.Args, bound...)
	if candidates != nil {
		raw, err := json.Marshal(candidates)
		if err != nil {
			return out, err
		}
		// CROSS JOIN fixes the bounded candidates as the outer loop. Even a sparse
		// selection probes at most one browse row per candidate, rather than scanning
		// an unbounded remainder to fill a page of matching rows.
		out.SQL = strings.Replace(out.SQL, "FROM catalog_browse_rows e", "FROM json_each(?) candidate CROSS JOIN catalog_browse_rows e ON e.entity_id=CAST(candidate.value AS INTEGER)", 1)
		out.Args = append([]any{string(raw)}, out.Args...)
	}
	revision, err := s.browseRevision(BrowseRequest{Library: q.LibraryID, Profile: v.Profile, ViewerFence: v.Fence, Restrictions: v.EffectiveRestrictions(), Query: node}, pivot, sorts)
	if err != nil {
		return out, err
	}
	if revision != in.Expected.CatalogRevision {
		return out, ErrStaleContinuation
	}
	out.Revision = revision
	return out, nil
}

// BulkMetadataPageSize bounds candidates, not matches. Empty pages still advance
// After, and Done is based on source exhaustion, never on a filter's match count.
const BulkMetadataPageSize = 64

type BulkMetadataSelectionPage struct {
	Selection BulkMetadataSelection
	After     int64
	Done      bool
	// Fence includes both the metadata revision and the browse order revision of
	// each selected library. It is private server state, never a client grant.
	Fence string
}

func (s *Service) BulkMetadataSelectionPageTx(ctx context.Context, tx *sql.Tx, v Viewer, in BulkMetadataPreviewRequest, after int64) (BulkMetadataSelectionPage, error) {
	return s.bulkMetadataSelectionPageTx(ctx, tx, v, in, after, BulkMetadataPageSize)
}

// BulkMetadataSelectionUnitTx advances one raw candidate before predicates.
// General metadata preparation yields after this unit, including empty matches.
func (s *Service) BulkMetadataSelectionUnitTx(ctx context.Context, tx *sql.Tx, v Viewer, in BulkMetadataPreviewRequest, after int64) (BulkMetadataSelectionPage, error) {
	return s.bulkMetadataSelectionPageTx(ctx, tx, v, in, after, 1)
}

func (s *Service) bulkMetadataSelectionPageTx(ctx context.Context, tx *sql.Tx, v Viewer, in BulkMetadataPreviewRequest, after int64, size int) (BulkMetadataSelectionPage, error) {
	out := BulkMetadataSelectionPage{After: after}
	if err := in.ValidateSelection(); err != nil {
		return out, err
	}
	if after < 0 {
		return out, ErrStaleContinuation
	}
	libraries := []string{}
	if len(in.Targets) > 0 {
		seen := map[string]bool{}
		for _, t := range in.Targets {
			if !seen[t.LibraryID] {
				libraries = append(libraries, t.LibraryID)
				seen[t.LibraryID] = true
			}
		}
	} else {
		libraries = append(libraries, in.Selector.Query.LibraryID)
	}
	sort.Strings(libraries)
	// Explicit input is bounded at 2048. A query contributes one library, never a
	// slice of every entity or every library in the catalogue.

	type revision struct {
		Library                                                                string
		Catalog, Metadata, Order, ReadingNamespace, ReadingInput, ReadingReady int64
	}
	for _, library := range libraries {
		if !v.AllowsLibrary(library) {
			return out, sql.ErrNoRows
		}
	}
	raw, err := json.Marshal(libraries)
	if err != nil {
		return out, err
	}
	fenceRows, err := tx.QueryContext(ctx, `SELECT r.library_id,r.revision+(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),r.metadata_revision,r.order_revision,COALESCE(a.namespace,0),COALESCE(a.input_generation,0),COALESCE(a.ready_generation,0)
 FROM json_each(?) requested JOIN library_revisions r ON r.library_id=requested.value
 LEFT JOIN catalog_libraries l ON l.library_id=r.library_id
 LEFT JOIN reading_attribution_state a ON a.library_id=l.id ORDER BY r.library_id`, string(raw))
	if err != nil {
		return out, err
	}
	revisions := make([]revision, 0, len(libraries))
	for fenceRows.Next() {
		var r revision
		if err = fenceRows.Scan(&r.Library, &r.Catalog, &r.Metadata, &r.Order, &r.ReadingNamespace, &r.ReadingInput, &r.ReadingReady); err != nil {
			break
		}
		revisions = append(revisions, r)
	}
	if err == nil {
		err = fenceRows.Err()
	}
	closeErr := fenceRows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return out, err
	}
	if len(revisions) != len(libraries) {
		return out, sql.ErrNoRows
	}
	raw, err = json.Marshal(revisions)
	if err != nil {
		return out, err
	}
	out.Fence = string(raw)
	if size == 1 {
		// Preparation outlives a request/page revision. Availability, policy
		// predicates, serving slots and Saved/group inputs can change without
		// changing the ordinary metadata revision. Bind their complete existing
		// witness too; explicit editor targets need no browse-readiness gate.
		var predicates, selection int64
		if err = tx.QueryRowContext(ctx, `SELECT p.revision,s.revision FROM catalog_browse_predicate_epoch p CROSS JOIN catalog_selection_predicate_epoch s WHERE p.singleton=1 AND s.singleton=1`).Scan(&predicates, &selection); err != nil {
			return out, err
		}
		if predicates <= 0 || selection <= 0 {
			return out, ErrStaleContinuation
		}
		var exact string
		if in.Selector != nil && in.Selector.Query != nil {
			read := *s.WithContext(ctx)
			read.readTx = tx
			exact, err = read.browseSelectionWitness(in.Selector.Query.LibraryID, v.Profile, v.EffectiveRestrictions())
			if err != nil {
				return out, err
			}
		}
		bound, e := json.Marshal([]any{out.Fence, predicates, selection, exact})
		if e != nil {
			return out, e
		}
		out.Fence = string(bound)
	}
	if len(in.Targets) > 0 {
		if after > int64(len(in.Targets)) {
			return out, ErrStaleContinuation
		}
		// Per-target CAS reads are heavier than the grouped query vector.
		end := min(after+int64(min(size, 16)), int64(len(in.Targets)))
		out.After = end
		out.Done = end == int64(len(in.Targets))
		if end == after {
			return out, nil
		}
		page := BulkMetadataPreviewRequest{Targets: in.Targets[after:end]}
		out.Selection, err = s.bulkMetadataSelectionTx(ctx, tx, v, page, nil)
		return out, err
	}
	// Read the library's entity-ID index without a match predicate. In particular,
	// the final page of a sparse filter cannot read millions of excluded rows in
	// one snapshot. Retired rows also advance this source cursor.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND id>? ORDER BY id LIMIT ?`, libraries[0], after, size)
	if err != nil {
		return out, err
	}
	ids := make([]int64, 0, size)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
		out.After = id
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr = rows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return out, err
	}
	out.Done = len(ids) < size
	out.Selection, err = s.bulkMetadataSelectionTx(ctx, tx, v, in, ids)
	return out, err
}
