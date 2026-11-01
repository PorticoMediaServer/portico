package catalog

import (
	"hash/fnv"
	"sort"
	"strings"
	"unicode"
)

// Personal rows ("More recommendations" in Customize Home). Each generator is a ranking with a name:
// the profile's strongest genres and people, the next film of each franchise
// it has started, recent additions it will like, hidden gems, top-rated titles
// it hasn't seen, and favorites to rediscover. Home shows the strongest few
// (rotating daily among close ones), each title in at most one of them and not
// on Recommended's first page. The layout arranges them as one family,
// "for_you"; each row pages on its own (/v1/home/rows/for_you:…).

const (
	recFamily          = "for_you"
	recPersonalRows    = 5
	recRowMinimum      = 6 // a row with fewer titles isn't shown
	recShortRowMinimum = 3 // …except the naturally short ones (franchises, rediscover)
	recGenreRows       = 2
	recSubgenreRows    = 2
	recAppealRows      = 1
	recPersonRows      = 2
)

// recRow is one generated personal row.
type recRow struct {
	spec     homeRowSpec
	items    []recCandidate
	strength float64
	minimum  int
	// precise rows (the next film of a franchise) keep titles Recommended
	// also shows: the row says something Recommended doesn't.
	precise bool
	// filter is the browse filter equal to the row (a genre, a person), for
	// its See all; nil: the whole library, For you.
	filter *BrowseNode
}

// recSeeAll is a personal row's complete list: the library's works pivot with
// the row's filter, sorted For you.
// A row with no browse equivalent (a franchise's next film, hidden gems) has
// no See all: the whole library isn't that row.
func recSeeAll(libraryKind string, filter *BrowseNode) *ContentSeeAll {
	pivot := map[string]string{"movie": "movies", "tv": "shows", "anime": "shows", "music": "albums", "audiobook": "books"}[libraryKind]
	if pivot == "" || filter == nil {
		return nil
	}
	return &ContentSeeAll{Pivot: pivot, Query: filter, Sort: []BrowseSortSelection{{Field: "forYou", Direction: "desc"}}}
}

func recRowSpec(id, title, code string, params map[string]string, priority int) homeRowSpec {
	return homeRowSpec{ID: id, Title: title, TitleCode: code, TitleParams: params, Kind: "recommendation", Family: recFamily, ArtworkShape: "poster",
		Priority: priority, CacheTTL: 300, Cursors: true, Privacy: "personal", PolicyState: "available", Direction: "asc"}
}

