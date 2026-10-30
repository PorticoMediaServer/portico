package catalog

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type ListeningSimilar struct {
	ItemID    string          `json:"itemId"`
	LibraryID string          `json:"libraryId"`
	Revision  ContentRevision `json:"revision"`
	Entries   []ContentEntry  `json:"entries"`
	// NextCursor continues the ranking from the next page while HasMore.
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}
type listeningSimilarity struct {
	id                  int64
	public, title, kind string
	genres              map[string]bool
	profiles            map[string]bool
}

// ListeningSimilar is the first page of the genre ranking (listeningSimilarRanked).
func (s *Service) ListeningSimilar(viewer Viewer, id string, limit int) (ListeningSimilar, error) {
	out, err := s.ListeningSimilarPage(viewer, id, false, "", limit)
	return out.Items, err
}

// listeningSimilarRanked reads only local maintained facet postings and
// bounded co-play signals. It never invokes a metadata provider or uses audio
// analysis. Genres lead the order; co-play breaks ties only with three actual
// currently sharing active profiles per candidate. No contributor identities
// leave here. The whole ranking is returned, best first, as public ids.
func (s *Service) listeningSimilarRanked(viewer Viewer, id string) (string, []string, error) {
	out := ListeningSimilar{ItemID: id, Entries: []ContentEntry{}}

	var seed int64
	var kind int
	var libraryID int64
	err := s.read().QueryRow(`SELECT e.id,e.kind,l.library_id,l.id FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, id).Scan(&seed, &kind, &out.LibraryID, &libraryID)
	if err != nil {
		return out.LibraryID, nil, err
	}
	if !viewer.AllowsLibrary(out.LibraryID) {
		return out.LibraryID, nil, sql.ErrNoRows
	}
	if kind == 7 || kind == 9 {
		table, col, next := "catalog_songs", "album_id", 6
		if kind == 9 {
			table, col, next = "catalog_book_files", "book_id", 8
		}
		var parent int64
		if err = s.read().QueryRow(`SELECT `+col+` FROM `+table+` WHERE entity_id=?`, seed).Scan(&parent); err != nil {
			return out.LibraryID, nil, err
		}
		visible, args := viewer.itemVisibilitySQL("e.id")
		var ok bool
		if err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=? AND `+visible+`)`, append([]any{seed}, args...)...).Scan(&ok); err != nil {
			return out.LibraryID, nil, err
		}
		if !ok {
			return out.LibraryID, nil, sql.ErrNoRows
		}
		seed, kind = parent, next
	}
	kindName := map[int]string{5: "artist", 6: "album", 8: "book"}[kind]
	if kindName == "" {
		return out.LibraryID, nil, sql.ErrNoRows
	}
	visible, args := listeningSectionVisibility(kindName, viewer)
	visible = strings.ReplaceAll(visible, "projected.entity_id", "e.id")
	visible = strings.ReplaceAll(visible, "WHERE album.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND album.id=`)
	visible = strings.ReplaceAll(visible, "WHERE book.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND book.id=`)
	var permitted bool
	if err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=? AND `+visible+`)`, append([]any{seed}, args...)...).Scan(&permitted); err != nil {
		return out.LibraryID, nil, err
	}
	if !permitted {
		return out.LibraryID, nil, sql.ErrNoRows
	}
	if err = s.compactProjectionReady(18, 32); err != nil {
		return out.LibraryID, nil, err
	}
	seeds := []int64{seed}
	if kind == 5 {
		albumVisible, albumArgs := listeningSectionVisibility("album", viewer)
		albumVisible = strings.ReplaceAll(albumVisible, "projected.entity_id", "album.id")
		seeds, err = scanInt64s(s.read().Query(`SELECT album.id FROM catalog_albums a JOIN catalog_entities album ON album.id=a.entity_id WHERE a.artist_id=? AND album.retired=0 AND `+albumVisible+` ORDER BY album.id LIMIT 64`, append([]any{seed}, albumArgs...)...))
		if err != nil {
			return out.LibraryID, nil, err
		}
	}
	genres := []string{}
	seedVisible, seedArgs := viewer.itemVisibilitySQL("member.item_id")
	rows, err := s.read().Query(`SELECT f.facet FROM json_each(?) j JOIN catalog_browse_memberships member ON member.entity_id=j.value JOIN catalog_rec_facets f ON f.entity_id=member.item_id WHERE f.facet LIKE 'g:%' AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=member.item_id AND av.available=1 AND av.retired=0) AND `+seedVisible+` GROUP BY f.facet ORDER BY count(*) DESC,f.facet LIMIT 8`, append([]any{idsJSON64(seeds)}, seedArgs...)...)
	if err != nil {
		return out.LibraryID, nil, err
	}
	for rows.Next() {
		var g string
		if err = rows.Scan(&g); err != nil {
			break
		}
		genres = append(genres, g)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return out.LibraryID, nil, err
	}
	works := map[int64]map[string]bool{}
	for _, g := range genres {
		if err = s.recPostings(g, libraryID, 150, func(work int64, _ float64) {
			if works[work] == nil {
				works[work] = map[string]bool{}
			}
			works[work][g] = true
		}); err != nil {
			return out.LibraryID, nil, err
		}
	}
	co := map[int64]map[string]bool{}
	if s.serverWidePlays() {
		profiles := map[string]bool{}
		for _, work := range seeds {
			ps, e := listeningProfileKeys(s.read().Query(`SELECT c.profile_id FROM catalog_browse_memberships member CROSS JOIN personal_play_counts c INDEXED BY personal_play_counts_item ON c.item_id=member.item_id WHERE member.entity_id=? AND c.plays>0 GROUP BY c.profile_id ORDER BY max(c.plays) DESC,c.profile_id LIMIT 64`, work))
			if e != nil {
				return out.LibraryID, nil, e
			}
			for _, p := range ps {
				if len(profiles) < 64 {
					profiles[p] = true
				}
			}
		}
		keys := []string{}
		for p := range profiles {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		for _, p := range keys {
			sharing, e := s.profileSharesActivity(p, s.recommendationNow(time.Time{}))
			if e != nil {
				return out.LibraryID, nil, e
			}
			if !sharing {
				continue
			}
			contributor, e := s.listeningContributor(p, out.LibraryID)
			if e != nil {
				return out.LibraryID, nil, e
			}
			if contributor == nil {
				continue
			}
			permitted, permittedArgs := contributor.itemVisibilitySQL("member.item_id")
			var seedVisible bool
			if e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_browse_memberships member JOIN personal_play_counts c ON c.item_id=member.item_id AND c.profile_id=? AND c.plays>0 WHERE member.entity_id IN(SELECT value FROM json_each(?)) AND `+permitted+` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=member.item_id AND av.available=1 AND av.retired=0))`, append([]any{p, idsJSON64(seeds)}, permittedArgs...)...).Scan(&seedVisible); e != nil {
				return out.LibraryID, nil, e
			}
			if !seedVisible {
				continue
			}
			actor, _ := viewerFromPersonalKey(p)
			itemVisible, itemArgs := contributor.itemVisibilitySQL("item.id")
			recent, e := scanInt64s(s.read().Query(`SELECT COALESCE(song.album_id,part.book_id) FROM play_history h INDEXED BY play_history_listen_profile JOIN catalog_entities item ON item.id=h.item_id LEFT JOIN catalog_songs song ON song.entity_id=item.id LEFT JOIN catalog_book_files part ON part.entity_id=item.id WHERE h.authority=? AND h.account_id=? AND h.profile_id=? AND h.completed=1 AND h.kind IN(7,9) AND item.kind IN(7,9) AND h.started_ms<=? AND `+itemVisible+` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=item.id AND av.available=1 AND av.retired=0) ORDER BY h.started_ms DESC,h.id DESC LIMIT 100`, append([]any{actor.Authority, actor.AccountID, actor.ProfileID, s.recommendationNow(time.Time{}).UnixMilli()}, itemArgs...)...))
			if e != nil {
				return out.LibraryID, nil, e
			}
			for _, work := range recent {
				if co[work] == nil {
					co[work] = map[string]bool{}
				}
				co[work][p] = true
			}
		}
	}
	for work, profiles := range co {
		if len(profiles) >= 3 && works[work] == nil {
			works[work] = map[string]bool{}
		}
	}
	ids := []int64{}
	for work := range works {
		ids = append(ids, work)
	}
	if len(ids) == 0 {
		return out.LibraryID, nil, nil
	}
	// Candidate genres are rechecked over this viewer's permitted leaves;
	// a restricted track's tags cannot influence another visible track's album.
	if len(genres) > 0 {
		permitted, bind := viewer.itemVisibilitySQL("member.item_id")
		found, e := s.read().Query(`SELECT member.entity_id,f.facet FROM json_each(?) j JOIN catalog_browse_memberships member ON member.entity_id=j.value JOIN catalog_rec_facets f ON f.entity_id=member.item_id WHERE f.facet IN(SELECT value FROM json_each(?)) AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=member.item_id AND av.available=1 AND av.retired=0) AND `+permitted+` GROUP BY member.entity_id,f.facet`, append([]any{idsJSON64(ids), idsJSON(genres)}, bind...)...)
		if e != nil {
			return out.LibraryID, nil, e
		}
		for work := range works {
			works[work] = map[string]bool{}
		}
		for found.Next() {
			var work int64
			var g string
			if e = found.Scan(&work, &g); e != nil {
				break
			}
			works[work][g] = true
		}
		if x := found.Err(); e == nil {
			e = x
		}
		found.Close()
		if e != nil {
			return out.LibraryID, nil, e
		}
	}
	// Every candidate is authorized before it can affect the ranking. In
	// artist mode albums resolve to their real album artist, never to a name.
	candidateKind := kind
	if kind == 5 {
		candidateKind = 6
	}
	candidateName := map[int]string{6: "album", 8: "book"}[candidateKind]
	candidateVisible, candidateArgs := listeningSectionVisibility(candidateName, viewer)
	candidateVisible = strings.ReplaceAll(candidateVisible, "projected.entity_id", "work.id")
	candidateVisible = strings.ReplaceAll(candidateVisible, "WHERE album.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND album.id=`)
	candidateVisible = strings.ReplaceAll(candidateVisible, "WHERE book.id=", `WHERE EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) AND book.id=`)
	selectID, join := "work.id", ""
	if kind == 5 {
		selectID = "artist.id"
		join = ` JOIN catalog_albums album ON album.entity_id=work.id JOIN catalog_entities artist ON artist.id=album.artist_id AND artist.retired=0`
	}
	rows, err = s.read().Query(`SELECT work.id,`+selectID+`,pid(target.public_id),target.title FROM json_each(?) j JOIN catalog_entities work ON work.id=j.value`+join+` JOIN catalog_entities target ON target.id=`+selectID+` WHERE work.kind=? AND work.library_id=? AND work.retired=0 AND `+candidateVisible, append([]any{idsJSON64(ids), candidateKind, libraryID}, candidateArgs...)...)
	if err != nil {
		return out.LibraryID, nil, err
	}
	candidates := map[int64]*listeningSimilarity{}
	for rows.Next() {
		var work, target int64
		var public, title string
		if err = rows.Scan(&work, &target, &public, &title); err != nil {
			break
		}
		if target == seed {
			continue
		}
		c := candidates[target]
		if c == nil {
			c = &listeningSimilarity{id: target, public: public, title: title, kind: kindName, genres: map[string]bool{}, profiles: map[string]bool{}}
			candidates[target] = c
		}
		for g := range works[work] {
			c.genres[g] = true
		}
		for p := range co[work] {
			c.profiles[p] = true
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return out.LibraryID, nil, err
	}
	ranked := []*listeningSimilarity{}
	for _, c := range candidates {
		if len(c.profiles) < 3 {
			c.profiles = nil
		}
		if len(c.genres) > 0 || len(c.profiles) >= 3 {
			ranked = append(ranked, c)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if len(a.genres) != len(b.genres) {
			return len(a.genres) > len(b.genres)
		}
		if len(a.profiles) != len(b.profiles) {
			return len(a.profiles) > len(b.profiles)
		}
		if strings.ToLower(a.title) != strings.ToLower(b.title) {
			return strings.ToLower(a.title) < strings.ToLower(b.title)
		}
		return a.id < b.id
	})
	public := make([]string, 0, len(ranked))
	for _, c := range ranked {
		public = append(public, c.public)
	}
	return out.LibraryID, public, nil
}

func listeningProfileKeys(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Service) listeningContributor(key, library string) (*Viewer, error) {
	actor, ok := viewerFromPersonalKey(key)
	if !ok || actor.Authority != "local" {
		return nil, nil
	}
	var allowed bool
	err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.id=? AND p.account_id=? AND p.deleted=0 AND m.disabled=0 AND (m.role='owner' OR EXISTS(SELECT 1 FROM json_each(m.allowed_libraries) WHERE value=?)) AND (p.allowed_libraries IS NULL OR EXISTS(SELECT 1 FROM json_each(p.allowed_libraries) WHERE value=?)))`, actor.ProfileID, actor.AccountID, library, library).Scan(&allowed)
	if err != nil || !allowed {
		return nil, err
	}
	tx := dbwork.Snapshot(s.Context())
	if tx == nil {
		return nil, sql.ErrTxDone
	}
	restrictions, err := identity.RestrictionsForViewerTx(s.Context(), tx, actor)
	if err != nil {
		return nil, err
	}
	content, err := access.ContentLimitsTx(s.Context(), tx, actor.AccountID, restrictions.Content())
	if err != nil {
		return nil, err
	}
	return &Viewer{Profile: key, Libraries: []string{library}, Restrictions: content}, nil
}
