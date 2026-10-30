package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

const queueSelectionRevisionExpression = `^browse_[a-f0-9]{64}$`

var queueSelectionRevisionPattern = regexp.MustCompile(queueSelectionRevisionExpression)

// QueueQuery uses the browse expression and ordering vocabulary. Paging belongs
// to durable queue capture, so cursors, ranges and page sizes are absent.
type QueueQuery struct {
	SelectionBasis    string                `json:"selectionBasis,omitempty"`
	SelectionRevision string                `json:"selectionRevision,omitempty"`
	LibraryID         string                `json:"libraryId"`
	Pivot             string                `json:"pivot,omitempty"`
	Query             *BrowseNode           `json:"query,omitempty"`
	Sort              []BrowseSortSelection `json:"sort,omitempty"`
}

// JSONSchema publishes the same optional-but-nonempty proof the decoder accepts.
func (QueueQuery) JSONSchema() map[string]any {
	type wire QueueQuery
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	proof := shape["properties"].(map[string]any)["selectionRevision"].(map[string]any)
	proof["pattern"] = queueSelectionRevisionExpression
	shape["properties"].(map[string]any)["selectionBasis"].(map[string]any)["pattern"] = SelectionBasisExpression
	return shape
}

type QueueSelection struct {
	// Iterator replaces SQL only for bounded local-search replay. It owns no
	// transaction; Close releases its private shared-store handle/delivery.
	Iterator QueueKeyIterator
	// CheckBasis is ephemeral content proof, checked in each fresh capture/commit snapshot.
	CheckBasis func(context.Context, *sql.Tx) error
	SQL        string
	Args       []any
	Revision   string
	Label      string
}