// recGenerators lists the personal rows this profile can have now, each with
// its ranking. Memoised per session key, like every ranking.
func (x *recSession) recGenerators() ([]recRow, error) {
	var rows []recRow
	var filter *BrowseNode // the filter of the row add is given next
	add := func(spec homeRowSpec, minimum int, bonus float64, rank func() ([]recCandidate, error)) error {
		defer func() { filter = nil }()
		items, err := x.memo(spec.ID, rank)
		if err != nil {
			return err
		}
		strength := bonus
		for i := 0; i < len(items) && i < recRowMinimum; i++ {
			strength += items[i].Score / recRowMinimum
		}
		rows = append(rows, recRow{spec: spec, items: items, strength: strength, minimum: minimum, precise: spec.ID == "for_you:franchise" || spec.ID == "for_you:series", filter: filter})
		return nil
	}
	// Genres: the profile's strongest by taste (rarity would favor a rare
	// genre seen once over the one it watches every week).
	var genres []string
	for f, v := range x.taste.values {
		if strings.HasPrefix(f, "g:") && v > 0 {
			genres = append(genres, f)
		}
	}
	sort.Slice(genres, func(i, j int) bool {
		if a, b := x.taste.values[genres[i]], x.taste.values[genres[j]]; a != b {
			return a > b
		}
		return genres[i] < genres[j]
	})
	for i, g := range genres {
		if i == recGenreRows {
			break
		}
		label := x.s.recGenreLabel(strings.TrimPrefix(g, "g:"))
		facet := g
		filter = &BrowseNode{Field: "genre", Operator: "contains", Value: label}
		if err := add(recRowSpec("for_you:genre:"+strings.TrimPrefix(g, "g:"), label+" for you", "home.row.genreForYou", map[string]string{"genre": label}, 61+i), recRowMinimum, 0,
			func() ([]recCandidate, error) { return x.rank(x.facetRow(facet)) }); err != nil {
			return nil, err
		}
	}
	// People: directors, creators and actors the profile keeps finishing.
	var people []string
	for _, f := range x.strongest {
		if strings.HasPrefix(f, "p:") || strings.HasPrefix(f, "cd:directing:") || strings.HasPrefix(f, "cd:creator:") || strings.HasPrefix(f, "a:") || strings.HasPrefix(f, "b:") {
			people = append(people, f)
		}
	}
	named := map[string]bool{}
	for _, f := range people {
		if len(named) == recPersonRows {
			break
		}
		name, code, title := x.s.recPersonTitle(f)
		// A director's credit is both a person facet and a department facet:
		// one row per person.
		if named[strings.ToLower(name)] {
			continue
		}
		named[strings.ToLower(name)] = true
		i := len(named) - 1
		facet := f
		if field := map[string]string{"p": "actor", "cd": "director", "a": "artist", "b": "author"}[strings.SplitN(f, ":", 2)[0]]; field != "" && !strings.HasPrefix(f, "cd:creator:") {
			filter = &BrowseNode{Field: field, Operator: "contains", Value: name}
		}
		if err := add(recRowSpec("for_you:person:"+f, title, code, map[string]string{"name": name}, 63+i), recRowMinimum, 0,
			func() ([]recCandidate, error) { return x.rank(x.facetRow(facet)) }); err != nil {
			return nil, err
		}
	}
	// Moods, themes, subgenres and appeals, from the Portico title dataset's
	// tags ('x:<tag>'): the profile's strongest of each, named by the dataset's
	// labels. Each family keeps its own count, so a profile with many genres of
	// taste still gets the distinctive kind of row it has.
	moods, subgenres, appeals := 0, 0, 0
	for _, f := range x.strongest {
		if !strings.HasPrefix(f, "x:") {
			continue
		}
		tag := strings.TrimPrefix(f, "x:")
		var family, label string
		if err := x.s.read().QueryRow(`SELECT family,label FROM catalog_dataset_vocabulary WHERE tag=?`, tag).Scan(&family, &label); err != nil {
			continue
		}
		code, title, param, priority := "", "", "", 0
		switch family {
		case "mood", "tone", "pacing", "style":
			if moods == recGenreRows {
				continue
			}
			moods++
			code, title, param, priority = "home.row.moodForYou", label+" picks for you", "mood", 62+moods
		case "theme", "setting", "narrative":
			if moods == recGenreRows {
				continue
			}
			moods++
			code, title, param, priority = "home.row.themeForYou", label+" for you", "theme", 62+moods
		case "subgenre":
			if subgenres == recSubgenreRows {
				continue
			}
			subgenres++
			code, title, param, priority = "home.row.subgenreForYou", label+" for you", "subgenre", 62+recGenreRows+subgenres
		case "appeal":
			if appeals == recAppealRows {
				continue
			}
			appeals++
			code, title, param, priority = "home.row.appealForYou", label+" for you", "appeal", 62+recGenreRows+recSubgenreRows+appeals
		default:
			continue // origin, audience and intensity describe, they don't make a row
		}
		facet := f
		if err := add(recRowSpec("for_you:tag:"+tag, title, code, map[string]string{param: label}, priority), recRowMinimum, 0,
			func() ([]recCandidate, error) { return x.rank(x.facetRow(facet)) }); err != nil {
			return nil, err
		}
	}
	if len(x.taste.values) > 0 {
		if err := add(recRowSpec("for_you:series", "Next in the series", "home.row.nextInSeries", nil, 60), 2, 0.6, x.seriesRow); err != nil {
			return nil, err
		}
		if err := add(recRowSpec("for_you:franchise", "Next in the franchise", "home.row.nextInFranchise", nil, 60), 2, 0.6, x.franchiseRow); err != nil {
			return nil, err
		}
		if err := add(recRowSpec("for_you:rediscover", "Rediscover", "home.row.rediscover", nil, 69), recShortRowMinimum, 0.1, x.rediscoverRow); err != nil {
			return nil, err
		}
	}
	// No "new for you" row: Home's Recently added shelf is what is new, and
	// Recommended is what suits the viewer; a third row saying both said
	// neither.
	if x.discover == "movie" {
		filter = &BrowseNode{Field: "durationMs", Operator: "at-most", Value: float64(100 * 60 * 1000)}
		if err := add(recRowSpec("for_you:short", "Short picks for you", "home.row.shortPicks", nil, 66), recRowMinimum, 0, func() ([]recCandidate, error) {
			return x.rank(recOptions{diversify: true, keep: func(c *recScored) bool { return c.duration > 0 && c.duration <= 100*60 }})
		}); err != nil {
			return nil, err
		}
	}
	if len(x.strongest) > 0 {
		if err := add(recRowSpec("for_you:gems", "Hidden gems for you", "home.row.hiddenGems", nil, 67), recRowMinimum, 0, func() ([]recCandidate, error) {
			return x.rank(recOptions{noFill: true, diversify: true, keep: func(c *recScored) bool { return c.quality >= 7.2 && c.votes >= 20 && c.votes < 1500 }})
		}); err != nil {
			return nil, err
		}
		if err := add(recRowSpec("for_you:top", "Top rated you haven't seen", "home.row.topRated", nil, 68), recRowMinimum, 0, func() ([]recCandidate, error) {
			return x.rank(recOptions{diversify: true, keep: func(c *recScored) bool { return c.quality >= 7.8 && c.votes >= 1500 }})
		}); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// facetRow ranks the works of one facet (a genre, a person) by taste.
func (x *recSession) facetRow(facet string) recOptions {
	return recOptions{facets: []string{facet}, head: 200, noFill: true, noSimilar: true, anyMatch: true, diversify: true,
		keep: func(c *recScored) bool { return recHasFacet(c.facets, facet) }}
}

// franchiseRow is the next unwatched film of each franchise (collection) the
// profile has started: the first member, in year order, after the last one it
// engaged with.
func (x *recSession) franchiseRow() ([]recCandidate, error) {
	rows, err := x.s.read().Query(`SELECT cm.collection_id,max(s.at) FROM rec_profile_signals s
	 CROSS JOIN catalog_collection_members cm ON cm.item_id=s.work_id
	 WHERE s.profile_id=? AND s.engaged=1 AND s.weight>0 GROUP BY cm.collection_id ORDER BY 2 DESC LIMIT 40`, x.r.Profile)
	if err != nil {
		return nil, err
	}
	var collections []int64
	for rows.Next() {
		var id int64
		var at string
		if err = rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		collections = append(collections, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var next []int64
	for _, collection := range collections {
		members, err := x.s.read().Query(`SELECT m.item_id FROM catalog_collection_members m CROSS JOIN catalog_entities e ON e.id=m.item_id WHERE m.collection_id=? AND e.retired=0 ORDER BY e.year,e.id`, collection)
		if err != nil {
			return nil, err
		}
		var order []int64
		for members.Next() {
			var id int64
			if err = members.Scan(&id); err != nil {
				members.Close()
				return nil, err
			}
			order = append(order, id)
		}
		members.Close()
		if err = members.Err(); err != nil {
			return nil, err
		}
		if err := x.s.recLoadSignals(x.r.Profile, &x.taste, order); err != nil {
			return nil, err
		}
		last := -1
		for i, id := range order {
			if x.taste.engaged[id] {
				last = i
			}
		}
		for _, id := range order[last+1:] {
			if !x.taste.engaged[id] && !x.taste.hidden[id] {
				next = append(next, id)
				break
			}
		}
	}
	return x.rank(recOptions{facets: []string{}, works: next, noFill: true, noSimilar: true, anyMatch: true})
}

// seriesRow is the next unread book of each series the profile has started, by
// series position: the first after the last one it engaged with.
func (x *recSession) seriesRow() ([]recCandidate, error) {
	rows, err := x.s.read().Query(`SELECT DISTINCT bc.library_id,bc.series_key FROM rec_profile_signals s
	 CROSS JOIN catalog_book_context bc ON bc.book_id=s.work_id
	 WHERE s.profile_id=? AND s.engaged=1 AND s.weight>0 AND bc.series_key<>'' LIMIT 40`, x.r.Profile)
	if err != nil {
		return nil, err
	}
	type series struct {
		library int64
		key     string
	}
	var all []series
	for rows.Next() {
		var sr series
		if err = rows.Scan(&sr.library, &sr.key); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, sr)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var next []int64
	for _, sr := range all {
		order, err := scanInt64s(x.s.read().Query(`SELECT book_id FROM catalog_book_context INDEXED BY catalog_book_context_series WHERE library_id=? AND series_key=? AND series_key<>'' ORDER BY series_index,book_id`, sr.library, sr.key))
		if err != nil {
			return nil, err
		}
		if err := x.s.recLoadSignals(x.r.Profile, &x.taste, order); err != nil {
			return nil, err
		}
		last := -1
		for i, id := range order {
			if x.taste.engaged[id] {
				last = i
			}
		}
		for _, id := range order[last+1:] {
			if !x.taste.engaged[id] && !x.taste.hidden[id] {
				next = append(next, id)
				break
			}
		}
	}
	return x.rank(recOptions{facets: []string{}, works: next, noFill: true, noSimilar: true, anyMatch: true})
}

// rediscoverRow is favorites and high ratings last watched over a year ago.
func (x *recSession) rediscoverRow() ([]recCandidate, error) {
	cutoff := x.now.AddDate(-1, 0, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	works, err := scanInt64s(x.s.read().Query(`SELECT work_id FROM rec_profile_signals INDEXED BY rec_profile_signals_recent WHERE profile_id=? AND at<? AND weight>=1.5 AND hidden=0 ORDER BY weight DESC LIMIT 100`, x.r.Profile, cutoff))
	if err != nil {
		return nil, err
	}
	return x.rank(recOptions{facets: []string{}, works: works, noFill: true, noSimilar: true, includeEngaged: true, anyMatch: true, diversify: true})
}

// recPersonalRows chooses the personal rows Home shows: the strongest,
// rotating daily among close ones, each title in one row only and none of
// Recommended's first page.
func (s *Service) recPersonalRows(r HomeRequest) ([]recRow, error) {
	return s.recPersonalRowsFor(r, "")
}

// recPersonalRowsFor is recPersonalRows for Home ("") or one library's
// Discover view (its kind: "movie", "tv", "anime", "music", "audiobook"),
// which adds the rows that only make sense within a library.
func (s *Service) recPersonalRowsFor(r HomeRequest, discover string) ([]recRow, error) {
	x, err := s.recSession(r)
	if err != nil {
		return nil, err
	}
	x.discover = discover
	x.key += "\x00discover:" + discover
	generated, err := x.recGenerators()
	if err != nil {
		return nil, err
	}
	recommended, err := x.memo("recommended", func() ([]recCandidate, error) { return x.rank(recOptions{diversify: true}) })
	if err != nil {
		return nil, err
	}
	used, onRecommended := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(recommended) && i < HomeRowDefaultLimit; i++ {
		onRecommended[recommended[i].ID] = true
	}
	jitter := func(id string) float64 {
		h := fnv.New64a()
		h.Write([]byte(r.Profile + "\x00" + x.day() + "\x00" + id))
		return 0.9 + 0.2*float64(h.Sum64()%1000)/1000
	}
	sort.SliceStable(generated, func(i, j int) bool {
		return generated[i].strength*jitter(generated[i].spec.ID) > generated[j].strength*jitter(generated[j].spec.ID)
	})
	var out []recRow
	for _, row := range generated {
		if len(out) == recPersonalRows {
			break
		}
		kept := make([]recCandidate, 0, len(row.items))
		for _, c := range row.items {
			if !used[c.ID] && (row.precise || !onRecommended[c.ID]) {
				kept = append(kept, c)
			}
		}
		if len(kept) < row.minimum {
			continue
		}
		// A title belongs to the first row that shows it, on every page.
		for _, c := range kept {
			used[c.ID] = true
		}
		row.items = kept
		if err := x.recWhy(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// recPersonalRow finds one personal row by id, as Home shows it (for paging);
// a row Home didn't choose today is generated on its own.
func (s *Service) recPersonalRow(r HomeRequest, id string) (recRow, error) {
	rows, err := s.recPersonalRows(r)
	if err != nil {
		return recRow{}, err
	}
	for _, row := range rows {
		if row.spec.ID == id {
			return row, nil
		}
	}
	x, err := s.recSession(r)
	if err != nil {
		return recRow{}, err
	}
	generated, err := x.recGenerators()
	if err != nil {
		return recRow{}, err
	}
	for _, row := range generated {
		if row.spec.ID == id {
			if err := x.recWhy(&row); err != nil {
				return recRow{}, err
			}
			return row, nil
		}
	}
	return recRow{}, ErrHomeRowUnknown
}

func (s *Service) recGenreLabel(folded string) string {
	var label string
	if err := s.read().QueryRow(`SELECT label FROM catalog_terms WHERE vocab=1 AND lower(label)=? ORDER BY id LIMIT 1`, folded).Scan(&label); err == nil && label != "" {
		return label
	}
	return recTitleCase(folded)
}

// recPersonTitle names a person row: "More with Frances McDormand",
// "Directed by Denis Villeneuve", "From Vince Gilligan".
func (s *Service) recPersonTitle(facet string) (name, code, title string) {
	switch {
	case strings.HasPrefix(facet, "p:"):
		provider, id, _ := strings.Cut(strings.TrimPrefix(facet, "p:"), ":")
		if err := s.read().QueryRow(`SELECT credited_name FROM catalog_credits INDEXED BY catalog_credits_provider_person WHERE provider=? AND provider_person_id=? AND provider_person_id<>'' AND credited_name<>'' LIMIT 1`, provider, id).Scan(&name); err != nil || name == "" {
			name = id
		}
		return name, "home.row.moreWith", "More with " + name
	case strings.HasPrefix(facet, "a:"):
		if err := s.read().QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, strings.TrimPrefix(facet, "a:")).Scan(&name); err != nil || name == "" {
			name = strings.TrimPrefix(facet, "a:")
		}
		return name, "home.row.moreFromArtist", "More from " + name
	case strings.HasPrefix(facet, "b:"):
		name = recTitleCase(strings.TrimPrefix(facet, "b:"))
		return name, "home.row.moreByAuthor", "More by " + name
	case strings.HasPrefix(facet, "cd:creator:"):
		name = recTitleCase(strings.TrimPrefix(facet, "cd:creator:"))
		return name, "home.row.fromCreator", "From " + name
	}
	name = recTitleCase(strings.TrimPrefix(facet, "cd:directing:"))
	return name, "home.row.directedBy", "Directed by " + name
}

func recTitleCase(v string) string {
	out := []rune(v)
	start := true
	for i, r := range out {
		if start && unicode.IsLetter(r) {
			out[i] = unicode.ToUpper(r)
		}
		start = r == ' ' || r == '-'
	}
	return string(out)
}

func recHasFacet(facets []string, facet string) bool {
	for _, f := range facets {
		if f == facet {
			return true
		}
	}
	return false
}
