package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// SearchGenres is the shared video-only genre read (ruling 36). Counts and art
// use one authorized snapshot. Missing art does not remove a counted genre:
// each surface skips slots it cannot draw, and All genres keeps the same list.
func (s *Service) searchGenresReference(viewer Viewer, request SearchGenreRequest) (SearchGenrePage, error) {
	out := SearchGenrePage{Genres: []SearchGenre{}}
	if request.Limit == 0 {
		request.Limit = SearchGenresMaximumLimit
	}
	if request.Limit < 1 || request.Limit > SearchGenresMaximumLimit {
		return out, browseIssue("limit", "genre limit must be between 1 and 50")
	}
	if dbwork.Snapshot(s.Context()) == nil {
		if err := s.PrepareRead(s.Context(), viewer.Restrictions); err != nil {
			return out, err
		}
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).searchGenresReference(viewer, request)
			return err
		})
		return out, err
	}
	scope, args, revision, err := s.browseMeasureScope(viewer, FacetRequest{Pivot: "titles"})
	if err != nil {
		return out, err
	}
	// Browse ordering revisions deliberately omit artwork changes. This read
	// also binds the ordinary library revision, which selected-art writes bump.
	var state string
	if err = s.read().QueryRow(`SELECT COALESCE(group_concat(state,','),'') FROM (
	 SELECT r.library_id||':'||r.revision state FROM library_revisions r
	 WHERE r.library_id IN(SELECT value FROM json_each(?)) ORDER BY r.library_id)`, viewer.librariesJSON()).Scan(&state); err != nil {
		return out, err
	}
	digest := sha256.Sum256([]byte("search-genres\x00" + revision + "\x00" + state))
	out.Revision = hex.EncodeToString(digest[:])
	cursorScope := cursorScope{Profile: viewer.Profile, Viewer: viewer.Fence, View: "search-genres", Limit: request.Limit, Sort: "count:desc,title:asc,value:asc"}
	offset := 0
	if request.Cursor != "" {
		cursor, e := s.decodeRevisionCursor(request.Cursor, cursorScope, ContentRevision{})
		if e != nil {
			return out, e
		}
		if cursor.Fingerprint != out.Revision {
			return out, ErrStaleContinuation
		}
		offset, e = strconv.Atoi(cursor.Value)
		if e != nil || offset < 0 {
			return out, ErrCursor
		}
	}
	prefix, bound := searchGenreMembership(scope, args, viewer)
	key := fmt.Sprintf("search-genres:%s:%s:%d:%d", out.Revision, viewer.Profile, request.Limit, offset)
	counted, hit := s.state.facetCache.get(key, 0)
	if !hit {
		counted, err = s.state.facetCache.do(key, func() (FacetPage, error) {
			if cached, ok := s.state.facetCache.get(key, 0); ok {
				return cached, nil
			}
			page := FacetPage{Values: []FacetValue{}}
			rows, e := s.read().Query(prefix+` SELECT value,value,count(*) total FROM genres GROUP BY value
			 ORDER BY total DESC,portico_sort_title(value,'',''),value LIMIT ? OFFSET ?`, append(bound, request.Limit+1, offset)...)
			if e != nil {
				return page, e
			}
			for rows.Next() {
				var value FacetValue
				if e = rows.Scan(&value.Value, &value.Label, &value.Count); e != nil {
					break
				}
				page.Values = append(page.Values, value)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e == nil {
				s.state.facetCache.put(key, facetCacheEntry{expires: time.Now().Add(time.Minute), page: page})
			}
			return page, e
		})
		if err != nil {
			return out, err
		}
	}
	values := counted.Values
	if len(values) > request.Limit {
		values = values[:request.Limit]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: cursorScope, Value: strconv.Itoa(offset + request.Limit), Fingerprint: out.Revision, Expires: time.Now().Add(30 * time.Minute).Unix()}, ContentRevision{})
		if err != nil {
			return out, err
		}
	}
	if len(values) == 0 {
		return out, nil
	}
	names := make([]string, len(values))
	for i, value := range values {
		names[i] = value.Value
		out.Genres = append(out.Genres, SearchGenre{Value: value.Value, Label: value.Label, Count: value.Count, Covers: []SearchGenreArt{}})
	}
	raw, _ := json.Marshal(names)
	// Before genre n, at most 2*(n-1) covers have been used. Keeping the best
	// 2*N candidates per role therefore contains its best two unused titles,
	// or every candidate when all are used. Go never materializes a library.
	// Ready status is deliberately re-read even when the counted list is cached.
	rows, err := s.read().Query(prefix+`, ranked AS (
	 SELECT genre.value,title.entity_id,a.kind,pid(entity.public_id) id,a.role,a.digest,
	 row_number() OVER(PARTITION BY genre.value,a.role ORDER BY COALESCE(title.rating_max,0) DESC,title.sort_key,title.entity_id) position
	 FROM genres genre JOIN catalog_browse_rows title ON title.entity_id=genre.entity_id
	 JOIN catalog_entities entity ON entity.id=title.entity_id
	 CROSS JOIN artwork_selections a ON a.entity_id=title.entity_id AND a.kind=CASE title.kind WHEN 1 THEN 'item' ELSE 'show' END
	 AND a.subject='' AND a.role IN('backdrop','poster')
	 CROSS JOIN artwork_sources object ON object.digest=a.digest
	 WHERE genre.value IN(SELECT value FROM json_each(?))
	 ) SELECT value,entity_id,kind,id,role,ranked.digest,`+artworkref.Columns+` FROM ranked`+artworkref.Joins("ranked.digest")+` WHERE position<=? ORDER BY value,role,position`, append(bound, string(raw), 2*len(values))...)
	if err != nil {
		return out, err
	}
	type candidate struct {
		entity int64
		art    SearchGenreArt
	}
	backdrops, covers := map[string][]candidate{}, map[string][]candidate{}
	for rows.Next() {
		var value, role, digest string
		var c candidate
		var d artworkref.Descriptor
		if err = rows.Scan(append([]any{&value, &c.entity, &c.art.Kind, &c.art.ID, &role, &digest}, d.ScanTargets()...)...); err != nil {
			break
		}
		c.art.Path = selectedArtworkURL(c.art.Kind, c.art.ID, role, "", digest, d)
		if role == "backdrop" {
			backdrops[value] = append(backdrops[value], c)
		} else {
			covers[value] = append(covers[value], c)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	usedBackdrops, usedCovers := map[int64]bool{}, map[int64]bool{}
	choose := func(candidates []candidate, used map[int64]bool, maximum int) []SearchGenreArt {
		chosen := []SearchGenreArt{}
		selected := map[int64]bool{}
		// Prefer unused titles, then fill from the best already-used ones.
		for _, unusedOnly := range []bool{true, false} {
			for _, c := range candidates {
				if len(chosen) == maximum {
					break
				}
				if selected[c.entity] || unusedOnly && used[c.entity] {
					continue
				}
				chosen = append(chosen, c.art)
				selected[c.entity], used[c.entity] = true, true
			}
		}
		return chosen
	}
	for i := range out.Genres {
		genre := &out.Genres[i]
		if chosen := choose(backdrops[genre.Value], usedBackdrops, 1); len(chosen) != 0 {
			genre.Backdrop = &chosen[0]
		}
		if candidates := covers[genre.Value]; len(candidates) < 2 {
			// A one-cover genre cannot be drawn on the phone. Do not reserve
			// that title ahead of a later genre that can fill its two slots.
			for _, c := range candidates {
				genre.Covers = append(genre.Covers, c.art)
			}
		} else {
			genre.Covers = choose(candidates, usedCovers, 2)
		}
	}
	return out, nil
}

// searchGenreMembership is shared by the counted tiles and their existing
// across-library destination, so an opened genre contains the titles it counted.
func searchGenreMembership(scope string, args []any, viewer Viewer) (string, []any) {
	memberVisible, memberArgs := viewer.itemVisibilitySQL("member.item_id")
	prefix := `WITH targets AS MATERIALIZED (
	 SELECT target.* FROM (` + scope + `) target
	 JOIN catalog_browse_rows title ON title.entity_id=target.entity_id AND title.kind IN(1,2)
	 JOIN catalog_libraries library ON library.id=target.library_id AND library.retired=0
	 JOIN libraries public_library ON public_library.id=library.library_id AND public_library.kind IN('movie','tv','anime')
	 ), e AS MATERIALIZED (
	 SELECT DISTINCT target.entity_id,member.item_id FROM targets target
	 JOIN catalog_browse_memberships member ON member.entity_id=target.entity_id WHERE ` + memberVisible + `
	 UNION SELECT target.entity_id,target.entity_id FROM targets target
	 WHERE NOT EXISTS(SELECT 1 FROM catalog_browse_memberships member WHERE member.entity_id=target.entity_id)
	 ), genres AS MATERIALIZED (
	 SELECT DISTINCT e.entity_id,CAST(COALESCE(ts.label_override,mg.label) AS TEXT) value
	 FROM e ` + facetSources["genre"].Join + `
	 WHERE COALESCE(ts.label_override,mg.label) IS NOT NULL AND COALESCE(ts.label_override,mg.label)<>''
	 )`
	bound := append(append([]any{}, args...), memberArgs...)
	return prefix, bound
}
