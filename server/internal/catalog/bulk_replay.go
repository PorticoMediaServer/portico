package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// A stored command intent marker, never an executable search/filter plan.
const bulkReplaySelection = "bounded-local-search-replay-v1"

func (s *Service) bulkReplayBound(v Viewer, q *JobQuery) (BoundBrowseQuery, error) {
	var out BoundBrowseQuery
	if q == nil || q.SelectionBasis == "" {
		return out, ErrSelectionBatchStale
	}
	if err := s.checkBrowseSelectionBasis(v, q.LibraryID, q.SelectionBasis); err != nil {
		return out, err
	}
	node, err := ParseBrowseQuery(q.Filter, "query.filter")
	if err != nil {
		return out, err
	}
	library, err := s.library(q.LibraryID)
	if err != nil {
		return out, err
	}
	pivot, ok := pivotForKind(library.Kind, q.Pivot)
	if !ok || !pivot.Browsable || !selectionLeafPivot(pivot) || len(entityKindMembers(pivot)) != 1 || !selectionAggregateLibraries[library.Kind] {
		return out, ErrSelectionBatchUnsupported
	}
	sorts, err := resolveBrowseSorts(pivot, q.Sort)
	if err != nil {
		return out, err
	}
	if !browseQueryReplayable(node, sorts) {
		return out, ErrSelectionBatchUnsupported
	}
	if !v.AllowsLibrary(q.LibraryID) {
		return out, sql.ErrNoRows
	}
	if err = s.compactProjectionReady(); err != nil {
		return out, err
	}
	if err = s.browseReady(q.LibraryID); err != nil {
		return out, err
	}
	// Compile intent for validation only. Its SQL is never executed or persisted.
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: v.Profile, restrictions: v.EffectiveRestrictions()}
	if _, err = compiler.compile(node); err != nil {
		return out, err
	}
	revision, err := s.browseRevision(BrowseRequest{Library: q.LibraryID, Profile: v.Profile, ViewerFence: v.Fence, Restrictions: v.EffectiveRestrictions(), Query: node}, pivot, sorts)
	if err != nil {
		return out, err
	}
	return BoundBrowseQuery{Library: q.LibraryID, Pivot: q.Pivot, Query: node, Sorts: sorts, Revision: revision, snapshot: dbwork.Snapshot(s.Context())}, nil
}

func bulkReplayCaller(id string) readprep.Caller { return readprep.Caller("bulk-replay/" + id) }

func (s *Service) bulkReplayPage(id, cursor string, v Viewer, selector JobSelector, p identity.Principal, command string, arguments JobPersonalArgs, access BulkAccess) (BrowseSelectionReplayPage, error) {
	var out BrowseSelectionReplayPage
	store := s.ReadPreparations()
	if store == nil {
		return out, readprep.ErrCapacity
	}
	bound, err := s.bulkReplayBound(v, selector.Query)
	if err != nil {
		return out, err
	}
	// Authority is checked on admission, every background snapshot, every poll,
	// accepting capture write, and apply. Capture callback performs no writes.
	check := func(read *Service) error {
		tx := dbwork.Snapshot(read.Context())
		if tx == nil {
			return sql.ErrTxDone
		}
		if access == nil {
			return identity.ErrUnauthorized
		}
		if _, _, err := access(read.Context(), tx, p, "i.id"); err != nil {
			return err
		}
		return read.authorizeBulkCommand(read.Context(), tx, p, command, arguments, false)
	}
	captures, err := json.Marshal([]any{p, command, arguments})
	if err != nil {
		return out, err
	}
	private := SelectionPreparationAccess{Caller: bulkReplayCaller(id), Check: check, CapturedBytes: 8192 + int64(8*len(captures)+len(id))}
	after, err := bulkReplayAfter(cursor)
	if err != nil {
		return out, err
	}
	pending, err := s.submitBrowseSelectionReplay(BrowseRequest{}, v, bound, after, private, true)
	if err != nil {
		return out, err
	}
	handle := readprep.Handle(pending.QueryID)
	out, err = s.PollBrowseSelectionReplay(bound.Library, v, pending.QueryID, private)
	if selectionReplayStale(err) {
		_ = store.Cancel(context.WithoutCancel(s.Context()), private.Caller, handle)
		return BrowseSelectionReplayPage{Preparing: true}, nil
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

// Local replay emits only qualified leaf identities. Retain the command's
// ordinary singleton target checks rather than compiling the search again.
func (s *Service) bulkReplayTarget(tx *sql.Tx, v Viewer, selector JobSelector, command string, id int64) error {
	visible, args := v.itemVisibilitySQL("i.id")
	kind := "k.playable=1"
	if command == "metadata-edit" {
		visible, args = v.entityVisibilitySQL("i.id", "l.library_id")
		// The validated single leaf pivot replaces the foreground DISTINCT census.
		library, err := s.library(selector.Query.LibraryID)
		if err != nil {
			return err
		}
		pivot, ok := pivotForKind(library.Kind, selector.Query.Pivot)
		if !ok || len(entityKindMembers(pivot)) != 1 {
			return ErrSelectionBatchUnsupported
		}
		kind = "k.name=?"
		args = append([]any{entityKindMembers(pivot)[0]}, args...)
	}
	args = append([]any{id}, args...)
	var yes bool
	err := tx.QueryRowContext(s.Context(), `SELECT 1 FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind WHERE i.id=? AND i.retired=0 AND `+kind+` AND (`+visible+`)`+recordingsClause("i.id", v.EffectiveRestrictions())+` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)`, args...).Scan(&yes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !yes {
		return ErrSelectionBatchStale
	}
	return err
}

func bulkReplayAfter(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrCursor
	}
	return n, nil
}
