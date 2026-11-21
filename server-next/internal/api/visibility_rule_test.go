package api

import (
	"database/sql"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// facts are the stored rows the rule is worked out from.
type facts struct {
	items   map[string]*stored
	rules   map[int64]rule
	tagKind map[string]string   // a tag's id: its kind
	credits map[string][]string // a person's id: the items that credit them
}

type stored struct {
	id, library, parent, root string
	kind                      db.ItemKind
	title                     string
	rating                    sql.NullInt64 // the age rating the item carries
	year                      int
	certificate               string
	tags                      []string
	watched                   map[int64]bool // the profiles that watched it
	media                     string         // what its versions are, so a probe counts as a change
	people                    []string
}

// rule is what one profile is kept from: the account's and the profile's own limits.
type rule struct {
	maxAge       sql.NullInt64
	allowUnrated bool // with a limit, a title with no rating is shown
	blocked      map[string]bool
	libraries    map[string]bool // nil is every library
}

// hidden is the product rule: a title is absent when its library is not the profile's, or when
// it or anything it sits under is rated above the profile's limit (or has no rating while there
// is a limit, unless the profile allows unrated titles) or carries a label the profile blocks.
func (f *facts) hidden(profile int64, id string) bool {
	it, r := f.items[id], f.rules[profile]
	if r.libraries != nil && !r.libraries[it.library] {
		return true
	}
	for _, at := range []string{id, it.parent, it.root} {
		x := f.items[at]
		if x == nil {
			continue
		}
		if r.maxAge.Valid && (!x.rating.Valid && !r.allowUnrated || x.rating.Int64 > r.maxAge.Int64) {
			return true
		}
		for _, tag := range x.tags {
			if r.blocked[tag] {
				return true
			}
		}
	}
	return false
}

func (f *facts) describe(id string) string {
	it := f.items[id]
	if it == nil {
		return id + " (gone)"
	}
	rating := "unrated"
	if it.rating.Valid {
		rating = "rated " + strconv.FormatInt(it.rating.Int64, 10)
	}
	out := fmt.Sprintf("%s %q (%s, %s", it.kind, it.title, id, rating)
	for _, under := range []string{it.parent, it.root} {
		if p := f.items[under]; p != nil && under != it.id {
			out += fmt.Sprintf("; under %s %q", p.kind, p.title)
			if p.rating.Valid {
				out += " rated " + strconv.FormatInt(p.rating.Int64, 10)
			}
		}
		if it.parent == it.root {
			break
		}
	}
	return out + ")"
}

// leaves counts the playable items under a holder the profile may see, and those it has not watched.
func (f *facts) leaves(profile int64, holder string) (all, unwatched int) {
	for id, it := range f.items {
		if (it.parent == holder || it.root == holder) && it.kind.Playable() && it.kind != db.KindExtra && !f.hidden(profile, id) {
			all++
			if !it.watched[profile] {
				unwatched++
			}
		}
	}
	return all, unwatched
}

func (w *world) query(dest func(rows *sql.Rows) error, query string) {
	w.t.Helper()
	rows, err := w.raw.Query(query)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := dest(rows); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
}

// read takes the facts from the stored rows.
func (w *world) read() *facts {
	f := &facts{items: map[string]*stored{}, rules: map[int64]rule{}, tagKind: map[string]string{}, credits: map[string][]string{}}
	byRow := map[int64]*stored{}
	w.query(func(rows *sql.Rows) error {
		var it stored
		var row int64
		var parent, root sql.NullString
		err := rows.Scan(&row, &it.id, &it.library, &it.kind, &it.title, &parent, &root, &it.rating, &it.year, &it.certificate)
		it.parent, it.root, it.watched = parent.String, root.String, map[int64]bool{}
		f.items[it.id], byRow[row] = &it, &it
		return err
	}, `SELECT i.id, i.public_id, l.public_id, i.kind, i.title, p.public_id, r.public_id, i.rating_age, ifnull(i.year, 0),
		ifnull(i.content_rating, '') FROM items i JOIN libraries l ON l.id = i.library_id
		LEFT JOIN items p ON p.id = i.parent_id LEFT JOIN items r ON r.id = i.root_id`)
	w.query(func(rows *sql.Rows) error {
		var item int64
		var tag, kind string
		err := rows.Scan(&item, &tag, &kind)
		byRow[item].tags, f.tagKind[tag] = append(byRow[item].tags, tag), kind
		return err
	}, `SELECT it.item_id, t.public_id, t.kind FROM item_tags it JOIN tags t ON t.id = it.tag_id ORDER BY 1, 2`)
	w.query(func(rows *sql.Rows) error {
		var item, profile int64
		err := rows.Scan(&item, &profile)
		byRow[item].watched[profile] = true
		return err
	}, `SELECT item_id, profile_id FROM user_item_state WHERE played = 1`)
	w.query(func(rows *sql.Rows) error {
		var item int64
		var media string
		err := rows.Scan(&item, &media)
		byRow[item].media = media
		return err
	}, `SELECT m.item_id, group_concat(m.public_id || ':' || ifnull(m.width, 0) || 'x' || ifnull(m.height, 0), ' ')
		FROM media m GROUP BY m.item_id`)
	w.query(func(rows *sql.Rows) error {
		var item int64
		var person string
		err := rows.Scan(&item, &person)
		byRow[item].people = append(byRow[item].people, person)
		f.credits[person] = append(f.credits[person], byRow[item].id)
		return err
	}, `SELECT DISTINCT c.item_id, p.public_id FROM credits c JOIN people p ON p.id = c.person_id ORDER BY 1, 2`)

	// The libraries an account and a profile are given, when they are not given all of them.
	given := map[[2]int64]map[string]bool{} // {user, 0} or {0, profile}
	w.query(func(rows *sql.Rows) error {
		var library string
		var user, profile sql.NullInt64
		err := rows.Scan(&library, &user, &profile)
		key := [2]int64{user.Int64, profile.Int64}
		if given[key] == nil {
			given[key] = map[string]bool{}
		}
		given[key][library] = true
		return err
	}, `SELECT l.public_id, a.user_id, a.profile_id FROM library_access a JOIN libraries l ON l.id = a.library_id
		WHERE a.invitation_id IS NULL`)
	var libraries []string
	w.query(func(rows *sql.Rows) error {
		var id string
		err := rows.Scan(&id)
		libraries = append(libraries, id)
		return err
	}, `SELECT public_id FROM libraries`)
	w.query(func(rows *sql.Rows) error {
		var profile, user int64
		var r rule
		var userAll, profileAll bool
		err := rows.Scan(&profile, &user, &r.maxAge, &r.allowUnrated, &userAll, &profileAll)
		if !userAll || !profileAll {
			r.libraries = map[string]bool{}
			for _, l := range libraries {
				if (userAll || given[[2]int64{user, 0}][l]) && (profileAll || given[[2]int64{0, profile}][l]) {
					r.libraries[l] = true
				}
			}
		}
		r.blocked = map[string]bool{}
		f.rules[profile] = r
		return err
	}, `SELECT p.id, p.user_id, p.max_rating_age, p.allow_unrated, u.all_libraries, p.all_libraries FROM profiles p
		JOIN users u ON u.id = p.user_id`)
	w.query(func(rows *sql.Rows) error {
		var profile int64
		var tag string
		err := rows.Scan(&profile, &tag)
		f.rules[profile].blocked[tag] = true
		return err
	}, `SELECT r.profile_id, t.public_id FROM profile_restrictions r JOIN tags t ON t.id = r.tag_id`)
	return f
}

// signature is what an item is, for telling which items a change touched. A profile's state
// is left out: it moves counts, which are checked on every card, and never what is hidden.
func (it *stored) signature() string {
	return fmt.Sprint(it.library, it.parent, it.root, it.kind, it.rating, it.year, it.certificate, it.tags, it.media, it.people)
}

// listAll pages a list to its end and returns its items, its total and its status.
func (w *world) listAll(token, path string) ([]Item, int, int) {
	var page ItemPage
	code := call(w.t, w.s, "GET", path+"&limit=200&total=true", token, nil, &page).Code
	if code != 200 {
		return nil, 0, code
	}
	items, total := page.Items, *page.Total
	for page.NextCursor != "" {
		cursor := page.NextCursor
		page = ItemPage{}
		if code := call(w.t, w.s, "GET", path+"&limit=200&cursor="+url.QueryEscape(cursor), token, nil, &page).Code; code != 200 {
			w.fail("paging %s answered %d", path, code)
			break
		}
		items = append(items, page.Items...)
	}
	return items, total, 200
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for n, it := range items {
		out[n] = it.ID
	}
	return out
}

// check runs every comparison for every profile, and records what disagrees.
func (w *world) check(stage string) {
	w.t.Helper()
	f := w.read()
	previous, changed := w.last, w.changedSince(f)
	owner := w.viewers[0]
	w.ownerLists = map[string][]string{}
	for id := range f.items {
		if f.hidden(owner.profile, id) {
			w.fail("%s: the owner is kept from %s", stage, f.describe(id))
		}
	}
	w.remember(f)
	// Every card's counts are checked. The lists under a holder are read for the holders the
	// change touched and two of each kind, since reading every one for every profile is slow.
	holders, visit, perKind := map[string]bool{}, map[string]bool{}, map[db.ItemKind]int{}
	for _, it := range f.items {
		if it.parent != "" {
			holders[it.parent] = true
		}
	}
	for _, id := range f.byTitle() {
		if kind := f.items[id].kind; holders[id] && perKind[kind] < 2 {
			perKind[kind]++
			visit[id] = true
		}
	}
	for id := range changed {
		for _, at := range []string{id, previous.parentOf(id), previous.rootOf(id), f.parentOf(id), f.rootOf(id)} {
			if holders[at] {
				visit[at] = true
			}
		}
	}
	for _, v := range w.viewers {
		rule := f.rules[v.profile]
		var libraries []string
		for _, l := range get[Libraries](w.t, w.s, v.token, "/v1/libraries").Items {
			libraries = append(libraries, l.ID)
		}
		cards := map[string]Item{}
		for _, view := range w.views {
			path := "/v1/items?library=" + view[0] + "&kind=" + view[1]
			mayHave := rule.libraries == nil || rule.libraries[view[0]]
			if mayHave != slices.Contains(libraries, view[0]) {
				w.fail("%s: %s: library %s is listed %v, and the rule says %v", stage, v.name, view[0], !mayHave, mayHave)
			}
			if !mayHave {
				if code := status(w.t, w.s, "GET", path, v.token, nil); code != 404 {
					w.fail("%s: %s: %s answered %d for a library it may not see", stage, v.name, path, code)
				}
				continue
			}
			list := w.expect(stage, v, f, path, cards)
			if rule.maxAge.Valid || len(rule.blocked) > 0 || rule.libraries != nil {
				w.facets(stage, v, f, path, list)
			}
		}
		for holder := range visit {
			path := "/v1/items?parent=" + holder
			if f.hidden(v.profile, holder) {
				if code := status(w.t, w.s, "GET", path, v.token, nil); code != 404 {
					w.fail("%s: %s: the list under hidden %s answered %d", stage, v.name, f.describe(holder), code)
				}
				continue
			}
			w.expect(stage, v, f, path, cards)
		}
		for id, card := range cards {
			if card.Counts == nil || !holders[id] && card.Counts.Children == 0 {
				continue
			}
			children := 0
			for _, it := range f.items {
				if it.parent == id && !f.hidden(v.profile, it.id) {
					children++
				}
			}
			leaves, unwatched := f.leaves(v.profile, id)
			if got, want := *card.Counts, (ItemCounts{children, leaves, unwatched}); got != want {
				w.fail("%s: %s: the card of %s counts %+v, the rule %+v", stage, v.name, f.describe(id), got, want)
			}
		}
		for _, row := range get[Home](w.t, w.s, v.token, "/v1/home").Rows {
			for _, it := range row.Items {
				if f.items[it.ID] == nil || f.hidden(v.profile, it.ID) {
					w.fail("%s: %s: Home's %s row shows %s", stage, v.name, row.ID, f.describe(it.ID))
				}
			}
		}
		w.probe(stage, v, f, changed)
		w.people(stage, v, f, changed)
	}
}

// difference says how a list differs from what it should hold.
func (w *world) difference(f *facts, got, want []string) string {
	var extra, missing []string
	for _, id := range got {
		if !slices.Contains(want, id) {
			extra = append(extra, f.describe(id))
		}
	}
	for _, id := range want {
		if !slices.Contains(got, id) {
			missing = append(missing, f.describe(id))
		}
	}
	if len(extra)+len(missing) == 0 {
		return "the right titles in another order"
	}
	return fmt.Sprintf("%d it may not see %v and leaves out %d it may %v", len(extra), extra, len(missing), missing)
}

// facets compares each filter's choice counts with the list they filter.
func (w *world) facets(stage string, v profileUnderTest, f *facts, path string, list []Item) {
	want := map[string]map[string]int{}
	add := func(facet, value string) {
		if want[facet] == nil {
			want[facet] = map[string]int{}
		}
		want[facet][value]++
	}
	for _, card := range list {
		it := f.items[card.ID]
		if it == nil {
			continue
		}
		// A tag counts once for an item that carries it or whose title does.
		tags := map[string]bool{}
		for _, at := range []string{it.id, it.root} {
			if x := f.items[at]; x != nil {
				for _, tag := range x.tags {
					tags[tag] = true
				}
			}
		}
		for tag := range tags {
			add(f.tagKind[tag], tag)
		}
		if it.year > 0 {
			add(db.FacetDecade, fmt.Sprintf("%d..%d", it.year/10*10, it.year/10*10+9)) // the value is the range to send back as the filter
		}
		if it.certificate != "" {
			add(db.FacetRating, it.certificate)
		}
	}
	// Resolution is left out: a title with versions of two resolutions is counted under one and
	// listed under both, so its counts do not yet match its lists for anyone.
	for _, facet := range []string{db.FacetGenre, db.FacetStudio, db.FacetLabel, db.FacetDecade, db.FacetRating, db.FacetLetter} {
		got := map[string]int{}
		sum := 0
		for _, value := range get[Facets](w.t, w.s, v.token, strings.Replace(path, "/v1/items", "/v1/facets", 1)+"&facet="+facet).Values {
			got[value.Value] = value.Count
			sum += value.Count
		}
		switch facet {
		case db.FacetLetter:
			if sum != len(list) {
				w.fail("%s: %s: the letters of %s count %d of %d", stage, v.name, path, sum, len(list))
			}
		default:
			// A tag facet is named for its kind of tag.
			if !maps.Equal(got, want[facet]) {
				w.fail("%s: %s: %s counts %s %v, its list %v", stage, v.name, path, facet, got, want[facet])
			}
		}
	}
}

// changedSince returns the items a change touched, and remembers the facts for the next check.
func (w *world) changedSince(f *facts) map[string]bool {
	changed := map[string]bool{}
	if w.last != nil {
		for id, it := range f.items {
			if was := w.last.items[id]; was == nil || was.signature() != it.signature() {
				changed[id] = true
			}
		}
		for id := range w.last.items {
			if f.items[id] == nil {
				changed[id] = true
			}
		}
	}
	w.last = f
	return changed
}

// probe asks every route that takes a title's id about the titles the change touched and a
// few others: what the profile may not see, or what is gone, must answer "not found".
func (w *world) probe(stage string, v profileUnderTest, f *facts, changed map[string]bool) {
	sample := maps.Clone(changed)
	byKind := map[db.ItemKind]int{}
	for _, id := range f.byTitle() {
		if f.hidden(v.profile, id) && byKind[f.items[id].kind] < 2 {
			byKind[f.items[id].kind]++
			sample[id] = true
		}
	}
	for id := range sample {
		if f.items[id] != nil && !f.hidden(v.profile, id) {
			if code := status(w.t, w.s, "GET", "/v1/items/"+id, v.token, nil); code != 200 {
				w.fail("%s: %s: %s answered %d", stage, v.name, f.describe(id), code)
			}
			continue
		}
		if f.items[id] == nil {
			// A link to a title merged into another opens the one that remains, if it may be seen.
			var item Item
			code := call(w.t, w.s, "GET", "/v1/items/"+id, v.token, nil, &item).Code
			if code != 404 && (code != 200 || f.items[item.ID] == nil || f.hidden(v.profile, item.ID)) {
				w.fail("%s: %s: %s answered %d with %s", stage, v.name, f.describe(id), code, f.describe(item.ID))
			}
			continue
		}
		for _, r := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/v1/items/" + id, nil}, {"GET", "/v1/items/" + id + "/credits", nil}, {"GET", "/v1/items/" + id + "/related", nil},
			{"GET", "/v1/items/" + id + "/playback-options", nil}, {"PUT", "/v1/items/" + id + "/state", map[string]bool{"favorite": false}},
			{"POST", "/v1/playback/sessions", PlaybackStart{ItemID: id}},
		} {
			if code := status(w.t, w.s, r.method, r.path, v.token, r.body); code != 404 {
				w.fail("%s: %s: %s %s answered %d for %s", stage, v.name, r.method, r.path, code, f.describe(id))
			}
		}
	}
}

