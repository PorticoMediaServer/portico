package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SavedGroupAdmission must rehydrate the original normal profile and its live
// library/restriction scope in this exact snapshot, before private references
// are read. A browse-only Viewer is insufficient personal-state authority.
type SavedGroupAdmission func(context.Context, *sql.Tx) (Viewer, error)

func (s *Service) SavedWithReadingGroups(r ContentRequest, libraries []string, admit SavedGroupAdmission) (SavedContentEnvelope, error) {
	r.ReadingPersonalAdmission = admit
	if dbwork.Snapshot(s.Context()) == nil {
		var out SavedContentEnvelope
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).SavedWithReadingGroups(r, libraries, admit)
			return err
		})
		return out, err
	}
	if admit == nil || r.View != "watchlist" {
		out, err := s.Saved(r, libraries)
		return SavedContentEnvelope{ContentEnvelope: out}, err
	}
	viewer, err := admit(s.Context(), s.readTxOrSnapshot())
	if err != nil {
		return SavedContentEnvelope{}, err
	}
	if viewer.Profile == "" || viewer.Profile != r.Profile || viewer.Fence != r.ViewerFence {
		return SavedContentEnvelope{}, identity.ErrUnauthorized
	}
	r.Viewer = viewer
	r = r.scoped()
	libraries = viewer.Libraries
	var order string
	r, order, _, err = normalizeSavedRequest(r)
	if err != nil {
		return SavedContentEnvelope{}, err
	}
	groups, err := s.savedGroupContext(r)
	if err != nil {
		return SavedContentEnvelope{}, err
	}
	if groups.Count == 0 {
		out, err := s.Saved(r, libraries)
		return SavedContentEnvelope{ContentEnvelope: out}, err
	}
	// A group has no leaf read/progress/favorite flag. Such filters select only
	// ordinary entries, so the leaf page itself is the answer, while All and
	// poster counts still include private refs. Otherwise the leaf metadata and
	// counts come from the identical snapshot and predicates, and the union
	// below alone chooses this page.
	leafPage := r.Filter != "all" || r.Shape == "squares"
	metadataRequest := r
	if !leafPage {
		metadataRequest.Cursor = ""
	}
	base, err := s.saved(metadataRequest, libraries, !leafPage)
	if err != nil {
		return SavedContentEnvelope{}, err
	}
	out := SavedContentEnvelope{ContentEnvelope: base, SavedRevision: groups.Revision}
	if base.ShapeCounts == nil {
		return out, ErrReadingProjectionChanged
	}
	out.ShapeCounts.Posters += int(groups.Count)
	if r.Shape != "squares" {
		for i := range out.Filters {
			for j := range out.Filters[i].Options {
				if out.Filters[i].Options[j].ID == "all" {
					out.Filters[i].Options[j].Count += int(groups.Count)
				}
			}
		}
	}
	if leafPage {
		return out, nil
	}
	out.Query.Limit = r.Limit
	out.Sections = []ContentSection{}
	out.Empty = nil
	readingScope, err := s.savedReading(r, libraries, "watchlisted")
	if err != nil {
		return out, err
	}
	leafBase, leafArgs := savedLeafBase(r, libraries, "watchlisted", readingScope)
	cte, args := savedMixedCTE(r, groups, leafBase, leafArgs, order)
	// Full-group consistency is checked independently of LIMIT and the anchor,
	// in the same expansion of the group members as the candidate count.
	var invalid, count int
	if err = s.read().QueryRow(cte+` SELECT (SELECT count(*) FROM groups WHERE hidden=0 AND (series_id='' OR series_id<>last_id OR title='' OR total<1 OR total>9007199254740991)),(SELECT count(*) FROM candidates)`, args...).Scan(&invalid, &count); err != nil {
		return out, err
	}
	if invalid != 0 {
		return out, ErrReadingProjectionChanged
	}
	scope := cursorScope{Profile: r.Profile, View: r.View, Section: "saved-reading-groups-v1", Library: r.ServerID, Viewer: r.ViewerFence, Sort: r.Sort, Direction: r.Direction, Category: r.Filter + ":" + r.Shape, Limit: r.Limit, Search: readingScope.fence, Collection: groups.Fence}
	expiry := time.Now().Add(30 * time.Minute).Unix()
	where := ""
	pageArgs := append([]any{}, args...)
	op := ">"
	if r.Direction == "desc" {
		op = "<"
	}
	if r.Cursor != "" {
		cursor, err := s.readSignedBrowseCursor(r.Cursor)
		if err != nil {
			return out, err
		}
		if cursor.Scope != scope || cursor.Expires < time.Now().Unix() || cursor.CatalogRevision != base.Revision.Catalog || cursor.ViewerRevision != base.Revision.Viewer {
			return out, ErrCursor
		}
		anchor, err := decodeSavedMixedAnchor(cursor.ID)
		if err != nil {
			return out, err
		}
		var actual string
		anchorArgs := append(append([]any{}, args...), anchor.Kind, anchor.Entity, anchor.Reference)
		if err = s.read().QueryRow(cte+` SELECT order_value FROM candidates WHERE card_kind=? AND entity_id=? AND reference_id=?`, anchorArgs...).Scan(&actual); err != nil {
			if err == sql.ErrNoRows {
				return out, ErrCursor
			}
			return out, err
		}
		if savedMixedOrderDigest(actual) != cursor.Value {
			return out, ErrCursor
		}
		value, err := cursorOrderValue(actual, savedNumericSorts[r.Sort])
		if err != nil {
			return out, err
		}
		where = ` WHERE (order_value,card_kind,entity_id,reference_id)` + op + `(?,?,?,?)`
		pageArgs = append(pageArgs, value, anchor.Kind, anchor.Entity, anchor.Reference)
		expiry = cursor.Expires
	}
	pageArgs = append(pageArgs, r.Limit+1)
	rows, err := s.read().Query(cte+` SELECT card_kind,entity_id,reference_id,order_value,reference_revision,library_id,group_kind,series_id,title,total,cover_entity,cover_public_id,hidden FROM candidates`+where+` ORDER BY order_value `+r.Direction+`,card_kind `+r.Direction+`,entity_id `+r.Direction+`,reference_id COLLATE BINARY `+r.Direction+` LIMIT ?`, pageArgs...)
	if err != nil {
		return out, err
	}
	selected := []savedMixedRow{}
	for rows.Next() {
		var row savedMixedRow
		if err = rows.Scan(&row.Kind, &row.Entity, &row.Reference, &row.Order, &row.Revision, &row.Library, &row.GroupKind, &row.Series, &row.Title, &row.Count, &row.Cover, &row.CoverPublic, &row.Hidden); err != nil {
			rows.Close()
			return out, err
		}
		selected = append(selected, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	next := ""
	if len(selected) > r.Limit {
		selected = selected[:r.Limit]
		last := selected[len(selected)-1]
		next, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: savedMixedOrderDigest(last.Order), ID: (savedMixedAnchor{last.Kind, last.Entity, last.Reference}).encode(), Expires: expiry}, base.Revision)
		if err != nil {
			return out, err
		}
	}
	cards, err := s.savedMixedCards(r, selected, readingScope)
	if err != nil {
		return out, err
	}
	if len(cards) == 0 {
		out.Empty = &ContentHeading{Key: "saved.empty", Fallback: "Nothing saved here yet."}
	} else {
		section := contentSection(r.View, "grid", base.Heading.Fallback, nil, count, next)
		out.SavedSections = []SavedSection{{ContentSection: section, Cards: cards}}
	}
	return out, nil
}

