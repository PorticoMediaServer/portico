package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

// SelectionActionWalk retains only the existing incremental predicate reducer.
// A selected identity is emitted in its qualifying snapshot, never recovered
// from a page or from a second independently compiled selection query.
type SelectionActionWalk struct {
	state                 *BrowseSelectionBatchState
	viewer                Viewer
	query                 JobQuery
	matches               [128]int64
	matchCount, nextMatch int
}

func (w *SelectionActionWalk) Bytes() int64 {
	if w == nil {
		return 0
	}
	return int64(w.state.Bytes() + len(w.query.Filter) + len(w.query.LibraryID) + len(w.query.Pivot) + len(w.viewer.Profile) + 4096)
}

func (s *Service) NewSelectionActionWalk(query JobQuery, expected JobExpected, viewer Viewer) (*SelectionActionWalk, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		return nil, sql.ErrTxDone
	}
	if query.SelectionBasis == "" || !selectionBasisPattern.MatchString(query.SelectionBasis) || expected.CatalogRevision == "" {
		return nil, selectionactions.ErrInput
	}
	if err := s.checkBrowseSelectionBasis(viewer, query.LibraryID, query.SelectionBasis); err != nil {
		return nil, err
	}
	lib, err := s.library(query.LibraryID)
	if err != nil {
		return nil, err
	}
	pivot, ok := pivotForKind(lib.Kind, query.Pivot)
	if !ok || !selectionLeafPivot(pivot) {
		return nil, ErrSelectionBatchUnsupported
	}
	var node *BrowseNode
	if len(query.Filter) > 0 {
		node, err = ParseBrowseQuery(query.Filter, "selector.query.filter")
		if err != nil {
			return nil, err
		}
	}
	sorts, err := resolveBrowseSorts(pivot, query.Sort)
	if err != nil {
		return nil, err
	}
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: viewer.EffectiveRestrictions()}
	if _, err = compiler.compile(node); err != nil {
		return nil, err
	}
	revision, err := s.browseRevision(BrowseRequest{Library: query.LibraryID, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.EffectiveRestrictions(), Query: node}, pivot, sorts)
	if err != nil {
		return nil, err
	}
	if revision != expected.CatalogRevision {
		return nil, ErrStaleContinuation
	}
	bound := BoundBrowseQuery{Library: query.LibraryID, Pivot: query.Pivot, Query: node, Sorts: sorts, Revision: revision, snapshot: dbwork.Snapshot(s.Context())}
	state, err := s.NewBrowseSelectionBatch(BrowseRequest{}, viewer, bound, query.SelectionBasis)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &query); err != nil {
		return nil, err
	}
	return &SelectionActionWalk{state: state, viewer: viewer, query: query}, nil
}

// SelectionActionColdSearch uses the exact shared predicate classifier; action
// preparations share its single cold-build admission lane.
func SelectionActionColdSearch(query JobQuery) (bool, error) {
	var node *BrowseNode
	var err error
	if len(query.Filter) > 0 {
		node, err = ParseBrowseQuery(query.Filter, "selector.query.filter")
		if err != nil {
			return false, err
		}
	}
	return selectionBatchHasLocalSearch(node), nil
}

// StepSelectionActionWalk consumes the shared raw-first walker, including its
// exact SQLite tokenizer, scalar reservation and complete witness. At most128
// qualifying IDs are retained transiently while individual facts are read.
func (s *Service) StepSelectionActionWalk(ctx context.Context, w *SelectionActionWalk, check func(*Service) error, reserve func(int64) error) (*SelectionActionWalk, *selectionactions.Target, bool, error) {
	if w == nil || w.state == nil || check == nil {
		return nil, nil, false, selectionactions.ErrInput
	}
	next := *w
	witness := func(read *Service) (string, error) {
		if err := check(read); err != nil {
			return "", err
		}
		return read.browseSelectionWitness(w.query.LibraryID, w.viewer.Profile, w.viewer.EffectiveRestrictions())
	}
	if next.nextMatch >= next.matchCount {
		if next.state.Complete() {
			return &next, nil, true, nil
		}
		state, work, err := s.StepBrowseSelectionBatch(ctx, next.state, witness, SelectionBatchLimits{ReserveTransient: reserve})
		if err != nil {
			return nil, nil, false, err
		}
		next.state = state
		next.matches = work.Matches
		next.matchCount = work.MatchCount
		next.nextMatch = 0
		if next.matchCount == 0 {
			return &next, nil, next.state.Complete(), nil
		}
	}
	id := next.matches[next.nextMatch]
	value := selectionactions.Target{EntityID: id, LibraryID: w.query.LibraryID}
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(inner context.Context) error {
		read := s.WithContext(inner)
		current, err := witness(read)
		if err != nil {
			return err
		}
		if current != w.query.SelectionBasis {
			return ErrSelectionBatchStale
		}
		var parent, added sql.NullInt64
		var titleBytes int
		err = read.read().QueryRow(`SELECT pid(e.public_id),CASE WHEN length(CAST(e.title AS BLOB))<=? THEN e.title ELSE '' END,k.name,e.parent_id,e.added_ms,length(CAST(e.title AS BLOB)) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=? AND l.library_id=? AND e.retired=0`, SelectionBatchMaximumBytes, id, w.query.LibraryID).Scan(&value.ItemID, &value.Title, &value.Kind, &parent, &added, &titleBytes)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSelectionBatchStale
		}
		if err != nil {
			return err
		}
		if titleBytes > SelectionBatchMaximumBytes || len(value.Title)+len(value.ItemID)+next.state.Bytes() > SelectionBatchMaximumBytes {
			return ErrSelectionBatchCapacity
		}
		var p any
		if parent.Valid {
			p = parent.Int64
		}
		value.Identity = selectionactions.TargetIdentity(value.ItemID, value.LibraryID, value.Kind, p)
		if added.Valid {
			value.Lifetime = strconv.FormatInt(added.Int64, 10)
		}
		current, err = witness(read)
		if err != nil {
			return err
		}
		if current != w.query.SelectionBasis {
			return ErrSelectionBatchStale
		}
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	next.nextMatch++
	return &next, &value, next.state.Complete() && next.nextMatch == next.matchCount, nil
}