// QueueKeysTx compiles a saved resource or browse query on the capture's own
// read snapshot. Containers expand in browse order, then their listening or
// episode order; overlapping containers include each playable title once.
// rows holds the members of any Home row the query names, resolved through
// Home by the caller when Play was pressed (at most a row's 500 titles).
func (s *Service) QueueKeysTx(ctx context.Context, tx *sql.Tx, viewer Viewer, actor ResourceActor, query *QueueQuery, saved string, rows map[string][]int64) (QueueSelection, error) {
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	s = &clone
	if query != nil {
		if saved != "" && query.SelectionBasis != "" {
			return QueueSelection{}, ErrStaleContinuation
		}
		if err := s.checkBrowseSelectionBasis(viewer, query.LibraryID, query.SelectionBasis); err != nil {
			return QueueSelection{}, err
		}
	}
	prefix, label := "", ""
	if saved != "" {
		role, kind, revision, deleted, err := savedResourceRole(s.read(), saved, actor)
		if err != nil || deleted || role == "" {
			return QueueSelection{}, sql.ErrNoRows
		}
		prefix = saved + ":" + strconv.FormatInt(revision, 10) + ":"
		var definition string
		if err = tx.QueryRowContext(ctx, `SELECT name,definition FROM saved_resources WHERE id=?`, saved).Scan(&label, &definition); err != nil {
			return QueueSelection{}, err
		}
		if kind == "collection" {
			fence, err := s.bulkSelectionFence(tx, viewer, JobSelector{})
			if err != nil {
				return QueueSelection{}, err
			}
			prefix += fence + ":"
			// A collection plays in the order it is shown: its own.
			visible, args := viewer.itemVisibilitySQL("i.id")
			args = append([]any{saved}, args...)
			return QueueSelection{SQL: `SELECT pid(i.public_id) FROM saved_resource_entries entry JOIN catalog_entities i ON i.id=entry.item_id JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 WHERE entry.resource_id=? AND ` + visible + recordingsClause("i.id", viewer.Restrictions) + ` ORDER BY entry.order_key,entry.id`, Args: args, Revision: prefix + viewer.Fence, Label: label}, nil
		}
		var d SavedViewDefinition
		if json.Unmarshal([]byte(definition), &d) != nil || len(validateSavedView(s.read(), d)) != 0 {
			return QueueSelection{}, browseIssue("saved", "review this saved view before playing it")
		}
		query = &QueueQuery{LibraryID: d.LibraryID, Pivot: d.Pivot, Query: d.Query, Sort: d.Sort}
	}
	if query != nil && query.LibraryID == AcrossLibraries {
		if selectionBatchHasLocalSearch(query.Query) {
			return QueueSelection{}, ErrSelectionBatchUnsupported
		}
		return s.acrossQueue(ctx, tx, viewer, query, rows)
	}
	if query == nil || !viewer.AllowsLibrary(query.LibraryID) {
		return QueueSelection{}, sql.ErrNoRows
	}
	lib, err := s.library(query.LibraryID)
	if err != nil {
		return QueueSelection{}, err
	}
	pivotID := query.Pivot
	if pivotID == "" {
		pivotID = defaultPivot(lib.Kind)
	}
	pivot, ok := pivotForKind(lib.Kind, pivotID)
	if !ok || !pivot.Browsable {
		return QueueSelection{}, ErrBrowsePivot
	}
	if err = s.browseReady(query.LibraryID); err != nil {
		return QueueSelection{}, err
	}
	sorts, err := resolveBrowseSorts(pivot, query.Sort)
	if err != nil {
		return QueueSelection{}, err
	}
	raw, err := json.Marshal(query.Query)
	if err != nil {
		return QueueSelection{}, err
	}
	node, err := ParseBrowseQuery(raw, "query")
	if err != nil {
		return QueueSelection{}, err
	}
	if selectionBatchHasLocalSearch(node) {
		if !LocalSearchQueueSupported(node, sorts) {
			return QueueSelection{}, ErrSelectionBatchUnsupported
		}
		return s.localOrderedQueue(ctx, tx, viewer, query, node, pivot, sorts, prefix, label, lib.Name)
	}
	where, args := browseScopeSQL(query.LibraryID, pivot)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: viewer.Restrictions, rows: rows}
	clause, err := compiler.compile(node)
	if err != nil {
		return QueueSelection{}, err
	}
	where += ` AND ` + clause
	args = append(args, compiler.args...)
	restriction, values := EntityRestrictionSQL("e.entity_id", viewer.Restrictions)
	where += ` AND ` + restriction
	args = append(args, values...)
	order, orderArgs, err := s.queueBrowseOrder(viewer, query, sorts, where, args, rows)
	if err != nil {
		return QueueSelection{}, err
	}
	// ORDER BY's placeholders precede WHERE's in the ranked SELECT.
	args = append(orderArgs, args...)
	visible, values := viewer.itemVisibilitySQL("i.id")
	args = append(args, values...)
	revision, err := s.browseRevision(BrowseRequest{Library: query.LibraryID, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.EffectiveRestrictions(), Query: node, Rows: rows}, pivot, sorts)
	if err != nil {
		return QueueSelection{}, err
	}
	sql := `WITH ranked AS (SELECT e.entity_id,row_number() OVER(ORDER BY ` + order + `) AS ordinal FROM catalog_browse_rows e WHERE ` + where + `), members AS (
 SELECT i.id,min(r.ordinal) AS ordinal FROM ranked r JOIN catalog_browse_memberships bm ON bm.entity_id=r.entity_id JOIN catalog_entities i ON i.id=bm.item_id JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1
 WHERE ` + visible + recordingsClause("i.id", viewer.Restrictions) + ` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) GROUP BY i.id)
 SELECT pid(i.public_id) FROM members m JOIN catalog_entities i ON i.id=m.id JOIN ranked parent ON parent.ordinal=m.ordinal
 LEFT JOIN catalog_collection_members collection ON collection.collection_id=parent.entity_id AND collection.item_id=i.id
 LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id LEFT JOIN catalog_seasons season ON season.entity_id=ep.season_id
 LEFT JOIN catalog_songs song ON song.entity_id=i.id LEFT JOIN catalog_entities album ON album.id=song.album_id LEFT JOIN catalog_book_files part ON part.entity_id=i.id
 ORDER BY m.ordinal,COALESCE(collection.order_key,''),COALESCE(season.number,1),CASE WHEN ep.numbering='date' THEN COALESCE(ep.air_date,0) ELSE COALESCE(ep.number,0) END,COALESCE(album.year,0),COALESCE(album.sort_key,''),COALESCE(album.id,0),COALESCE(song.disc_number,part.disc_number,1),COALESCE(song.track_number,part.part_number,0),i.id`
	if kinds := entityKindMembers(pivot); len(kinds) == 1 && (kinds[0] == "movie" || kinds[0] == "episode" || kinds[0] == "song" || kinds[0] == "audiobook_file") {
		filterArgs := append([]any{}, args[len(orderArgs):]...)
		sql = `SELECT pid(i.public_id) FROM catalog_browse_rows e JOIN catalog_entities i ON i.id=e.entity_id WHERE ` + where + ` AND ` + visible + recordingsClause("i.id", viewer.Restrictions) + ` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) ORDER BY ` + order
		args = append(filterArgs, orderArgs...)
	}
	sql, err = compactcatalog.SearchServingSQL(ctx, tx, compactcatalog.ServingSQL(sql))
	if label == "" {
		label = s.queueGroupLabel(node)
	}
	if label == "" {
		label = lib.Name
	}
	return QueueSelection{SQL: sql, Args: args, Revision: prefix + revision, Label: label, CheckBasis: s.selectionBasisCheck(viewer, query.LibraryID, query.SelectionBasis)}, err
}