type savedMixedRow struct {
	savedMixedAnchor
	Order         string
	Revision      int64
	Library       string
	GroupKind     ReadingSavedGroupKind
	Series, Title string
	Count, Cover  int64
	CoverPublic   string
	Hidden        bool
}

func (s *Service) savedMixedCards(r ContentRequest, rows []savedMixedRow, scope savedReadingScope) ([]SavedCard, error) {
	entityIDs := []int64{}
	coverIDs := []int64{}
	for _, row := range rows {
		if row.Kind == 0 {
			entityIDs = append(entityIDs, row.Entity)
		} else if !row.Hidden && row.Cover > 0 {
			coverIDs = append(coverIDs, row.Cover)
		}
	}
	raw, _ := json.Marshal(entityIDs)
	publicRows, err := s.read().Query(`SELECT id,pid(public_id) FROM catalog_entities WHERE id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	public := map[int64]string{}
	for publicRows.Next() {
		var id int64
		var value string
		if err = publicRows.Scan(&id, &value); err != nil {
			publicRows.Close()
			return nil, err
		}
		public[id] = value
	}
	err = publicRows.Err()
	publicRows.Close()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, id := range entityIDs {
		value, ok := public[id]
		if !ok {
			return nil, ErrStaleContinuation
		}
		ids = append(ids, value)
	}
	entries, err := s.WithRecommendationRestrictions(r.Viewer.EffectiveRestrictions()).savedEntries(r.Profile, ids, scope)
	if err != nil {
		return nil, err
	}
	byID := map[string]*ContentEntry{}
	for i := range entries {
		entry := &entries[i]
		if entry.Available != nil && !*entry.Available {
			entry.Playback = nil
		}
		byID[entry.ID] = entry
	}
	covers, err := compactcatalog.ReadingCoverQueriesTx(s.Context(), s.readTxOrSnapshot(), coverIDs)
	if err != nil {
		return nil, err
	}
	cards := make([]SavedCard, 0, len(rows))
	for _, row := range rows {
		if row.Kind == 0 {
			entry := byID[public[row.Entity]]
			if entry == nil {
				return nil, ErrStaleContinuation
			}
			cards = append(cards, SavedCard{Entry: entry})
			continue
		}
		card := &SavedReadingGroupCard{ID: row.Reference, Kind: "reading_group", Hidden: row.Hidden, Revision: row.Revision, RemoveMyList: true}
		if !row.Hidden {
			card.LibraryID = row.Library
			card.ReadingGroup = &SavedReadingGroupLink{GroupKind: row.GroupKind, SeriesID: row.Series}
			card.Title = row.Title
			count := row.Count
			card.Count = &count
			card.Navigation = &SavedReadingGroupNavigation{View: "series", EntityID: row.Series}
			switch row.GroupKind {
			case ReadingSavedSeries:
			case ReadingSavedMagazine:
				card.Navigation.Pivot = "magazines"
			case ReadingSavedNewspaper:
				card.Navigation.Pivot = "newspapers"
			default:
				return nil, ErrReadingProjectionChanged
			}
			if digest := covers[row.Cover]; digest != "" {
				card.PosterURL = "/v1/items/" + url.PathEscape(row.CoverPublic) + "/art/cover?" + digest
			}
		}
		cards = append(cards, SavedCard{ReadingGroup: card})
	}
	return cards, nil
}
