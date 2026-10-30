package catalog

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// BrowseSelectionConversion owns a delivery lease for a COMPLETE, small result
// retained by the original off-request count build. It never runs a census.
// Hold Release through hydration and response serialization.
type BrowseSelectionConversion struct {
	IDs      []int64
	Revision string
	Basis    string
	Ordered  bool
	lease    readprep.Result
}

func (c BrowseSelectionConversion) Release()                  { c.lease.Release() }
func (c BrowseSelectionConversion) Reserve(bytes int64) error { return c.lease.ResizeDelivery(bytes) }

func (s *Service) ResolvePreparedBrowseSelection(viewer Viewer, query JobQuery, revision string) (BrowseSelectionConversion, error) {
	return s.resolvePreparedBrowseSelection(viewer, query, revision, true)
}

func (s *Service) resolvePreparedBrowseSelection(viewer Viewer, query JobQuery, revision string, small bool) (out BrowseSelectionConversion, err error) {
	if dbwork.Snapshot(s.Context()) == nil || query.SelectionBasis == "" || revision == "" {
		return out, ErrStaleContinuation
	}
	if err = s.checkBrowseSelectionBasis(viewer, query.LibraryID, query.SelectionBasis); err != nil {
		return out, err
	}
	var node *BrowseNode
	if len(query.Filter) > 0 {
		if err = json.Unmarshal(query.Filter, &node); err != nil {
			return out, err
		}
	}
	if !selectionBatchHasLocalSearch(node) {
		return out, ErrSelectionBatchUnsupported
	}
	library, err := s.library(query.LibraryID)
	if err != nil {
		return out, err
	}
	pivot, ok := pivotForKind(library.Kind, query.Pivot)
	if !ok || !selectionLeafPivot(pivot) {
		return out, ErrSelectionBatchUnsupported
	}
	sorts, err := resolveBrowseSorts(pivot, query.Sort)
	if err != nil {
		return out, err
	}
	// Page revisions include route-private authority; the shared content key
	// deliberately omits them. The full live basis proves current membership,
	// and exact canonical query/policy equality below proves this count belongs
	// to the supplied query. Revision is an opaque transport echo for metadata
	// and jobs, never an authority or cardinality proof. Browse conversion also
	// validates its own route revision before returning the explicit page.
	if len(revision) != 71 || !strings.HasPrefix(revision, "browse_") {
		return out, ErrStaleContinuation
	}
	if _, err = hex.DecodeString(revision[7:]); err != nil {
		return out, ErrStaleContinuation
	}

	raw, err := json.Marshal(node)
	if err != nil {
		return out, err
	}
	spec, err := json.Marshal(selectionPreparationSpec{Library: query.LibraryID, Pivot: query.Pivot, Query: raw, Sorts: sorts, Viewer: selectionPreparationViewer(viewer, query.LibraryID)})
	if err != nil {
		return out, err
	}
	store := s.ReadPreparations()
	if store == nil {
		return out, readprep.ErrStale
	}
	key := readprep.Key{Namespace: browseSelectionPreparationDomain, Basis: query.SelectionBasis, Canonical: string(spec)}
	// Opening a private lease on existing exact content does not submit work.
	// It grants no authority: scope, query, current witness and viewer were all
	// checked above in this snapshot, and HTTP checks live authority again.
	caller := readprep.Caller("browse-conversion/" + operationHash(viewer))
	h, err := store.Open(s.Context(), caller, key)
	if err != nil {
		return out, err
	}
	defer func() { _ = store.Cancel(context.WithoutCancel(s.Context()), caller, h) }()
	result, err := store.Poll(s.Context(), caller, h)
	if err != nil {
		return out, err
	}
	keep := false
	defer func() {
		if !keep {
			result.Release()
		}
	}()
	if result.State != readprep.Ready {
		return out, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(8*len(result.Payload) + 65536)); err != nil {
		return out, err
	}
	var content selectionPreparationContent
	if err = json.Unmarshal(result.Payload, &content); err != nil {
		return out, err
	}
	if content.Ordered && len(content.Sorts) == 1 && content.Sorts[0].Field == "forYou" && content.Profile != viewer.Profile {
		return out, ErrStaleContinuation
	}
	if err = s.checkSmallForYouActivityContent(content, func(n int64) error { return result.ResizeDelivery(int64(8*len(result.Payload)+65536) + n) }); err != nil {
		return out, err
	}
	total := 0
	for _, counts := range content.Counts {
		if counts[0] < 0 || counts[1] < 0 || counts[1] > counts[0] {
			return out, readprep.ErrInvalid
		}
		total += counts[0]
	}
	if small && total > BrowseSelectionConversionLimit {
		return out, fmt.Errorf("%w: explicit conversion exceeds %d", ErrSelectionBatchUnsupported, BrowseSelectionConversionLimit)
	}
	if content.Library != query.LibraryID || content.Pivot != query.Pivot || min(total, 201) != len(content.IDs) {
		return out, ErrStaleContinuation
	}
	seen := map[int64]bool{}
	for _, id := range content.IDs {
		if id <= 0 || seen[id] {
			return out, readprep.ErrInvalid
		}
		seen[id] = true
	}
	keep = true
	return BrowseSelectionConversion{IDs: content.IDs, Revision: revision, Basis: query.SelectionBasis, Ordered: content.Ordered, lease: result}, nil
}

