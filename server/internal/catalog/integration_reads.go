package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const IntegrationReadMaximumLimit = 100
const integrationInlineSources = 16

// IntegrationItemQuery uses native identities exclusively. An omitted Kind lists
// all native kinds in the selected scope; an omitted Limit still means one page.
type IntegrationItemQuery struct {
	Sort, Order, AddedAfter, AddedBefore                                 string
	IncludeFiles, HasPaths                                               bool
	IDs                                                                  []string
	LibraryID, LibraryKind, Kind, ParentID, Provider, ProviderID, Search string
	Year                                                                 int
	SeasonNumber                                                         *int
	Cursor                                                               string
	Limit                                                                int
}

type IntegrationItem struct {
	DurationSeconds float64                  `json:"durationSeconds,omitempty"`
	Width           *int                     `json:"width,omitempty"`
	Height          *int                     `json:"height,omitempty"`
	ID              string                   `json:"id"`
	LibraryID       string                   `json:"libraryId"`
	Kind            string                   `json:"kind"`
	Title           string                   `json:"title"`
	Year            int                      `json:"year,omitempty"`
	AddedAt         string                   `json:"addedAt,omitempty"`
	Available       bool                     `json:"available"`
	ProviderIDs     map[string]string        `json:"providerIds"`
	Paths           []string                 `json:"paths"`
	Episode         *EpisodeInfo             `json:"episode,omitempty"`
	Season          *IntegrationSeason       `json:"season,omitempty"`
	MediaSources    []IntegrationMediaSource `json:"mediaSources"`
	// Follow on /items/{id}/media-info with this cursor and limit=16.
	MediaSourcesNextCursor string `json:"mediaSourcesNextCursor,omitempty"`
	entity                 int64
	sortValue              string
}

type IntegrationSeason struct {
	ShowID string `json:"showId"`
	Number int    `json:"number"`
}

// IntegrationMediaSource is a stored technical summary, never a playback
// decision. Codec names and geometry come from the scanned native asset. This
// deliberately does not claim to contain all audio/subtitle track details.
type IntegrationMediaSource struct {
	ID              string          `json:"id"`
	Path            string          `json:"path,omitempty"`
	Size            int64           `json:"size"`
	Container       string          `json:"container"`
	DurationSeconds float64         `json:"durationSeconds"`
	BitRate         int64           `json:"bitRate,omitempty"`
	Available       bool            `json:"available"`
	PartIndex       int             `json:"partIndex"`
	Edition         string          `json:"edition,omitempty"`
	Video           *TitleFileVideo `json:"video,omitempty"`
	AudioCodec      string          `json:"audioCodec,omitempty"`
	asset           int64
}

