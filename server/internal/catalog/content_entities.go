package catalog

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func decodeCursorPayload(raw string) (cursorValue, error) {
	var c cursorValue
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e == nil {
		e = json.Unmarshal(b, &c)
	}
	return c, e
}

// contentMovieCount reads published integer facts for common scopes. An ad hoc
// intersection (a search, a collection) has no durable count cube, so it is
// counted exactly over its matches.
func (s *Service) contentMovieCount(r ContentRequest) (int, error) {
	domains := []int{18}
	if r.Category != "" {
		domains = append(domains, 27)
	}
	if err := s.compactProjectionReady(domains...); err != nil {
		return 0, err
	}
	var libraryID int
	if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, r.Library).Scan(&libraryID); err != nil {
		return 0, err
	}
	if r.Q == "" && r.EntityID == "" {
		restrictions := r.Viewer.EffectiveRestrictions()
		classID, generation := 0, int64(0)
		if restrictions.Active() {
			key, published, ready, err := s.publishedVisibilityClass(r.Library, restrictions)
			if err != nil {
				return 0, err
			}
			if !ready {
				return 0, nil
			}
			generation = published
			_, classID, err = s.currentCategoryClass(r.Library, key, generation)
			if err != nil {
				return 0, err
			}
		}
		var count int
		if r.Category != "" {
			kind, value, err := movieCategoryKey(r.Category)
			if err != nil {
				return 0, err
			}
			if classID != 0 {
				if err = s.compactProjectionReady(18); err != nil {
					return 0, err
				}
				err = s.read().QueryRow(`SELECT COALESCE((SELECT total FROM catalog_movie_category_visible_summaries WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=?),0)`, classID, generation, libraryID, kind, value).Scan(&count)
			} else {
				err = s.read().QueryRow(`SELECT COALESCE((SELECT total FROM catalog_movie_category_summaries WHERE library_id=? AND kind=? AND value=?),0)`, libraryID, kind, value).Scan(&count)
			}
			return count, err
		}
		if classID != 0 {
			if err := s.compactProjectionReady(18); err != nil {
				return 0, err
			}
			err := s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM catalog_visibility_sort_buckets WHERE class_id=? AND generation=? AND library_id=? AND kind=1 AND axis=0`, classID, generation, libraryID).Scan(&count)
			return count, err
		}
		err := s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM catalog_browse_buckets WHERE library_id=? AND kind=1 AND container=0 AND axis=0`, libraryID).Scan(&count)
		return count, err
	}
	where, args, err := s.movieContentWhere(r, libraryID)
	if err != nil {
		return 0, err
	}
	var count int
	err = s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+where, args...).Scan(&count)
	return count, err
}

func (s *Service) movieContentWhere(r ContentRequest, libraryID int) (string, []any, error) {
	where := `e.library_id=? AND e.kind=1 AND e.item_id IS NOT NULL`
	args := []any{libraryID}
	if r.Q != "" {
		where += ` AND e.title LIKE ? ESCAPE '\'`
		args = append(args, searchPrefix(r.Q))
	}
	if r.Category != "" {
		kind, value, err := movieCategoryKey(r.Category)
		if err != nil {
			return "", nil, err
		}
		where += ` AND EXISTS(SELECT 1 FROM catalog_movie_category_members cm WHERE cm.item_id=e.entity_id AND cm.library_id=e.library_id AND cm.kind=? AND cm.value=?)`
		args = append(args, kind, value)
	}
	if r.EntityID != "" {
		where += ` AND EXISTS(SELECT 1 FROM catalog_collection_members cm WHERE cm.item_id=e.entity_id AND cm.collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=10))`
		args = append(args, r.EntityID)
	}
	if restrictions := r.Viewer.EffectiveRestrictions(); restrictions.Active() {
		key, generation, ready, err := s.publishedVisibilityClass(r.Library, restrictions)
		if err != nil {
			return "", nil, err
		}
		if !ready {
			return where + " AND 0", args, nil
		}
		_, classID, err := s.currentCategoryClass(r.Library, key, generation)
		if err != nil {
			return "", nil, err
		}
		where += ` AND EXISTS(SELECT 1 FROM compact_visibility_rows v WHERE v.class_id=? AND v.generation=? AND v.entity_id=e.entity_id AND v.library_id=e.library_id)`
		args = append(args, classID, generation)
	}
	return where, args, nil
}

