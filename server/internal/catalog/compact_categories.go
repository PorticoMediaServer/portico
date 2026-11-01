package catalog

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// compactMovieCategories reads maintained totals and title-ordered poster
// samples. The serving generation stays readable while source work is queued.
func (s *Service) compactMovieCategories(library string) ([]Category, error) {
	if err := s.compactProjectionReady(18, 27); err != nil {
		return nil, err
	}
	var libraryID int
	if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, library).Scan(&libraryID); err != nil {
		return nil, err
	}
	return s.compactMovieCategoryRows(libraryID, 0, 0)
}

func (s *Service) compactMovieCategoriesForClass(library, classKey string, generation int64) ([]Category, error) {
	if err := s.compactProjectionReady(18, 19, 20, 27); err != nil {
		return nil, err
	}
	libraryID, classID, err := s.currentCategoryClass(library, classKey, generation)
	if err != nil {
		return nil, err
	}
	return s.compactMovieCategoryRows(libraryID, classID, generation)
}

func (s *Service) currentCategoryClass(library, classKey string, generation int64) (int, int, error) {
	var libraryID, classID int
	err := s.read().QueryRow(`SELECT c.library_id,c.id
	 FROM compact_visibility_classes c JOIN catalog_libraries l ON l.id=c.library_id
	 WHERE c.class_key=? AND c.active_generation=? AND l.library_id=?`, classKey, generation, library).Scan(&libraryID, &classID)
	if err != nil {
		return 0, 0, err
	}
	// Rating-age fan-out journals this class through its captured source
	// sequence; a raw library revision is not its policy freshness witness.
	return libraryID, classID, nil
}

