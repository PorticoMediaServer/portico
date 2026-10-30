package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"
)

// The title query is the bounded list a third party may ask for itself. Screens
// are composed by the server and stay that way; this is an addition for tools
// that need to walk a library or ask "do you have this?".
//
// It is bounded by construction rather than by a planner's good will:
//
//   - A list names one library and one kind, because every ordered index on
//     titles begins with those two. There is no cross-library sort.
//   - It sorts by title, date added or year: the three keys with an index. A
//     range filter is accepted only on the key being sorted by, so the range is
//     a slice of the index and never a scan.
//   - A lookup by provider id (TMDB, TVDB, IMDb, AniList) reads the provider
//     index and returns at most a page.
//   - Paging is by a signed cursor that seeks to the last row seen. It is not
//     tied to the library's revision, so a tool can walk a large library to the
//     end while a scan is changing it: a title added behind the cursor is not
//     seen, one added ahead of it is, and none is seen twice.
//   - The caller's libraries and content restrictions are part of the SQL, as
//     everywhere else, so a page is never filtered after it is cut.
const (
	TitleQueryDefaultLimit = 50
	TitleQueryMaximumLimit = 100
	titleCursorTTL         = 30 * time.Minute
)

// ErrTitleQuery reports a query outside what the indexes can answer. Field
// names the parameter at fault.
type ErrTitleQuery struct{ Field string }

func (e *ErrTitleQuery) Error() string { return "invalid title query: " + e.Field }

// TitleQuery is one request. Library and Kind are required unless Provider and
// ProviderID are given.
type TitleQuery struct {
	Library, Kind    string
	Sort, Order      string
	YearFrom, YearTo int
	AddedAfter       string
	AddedBefore      string
	Provider         string
	ProviderID       string
	Cursor           string
	Limit            int
}

// TitleRow is one title. It is deliberately small: details, files and playback
// are the title's own routes.
type TitleRow struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	LibraryID string `json:"libraryId"`
	Year      int    `json:"year,omitempty"`
	AddedAt   string `json:"addedAt,omitempty"`
	// Available is false while every file of the title is missing.
	Available       bool    `json:"available"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	Rating          float64 `json:"rating,omitempty"`
	PosterURL       string  `json:"posterUrl,omitempty"`
	// ProviderIDs maps a provider ("tmdb", "tvdb", "imdb", "anilist") to this
	// title's id there.
	ProviderIDs map[string]string `json:"providerIds"`
	entity      int64
	sortValue   string
}

type TitlePage struct {
	Items      []TitleRow `json:"items"`
	Limit      int        `json:"limit"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

var titleKinds = map[string]int{"movie": 1, "show": 2, "season": 3, "episode": 4, "artist": 5, "album": 6, "song": 7, "book": 8, "collection": 10, "author": 13}

// titleSorts maps a published sort to the exact expression its index is built
// on. Changing an expression here without its index turns a seek into a scan.
var titleSorts = map[string]string{
	"title": "e.sort_key COLLATE NOCASE",
	"added": "COALESCE(e.added_text,'')",
	"year":  "e.year",
}

var titleProviders = map[string]bool{"tmdb": true, "tvdb": true, "imdb": true, "anilist": true}

func titleTime(raw, field string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", &ErrTitleQuery{field}
	}
	// The stored form: UTC to the millisecond, so text order is time order.
	return t.UTC().Format("2006-01-02T15:04:05.000Z"), nil
}