func (s *Service) browsePreparedSelection(viewer Viewer, request BrowseRequest) (BrowseResult, error) {
	out := BrowseResult{Entries: []ContentEntry{}, PositionIndex: []BrowsePositionAnchor{}}
	if request.Cursor != "" || request.Seek != nil || request.HomeRowDisplay != "" || request.Range == nil || request.Range.Start != 0 || request.Range.Revision == "" {
		return out, ErrSelectionBatchUnsupported
	}
	filter, err := json.Marshal(request.Query)
	if err != nil {
		return out, err
	}
	converted, err := s.ResolvePreparedBrowseSelection(viewer, JobQuery{LibraryID: request.Library, Pivot: request.Pivot, Filter: filter, Sort: request.Sort, SelectionBasis: request.SelectionBasis}, request.Range.Revision)
	if err != nil {
		return out, err
	}
	keep := false
	defer func() {
		if !keep {
			converted.Release()
		}
	}()
	if request.Limit < len(converted.IDs) {
		return out, ErrSelectionBatchUnsupported
	}
	ids, err := json.Marshal(converted.IDs)
	if err != nil {
		return out, err
	}
	library, err := s.library(request.Library)
	if err != nil {
		return out, err
	}
	pivot, _ := pivotForKind(library.Kind, request.Pivot)
	sorts, err := resolveBrowseSorts(pivot, request.Sort)
	if err != nil {
		return out, err
	}
	liveRevision, err := s.browseRevision(BrowseRequest{Library: request.Library, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.EffectiveRestrictions(), Query: request.Query}, pivot, sorts)
	if err != nil {
		return out, err
	}
	if liveRevision != request.Range.Revision {
		return out, ErrStaleContinuation
	}
	if !localSearchConversionOrderSupported(request.Query, sorts) {
		return out, ErrSelectionBatchUnsupported
	}
	var order, sizeExpr string
	var orderArgs, sizeArgs []any
	if sorts[0].Field == "groupOrder" || sorts[0].Field == "forYou" {
		if !converted.Ordered {
			return out, ErrStaleContinuation
		}
		order, sizeExpr = "picked.key ASC", "''"
	} else {
		order, orderArgs, sizeExpr, sizeArgs, err = preparedSelectionOrder(viewer.Profile, sorts[0])
		if err != nil {
			return out, err
		}
	}
	// This endpoint is a complete bounded action conversion. Its consumer needs
	// canonical identities/titles, not media details, asset lists or artwork.
	// Preflight all variable row/order bytes on <=200 singleton identities before
	// allocating any Go row, entry or encoding buffer. One SQLite cell remains
	// the documented indivisible O(B) input probe; no native text is retained.
	var sourceBytes int64
	sizeSQL := `SELECT COALESCE(sum(length(CAST(e.title AS BLOB))+length(CAST(e.sort_key AS BLOB))+length(CAST(COALESCE(e.added_text,'') AS BLOB))+COALESCE(length(CAST((` + sizeExpr + `) AS BLOB)),0)),0) FROM json_each(?) picked CROSS JOIN catalog_browse_rows e ON e.entity_id=picked.value`
	// SELECT-expression parameters precede the bounded JSON driver parameter.
	sizeArgs = append(sizeArgs, string(ids))
	if err = s.read().QueryRow(sizeSQL, sizeArgs...).Scan(&sourceBytes); err != nil {
		return out, err
	}
	fixed := int64(len(converted.IDs))*(8*int64(unsafe.Sizeof(ContentEntry{}))+4*int64(unsafe.Sizeof(browseRow{}))+2048) + 65536
	if sourceBytes < 0 || sourceBytes > (32<<20-fixed)/16 {
		return out, readprep.ErrCapacity
	}
	if err = converted.Reserve(fixed + 16*sourceBytes); err != nil {
		return out, err
	}
	args := append([]any{string(ids)}, orderArgs...)
	rows, err := s.queryBrowseRows(`SELECT `+browseRowColumns+` FROM json_each(?) picked CROSS JOIN catalog_browse_rows e ON e.entity_id=picked.value CROSS JOIN catalog_entities ce ON ce.id=e.entity_id ORDER BY `+order, args...)
	if err != nil {
		return out, err
	}
	if len(rows) != len(converted.IDs) {
		return out, ErrStaleContinuation
	}
	out.Entries = make([]ContentEntry, 0, len(rows))
	for _, row := range rows {
		var missing bool
		if err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_missing_titles m WHERE m.entity_id=? AND m.missing_since_ms IS NOT NULL)`, row.entity).Scan(&missing); err != nil {
			return out, err
		}
		out.Entries = append(out.Entries, ContentEntry{ID: row.id, Kind: row.kind, Title: row.title, LibraryID: request.Library, Missing: missing})
	}
	out.Pivot = request.Pivot
	out.Applied = BrowseApplied{Query: request.Query, Sort: sorts}
	out.PageInfo = BrowsePageInfo{Total: len(rows), Revision: converted.Revision}
	// This is a selection conversion, not a new browse window. Its duration is
	// unused by the explicit action paths and no whole-query duration runs.
	out.selectionRelease = converted.Release
	keep = true
	return out, nil
}

// The same maintained scalar/value-rest semantics used by ordinary Browse and
// Queue. Group/ForYou orders are deliberately not routed through this helper.
func preparedSelectionOrder(profile string, sort BrowseSortSelection) (string, []any, string, []any, error) {
	d, ok := browseSortByID(sort.Field)
	if !ok {
		return "", nil, "", nil, ErrSelectionBatchUnsupported
	}
	direction := "ASC"
	if sort.Direction == "desc" {
		direction = "DESC"
	}
	if !d.Profile {
		return d.Expression + " " + direction + ",e.entity_id " + direction, nil, d.Expression, nil, nil
	}
	column, zero := "", "0"
	switch sort.Field {
	case "personalRating":
		column = "rating"
	case "lastPlayed":
		column = "last_played_at"
		zero = "''"
	case "playCount":
		column = "play_count"
	default:
		return "", nil, "", nil, ErrSelectionBatchUnsupported
	}
	expr := "COALESCE((SELECT p." + column + " FROM catalog_personal_sort_rows p WHERE p.profile_id=? AND p.entity_id=e.entity_id)," + zero + ")"
	condition := expr + ">" + zero
	valued, rest := "1", "0"
	if sort.Direction == "desc" {
		valued, rest = "0", "1"
	}
	order := "CASE WHEN " + condition + " THEN " + valued + " ELSE " + rest + " END ASC,CASE WHEN " + condition + " THEN " + expr + " END " + direction + ",CASE WHEN " + condition + " THEN e.entity_id END " + direction + ",e.sort_key COLLATE NOCASE ASC,e.entity_id ASC"
	return order, []any{profile, profile, profile, profile}, expr, []any{profile}, nil
}