func movieCategoryKey(category string) (int, string, error) {
	field, value, ok := strings.Cut(category, ":")
	if !ok || value == "" {
		return 0, "", requestinput.New("unknown category")
	}
	switch field {
	case "decade":
		year, err := categoryRange(category)
		if err != nil {
			return 0, "", err
		}
		return 0, strconv.Itoa(year), nil
	case "genre":
		return 1, value, nil
	case "studio":
		return 2, value, nil
	}
	return 0, "", requestinput.New("unknown category")
}
func (s *Service) contentEntities(r ContentRequest, cursor string, limit int) ([]ContentEntry, string, int, error) {
	entries := []ContentEntry{}
	var base, kind, nav string
	args := []any{r.Library}
	var libraryID int
	if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, r.Library).Scan(&libraryID); err != nil {
		return entries, "", 0, err
	}
	var collectionClass, collectionGeneration int64
	if r.View == "collections" && r.Viewer.EffectiveRestrictions().Active() {
		var err error
		collectionClass, collectionGeneration, err = s.collectionClassReady(r.Library, r.Viewer.EffectiveRestrictions())
		if err != nil {
			return entries, "", 0, err
		}
	}
	leafVisible, leafArgs := r.Viewer.itemVisibilitySQL("item.id")
	switch {
	case r.View == "collections":
		kind, nav = "collection", "collection"
		if r.Viewer.EffectiveRestrictions().Active() {
			base = `SELECT pid(e.public_id) id,e.title title,'' subtitle,COALESCE(vc.total,0) n
			 FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_libraries l ON l.id=c.library_id
			 LEFT JOIN catalog_collection_visible_counts vc ON vc.class_id=? AND vc.generation=? AND vc.collection_id=c.entity_id
			 WHERE l.library_id=? AND ` + collectionHeadFence + ` AND (c.member_count=0 OR COALESCE(vc.total,0)>0)`
			args = []any{collectionClass, collectionGeneration, r.Library}
		} else {
			base = `SELECT pid(e.public_id) id,e.title title,'' subtitle,c.member_count n FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_libraries l ON l.id=c.library_id WHERE l.library_id=? AND ` + collectionHeadFence
		}
	case r.View == "artist":
		kind, nav = "album", "album"
		base = `SELECT pid(album.public_id) id,album.title title,'' subtitle,0 n FROM catalog_entities album JOIN catalog_albums ad ON ad.entity_id=album.id JOIN catalog_libraries l ON l.id=album.library_id
 WHERE l.library_id=? AND album.kind=6 AND album.retired=0
 AND EXISTS(SELECT 1 FROM catalog_songs song JOIN catalog_entities item ON item.id=song.entity_id WHERE song.album_id=album.id AND ` + leafVisible + `)
 AND (ad.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5) OR EXISTS(SELECT 1 FROM catalog_songs song JOIN catalog_song_artists sa ON sa.song_id=song.entity_id JOIN catalog_entities artist ON artist.id=sa.artist_id WHERE song.album_id=album.id AND artist.public_id=pid_blob(?)))`
		args = append(args, leafArgs...)
		args = append(args, r.EntityID, r.EntityID)
	case true:
		var libraryKind string
		if e := s.read().QueryRow(`SELECT kind FROM libraries WHERE id=?`, r.Library).Scan(&libraryKind); e != nil {
			return entries, "", 0, e
		}
		switch libraryKind {
		case "tv", "anime":
			kind, nav = "show", "show"
			base = `SELECT pid(show.public_id) id,show.title title,'' subtitle,0 n FROM catalog_entities show JOIN catalog_shows sd ON sd.entity_id=show.id JOIN catalog_libraries l ON l.id=show.library_id
 WHERE l.library_id=? AND show.kind=2 AND show.retired=0
 AND EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_asset_links a ON a.entity_id=item.id WHERE ep.show_id=show.id
 AND ` + leafVisible + `)`
			args = append(args, leafArgs...)
		case "music":
			kind, nav = "artist", "artist"
			base = `SELECT pid(artist.public_id) id,artist.title title,'' subtitle,0 n FROM catalog_entities artist JOIN catalog_artists ad ON ad.entity_id=artist.id JOIN catalog_libraries l ON l.id=artist.library_id
 WHERE l.library_id=? AND artist.kind=5 AND artist.retired=0
 AND (EXISTS(SELECT 1 FROM catalog_song_artists sa JOIN catalog_songs song ON song.entity_id=sa.song_id JOIN catalog_entities item ON item.id=song.entity_id WHERE sa.artist_id=artist.id AND ` + leafVisible + `)
 OR EXISTS(SELECT 1 FROM catalog_albums album JOIN catalog_songs song ON song.album_id=album.entity_id JOIN catalog_entities item ON item.id=song.entity_id WHERE album.artist_id=artist.id AND EXISTS(SELECT 1 FROM catalog_entities ae WHERE ae.id=album.entity_id AND ae.retired=0)
 AND ` + leafVisible + `))`
			args = append(args, leafArgs...)
			args = append(args, leafArgs...)
		case "audiobook":
			kind, nav = "book", "book"
			base = `SELECT pid(book.public_id) id,book.title title,bd.author subtitle,0 n FROM catalog_entities book JOIN catalog_books bd ON bd.entity_id=book.id JOIN catalog_libraries l ON l.id=book.library_id
 WHERE l.library_id=? AND book.kind=8 AND book.retired=0
 AND EXISTS(SELECT 1 FROM catalog_book_files f JOIN catalog_entities item ON item.id=f.entity_id WHERE f.book_id=book.id AND ` + leafVisible + `)`
			args = append(args, leafArgs...)
		default:
			return entries, "", 0, requestinput.New("unsupported hierarchy")
		}
	}
	where := `1=1`
	if r.Q != "" {
		where += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, searchPrefix(r.Q))
	}
	var count int
	classKey, generation := "", int64(0)
	classReady := false
	if (r.View == "collections" || (r.Q == "" && r.EntityID == "")) && r.View != "artist" && r.Viewer.EffectiveRestrictions().Active() {
		var e error
		classKey, generation, classReady, e = s.publishedVisibilityClass(r.Library, r.Viewer.EffectiveRestrictions())
		if e != nil {
			return entries, "", 0, e
		}
		if !classReady {
			var small bool
			if small, e = s.visibilitySmallLibrary(r.Library); e != nil {
				return entries, "", 0, e
			}
			if !small {
				return entries, "", 0, ErrVisibilityBuilding
			}
		}
	}
	if r.View == "collections" && r.Viewer.EffectiveRestrictions().Active() && !classReady {
		return entries, "", 0, ErrVisibilityBuilding
	}
	// A restricted viewer without a published class reaches here only for a
	// small library (checked above): its count and page apply the
	// restriction row by row (leafVisible), never an unrestricted total.
	if classReady && r.View == "collections" && r.Q != "" {
		e := s.read().QueryRow(`SELECT count(*) FROM (SELECT 1 FROM catalog_collections c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_libraries l ON l.id=c.library_id
			 LEFT JOIN catalog_collection_visible_counts vc ON vc.class_id=? AND vc.generation=? AND vc.collection_id=c.entity_id
			 WHERE l.library_id=? AND `+collectionHeadFence+` AND (c.member_count=0 OR COALESCE(vc.total,0)>0) AND e.title LIKE ? ESCAPE '\')`,
			collectionClass, collectionGeneration, r.Library, searchPrefix(r.Q)).Scan(&count)
		if e != nil {
			return entries, "", 0, e
		}
	} else if classReady {
		kindID, parseErr := compactcatalog.ParseKind(kind)
		if parseErr != nil {
			return entries, "", 0, parseErr
		}
		classID, _, classErr := s.contentEntityClass(r, classKey, generation, collectionClass)
		if classErr != nil {
			return entries, "", 0, classErr
		}
		if e := s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM compact_visibility_counts WHERE class_id=? AND generation=? AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=?`, classID, generation, r.Library, int(kindID)).Scan(&count); e != nil {
			return entries, "", 0, e
		}
	} else if r.Q == "" && r.View != "artist" && !r.Viewer.EffectiveRestrictions().Active() && (kind == "show" || kind == "artist" || kind == "book" || kind == "collection") {
		if e := s.contentEligibleReady(kind); e != nil {
			return entries, "", 0, e
		}
		kindID, parseErr := compactcatalog.ParseKind(kind)
		if parseErr != nil {
			return entries, "", 0, parseErr
		}
		if e := s.read().QueryRow(`SELECT COALESCE((SELECT total FROM catalog_content_eligible_counts WHERE library_id=? AND kind=?),0)`, libraryID, int(kindID)).Scan(&count); e != nil {
			return entries, "", 0, e
		}
	} else if e := s.read().QueryRow(`SELECT count(*) FROM (`+base+`) WHERE `+where, args...).Scan(&count); e != nil {
		return entries, "", 0, e
	}
	scope := cursorScope{Library: r.Library, Viewer: r.ViewerFence, Profile: r.Profile, View: r.View, Entity: r.EntityID, Sort: r.Sort, Direction: r.Direction, Search: r.Q, Limit: r.Limit}
	// A grid opened at an index (M10) skips that many rows once; its next
	// cursor then continues by key like any other page.
	offset := 0
	if cursor == "" && r.Start > 0 {
		offset = r.Start
	}
	order, op := "ASC", ">"
	if r.Direction == "desc" {
		order, op = "DESC", "<"
	}
	if cursor != "" && !classReady {
		c, e := s.decodeCursor(cursor, scope)
		if e != nil {
			return entries, "", 0, e
		}
		where += ` AND title COLLATE NOCASE ` + op + `= ? AND (title COLLATE NOCASE ` + op + ` ? OR (title COLLATE NOCASE = ? AND id ` + op + ` ?))`
		args = append(args, c.Value, c.Value, c.Value, c.ID)
	}
	var rows *sql.Rows
	var e error
	if classReady {
		kindID, parseErr := compactcatalog.ParseKind(kind)
		if parseErr != nil {
			return entries, "", 0, parseErr
		}
		classID, _, classErr := s.contentEntityClass(r, classKey, generation, collectionClass)
		if classErr != nil {
			return entries, "", 0, classErr
		}
		classWhere := `v.class_id=? AND v.generation=? AND v.library_id=? AND v.kind=?`
		classArgs := []any{classID, generation, libraryID, int(kindID)}
		if cursor != "" {
			c, decodeErr := s.decodeCursor(cursor, scope)
			if decodeErr != nil {
				return entries, "", 0, decodeErr
			}
			classWhere += ` AND (v.sort_key COLLATE NOCASE ` + op + ` ? OR (v.sort_key COLLATE NOCASE=? AND pid(ce.public_id) ` + op + ` ?))`
			classArgs = append(classArgs, c.Value, c.Value, c.ID)
		}
		if r.View == "collections" && r.Q != "" {
			classWhere += ` AND br.title LIKE ? ESCAPE '\'`
			classArgs = append(classArgs, searchPrefix(r.Q))
		}
		// Restricted collection counts come from maintained integer-key
		// contributions; no page request scans a collection's members.
		n := `0`
		if kind == "collection" {
			n = `COALESCE((SELECT cc.total FROM catalog_collection_visible_counts cc
			 WHERE cc.class_id=v.class_id AND cc.generation=v.generation AND cc.collection_id=v.entity_id),0)`
		}
		query := `SELECT pid(ce.public_id),br.title,COALESCE(b.author,''),` + n + `,v.sort_key
			FROM compact_visibility_rows v INDEXED BY compact_visibility_page
			JOIN catalog_entities ce ON ce.id=v.entity_id
			JOIN catalog_browse_rows br ON br.entity_id=v.entity_id
			LEFT JOIN catalog_books b ON b.entity_id=v.entity_id WHERE ` + classWhere + `
			ORDER BY v.sort_key COLLATE NOCASE ` + order + `,pid(ce.public_id) ` + order + ` LIMIT ? OFFSET ?`
		rows, e = s.read().Query(query, append(classArgs, limit+1, offset)...)
	} else {
		args = append(args, limit+1, offset)
		rows, e = s.read().Query(`SELECT id,title,subtitle,n,title FROM (`+base+`) WHERE `+where+` ORDER BY title COLLATE NOCASE `+order+`,id `+order+` LIMIT ? OFFSET ?`, args...)
	}
	if e != nil {
		return entries, "", 0, e
	}
	keys := []string{}
	for rows.Next() {
		var entry ContentEntry
		var memberCount int
		var sortKey string
		if e = rows.Scan(&entry.ID, &entry.Title, &entry.Subtitle, &memberCount, &sortKey); e != nil {
			rows.Close()
			return entries, "", 0, e
		}
		entry.Kind = kind
		entry.Navigation = &ContentNavigation{View: nav, EntityID: entry.ID}
		if kind == "collection" {
			entry.Count = &memberCount
		}
		entries = append(entries, entry)
		keys = append(keys, sortKey)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return entries, "", 0, e
	}
	next := ""
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1]
		next, e = s.encodeCursor(cursorValue{Scope: scope, Value: keys[len(entries)-1], ID: last.ID, Expires: time.Now().Add(30 * time.Minute).Unix()})
	}
	// PERF-12: entity grids omit overviews. No client reads an overview in a
	// grid or row (only heroes/detail and episode rows do), so the per-page
	// show-overview query is gone, not just its assignment.

	targets := make([]artworkTarget, 0, len(entries))
	for _, entry := range entries {
		targets = append(targets, artworkTarget{entry.Kind, entry.ID})
	}
	artwork, err := s.resolveArtwork(targets)
	if err != nil {
		return entries, "", 0, err
	}
	type resumeValue struct {
		item     string
		position float64
	}
	resumes := map[string]resumeValue{}
	if kind == "book" {
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			ids = append(ids, entry.ID)
		}
		resumeVisibility, resumeArgs := r.Viewer.itemVisibilitySQL("r.item_id")
		rows, err := s.read().Query(`SELECT pid(b.public_id),pid(i.public_id),r.position/1000.0 FROM book_resume r JOIN catalog_entities b ON b.id=r.book_id JOIN catalog_entities i ON i.id=r.item_id JOIN catalog_item_availability v ON v.entity_id=r.item_id AND v.available=1 WHERE r.profile_id=? AND b.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND `+resumeVisibility, append([]any{r.Profile, idsJSON(ids)}, resumeArgs...)...)
		if err != nil {
			return entries, "", 0, err
		}
		for rows.Next() {
			var id string
			var v resumeValue
			if err = rows.Scan(&id, &v.item, &v.position); err != nil {
				rows.Close()
				return entries, "", 0, err
			}
			resumes[id] = v
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return entries, "", 0, err
		}
	}
	for index := range entries {
		entry := &entries[index]
		art := artwork[artworkTarget{entry.Kind, entry.ID}]
		entry.PosterURL, entry.BackdropURL = art.PosterURL, art.BackdropURL
		entry.Overview = ""
		if v, ok := resumes[entry.ID]; ok {
			entry.Playback = &ContentPlayback{ItemID: v.item, StartSeconds: &v.position}
			entry.ProgressSeconds = &v.position
		}
	}

	return entries, next, count, e
}

