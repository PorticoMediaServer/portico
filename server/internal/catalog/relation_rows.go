package catalog

import (
	"container/list"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Relation rows page to exhaustion (JUSTIN-ANSWERS #34). Every row a title or
// show page displays has one producer: the row's candidates, best first. The
// first page and every continuation are slices of that one order, so a row
// never repeats or skips a title between pages. A continuation's cursor
// carries the digest of everything already shown: when the order under it
// moved (a new publication, the engine's daily rotation, a taste update) the
// continuation is an explicit stale continuation.
//
// Orders read only from revisioned facts (catalogue facets and credits, the
// viewer's access and personal state) are kept by their complete revision in
// a bounded cache, so paging More like X scores its candidates once per
// publication rather than twice per page (B6). Orders that read facts no
// revision names (the engine's taste ranking, other members' plays, sound
// vectors) are computed fresh for a first page, and its continuations reuse
// that order for the cursor's life.

const (
	relationRowDefaultLimit = 37
	relationRowMaxLimit     = 200
	// relationRowScan bounds how much of an order one page reads past titles
	// the recommendation boundary leaves out.
	relationRowScan = 4
	// relationRowPrefix is the first depth a nearest-year order is read to.
	relationRowPrefix = 96
)

type relationCaching int

const (
	// relationFresh orders are recomputed for every page.
	relationFresh relationCaching = iota
	// relationRevision orders are cached by their complete revision.
	relationRevision
	// relationContinuation orders are recomputed for a first page and reused
	// by its continuations.
	relationContinuation
)

// relationOrdered is one row's order, best first: entity ids, or public ids
// where the producer ranks by public id. complete is false for an order read
// only as deep as a page needed.
type relationOrdered struct {
	ids      []int64
	public   []string
	complete bool
}

func (o *relationOrdered) len() int {
	if o.public != nil {
		return len(o.public)
	}
	return len(o.ids)
}

// digest names the first n entries of the order.
func (o *relationOrdered) digest(n int) string {
	h := sha256.New()
	var b [8]byte
	for i := 0; i < n; i++ {
		if o.public != nil {
			h.Write([]byte(o.public[i]))
			h.Write([]byte{0})
			continue
		}
		binary.BigEndian.PutUint64(b[:], uint64(o.ids[i]))
		h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func (o *relationOrdered) cost() int {
	n := 64 + 8*len(o.ids)
	for _, id := range o.public {
		n += 16 + len(id)
	}
	return n
}

// relationSpec is one displayable row and its producer. order reads at least
// need entries when it can stop early; most producers read their whole order.
type relationSpec struct {
	id, relation, provider, evidence, heading string
	caching                                   relationCaching
	// boundary passes each page through filterRecommendationRows (editions
	// collapse to their work, negatives and the viewer's restrictions drop).
	// More like X applies the viewer's visibility and negatives in its order.
	boundary bool
	personal bool
	order    func(need int) (*relationOrdered, error)
}

// relationOwner is the page a row belongs to: a title, or a show (whose rows
// are its seed episode's).
type relationOwner struct {
	kind, id, library, itemKind string
}

func (o relationOwner) endpoint(row string) string {
	base := "/v1/items/"
	if o.kind == "show" {
		base = "/v1/shows/"
	}
	path := base + url.PathEscape(o.id) + "/recommendations/rows/" + url.PathEscape(row)
	if len(path) > 256 {
		return base + url.PathEscape(o.id) + "/recommendations"
	}
	return path
}

// relationRowMatches admits a row by its id, and More like X by its short name.
func relationRowMatches(spec relationSpec, row string) bool {
	return spec.id == row || row == "more_like" && spec.relation == "more_like"
}

// relationPage is one page of a row's order. The caller has authorized the
// owner for the viewer; every entry is re-authorized here.
func (s *Service) relationPage(viewer Viewer, owner relationOwner, spec relationSpec, cursor string, limit int) (HomeRow, error) {
	out := HomeRow{Entries: []ContentEntry{}}
	if limit == 0 {
		limit = relationRowDefaultLimit
	}
	if limit < 1 || limit > relationRowMaxLimit {
		return out, ErrCursor
	}
	revision, err := s.ContentRevision(owner.library, viewer.Profile)
	if err != nil {
		return out, err
	}
	// The cursor names the row and how much of its order was shown, not
	// the page size: a short first page continues in longer pages.
	scope := cursorScope{Library: owner.library, Profile: viewer.Profile, Viewer: viewer.Fence, View: "relation_row", Entity: owner.kind + ":" + owner.id, Section: spec.id}
	start, shown := 0, ""
	if cursor != "" {
		value, err := s.decodeDetailCursor(cursor, scope, revision)
		if err != nil {
			return out, err
		}
		if value.Start <= 0 || value.Fingerprint == "" {
			return out, ErrCursor
		}
		start, shown = value.Start, value.Fingerprint
	}
	key := ""
	if spec.caching != relationFresh {
		key = strings.Join([]string{owner.kind, owner.id, spec.id, viewer.Profile, viewer.Fence, RestrictionFence(viewer.EffectiveRestrictions()), RestrictionFence(s.recRestrictions), idsJSON(viewer.Libraries), itoa(revision.Catalog), itoa(revision.Viewer)}, "\x00")
	}
	order, err := s.relationOrder(key, spec, cursor == "", start+limit*relationRowScan+1)
	if err != nil {
		return out, err
	}
	if cursor != "" && (order.len() < start || order.digest(start) != shown) {
		return out, ErrStaleContinuation
	}
	next := start
	for len(out.Entries) < limit && next < order.len() && next-start < limit*relationRowScan {
		end := min(order.len(), next+limit-len(out.Entries))
		page, err := s.relationEntries(viewer, order, next, end, spec.boundary)
		if err != nil {
			return out, err
		}
		seen := map[string]bool{}
		for _, entry := range out.Entries {
			seen[entry.ID] = true
		}
		for _, entry := range page {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				out.Entries = append(out.Entries, entry)
			}
		}
		next = end
	}
	shape := owner.itemKind
	if len(out.Entries) > 0 {
		shape = out.Entries[0].Kind
	}
	out.ID = spec.id
	out.Relation = spec.relation
	out.Provider = spec.provider
	out.EvidenceID = spec.evidence
	out.Title = spec.heading
	out.TitleText = relatedRowText(spec.relation, spec.heading)
	out.Kind = "related"
	out.ArtworkShape = recommendationRowShape(shape)
	out.Endpoint = owner.endpoint(spec.id)
	out.LibraryID = owner.library
	out.ViewerFence = viewer.Fence
	out.PrivacySensitivity = "catalog"
	if spec.personal {
		out.PrivacySensitivity = "personal"
	}
	out.PolicyState = "available"
	out.CacheTTLMs = 300000
	out.Hideable = true
	out.Revision = revision
	out.CursorCapable = true
	out.Start = start
	out.Limit = limit
	out.Total = order.len()
	out.HasMore = next < order.len() || !order.complete
	if out.HasMore {
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Start: next, Fingerprint: order.digest(next), Expires: time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()}, revision)
		if err != nil {
			return out, err
		}
	}
	after, err := s.ContentRevision(owner.library, viewer.Profile)
	if err != nil {
		return out, err
	}
	if after != revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// relationOrder reads a row's order at least need deep, through the cache
// its caching allows.
func (s *Service) relationOrder(key string, spec relationSpec, first bool, need int) (*relationOrdered, error) {
	cached := key != "" && (spec.caching == relationRevision || !first)
	if cached {
		if order, ok := s.state.relationOrders.get(key); ok && (order.complete || order.len() >= need) {
			return order, nil
		}
	}
	order, err := spec.order(need)
	if err != nil {
		return nil, err
	}
	if key != "" {
		s.state.relationOrders.put(key, order)
	}
	return order, nil
}

// relationEntries hydrates order[from:to] for the viewer.
func (s *Service) relationEntries(viewer Viewer, order *relationOrdered, from, to int, boundary bool) ([]ContentEntry, error) {
	var ids []string
	if order.public != nil {
		ids = append(ids, order.public[from:to]...)
	} else {
		rows, err := s.read().Query(`SELECT pid(e.public_id) FROM json_each(?) j CROSS JOIN catalog_entities e ON e.id=j.value ORDER BY j.key`, idsJSON64(order.ids[from:to]))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if !boundary {
		return s.homeEngineEntries(viewer.Profile, ids)
	}
	row := HomeRow{Entries: make([]ContentEntry, 0, len(ids))}
	for _, id := range ids {
		row.Entries = append(row.Entries, ContentEntry{ID: id})
	}
	rows, err := s.filterRecommendationRows(viewer, []HomeRow{row})
	if err != nil || len(rows) == 0 {
		return []ContentEntry{}, err
	}
	return rows[0].Entries, nil
}

// relationRowSpec finds one displayable row of a title.
func (s *Service) relationRowSpec(viewer Viewer, item Item, row string, full, community bool) (relationSpec, error) {
	genres, err := s.itemGenreList(item.ID)
	if err != nil {
		return relationSpec{}, err
	}
	specs, err := s.itemRelationSpecs(viewer, item, genres, full, community)
	if err != nil {
		return relationSpec{}, err
	}
	for _, spec := range specs {
		if relationRowMatches(spec, row) {
			return spec, nil
		}
	}
	return relationSpec{}, sql.ErrNoRows
}

// ItemRecommendationRow pages one of a title's rows to exhaustion: any row
// the title's recommendations or detail.related can show.
func (s *Service) ItemRecommendationRow(viewer Viewer, item, row, cursor string, limit int, community bool) (HomeRow, error) {
	if err := s.VisibleItem(s.Context(), viewer, item); err != nil {
		return HomeRow{}, err
	}
	loaded, err := s.Get(viewer.Profile, item)
	if err != nil {
		return HomeRow{}, err
	}
	spec, err := s.relationRowSpec(viewer, loaded, row, true, community)
	if err != nil {
		return HomeRow{}, err
	}
	return s.relationPage(viewer, relationOwner{kind: "item", id: item, library: loaded.LibraryID, itemKind: loaded.Kind}, spec, cursor, limit)
}

// ShowRecommendationRow pages one of a show page's rows to exhaustion.
func (s *Service) ShowRecommendationRow(viewer Viewer, show, row, cursor string, limit int) (HomeRow, error) {
	seed, title, library, err := s.showRecommendationSeed(viewer, show)
	if err != nil {
		return HomeRow{}, err
	}
	loaded, err := s.Get(viewer.Profile, seed)
	if err != nil {
		return HomeRow{}, err
	}
	spec, err := s.relationRowSpec(viewer, loaded, row, false, false)
	if err != nil {
		return HomeRow{}, err
	}
	if spec.relation == "more_like" {
		spec.heading = "More like " + title
	}
	return s.relationPage(viewer, relationOwner{kind: "show", id: show, library: library, itemKind: "show"}, spec, cursor, limit)
}

// itemRelationSpecs are a title's rows in display order: the engine's (More
// like X, From its creator, Starring, Viewers also watched), the director,
// the listening or movie genre and person rows, then its collections. full
// adds the rows only the per-item endpoint shows (director, collections).
func (s *Service) itemRelationSpecs(viewer Viewer, item Item, genres []Genre, full, community bool) ([]relationSpec, error) {
	out := []relationSpec{}
	if item.Kind == "movie" || item.Kind == "episode" || item.Kind == "show" {
		engine, err := s.recTitleRelations(viewer, item, full && community)
		if err != nil {
			return nil, err
		}
		out = append(out, engine...)
	}
	if full && item.Kind == "movie" {
		director, err := s.directorRelation(item)
		if err != nil {
			return nil, err
		}
		out = append(out, director...)
	}
	switch {
	case item.Kind == "song" || item.Kind == "audiobook_file":
		listening, err := s.listeningRelations(viewer.Profile, item)
		if err != nil {
			return nil, err
		}
		out = append(out, listening...)
	case item.Kind == "movie":
		movie, err := s.movieRelations(item, genres)
		if err != nil {
			return nil, err
		}
		out = append(out, movie...)
	}
	if full {
		collections, err := s.collectionRelations(item)
		if err != nil {
			return nil, err
		}
		out = append(out, collections...)
	}
	return out, nil
}

// relationRanked is an engine or listening ranking as a row order.
func relationRanked(ranked []recCandidate) *relationOrdered {
	out := &relationOrdered{public: make([]string, 0, len(ranked)), complete: true}
	for _, c := range ranked {
		out.public = append(out.public, c.ID)
	}
	return out
}

// moreLikeOrder ranks every eligible title by the catalogue facets it shares
// with work, then catalogue quality and stable id: the viewer's visibility and
// negatives apply inside the order, so its length is the row's exact total.
func (s *Service) moreLikeOrder(viewer Viewer, work int64) func(int) (*relationOrdered, error) {
	return func(int) (*relationOrdered, error) {
		visible, args := viewer.entityVisibilitySQL("i.id", "l.library_id")
		visible += ` AND NOT EXISTS(SELECT 1 FROM personal_items negative WHERE negative.item_id=i.id AND negative.profile_id=? AND (negative.not_interested=1 OR negative.rating<=4))`
		args = append(args, viewer.Profile)
		ids, err := scanInt64s(s.read().Query(`SELECT id FROM (SELECT i.id,count(DISTINCT seed.facet) score,max(candidate.quality) quality FROM catalog_rec_facets seed
 JOIN catalog_rec_postings candidate ON candidate.facet=seed.facet JOIN catalog_entities i ON i.id=candidate.entity_id JOIN catalog_libraries l ON l.id=i.library_id
 WHERE seed.entity_id=? AND i.library_id=(SELECT library_id FROM catalog_entities WHERE id=?) AND i.id<>? AND `+visible+` GROUP BY i.id) ORDER BY score DESC,quality DESC,id`, append([]any{work, work, work}, args...)...))
		return &relationOrdered{ids: ids, complete: true}, err
	}
}

// directorRelation is "Directed by" a film's first credited director: every
// film of theirs in the library, newest first.
func (s *Service) directorRelation(item Item) ([]relationSpec, error) {
	var provider, person, name string
	err := s.read().QueryRow(`SELECT c.provider,c.provider_person_id,c.credited_name
 FROM catalog_entities i JOIN catalog_credits c ON c.entity_id=i.id
 JOIN catalog_credit_labels department ON department.id=c.department_id
 WHERE i.public_id=pid_blob(?) AND c.provider_person_id<>'' AND department.label='Directing'
 ORDER BY c.source_ordinal,c.provider,c.provider_person_id LIMIT 1`, item.ID).Scan(&provider, &person, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	restriction, bind := ItemRestrictionSQL("i.id", s.recRestrictions)
	order := func(int) (*relationOrdered, error) {
		ids, err := scanInt64s(s.read().Query(`SELECT i.id FROM catalog_credits c JOIN catalog_entities i ON i.id=c.entity_id JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_credit_labels d ON d.id=c.department_id
 WHERE c.provider=? AND c.provider_person_id=? AND d.label='Directing' AND l.library_id=? AND i.kind=1 AND i.public_id<>pid_blob(?) AND `+recommendationVisible("i.id")+` AND `+restriction+`
 GROUP BY i.id ORDER BY i.year DESC,i.id`, append([]any{provider, person, item.LibraryID, item.ID}, bind...)...))
		return &relationOrdered{ids: ids, complete: true}, err
	}
	return []relationSpec{{id: "director:" + provider + ":" + person, relation: "director", provider: provider, evidence: person, heading: "Directed by " + name, caching: relationRevision, boundary: true, order: order}}, nil
}

// collectionRelations are "More from" each of the title's (at most two)
// collections: every other member, in year then title order.
func (s *Service) collectionRelations(item Item) ([]relationSpec, error) {
	rows, err := s.read().Query(`SELECT pid(e.public_id),e.title FROM catalog_entities item JOIN catalog_collection_members m ON m.item_id=item.id JOIN catalog_entities e ON e.id=m.collection_id JOIN catalog_collections c ON c.entity_id=e.id WHERE item.public_id=pid_blob(?) AND `+collectionHeadFence+` ORDER BY c.name_key,e.id LIMIT 2`, item.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []relationSpec{}
	restriction, bind := ItemRestrictionSQL("i.id", s.recRestrictions)
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		collection := id
		order := func(int) (*relationOrdered, error) {
			ids, err := scanInt64s(s.read().Query(`SELECT i.id FROM catalog_entities co JOIN catalog_collection_members m ON m.collection_id=co.id JOIN catalog_entities i ON i.id=m.item_id
 WHERE co.public_id=pid_blob(?) AND i.public_id<>pid_blob(?) AND `+recommendationVisible("i.id")+` AND `+restriction+` ORDER BY i.year,i.title COLLATE NOCASE,i.id`, append([]any{collection, item.ID}, bind...)...))
			return &relationOrdered{ids: ids, complete: true}, err
		}
		out = append(out, relationSpec{id: "collection:local:" + id, relation: "collection", provider: "local", evidence: id, heading: "More from " + name, caching: relationRevision, boundary: true, order: order})
	}
	return out, rows.Err()
}

// nearestYearOrder orders one related facet's films (a genre's, a person's)
// nearest the title's year first, the later year first at equal distance and
// undated films last. It reads each side of the year by its index only as
// deep as the page needs: the first need entries of the merged order are
// always among the first need of each side. Within one year a later year's
// side keeps descending id order and an earlier year's ascending, the order
// the index reads them in.
func (s *Service) nearestYearOrder(item Item, relation int, provider, facet, dedupe string) func(int) (*relationOrdered, error) {
	restriction, bind := ItemRestrictionSQL("i.id", s.recRestrictions)
	type candidate struct {
		id, year int64
		up       bool
		key      string
	}
	side := func(where string, whereArgs []any, order string, limit int, up bool) ([]candidate, error) {
		args := []any{dedupe, item.LibraryID, relation, provider, facet}
		args = append(args, whereArgs...)
		args = append(args, bind...)
		args = append(args, item.ID, item.ID, limit)
		rows, err := s.read().Query(nearestYearSideSQL(where, order, restriction), args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []candidate{}
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.id, &c.year, &c.key); err != nil {
				return nil, err
			}
			c.up = up
			out = append(out, c)
		}
		return out, rows.Err()
	}
	distance := func(year int64) int64 {
		if item.Year <= 0 || year <= 0 {
			return 100000
		}
		if v := year - int64(item.Year); v >= 0 {
			return v
		}
		return int64(item.Year) - year
	}
	return func(need int) (*relationOrdered, error) {
		depth := max(need, relationRowPrefix)
		for {
			var sides [][]candidate
			if item.Year > 0 {
				// Later years walk the index backwards, so ids descend there.
				up, err := side(`f.year>=?`, []any{item.Year}, `f.year ASC,f.entity_id DESC`, depth, true)
				if err != nil {
					return nil, err
				}
				down, err := side(`f.year<? AND f.year>0`, []any{item.Year}, `f.year DESC,f.entity_id`, depth, false)
				if err != nil {
					return nil, err
				}
				undated, err := side(`f.year<=0`, nil, `f.year DESC,f.entity_id`, depth, false)
				if err != nil {
					return nil, err
				}
				sides = [][]candidate{up, down, undated}
			} else {
				all, err := side(`1`, nil, `f.year DESC,f.entity_id`, depth, false)
				if err != nil {
					return nil, err
				}
				sides = [][]candidate{all}
			}
			complete := true
			merged := []candidate{}
			for _, list := range sides {
				complete = complete && len(list) < depth
				merged = append(merged, list...)
			}
			sort.Slice(merged, func(i, j int) bool {
				a, b := merged[i], merged[j]
				if da, db := distance(a.year), distance(b.year); da != db {
					return da < db
				}
				if a.year != b.year {
					return a.year > b.year
				}
				if a.up {
					return a.id > b.id
				}
				return a.id < b.id
			})
			if !complete && len(merged) > depth {
				merged = merged[:depth]
			}
			out := &relationOrdered{ids: []int64{}, complete: complete}
			seen := map[string]bool{}
			for _, c := range merged {
				key := "id:" + itoa(c.id)
				if c.key != "" {
					key = dedupe + ":" + c.key
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				out.ids = append(out.ids, c.id)
			}
			if complete || len(out.ids) >= need {
				return out, nil
			}
			depth *= 2
		}
	}
}

// nearestYearSideSQL reads one side of a related facet's year order by the
// facet index, as deep as its limit. Its arguments are the dedupe provider,
// the library, the relation, its provider and facet, the where clause's, the
// restriction's, the seed twice and the limit.
func nearestYearSideSQL(where, order, restriction string) string {
	return `SELECT f.entity_id,f.year,COALESCE(d.provider_id,'') FROM catalog_related_facets f INDEXED BY catalog_related_lookup CROSS JOIN catalog_entities i ON i.id=f.entity_id
 LEFT JOIN metadata_details d ON d.item_id=i.id AND d.provider=?
 WHERE f.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND f.relation=? AND f.provider=? AND f.facet_id=? AND ` + where + ` AND i.kind=1
 AND ` + recommendationVisible("i.id") + ` AND ` + restriction + `
 AND NOT EXISTS(SELECT 1 FROM catalog_entities seed JOIN catalog_asset_links original ON original.entity_id=seed.id JOIN catalog_asset_links other ON other.asset_id=original.asset_id WHERE seed.public_id=pid_blob(?) AND other.entity_id=i.id)
 AND NOT EXISTS(SELECT 1 FROM metadata_details original JOIN metadata_details other ON other.provider=original.provider AND other.provider_id=original.provider_id WHERE original.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND other.item_id=i.id AND original.provider_id<>'' AND original.provider IN('tmdb','tvdb','anilist','musicbrainz'))
 ORDER BY ` + order + ` LIMIT ?`
}

// relationOrderCache keeps recent row orders by their complete key; see
// relationPage for which orders it may hold.
type relationOrderCache struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
	used    int
}

type relationOrderNode struct {
	key     string
	value   *relationOrdered
	expires time.Time
	cost    int
}

const relationOrderEntries = 128

var relationOrderBytes = 24 << 20

func newRelationOrderCache() *relationOrderCache {
	return &relationOrderCache{order: list.New(), entries: map[string]*list.Element{}}
}

func (c *relationOrderCache) get(key string) (*relationOrdered, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	node := element.Value.(*relationOrderNode)
	if !time.Now().Before(node.expires) {
		c.order.Remove(element)
		delete(c.entries, key)
		c.used -= node.cost
		return nil, false
	}
	c.order.MoveToFront(element)
	return node.value, true
}

func (c *relationOrderCache) put(key string, value *relationOrdered) {
	if c == nil {
		return
	}
	node := &relationOrderNode{key: key, value: value, expires: time.Now().Add(BrowseCursorTTLSecond * time.Second), cost: value.cost()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.used -= element.Value.(*relationOrderNode).cost
		c.order.Remove(element)
		delete(c.entries, key)
	}
	if node.cost > relationOrderBytes/4 {
		return
	}
	c.entries[key] = c.order.PushFront(node)
	c.used += node.cost
	for c.order.Len() > relationOrderEntries || c.used > relationOrderBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		evicted := oldest.Value.(*relationOrderNode)
		delete(c.entries, evicted.key)
		c.used -= evicted.cost
	}
}