func (s *Service) compactMovieCategoryRows(libraryID, classID int, generation int64) ([]Category, error) {
	out := []Category{}
	for kind := 0; kind <= 2; kind++ {
		order := `value COLLATE NOCASE`
		limit := 24
		if kind == 0 {
			order = `CAST(value AS INTEGER) DESC`
			limit = 40
		} else {
			order = `total DESC,value COLLATE NOCASE`
		}
		table := `catalog_movie_category_summaries`
		args := []any{libraryID, kind, limit}
		where := `library_id=? AND kind=?`
		if classID != 0 {
			table = `catalog_movie_category_visible_summaries`
			where = `class_id=? AND generation=? AND ` + where
			args = []any{classID, generation, libraryID, kind, limit}
		}
		rows, err := s.read().Query(`SELECT value,total,posters_json FROM `+table+` WHERE `+where+` ORDER BY `+order+` LIMIT ?`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var value, posters string
			var total int
			if err = rows.Scan(&value, &total, &posters); err != nil {
				rows.Close()
				return nil, err
			}
			category := Category{Count: total}
			switch kind {
			case 0:
				category.ID, category.Name = "decade:"+value, value+"s"
			case 1:
				category.ID, category.Name = "genre:"+value, value
			case 2:
				category.ID, category.Name = "studio:"+value, value
			}
			if posters != "[]" {
				if err = json.Unmarshal([]byte(posters), &category.ArtworkPaths); err != nil {
					rows.Close()
					return nil, err
				}
			}
			out = append(out, category)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// An arbitrary search or collection slice has no materialized category
// cross-product, so its categories are counted from its memberships: one pass
// over every matching movie's categories (the price of an ad hoc slice), exact
// for every category. The chips show the decades and the leading genres and
// studios; each carries its exact count and up to four posters.
func (s *Service) compactFilteredMovieCategories(r ContentRequest) ([]Category, error) {
	if err := s.compactProjectionReady(18, 27); err != nil {
		return nil, err
	}
	var libraryID int
	if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, r.Library).Scan(&libraryID); err != nil {
		return nil, err
	}
	where := `cm.library_id=? AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=cm.item_id)`
	args := []any{libraryID}
	if r.Q != "" {
		// The LIKE keeps the filter's meaning. The NOCASE range is what lets the
		// title index narrow to the prefix: plans never read a bound pattern
		// (the driver's planner stability guarantee). NOCASE and LIKE both fold
		// ASCII case only, so every match lies inside the range.
		where += ` AND cm.title LIKE ? ESCAPE '\' AND cm.title COLLATE NOCASE>=? AND cm.title COLLATE NOCASE<?`
		args = append(args, searchPrefix(r.Q), r.Q, r.Q+"\U0010FFFF")
	}
	if r.EntityID != "" {
		where += ` AND EXISTS(SELECT 1 FROM catalog_collection_members member WHERE member.item_id=cm.item_id AND member.collection_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=10))`
		args = append(args, r.EntityID)
	}
	if restrictions := r.Viewer.EffectiveRestrictions(); restrictions.Active() {
		key, generation, ready, err := s.publishedVisibilityClass(r.Library, restrictions)
		if err != nil {
			return nil, err
		}
		if !ready {
			return []Category{}, nil
		}
		if _, _, err := s.currentCategoryClass(r.Library, key, generation); err != nil {
			return nil, err
		}
		where += ` AND EXISTS(SELECT 1 FROM compact_visibility_rows v JOIN compact_visibility_classes cl ON cl.id=v.class_id WHERE v.entity_id=cm.item_id AND v.kind=1 AND cl.class_key=? AND v.generation=? AND v.library_id=cm.library_id)`
		args = append(args, key, generation)
	}
	type tally struct {
		kind  int
		value string
		n     int
	}
	rows, err := s.read().Query(`SELECT cm.kind,cm.value,count(*) FROM catalog_movie_category_members cm WHERE `+where+` GROUP BY cm.kind,cm.value`, args...)
	if err != nil {
		return nil, err
	}
	byKind := map[int][]*tally{}
	for rows.Next() {
		entry := &tally{}
		if err = rows.Scan(&entry.kind, &entry.value, &entry.n); err != nil {
			rows.Close()
			return nil, err
		}
		byKind[entry.kind] = append(byKind[entry.kind], entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Decades order by value as an integer, descending, at most 40.
	decades := byKind[0]
	sort.Slice(decades, func(i, j int) bool { return decadeValue(decades[i].value) > decadeValue(decades[j].value) })
	if len(decades) > 40 {
		decades = decades[:40]
	}
	// Genres and studios order by count descending, ties by value
	// case-insensitively ascending, at most 24 each.
	for _, kind := range []int{1, 2} {
		group := byKind[kind]
		sort.Slice(group, func(i, j int) bool {
			if group[i].n != group[j].n {
				return group[i].n > group[j].n
			}
			lowerI, lowerJ := strings.ToLower(group[i].value), strings.ToLower(group[j].value)
			if lowerI != lowerJ {
				return lowerI < lowerJ
			}
			return group[i].value < group[j].value
		})
		if len(group) > 24 {
			group = group[:24]
		}
		byKind[kind] = group
	}
	out := []Category{}
	kept := append(append([]*tally{}, decades...), byKind[1]...)
	kept = append(kept, byKind[2]...)
	for _, entry := range kept {
		category := Category{Count: entry.n}
		// Up to four posters, in title order, from the slice's own movies.
		posters, err := s.read().Query(`SELECT cm.poster_url FROM catalog_movie_category_members cm INDEXED BY catalog_movie_category_poster WHERE `+where+
			` AND cm.kind=? AND cm.value=? AND cm.poster_url<>'' ORDER BY cm.title COLLATE NOCASE,cm.item_id LIMIT 4`, append(append([]any{}, args...), entry.kind, entry.value)...)
		if err != nil {
			return nil, err
		}
		for posters.Next() {
			var poster string
			if err = posters.Scan(&poster); err != nil {
				posters.Close()
				return nil, err
			}
			category.ArtworkPaths = append(category.ArtworkPaths, poster)
		}
		err = posters.Err()
		posters.Close()
		if err != nil {
			return nil, err
		}
		switch entry.kind {
		case 0:
			category.ID, category.Name = "decade:"+entry.value, entry.value+"s"
		case 1:
			category.ID, category.Name = "genre:"+entry.value, entry.value
		case 2:
			category.ID, category.Name = "studio:"+entry.value, entry.value
		}
		out = append(out, category)
	}
	return out, nil
}

// decadeValue reads a decade category value as an integer, like the
// maintained summaries' CAST(value AS INTEGER) ordering does.
func decadeValue(value string) int {
	year, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return year
}