func (s *Service) contentEntityClass(r ContentRequest, classKey string, generation, collectionClass int64) (int, int, error) {
	if err := s.compactProjectionReady(18); err != nil {
		return 0, 0, err
	}
	libraryID, classID, err := s.currentCategoryClass(r.Library, classKey, generation)
	if err != nil {
		return 0, 0, err
	}
	if collectionClass != 0 && int64(classID) != collectionClass {
		return 0, 0, ErrVisibilityBuilding
	}
	return classID, libraryID, nil
}

func (s *Service) contentEligibleReady(kind string) error {
	if err := s.compactProjectionReady(18, 19); err != nil {
		return err
	}
	return nil
}
func (s *Service) contentDiscoveryCount(library, profile, section string) (int, error) {
	var n int
	var e error
	if section == "recently_added" {
		e = s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM catalog_home_buckets WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=1 AND recent=1 AND available=1`, library).Scan(&n)
	} else {
		return s.continueCount(library, profile)
	}
	return n, e
}
func (s *Service) contentCategories(r ContentRequest) ([]Category, error) {
	if r.Q == "" && r.EntityID == "" && r.Viewer.EffectiveRestrictions().Active() {
		key, generation, ready, err := s.publishedVisibilityClass(r.Library, r.Viewer.EffectiveRestrictions())
		if err != nil {
			return nil, err
		}
		if ready {
			categories, err := s.compactMovieCategoriesForClass(r.Library, key, generation)
			return categories, err
		}
		return []Category{}, nil
	}
	if r.Q == "" && r.EntityID == "" && !r.Viewer.EffectiveRestrictions().Active() {
		categories, err := s.Categories(r.Viewer, r.Library)
		return categories, err
	}
	r.Category = ""
	return s.compactFilteredMovieCategories(r)
}