type IntegrationItemPage struct {
	Items      []IntegrationItem `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
	// Exact visible matches in the full query, in this page's read snapshot.
	Total int64 `json:"total"`
}

type IntegrationMediaInfo struct {
	MediaSources []IntegrationMediaSource `json:"mediaSources"`
	NextCursor   string                   `json:"nextCursor,omitempty"`
	Total        int64                    `json:"total"`
}

// QueryIntegrationItems uses the existing serving rows, FTS and visibility SQL.
// SQL may count/walk matching index rows; Go holds at most limit+1 titles and
// 17 sources per title. No persistent query state or whole-match arrays exist.
func (s *Service) QueryIntegrationItems(v Viewer, q IntegrationItemQuery, paths bool) (IntegrationItemPage, error) {
	out := IntegrationItemPage{Items: []IntegrationItem{}}
	if dbwork.Snapshot(s.Context()) == nil {
		return out, &ErrTitleQuery{Field: "snapshot"}
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > IntegrationReadMaximumLimit {
		return out, &ErrTitleQuery{Field: "limit"}
	}
	if len(q.IDs) > IntegrationReadMaximumLimit {
		return out, &ErrTitleQuery{Field: "ids"}
	}
	for _, id := range q.IDs {
		if id == "" || len(id) > 128 {
			return out, &ErrTitleQuery{Field: "ids"}
		}
	}
	if q.Year < 0 || q.Year > 9999 {
		return out, &ErrTitleQuery{Field: "year"}
	}
	if q.SeasonNumber != nil && (*q.SeasonNumber < 0 || q.ParentID == "" || q.Kind != "episode") {
		return out, &ErrTitleQuery{Field: "season"}
	}
	if len(q.Provider) > 64 || len(q.ProviderID) > 128 || (q.Provider == "") != (q.ProviderID == "") {
		return out, &ErrTitleQuery{Field: "providerId"}
	}
	if len(q.LibraryID) > 128 || len(q.LibraryKind) > 64 || len(q.ParentID) > 128 {
		return out, &ErrTitleQuery{Field: "libraryId"}
	}
	visible, args := v.entityVisibilitySQL("b.entity_id", "cl.library_id")
	where := `e.retired=0 AND cl.retired=0 AND ` + visible + recordingsClause("e.id", v.Restrictions)
	add := func(sql string, values ...any) { where += " AND " + sql; args = append(args, values...) }
	if q.HasPaths {
		if !paths {
			return out, &ErrTitleQuery{Field: "hasPaths"}
		}
		add(`(EXISTS(SELECT 1 FROM catalog_asset_links path_link JOIN catalog_assets path_asset ON path_asset.id=path_link.asset_id WHERE path_link.entity_id=e.id AND path_link.available=1 AND path_asset.available=1) OR EXISTS(SELECT 1 FROM catalog_identities path_identity JOIN library_sources path_source ON path_source.library_id=cl.library_id AND path_source.enabled=1 WHERE path_identity.public_id=e.public_id AND substr(path_identity.source_key,1,length('show:folder:'||path_source.id||':'))='show:folder:'||path_source.id||':'))`)
	}
	if q.LibraryID != "" {
		if !v.AllowsLibrary(q.LibraryID) {
			return out, ErrTitleLibrary
		}
		add(`cl.library_id=?`, q.LibraryID)
	}
	if q.LibraryKind != "" {
		add(`EXISTS(SELECT 1 FROM libraries l WHERE l.id=cl.library_id AND l.kind=?)`, q.LibraryKind)
	}
	kinds := []compactcatalog.Kind{}
	if q.Kind != "" {
		names := strings.Split(q.Kind, ",")
		if len(names) > 16 {
			return out, &ErrTitleQuery{Field: "kind"}
		}
		seen := map[compactcatalog.Kind]bool{}
		for _, name := range names {
			kind, e := compactcatalog.ParseKind(name)
			if e != nil || seen[kind] {
				return out, &ErrTitleQuery{Field: "kind"}
			}
			seen[kind] = true
			kinds = append(kinds, kind)
		}
		bound := []any{}
		for _, kind := range kinds {
			bound = append(bound, kind)
		}
		add(`b.kind IN (`+placeholders(len(bound))+`)`, bound...)
	}
	if q.Sort == "" {
		q.Sort = "id"
		if q.AddedAfter != "" || q.AddedBefore != "" {
			q.Sort = "added"
		}
	}
	if q.Order == "" {
		q.Order = "asc"
	}
	if q.Order != "asc" && q.Order != "desc" {
		return out, &ErrTitleQuery{Field: "order"}
	}
	if q.Sort != "id" && q.Sort != "title" && q.Sort != "added" && q.Sort != "year" {
		return out, &ErrTitleQuery{Field: "sort"}
	}
	for _, date := range []struct{ value, field, op string }{{q.AddedAfter, "addedAfter", ">"}, {q.AddedBefore, "addedBefore", "<"}} {
		if date.value == "" {
			continue
		}
		if q.Sort != "added" {
			return out, &ErrTitleQuery{Field: date.field}
		}
		normalized, e := titleTime(date.value, date.field)
		if e != nil {
			return out, e
		}
		add(`COALESCE(b.added_text,'')`+date.op+`?`, normalized)
	}

	if len(q.IDs) > 0 {
		add(`e.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, idsJSON(q.IDs))
	}
	if q.Year != 0 {
		add(`b.year=?`, q.Year)
	}
	if q.ParentID != "" {
		var parentKind string
		if err := s.read().QueryRow(`SELECT k.name FROM catalog_entities p JOIN catalog_kinds k ON k.id=p.kind WHERE p.public_id=pid_blob(?) AND p.retired=0`, q.ParentID).Scan(&parentKind); err != nil {
			return out, err
		}
		namespace := parentKind
		if isItemEntity(namespace) {
			namespace = "item"
		}
		if err := s.VisibleEntity(s.Context(), v, namespace, q.ParentID); err != nil {
			return out, err
		}
		if parentKind == "show" {
			// A show's native /episodes screen lists only unassigned_absolute. This
			// read deliberately selects ep.show_id, including every assigned season.
			add(`(e.parent_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) OR ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`, q.ParentID, q.ParentID)
		} else {
			add(`e.parent_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, q.ParentID)
		}
		if q.SeasonNumber != nil {
			// Resolve the actual season identity; never pass its number as an ID or
			// fall back to the show's unassigned bucket when it is absent/hidden.
			var season string
			err := s.read().QueryRow(`SELECT pid(se.public_id) FROM catalog_seasons sd JOIN catalog_entities se ON se.id=sd.entity_id WHERE sd.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND sd.number=? AND se.retired=0`, q.ParentID, *q.SeasonNumber).Scan(&season)
			if err != nil {
				return out, err
			}
			if err = s.VisibleEntity(s.Context(), v, "season", season); err != nil {
				return out, err
			}
			add(`ep.season_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, season)
		}
	}
	if q.Provider != "" {
		// No provider_kind restriction: episode and music identities are real data,
		// too. EXISTS avoids duplicate rows when a provider has multiple kinds.
		add(`EXISTS(SELECT 1 FROM catalog_external_ids x WHERE x.entity_id=e.id AND x.provider=? AND x.provider_id=?)`, q.Provider, q.ProviderID)
	}
	if q.Search != "" {
		normalized, match, err := NormalizeSearch(q.Search)
		if err != nil {
			return out, &ErrTitleQuery{Field: "search"}
		}
		q.Search = normalized
		add(`b.entity_id IN(SELECT rowid FROM catalog_search_titles WHERE catalog_search_titles MATCH ?)`, `search : (`+match+`)`)
	}
	from := `catalog_entities e JOIN catalog_browse_rows b ON b.entity_id=e.id JOIN catalog_libraries cl ON cl.id=b.library_id LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_seasons own_season ON own_season.entity_id=e.id LEFT JOIN catalog_entities season_show ON season_show.id=own_season.show_id`
	revision, err := s.homeRevision(v.Libraries, v.Profile)
	if err != nil {
		return out, err
	}
	// Capture the same complete visibility basis as exact browse totals.
	// Raw revisions alone miss background serving publication and classification.
	countWitness, err := s.integrationCountWitness(v)
	if err != nil {
		return out, err
	}
	// Cursor is signed and bound to every filter, viewer fence, path authority,
	// limit and catalogue revision. Rows may continue after a revision changes,
	// but the previous snapshot's count must be recomputed.
	fingerprint := q
	fingerprint.Cursor = ""
	raw, _ := json.Marshal(struct {
		Query IntegrationItemQuery
		Paths bool
	}{fingerprint, paths})
	sum := sha256.Sum256(raw)
	scope := cursorScope{Profile: v.Profile, Viewer: v.Fence, View: "integration_items", Category: hex.EncodeToString(sum[:]), Limit: q.Limit}
	var after int64
	var afterKey string
	countReusable := false
	if q.Cursor != "" {
		c, e := s.decodeRevisionCursor(q.Cursor, scope, revision)
		if e != nil {
			return out, e
		}
		if len(c.Keys) != 1 {
			return out, ErrCursor
		}
		afterKey = c.Keys[0]
		// The shared decoder clears TotalPresent on revision drift or expiry.
		// Value is a legacy scalar and is deliberately not an invalidation flag.
		countReusable = c.TotalPresent && countWitness != "" && c.Fingerprint == countWitness
		out.Total, e = strconv.ParseInt(c.Value, 10, 64)
		if e != nil || out.Total < 0 {
			return out, ErrCursor
		}
		after, e = strconv.ParseInt(c.ID, 10, 64)
		if e != nil || after < 0 {
			return out, ErrCursor
		}
	}
	if !countReusable {
		if err = s.read().QueryRowContext(s.Context(), `SELECT count(*) FROM `+from+` WHERE `+where, args...).Scan(&out.Total); err != nil {
			return out, err
		}
	}
	columns := `pid(e.public_id),e.id AS integration_entity,cl.library_id,` + compactBrowseKindSQL("b.kind") + `,b.title,b.year,COALESCE(b.added_text,''),b.available,COALESCE(b.duration_max,0),COALESCE(pid(sh.public_id),''),COALESCE(sh.title,''),COALESCE(pid((SELECT public_id FROM catalog_entities WHERE id=ep.season_id)),''),sd.number,COALESCE(ep.numbering,''),COALESCE(ep.number,0),COALESCE(ep.local_identity_status,''),COALESCE(ep.ordering_basis,''),COALESCE(pid(season_show.public_id),''),own_season.number`
	query, values, err := s.integrationOrderedPage(v, q, from, where, args, columns, kinds, after, afterKey)
	if err != nil {
		return out, err
	}
	rows, err := s.read().QueryContext(s.Context(), query, values...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		item := IntegrationItem{ProviderIDs: map[string]string{}, Paths: []string{}, MediaSources: []IntegrationMediaSource{}}
		ep := EpisodeInfo{}
		var seasonNumber, ownSeasonNumber sql.NullInt64
		var ownShow string
		if err = rows.Scan(&item.ID, &item.entity, &item.LibraryID, &item.Kind, &item.Title, &item.Year, &item.AddedAt, &item.Available, &item.DurationSeconds, &ep.ShowID, &ep.ShowTitle, &ep.SeasonID, &seasonNumber, &ep.Numbering, &ep.Number, &ep.LocalIdentityStatus, &ep.OrderingBasis, &ownShow, &ownSeasonNumber, &item.sortValue); err != nil {
			rows.Close()
			return out, err
		}
		if item.Kind == "episode" {
			if seasonNumber.Valid {
				n := int(seasonNumber.Int64)
				ep.SeasonNumber = &n
			}
			item.Episode = &ep
		}
		if ownSeasonNumber.Valid {
			item.Season = &IntegrationSeason{ShowID: ownShow, Number: int(ownSeasonNumber.Int64)}
		}
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, ID: strconv.FormatInt(out.Items[len(out.Items)-1].entity, 10), Keys: []string{out.Items[len(out.Items)-1].sortValue}, Value: strconv.FormatInt(out.Total, 10), TotalPresent: countWitness != "", Fingerprint: countWitness, Expires: time.Now().Add(titleCursorTTL).Unix()}, revision)
		if err != nil {
			return out, err
		}
	}
	if err = s.integrationDecorate(out.Items, v, revision, paths, q.IncludeFiles); err != nil {
		return IntegrationItemPage{}, err
	}
	return out, nil
}