// QueryTitles answers one page. viewer carries the libraries the caller may
// see and their restrictions.
func (s *Service) QueryTitles(viewer Viewer, q TitleQuery) (TitlePage, error) {
	out := TitlePage{Items: []TitleRow{}}
	if q.Limit == 0 {
		q.Limit = TitleQueryDefaultLimit
	}
	if q.Limit < 1 || q.Limit > TitleQueryMaximumLimit {
		return out, &ErrTitleQuery{"limit"}
	}
	out.Limit = q.Limit
	kind := 0
	if q.Kind != "" {
		var ok bool
		if kind, ok = titleKinds[q.Kind]; !ok {
			return out, &ErrTitleQuery{"kind"}
		}
	}
	restrictions := viewer.EffectiveRestrictions()
	restriction, restrictionArgs := EntityRestrictionSQL("e.entity_id", restrictions)
	columns := `pid(ce.public_id),e.entity_id,` + compactBrowseKindSQL("e.kind") + `,e.title,e.year,COALESCE(e.added_text,''),e.available,COALESCE(e.duration_max,0),COALESCE(e.rating_max,0),cl.library_id`
	var query string
	var args []any
	sortExpression := ""
	descending := false
	var scope cursorScope
	if q.Provider != "" || q.ProviderID != "" {
		if !titleProviders[q.Provider] {
			return out, &ErrTitleQuery{"provider"}
		}
		if q.ProviderID == "" || len(q.ProviderID) > 64 {
			return out, &ErrTitleQuery{"providerId"}
		}
		if q.Sort != "" || q.Order != "" || q.YearFrom != 0 || q.YearTo != 0 || q.AddedAfter != "" || q.AddedBefore != "" || q.Cursor != "" {
			return out, &ErrTitleQuery{"provider"}
		}
		if q.Library != "" && !viewer.AllowsLibrary(q.Library) {
			return out, nil
		}
		if len(viewer.Libraries) == 0 {
			return out, nil
		}
		query = `SELECT ` + columns + `,'' FROM catalog_external_ids x JOIN catalog_browse_rows e ON e.entity_id=x.entity_id JOIN catalog_entities ce ON ce.id=e.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id
 WHERE x.provider=? AND x.provider_kind IN('movie','tv','series','anime','title') AND x.provider_id=? AND cl.retired=0 AND cl.library_id IN(SELECT value FROM json_each(?)) AND (?='' OR cl.library_id=?) AND (?=0 OR e.kind=?) AND ` + restriction + ` ORDER BY e.entity_id LIMIT ?`
		args = append([]any{q.Provider, q.ProviderID, viewer.librariesJSON(), q.Library, q.Library, kind, kind}, restrictionArgs...)
		args = append(args, q.Limit)
	} else {
		if q.Library == "" {
			return out, &ErrTitleQuery{"library"}
		}
		if kind == 0 {
			return out, &ErrTitleQuery{"kind"}
		}
		if !viewer.AllowsLibrary(q.Library) {
			// A library the caller may not see reads as one that is not there.
			return out, ErrTitleLibrary
		}
		if q.Sort == "" {
			q.Sort = "title"
		}
		var ok bool
		if sortExpression, ok = titleSorts[q.Sort]; !ok {
			return out, &ErrTitleQuery{"sort"}
		}
		switch q.Order {
		case "", "asc":
			q.Order = "asc"
		case "desc":
			descending = true
		default:
			return out, &ErrTitleQuery{"order"}
		}
		after, err := titleTime(q.AddedAfter, "addedAfter")
		if err != nil {
			return out, err
		}
		before, err := titleTime(q.AddedBefore, "addedBefore")
		if err != nil {
			return out, err
		}
		if (q.YearFrom != 0 || q.YearTo != 0) && q.Sort != "year" {
			return out, &ErrTitleQuery{"yearFrom"}
		}
		if (after != "" || before != "") && q.Sort != "added" {
			return out, &ErrTitleQuery{"addedAfter"}
		}
		if q.YearFrom < 0 || q.YearFrom > 9999 || q.YearTo < 0 || q.YearTo > 9999 || q.YearFrom != 0 && q.YearTo != 0 && q.YearFrom > q.YearTo {
			return out, &ErrTitleQuery{"yearFrom"}
		}
		if after != "" && before != "" && after >= before {
			return out, &ErrTitleQuery{"addedAfter"}
		}
		where := `e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0) AND e.kind=?`
		args = []any{q.Library, kind}
		if q.YearFrom != 0 {
			where += ` AND e.year>=?`
			args = append(args, q.YearFrom)
		}
		if q.YearTo != 0 {
			where += ` AND e.year<=?`
			args = append(args, q.YearTo)
		}
		if after != "" {
			where += ` AND COALESCE(e.added_text,'')>?`
			args = append(args, after)
		}
		if before != "" {
			where += ` AND COALESCE(e.added_text,'')<?`
			args = append(args, before)
		}
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%d\x00%s\x00%s", q.YearFrom, q.YearTo, after, before)))
		scope = cursorScope{Profile: viewer.Profile, View: "titles:" + q.Kind, Library: q.Library, Viewer: viewer.Fence, Sort: q.Sort, Direction: q.Order, Category: hex.EncodeToString(fingerprint[:8]), Limit: q.Limit}
		direction := "ASC"
		if descending {
			direction = "DESC"
		}
		from := `catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id`
		where += ` AND ` + restriction
		args = append(args, restrictionArgs...)
		if q.Cursor != "" {
			last, err := s.decodeRevisionCursor(q.Cursor, scope, ContentRevision{})
			if err != nil {
				return out, err
			}
			entity, err := strconv.ParseInt(last.ID, 10, 64)
			if err != nil || len(last.Keys) != 1 {
				return out, ErrCursor
			}
			var key any = last.Keys[0]
			if q.Sort == "year" {
				year, err := strconv.Atoi(last.Keys[0])
				if err != nil {
					return out, ErrCursor
				}
				key = year
			}
			// Two separately limited seeks, the cursor's ties after its id and
			// then later keys, so a deep page never revisits the tie cohort or
			// the library prefix before the cursor.
			query, args = browseBoundedSQL(columns+",page.order_key", from, where, args, sortExpression, "e.entity_id", descending, false, key, entity, q.Limit+1, 0)
		} else {
			// The sort value is selected as it is stored, not cast: an
			// expression over the serving rows makes SQLite build and sort
			// everything instead of walking the ordered index. A year scans
			// into the cursor's text just as well.
			query = `SELECT ` + columns + `,` + strings.TrimSuffix(sortExpression, " COLLATE NOCASE") + ` FROM ` + from + ` WHERE ` + where +
				` ORDER BY ` + sortExpression + ` ` + direction + `,e.entity_id ` + direction + ` LIMIT ?`
			args = append(args, q.Limit+1)
		}
	}
	rows, err := s.read().QueryContext(s.Context(), query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row TitleRow
		var available int
		if err = rows.Scan(&row.ID, &row.entity, &row.Kind, &row.Title, &row.Year, &row.AddedAt, &available, &row.DurationSeconds, &row.Rating, &row.LibraryID, &row.sortValue); err != nil {
			rows.Close()
			return out, err
		}
		row.Available, row.ProviderIDs = available != 0, map[string]string{}
		out.Items = append(out.Items, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return TitlePage{Items: []TitleRow{}}, err
	}
	if sortExpression != "" && len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		last := out.Items[q.Limit-1]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Keys: []string{last.sortValue}, ID: strconv.FormatInt(last.entity, 10), Expires: time.Now().Add(titleCursorTTL).Unix()}, ContentRevision{})
		if err != nil {
			return TitlePage{Items: []TitleRow{}}, err
		}
	}
	if err = s.decorateTitles(out.Items); err != nil {
		return TitlePage{Items: []TitleRow{}}, err
	}
	return out, nil
}