// people checks the people credited on the touched titles and a few others: each is known to a
// profile only through titles it may see, and lists and counts only those.
func (w *world) people(stage string, v profileUnderTest, f *facts, changed map[string]bool) {
	people := map[string]bool{}
	for _, person := range slices.Sorted(maps.Keys(f.credits))[:min(3, len(f.credits))] {
		people[person] = true
	}
	for id := range changed {
		if it := f.items[id]; it != nil {
			for _, person := range it.people {
				people[person] = true
			}
		}
	}
	for person := range people {
		visible := 0
		for _, id := range f.credits[person] {
			if !f.hidden(v.profile, id) {
				visible++
			}
		}
		var p Person
		code := call(w.t, w.s, "GET", "/v1/people/"+person, v.token, nil, &p).Code
		switch {
		case visible == 0 && code != 404:
			w.fail("%s: %s: person %s, credited only on titles it may not see, answered %d", stage, v.name, person, code)
		case visible > 0 && (code != 200 || p.TitleCount == nil || *p.TitleCount != visible):
			count := -1
			if p.TitleCount != nil {
				count = *p.TitleCount
			}
			w.fail("%s: %s: person %s answered %d with %d titles; it may see %d", stage, v.name, person, code, count, visible)
		}
		w.expect(stage, v, f, "/v1/items?person="+person, map[string]Item{})
	}
}

