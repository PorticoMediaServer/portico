package reading

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// IndexedBodySearch is the private global Books body-search request. Visible is
// the caller's actual ready Reading membership and restriction predicate over
// the item column; Fence is computed in the query snapshot from the ready
// library set/revisions and the live viewer/policy/restriction facts.
type IndexedBodySearch struct {
	Query     string
	Limit     int
	Cursor    string
	Library   string
	Principal identity.Principal
	Visible   Visibility
	Fence     func(ctx context.Context, tx *sql.Tx) (string, error)
	Authorize func(ctx context.Context, tx *sql.Tx, itemID int64) (bool, error)
}

// IndexedBodyHit is one public body hit. EntityID/AssetID/ObjectID/
// InventoryRevision are the exact internal index tuple and are never encoded in
// the response; ItemID/SourceID/SourceRevision are the public identity and the
// exact selection fence the hit was resolved at.
type IndexedBodyHit struct {
	EntityID          int64
	AssetID           int64
	ObjectID          string
	InventoryRevision string
	LibraryID         string
	ItemID            string
	SourceID          string
	SourceRevision    string
	Title             string
	CoverURL          string
	SectionPath       string
	SectionIndex      int
	UTF16Offset       int
	Excerpt           string
	MatchStart        int
	MatchLength       int
}

type IndexedBodyPage struct {
	Hits          []IndexedBodyHit
	Next          string
	IndexRevision int64
	Fence         string
}

// SearchIndexedBody answers one indexed-only body page. It searches only the
// current canonical primary version whose exact observation has a complete
// index; the ready-membership/restriction predicate is in the query before any
// ordering or limit. Public identity, title and cover are resolved for the
// page's exact tuples inside the same snapshot in one batch; no source bytes,
// session, detail read or per-hit artwork read is opened.
func (s *Service) SearchIndexedBody(ctx context.Context, search IndexedBodySearch) (IndexedBodyPage, error) {
	var out IndexedBodyPage
	if s == nil || s.DB == nil {
		return out, ErrUnavailable
	}
	if search.Visible == nil || search.Fence == nil || search.Authorize == nil {
		return out, ErrInvalid
	}
	if search.Limit < 1 || search.Limit > 50 {
		return out, ErrInvalid
	}
	result, err := s.SearchTextIndex(ctx, TextIndexSearch{
		Query:     search.Query,
		Limit:     search.Limit,
		Cursor:    search.Cursor,
		FenceFunc: search.Fence,
		Principal: search.Principal,
		Visible:   search.Visible,
		Authorize: search.Authorize,
		Project:   s.projectIndexedBodyHits,
	})
	if err != nil {
		return out, err
	}
	if len(result.Hits) != len(result.Projected) {
		return out, ErrChanged
	}
	out.Hits = result.Projected
	out.Next = result.Next
	out.IndexRevision = result.IndexRevision
	out.Fence = result.Fence
	return out, nil
}