// ErrTitleLibrary is a library that does not exist for this caller.
var ErrTitleLibrary = requestinput.New("library is not available")

// decorateTitles adds provider ids and a poster to a page that has already
// passed the visibility checks, in two reads for the whole page.
func (s *Service) decorateTitles(items []TitleRow) error {
	if len(items) == 0 {
		return nil
	}
	index := map[int64]int{}
	ids := make([]any, 0, len(items))
	targets := make([]artworkTarget, 0, len(items))
	for i, item := range items {
		index[item.entity] = i
		ids = append(ids, item.entity)
		targets = append(targets, artworkTarget{item.Kind, item.ID})
	}
	rows, err := s.read().QueryContext(s.Context(), `SELECT entity_id,provider,provider_id FROM catalog_external_ids WHERE entity_id IN(`+placeholders(len(ids))+`) ORDER BY entity_id,provider,provider_kind`, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var entity int64
		var provider, id string
		if err = rows.Scan(&entity, &provider, &id); err != nil {
			rows.Close()
			return err
		}
		if i, ok := index[entity]; ok {
			if _, seen := items[i].ProviderIDs[provider]; !seen {
				items[i].ProviderIDs[provider] = id
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	artwork, err := s.resolveArtwork(targets)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].PosterURL = artwork[artworkTarget{items[i].Kind, items[i].ID}].PosterURL
		if items[i].PosterURL == "" && isItemEntity(items[i].Kind) {
			items[i].PosterURL = "/v1/items/" + items[i].ID + "/art/poster"
		}
	}
	return nil
}