// One fixed-size witness per authorized library, never retained query results.
// A serving first-build may not yet have a reusable proof: keep row recovery
// available and count the current snapshot, but do not carry a reusable total.
func (s *Service) integrationCountWitness(v Viewer) (string, error) {
	witnesses := make([]string, 0, len(v.Libraries))
	for _, library := range v.Libraries {
		witness, err := s.browseExactWitness(library, v.Profile, v.Restrictions)
		if errors.Is(err, ErrVisibilityBuilding) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		witnesses = append(witnesses, witness)
	}
	raw, err := json.Marshal(witnesses)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Batch provider facts preserve the native map convention used by TitleRow.
// As with TitleRow, the first stable native identity for a provider wins.
// No provider kinds (including episodes/music) are excluded from the read.
func (s *Service) integrationDecorate(items []IntegrationItem, v Viewer, revision ContentRevision, paths, files bool) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	byID := map[string]int{}
	for i, item := range items {
		ids = append(ids, item.ID)
		byID[item.ID] = i
	}
	rows, err := s.read().QueryContext(s.Context(), `SELECT requested.value,x.provider,x.provider_id FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_external_ids x ON x.entity_id=e.id ORDER BY requested.key,x.provider,x.provider_kind,x.provider_id LIMIT 6401`, idsJSON(ids))
	if err != nil {
		return err
	}
	providerRows := 0
	for rows.Next() {
		providerRows++
		if providerRows > 6400 {
			rows.Close()
			return &ErrTitleQuery{Field: "providerFacts"}
		}
		var id, provider, value string
		if err = rows.Scan(&id, &provider, &value); err != nil {
			rows.Close()
			return err
		}
		item := &items[byID[id]]
		if _, exists := item.ProviderIDs[provider]; !exists {
			item.ProviderIDs[provider] = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Maxima cover every available scanned source of the authorized page,
	// independent of inline file paging. NULL stays unknown, never a fake zero.
	rows, err = s.read().QueryContext(s.Context(), `SELECT requested.value,max(CASE WHEN a.width>0 THEN a.width END),max(CASE WHEN a.height>0 THEN a.height END) FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_asset_links link ON link.entity_id=e.id AND link.available=1 JOIN catalog_assets a ON a.id=link.asset_id AND a.available=1 GROUP BY requested.value`, idsJSON(ids))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var width, height sql.NullInt64
		if err = rows.Scan(&id, &width, &height); err != nil {
			rows.Close()
			return err
		}
		item := &items[byID[id]]
		if width.Valid {
			n := int(width.Int64)
			item.Width = &n
		}
		if height.Valid {
			n := int(height.Int64)
			item.Height = &n
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !files {
		return nil
	}
	// Show folder identities are maintained native facts, including aliases
	// retained after provider merges. Do not infer a series folder from a title
	// or from whichever episode happened to be on the first page.
	if paths {
		rows, e := s.read().QueryContext(s.Context(), `SELECT requested.value,s.root,substr(i.source_key,length('show:folder:'||s.id||':')+1) FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_identities i ON i.public_id=e.public_id JOIN library_sources s ON s.library_id=cl.library_id AND s.enabled=1 WHERE substr(i.source_key,1,length('show:folder:'||s.id||':'))='show:folder:'||s.id||':' ORDER BY requested.key,s.id,i.source_key LIMIT 6401`, idsJSON(ids))
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			count++
			if count > 6400 {
				rows.Close()
				return &ErrTitleQuery{Field: "paths"}
			}
			var id, root, relative string
			if e = rows.Scan(&id, &root, &relative); e != nil {
				rows.Close()
				return e
			}
			relative = filepath.FromSlash(relative)
			if relative == "." || !filepath.IsLocal(relative) {
				rows.Close()
				return &ErrTitleQuery{Field: "paths"}
			}
			items[byID[id]].Paths = append(items[byID[id]].Paths, filepath.Join(root, relative))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	// Read 17 sources per authorized title in a single SQL batch, then emit
	// 16 and a signed continuation. LIMIT sits inside the indexed link lookup.
	rows, err = s.read().QueryContext(s.Context(), `SELECT requested.value,`+integrationSourceColumns+` FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id IN(SELECT sub.asset_id FROM catalog_asset_links sub WHERE sub.entity_id=e.id ORDER BY sub.asset_id LIMIT 17) JOIN catalog_assets a ON a.id=link.asset_id ORDER BY CAST(requested.key AS INTEGER),a.id`, idsJSON(ids))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		source, scanErr := scanIntegrationSource(rows, &id, paths)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		item := &items[byID[id]]
		if len(item.MediaSources) == integrationInlineSources {
			if item.MediaSourcesNextCursor == "" {
				item.MediaSourcesNextCursor, err = s.integrationSourceCursor(v, revision, item.ID, item.MediaSources[len(item.MediaSources)-1].asset, integrationInlineSources, paths)
				if err != nil {
					rows.Close()
					return err
				}
			}
			continue
		}
		item.MediaSources = append(item.MediaSources, source)
		if source.Path != "" {
			item.Paths = append(item.Paths, source.Path)
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

const integrationSourceColumns = `a.id,a.token,a.path,a.size,a.container,a.duration,link.available,link.part_index,link.edition,a.video_codec,a.width,a.height,a.audio_codec`

func scanIntegrationSource(row interface{ Scan(...any) error }, item *string, paths bool) (IntegrationMediaSource, error) {
	var source IntegrationMediaSource
	var codec string
	var width, height int
	args := []any{}
	if item != nil {
		args = append(args, item)
	}
	args = append(args, &source.asset, &source.ID, &source.Path, &source.Size, &source.Container, &source.DurationSeconds, &source.Available, &source.PartIndex, &source.Edition, &codec, &width, &height, &source.AudioCodec)
	if err := row.Scan(args...); err != nil {
		return source, err
	}
	if !paths {
		source.Path = ""
	}
	if source.DurationSeconds > 0 && source.Size > 0 {
		source.BitRate = int64(float64(source.Size) * 8 / source.DurationSeconds)
	}
	if codec != "" || width > 0 || height > 0 {
		source.Video = &TitleFileVideo{Codec: codec, Width: width, Height: height}
	}
	return source, nil
}

func integrationSourceScope(v Viewer, item string, limit int, paths bool) cursorScope {
	return cursorScope{Profile: v.Profile, Viewer: v.Fence, View: "integration_media", Entity: item, Limit: limit, Category: strconv.FormatBool(paths)}
}
func (s *Service) integrationSourceCursor(v Viewer, revision ContentRevision, item string, after int64, limit int, paths bool) (string, error) {
	return s.encodeRevisionCursor(cursorValue{Scope: integrationSourceScope(v, item, limit, paths), ID: strconv.FormatInt(after, 10), Expires: time.Now().Add(titleCursorTTL).Unix()}, revision)
}

func (s *Service) IntegrationMediaInfo(v Viewer, item, cursor string, limit int, paths bool) (IntegrationMediaInfo, error) {
	out := IntegrationMediaInfo{MediaSources: []IntegrationMediaSource{}}
	if dbwork.Snapshot(s.Context()) == nil {
		return out, &ErrTitleQuery{Field: "snapshot"}
	}
	if limit == 0 {
		limit = integrationInlineSources
	}
	if limit < 1 || limit > IntegrationReadMaximumLimit {
		return out, &ErrTitleQuery{Field: "limit"}
	}
	// Works for containers and playable items; files are only the exact entity's
	// own links. Children must be paged with /items?parentId=, never expanded here.
	visible, args := v.entityVisibilitySQL("b.entity_id", "cl.library_id")
	visible += recordingsClause("e.id", v.Restrictions)
	values := append([]any{item}, args...)
	var found int
	err := s.read().QueryRowContext(s.Context(), `SELECT 1 FROM catalog_entities e JOIN catalog_browse_rows b ON b.entity_id=e.id JOIN catalog_libraries cl ON cl.id=b.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0 AND `+visible, values...).Scan(&found)
	if err != nil {
		return out, err
	}
	revision, err := s.homeRevision(v.Libraries, v.Profile)
	if err != nil {
		return out, err
	}
	var after int64
	if cursor != "" {
		c, e := s.decodeRevisionCursor(cursor, integrationSourceScope(v, item, limit, paths), revision)
		if e != nil {
			return out, e
		}
		after, e = strconv.ParseInt(c.ID, 10, 64)
		if e != nil || after < 0 {
			return out, ErrCursor
		}
	}
	if err = s.read().QueryRowContext(s.Context(), `SELECT count(*) FROM catalog_asset_links WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.read().QueryContext(s.Context(), `SELECT `+integrationSourceColumns+` FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND link.asset_id>? ORDER BY link.asset_id LIMIT ?`, item, after, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		source, e := scanIntegrationSource(rows, nil, paths)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.MediaSources = append(out.MediaSources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.MediaSources) > limit {
		out.MediaSources = out.MediaSources[:limit]
		out.NextCursor, err = s.integrationSourceCursor(v, revision, item, out.MediaSources[limit-1].asset, limit, paths)
	}
	return out, err
}

// Keep the normalizer shared by the route and tests: CSV IDs are bounded before
// SQL binding, never silently truncated or split into an unbounded working set.
func IntegrationIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > IntegrationReadMaximumLimit*129 {
		return nil, &ErrTitleQuery{Field: "ids"}
	}
	ids := strings.Split(raw, ",")
	if len(ids) > IntegrationReadMaximumLimit {
		return nil, &ErrTitleQuery{Field: "ids"}
	}
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id || len(id) > 128 {
			return nil, &ErrTitleQuery{Field: "ids"}
		}
	}
	return ids, nil
}

// Sorted reads merge at most 64 bounded library/kind index runs. The final SQL
// sort sees at most 64*(limit+1) candidates, never all matching titles. Larger
// servers request one library at a time and merge the sorted pages in the shim.
func (s *Service) integrationOrderedPage(v Viewer, q IntegrationItemQuery, from, where string, args []any, columns string, kinds []compactcatalog.Kind, after int64, afterKey string) (string, []any, error) {
	direction, op := "ASC", ">"
	if q.Order == "desc" {
		direction, op = "DESC", "<"
	}
	if q.Sort == "id" {
		values := append(append([]any{}, args...), after)
		seek := ` AND e.id` + op + `?`
		if q.Cursor == "" {
			seek = ""
			values = append([]any{}, args...)
		}
		// The validated limit is a literal: the planner weighs reading entities
		// in id order against sorting the browse rows by how few it needs, and it
		// never reads a bound LIMIT (the driver's planner stability guarantee).
		return `SELECT ` + columns + `,e.id AS integration_order FROM ` + from + ` WHERE ` + where + seek + ` ORDER BY e.id ` + direction + ` LIMIT ` + strconv.Itoa(q.Limit+1), values, nil
	}
	libraries := v.Libraries
	if q.LibraryID != "" {
		libraries = []string{q.LibraryID}
	}
	if len(kinds) == 0 {
		for _, name := range []string{"movie", "show", "season", "episode", "artist", "album", "song", "book", "audiobook_file", "collection", "extra", "disc", "author", "ebook", "comic"} {
			kind, _ := compactcatalog.ParseKind(name)
			kinds = append(kinds, kind)
		}
	}
	if len(libraries)*len(kinds) > 64 {
		return "", nil, &ErrTitleQuery{Field: "libraryId"}
	}
	expression := map[string]string{"title": "b.sort_key COLLATE NOCASE", "added": "COALESCE(b.added_text,'')", "year": "b.year"}[q.Sort]
	index := map[string]string{"title": "catalog_browse_title", "added": "catalog_browse_added", "year": "catalog_browse_year"}[q.Sort]
	// Anchor each run to its exact existing ordered index.
	indexed := `catalog_browse_rows b INDEXED BY ` + index + ` CROSS JOIN catalog_entities e ON e.id=b.entity_id` + strings.TrimPrefix(from, `catalog_entities e JOIN catalog_browse_rows b ON b.entity_id=e.id`)
	parts := []string{}
	values := []any{}
	for _, library := range libraries {
		for _, kind := range kinds {
			runWhere := where + ` AND b.library_id=(SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0) AND b.kind=?`
			runArgs := append(append([]any{}, args...), library, kind)
			var query string
			if q.Cursor != "" {
				var key any = afterKey
				if q.Sort == "year" {
					number, e := strconv.Atoi(afterKey)
					if e != nil {
						return "", nil, ErrCursor
					}
					key = number
				}
				cleanColumns := strings.Replace(columns, "e.id AS integration_entity", "e.id", 1) + `,page.order_key`
				query, runArgs = browseBoundedSQL(cleanColumns, indexed, runWhere, runArgs, expression, "b.entity_id", q.Order == "desc", false, key, after, q.Limit+1, 0)
				// The shared helper owns its inner aliases. Rename only its outer
				// projection, never add AS clauses to its expression inputs.
				_, projection := boundedColumns(cleanColumns)
				renamed := strings.Replace(projection, "page.c1", "page.c1 AS integration_entity", 1)
				renamed = strings.Replace(renamed, "page.order_key", "page.order_key AS integration_order", 1)
				query = strings.Replace(query, "SELECT "+projection+" FROM", "SELECT "+renamed+" FROM", 1)
			} else {
				query = `SELECT ` + columns + `,` + strings.TrimSuffix(expression, " COLLATE NOCASE") + ` AS integration_order FROM ` + indexed + ` WHERE ` + runWhere + ` ORDER BY ` + expression + ` ` + direction + `,b.entity_id ` + direction + ` LIMIT ?`
				runArgs = append(runArgs, q.Limit+1)
			}
			parts = append(parts, `SELECT * FROM (`+query+`)`)
			values = append(values, runArgs...)
		}
	}
	if len(parts) == 0 {
		return `SELECT ` + columns + `,0 AS integration_order FROM ` + from + ` WHERE 0`, nil, nil
	}
	collation := ""
	if q.Sort == "title" {
		collation = " COLLATE NOCASE"
	}
	values = append(values, q.Limit+1)
	return `SELECT * FROM (` + strings.Join(parts, ` UNION ALL `) + `) ORDER BY integration_order` + collation + ` ` + direction + `,integration_entity ` + direction + ` LIMIT ?`, values, nil
}