func (q *QueueQuery) UnmarshalJSON(raw []byte) error {
	var body struct {
		SelectionBasis    string                `json:"selectionBasis"`
		SelectionRevision json.RawMessage       `json:"selectionRevision"`
		LibraryID         string                `json:"libraryId"`
		Pivot             string                `json:"pivot"`
		Query             json.RawMessage       `json:"query"`
		Sort              []BrowseSortSelection `json:"sort"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > BrowseMaximumBytes {
		return browseIssue("query", "this query is too large")
	}
	if err := decoder.Decode(&body); err != nil {
		return err
	}
	if err := ValidateSelectionBasisJSON(raw); err != nil {
		return err
	}
	node, err := ParseBrowseQuery(body.Query, "query")
	if err != nil {
		return err
	}
	var selectionRevision string
	if body.SelectionRevision != nil && (json.Unmarshal(body.SelectionRevision, &selectionRevision) != nil || !queueSelectionRevisionPattern.MatchString(selectionRevision)) {
		return browseIssue("selectionRevision", "refresh the selected view")
	}
	if body.LibraryID == "" || len(body.LibraryID) > 256 || len(body.Pivot) > 64 {
		return browseIssue("libraryId", "choose a library")
	}
	*q = QueueQuery{SelectionBasis: body.SelectionBasis, SelectionRevision: selectionRevision, LibraryID: body.LibraryID, Pivot: body.Pivot, Query: node, Sort: body.Sort}
	return nil
}

func (s *Service) queueBrowseOrder(viewer Viewer, query *QueueQuery, sorts []BrowseSortSelection, where string, args []any, rows map[string][]int64) (string, []any, error) {
	first := sorts[0]
	direction, op := "ASC", "<"
	if first.Direction == "desc" {
		direction, op = "DESC", ">"
	}
	if first.Field == "random" {
		return `CASE WHEN e.random_key` + op + `? THEN 1 ELSE 0 END,e.random_key ` + direction + `,e.entity_id ` + direction, []any{int64(first.Seed)}, nil
	}
	if first.Field == "forYou" {
		var total int
		if err := s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+where, args...).Scan(&total); err != nil {
			return "", nil, err
		}
		valued, err := s.recForYouValued(pageShape{request: BrowseRequest{Library: query.LibraryID, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.Restrictions}, where: where, args: args, total: total})
		if err != nil {
			return "", nil, err
		}
		ids := make([]int64, 0, len(valued))
		for _, row := range valued {
			ids = append(ids, row.entity)
		}
		return `COALESCE((SELECT CAST(key AS INTEGER) FROM json_each(?) rank WHERE rank.value=e.entity_id),2147483647),e.sort_key COLLATE NOCASE,e.entity_id`, []any{idsJSON64(ids)}, nil
	}
	if first.Field == "groupOrder" {
		// The group's own order (a playlist's, a season's, an album's): its
		// members' places, read from the group and bounded by it.
		source, sourceArgs, err := s.groupOrderSource(groupOrderTarget(query.Query), rows)
		if err != nil {
			return "", nil, err
		}
		found, err := s.read().Query(`SELECT id FROM (`+source+`) ORDER BY pos `+direction+`,id `+direction, sourceArgs...)
		if err != nil {
			return "", nil, err
		}
		ids := []int64{}
		for found.Next() {
			var id int64
			if err = found.Scan(&id); err != nil {
				found.Close()
				return "", nil, err
			}
			ids = append(ids, id)
		}
		err = found.Err()
		found.Close()
		if err != nil {
			return "", nil, err
		}
		return `COALESCE((SELECT CAST(key AS INTEGER) FROM json_each(?) rank WHERE rank.value=e.entity_id),2147483647),e.entity_id`, []any{idsJSON64(ids)}, nil
	}
	definition, _ := browseSortByID(first.Field)
	if definition.Profile {
		column, zero, _ := personalSortColumn(first.Field)
		value := `COALESCE((SELECT p.` + column + ` FROM catalog_personal_sort_rows p WHERE p.profile_id=? AND p.entity_id=e.entity_id),` + zero + `)`
		return `(` + value + `>` + zero + `) ` + direction + `,` + value + ` ` + direction + `,CASE WHEN ` + value + `>` + zero + ` THEN NULL ELSE e.sort_key END COLLATE NOCASE,CASE WHEN ` + value + `>` + zero + ` THEN e.entity_id END ` + direction + `,e.entity_id`, []any{viewer.Profile, viewer.Profile, viewer.Profile, viewer.Profile}, nil
	}
	order, values := browseOrderSQL(viewer.Profile, sorts)
	return order, values, nil
}

// queueGroupLabel names a queue played from one of a show's episode groups
// after the show, as a season's queue is; "" for anything else.
func (s *Service) queueGroupLabel(node *BrowseNode) string {
	target := groupOrderTarget(node)
	if target == nil || target.Field != "episodeGroup" {
		return ""
	}
	show, _, ok := splitEpisodeGroup(target.Value)
	if !ok {
		return ""
	}
	var title string
	_ = s.read().QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, show).Scan(&title)
	return title
}