// RecheckIndexedBody repeats, in one fresh snapshot, the page's ready
// membership/restriction admission and its exact source tuples. A restriction,
// library or source change after the page refuses the whole page instead of
// publishing it under stale authority or a replaced observation.
func (s *Service) RecheckIndexedBody(ctx context.Context, p identity.Principal, visible Visibility, hits []IndexedBodyHit) error {
	if s == nil || s.DB == nil || visible == nil {
		return ErrUnavailable
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	defer g.Rollback()
	tx := g.Tx()
	ids := make([]any, 0, len(hits))
	marks := make([]string, 0, len(hits))
	seenItem := map[int64]bool{}
	for _, hit := range hits {
		if !seenItem[hit.EntityID] {
			seenItem[hit.EntityID] = true
			ids = append(ids, hit.EntityID)
			marks = append(marks, "?")
		}
	}
	filter, filterArgs, err := visible(ctx, tx, p, "e.id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(filter) == "" {
		return identity.ErrForbidden
	}
	if len(hits) == 0 {
		// The live actor/scope proof has run; an admitted empty page is a real
		// answer, not an unproven absence.
		return nil
	}
	query := `SELECT e.id FROM catalog_entities e WHERE e.id IN (` + strings.Join(marks, ",") + `) AND (` + filter + `)`
	args := append(append([]any{}, ids...), filterArgs...)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	admitted := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		admitted[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range seenItem {
		if !admitted[id] {
			return ErrChanged
		}
	}
	tuples := make([]textIndexTupleKey, 0, len(hits))
	for _, hit := range hits {
		tuples = append(tuples, textIndexTupleKey{hit.EntityID, hit.AssetID, hit.ObjectID, hit.InventoryRevision})
	}
	resolved, err := s.resolveIndexedTuples(ctx, tx, tuples)
	if err != nil {
		return err
	}
	for _, hit := range hits {
		value, ok := resolved[textIndexTupleKey{hit.EntityID, hit.AssetID, hit.ObjectID, hit.InventoryRevision}]
		if !ok || value.fence != hit.SourceRevision || value.publicID != hit.ItemID ||
			value.token != hit.SourceID || value.library != hit.LibraryID {
			return ErrChanged
		}
	}
	return nil
}

type textIndexTupleKey struct {
	item, asset int64
	object, rev string
}

type textIndexTupleValue struct {
	library, publicID, token, fence, title string
}

// projectIndexedBodyHits resolves the page's exact tuples in one bounded query
// plus one bounded cover batch, all in the query snapshot.
func (s *Service) projectIndexedBodyHits(ctx context.Context, tx *sql.Tx, hits []TextIndexHit) ([]IndexedBodyHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	if len(hits) > 50 {
		return nil, ErrLimit
	}
	seen := map[textIndexTupleKey]bool{}
	tuples := make([]textIndexTupleKey, 0, len(hits))
	seenItem := map[int64]bool{}
	items := make([]int64, 0, len(hits))
	for _, hit := range hits {
		key := textIndexTupleKey{hit.ItemID, hit.AssetID, hit.ObjectID, hit.Revision}
		if !seen[key] {
			seen[key] = true
			tuples = append(tuples, key)
		}
		if !seenItem[hit.ItemID] {
			seenItem[hit.ItemID] = true
			items = append(items, hit.ItemID)
		}
	}
	resolved, err := s.resolveIndexedTuples(ctx, tx, tuples)
	if err != nil {
		return nil, err
	}
	covers, err := compactcatalog.ReadingCoverQueriesTx(ctx, tx, items)
	if err != nil {
		return nil, err
	}
	out := make([]IndexedBodyHit, 0, len(hits))
	for _, hit := range hits {
		value, ok := resolved[textIndexTupleKey{hit.ItemID, hit.AssetID, hit.ObjectID, hit.Revision}]
		if !ok {
			return nil, ErrChanged
		}
		cover := ""
		if digest := covers[hit.ItemID]; digest != "" {
			cover = "/v1/items/" + url.PathEscape(value.publicID) + "/art/cover?" + digest
		}
		out = append(out, IndexedBodyHit{
			EntityID:          hit.ItemID,
			AssetID:           hit.AssetID,
			ObjectID:          hit.ObjectID,
			InventoryRevision: hit.Revision,
			LibraryID:         value.library,
			ItemID:            value.publicID,
			SourceID:          value.token,
			SourceRevision:    value.fence,
			Title:             value.title,
			CoverURL:          cover,
			SectionPath:       hit.SectionPath,
			SectionIndex:      hit.SectionIndex,
			UTF16Offset:       hit.UTF16Offset,
			Excerpt:           hit.Excerpt,
			MatchStart:        hit.MatchStart,
			MatchLength:       hit.MatchLength,
		})
	}
	return out, nil
}

// resolveIndexedTuples selects the exact (item, asset, inventory object,
// inventory revision) tuples in one bounded query and computes the same public
// selection fence a session would use.
func (s *Service) resolveIndexedTuples(ctx context.Context, tx *sql.Tx, tuples []textIndexTupleKey) (map[textIndexTupleKey]textIndexTupleValue, error) {
	out := map[textIndexTupleKey]textIndexTupleValue{}
	if len(tuples) == 0 {
		return out, nil
	}
	if len(tuples) > 50 {
		return out, ErrLimit
	}
	parts := make([]string, 0, len(tuples))
	args := make([]any, 0, len(tuples)*4)
	for _, tuple := range tuples {
		parts = append(parts, "(e.id=? AND p.asset_id=? AND o.id=? AND o.revision=?)")
		args = append(args, tuple.item, tuple.asset, tuple.object, tuple.rev)
	}
	query := `SELECT ` + publicationFields + `,pid(e.public_id) FROM catalog_entities e` + publicationJoins + `
 WHERE (` + strings.Join(parts, " OR ") + `) AND ` + publicationEligible + `
 AND p.asset_id=` + s.primaryPublicationSQL() + `
 AND o.id=` + publicationChosenObject
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var selection Selection
		var publicID string
		columns := append(selectionColumns(&selection), &publicID)
		if err = rows.Scan(columns...); err != nil {
			return out, err
		}
		key := textIndexTupleKey{selection.Entity, selection.Asset, selection.inventoryRow, selection.inventory}
		out[key] = textIndexTupleValue{
			library:  selection.library,
			publicID: publicID,
			token:    selection.token,
			fence:    selection.fence(),
			title:    selection.Title,
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}