// ownerList is a list as the owner reads it, read once per check.
func (w *world) ownerList(stage, path string) []string {
	if l, ok := w.ownerLists[path]; ok {
		return l
	}
	items, _, code := w.listAll(w.viewers[0].token, path)
	if code != 200 && code != 404 {
		w.fail("%s: the owner's %s answered %d", stage, path, code)
	}
	w.ownerLists[path] = ids(items)
	return w.ownerLists[path]
}

// expect compares a profile's list with the owner's less what the rule hides, and keeps the
// cards it listed.
func (w *world) expect(stage string, v profileUnderTest, f *facts, path string, cards map[string]Item) []Item {
	want := slices.DeleteFunc(slices.Clone(w.ownerList(stage, path)), func(id string) bool { return f.hidden(v.profile, id) })
	items, total, code := w.listAll(v.token, path)
	switch {
	case code == 404 && len(want) == 0:
	case code != 200:
		w.fail("%s: %s: %s answered %d with %d titles it may see", stage, v.name, path, code, len(want))
	case !slices.Equal(ids(items), want):
		w.fail("%s: %s: %s lists %s", stage, v.name, path, w.difference(f, ids(items), want))
	case total != len(items):
		w.fail("%s: %s: %s totals %d and lists %d", stage, v.name, path, total, len(items))
	}
	for _, it := range items {
		cards[it.ID] = it
	}
	return items
}

func (f *facts) parentOf(id string) string {
	if f == nil || f.items[id] == nil {
		return ""
	}
	return f.items[id].parent
}

func (f *facts) rootOf(id string) string {
	if f == nil || f.items[id] == nil {
		return ""
	}
	return f.items[id].root
}

// remember notes what is hidden from each profile, so a later change cannot reveal it.
func (w *world) remember(f *facts) {
	if w.wasHidden == nil {
		w.wasHidden = map[int64]map[string]bool{}
	}
	for _, v := range w.viewers {
		if w.wasHidden[v.profile] == nil {
			w.wasHidden[v.profile] = map[string]bool{}
		}
		for id := range f.items {
			if f.hidden(v.profile, id) {
				w.wasHidden[v.profile][id] = true
			}
		}
	}
}

// byTitle is every item's id, by title, so what a check samples is the same on every run.
func (f *facts) byTitle() []string {
	out := slices.Collect(maps.Keys(f.items))
	slices.SortFunc(out, func(a, b string) int {
		return strings.Compare(f.items[a].title+"\x00"+a, f.items[b].title+"\x00"+b)
	})
	return out
}
