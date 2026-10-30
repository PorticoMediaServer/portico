package catalog

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Title-page rows on the recommendation engine: "More like X" (X's provider
// similar titles and X's own facets, ranked by the viewer's taste when signed
// in), "Starring" its top-billed actor, "From" a show's creator, and "Viewers
// also watched" (opted-in members who finished X, when the owner allows
// community activity). Each is a ranking over the same bounded reads as Home.

const (
	recTitleFacets  = 12 // X's strongest facets read for More like X
	recTitleHead    = 100
	recCoViewersMin = 3  // distinct other viewers before Viewers also watched shows
	recCoRecent     = 50 // each co-viewer's recent finishes read
	recCoViewers    = 50 // the most recent finishers of a title read
)

// recTitleSession is a session whose "taste" is the title's own facets
// (rarity weighting does the rest), with the viewer's taste as a lighter
// second voice, and the title as the only similar-title seed.
func (s *Service) recTitleSession(base *recSession, work int64) (*recSession, error) {
	copied := *base
	x := &copied
	// The title replaces taste and seeds, but immutable catalogue inputs can
	// be shared with the viewer's rows inside this same read snapshot. Edges
	// are keyed by exact seeds/weights and each session keeps its own IDF map.
	if base.hydration == nil || base.hydration.snapshot == nil || base.hydration.snapshot != dbwork.Snapshot(s.Context()) {
		x.hydration = &recHydration{snapshot: dbwork.Snapshot(s.Context())}
	}
	x.taste.seeds = nil
	x.idf = map[string]float64{}
	x.strongest = nil
	facets, err := compactcatalog.RecWorkFacets(s.Context(), s.read(), work)
	if err != nil {
		return nil, err
	}
	personal := x.taste.values
	norm := 0.0
	for _, v := range personal {
		if v > 0 {
			norm = max(norm, v)
		}
	}
	values := map[string]float64{}
	for _, f := range facets {
		values[f] = 1
	}
	if norm > 0 {
		for f, v := range personal {
			values[f] += 0.3 * v / norm
		}
	}
	x.taste.values = values
	x.taste.seeds = []recSeed{{work, 1}}
	keys := make([]string, 0, len(values))
	for f := range values {
		if recFacetWeight(f) > 0 {
			keys = append(keys, f)
		}
	}
	if x.idf, err = x.recRarity(keys); err != nil {
		return nil, err
	}
	for _, f := range facets {
		if recFacetWeight(f) > 0 && !strings.HasPrefix(f, "e:") {
			x.strongest = append(x.strongest, f)
		}
	}
	sort.Slice(x.strongest, func(i, j int) bool {
		if a, b := x.strength(x.strongest[i]), x.strength(x.strongest[j]); a != b {
			return a > b
		}
		return x.strongest[i] < x.strongest[j]
	})
	if len(x.strongest) > recTitleFacets {
		x.strongest = x.strongest[:recTitleFacets]
	}
	x.key += "\x00title:" + itoa(work)
	return x, nil
}

// recMoreLike ranks the titles most like work for the viewer. Titles the
// viewer has already seen stay (they are like it) but follow the rest.
func (s *Service) recMoreLike(base *recSession, work int64) ([]recCandidate, error) {
	x, err := s.recTitleSession(base, work)
	if err != nil {
		return nil, err
	}
	ranked, err := x.memo("more_like", func() ([]recCandidate, error) {
		return x.rank(recOptions{head: recTitleHead, noFill: true, includeEngaged: true, diversify: true,
			keep: func(c *recScored) bool { return c.work != work }})
	})
	if err != nil {
		return nil, err
	}
	if err := s.recLoadSignals(x.r.Profile, &x.taste, recCandidateWorks(ranked)); err != nil {
		return nil, err
	}
	engaged := x.taste.engaged
	sort.SliceStable(ranked, func(i, j int) bool {
		a, _ := strconv.ParseInt(ranked[i].Work, 10, 64)
		b, _ := strconv.ParseInt(ranked[j].Work, 10, 64)
		return !engaged[a] && engaged[b]
	})
	return ranked, nil
}

// recFacetTitles ranks one facet's titles (a person's, a creator's) for the
// viewer, the title itself left out.
func (s *Service) recFacetTitles(x *recSession, work int64, facet string) ([]recCandidate, error) {
	if _, ok := x.idf[facet]; !ok {
		more, err := x.recRarity([]string{facet})
		if err != nil {
			return nil, err
		}
		x.idf[facet] = more[facet]
	}
	o := x.facetRow(facet)
	keep := o.keep
	o.keep = func(c *recScored) bool { return c.work != work && keep(c) }
	o.includeEngaged = true
	return x.memo("facet:"+facet+":"+itoa(work), func() ([]recCandidate, error) { return x.rank(o) })
}

// recViewersAlsoWatched is what other members who finished work also
// finished: only members who share their activity (the owner allows community
// activity, the member shows it and hasn't paused history), and only when at
// least recCoViewersMin of them finished it.
func (s *Service) recViewersAlsoWatched(x *recSession, work int64, now time.Time) ([]recCandidate, error) {
	// Finishers only (a watchlist or a like isn't watching it), the most
	// recent recCoViewers of them: a ranking, and a bounded read on a popular
	// title of a large server.
	rows, err := s.read().Query(`SELECT profile_id FROM rec_profile_signals INDEXED BY rec_profile_signals_work WHERE work_id=? AND finished=1 AND profile_id<>? ORDER BY at DESC LIMIT ?`, work, x.r.Profile, recCoViewers)
	if err != nil {
		return nil, err
	}
	var profiles []string
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		profiles = append(profiles, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var sharing []string
	for _, p := range profiles {
		ok, err := s.profileSharesActivity(p, now)
		if err != nil {
			return nil, err
		}
		if ok {
			sharing = append(sharing, p)
		}
	}
	if len(sharing) < recCoViewersMin {
		return nil, nil
	}
	counts := map[int64]int{}
	for _, p := range sharing {
		works, err := scanInt64s(s.read().Query(`SELECT work_id FROM rec_profile_signals INDEXED BY rec_profile_signals_recent WHERE profile_id=? AND finished=1 AND hidden=0 AND work_id<>? ORDER BY at DESC LIMIT ?`, p, work, recCoRecent))
		if err != nil {
			return nil, err
		}
		for _, w := range works {
			counts[w]++
		}
	}
	var candidates []int64
	for w, n := range counts {
		if n >= 2 {
			candidates = append(candidates, w)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ranked, err := x.rank(recOptions{facets: []string{}, works: candidates, noFill: true, noSimilar: true, anyMatch: true, includeEngaged: true})
	if err != nil {
		return nil, err
	}
	// Order by how many of them went on to it, then by the viewer's taste.
	sort.SliceStable(ranked, func(i, j int) bool {
		a, _ := strconv.ParseInt(ranked[i].Work, 10, 64)
		b, _ := strconv.ParseInt(ranked[j].Work, 10, 64)
		return counts[a] > counts[b]
	})
	return ranked, nil
}

// profileSharesActivity reports whether a profile (a personal key) is an
// active member who shows activity to members and hasn't paused history.
func (s *Service) profileSharesActivity(key string, now time.Time) (bool, error) {
	v, ok := viewerFromPersonalKey(key)
	if !ok {
		return false, nil
	}
	var active bool
	var err error
	switch v.Authority {
	case "local":
		err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.id=? AND p.account_id=? AND p.deleted=0 AND m.disabled=0)`, v.ProfileID, v.AccountID).Scan(&active)
	default:
		return false, nil
	}
	if err != nil || !active {
		return false, err
	}
	prefs, _, _, err := operations.EffectivePreferences(s.read(), v, "")
	if err != nil {
		return false, err
	}
	return prefs.Bool("privacy.showActivityToMembers") && !prefs.Bool("privacy.pauseWatchHistory"), nil
}

// recTitleWork is the work a title page is about: a film, or an episode's
// show; with its library and title.
func (s *Service) recTitleWork(item Item) (work int64, title string, err error) {
	err = s.read().QueryRow(`SELECT COALESCE((SELECT show_id FROM catalog_episodes WHERE entity_id=i.id),i.id) FROM catalog_entities i WHERE i.public_id=pid_blob(?)`, item.ID).Scan(&work)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	err = s.read().QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, work).Scan(&title)
	return work, title, err
}

// moreLikeRelation is More like X: the title's work ranked against every
// eligible title by shared catalogue facets (moreLikeOrder).
func (s *Service) moreLikeRelation(viewer Viewer, item Item) (relationSpec, bool, error) {
	work, title, err := s.recTitleWork(item)
	if err != nil || work == 0 {
		return relationSpec{}, false, err
	}
	return relationSpec{id: "more_like:local:" + item.ID, relation: "more_like", provider: "local", evidence: item.ID, heading: "More like " + title,
		caching: relationRevision, personal: true, order: s.moreLikeOrder(viewer, work)}, true, nil
}

// recTitleRelations are the engine's rows for a title page: More like X, From
// its creator, Starring its top-billed actor and Viewers also watched. The
// engine session is built only when a ranked row is read.
func (s *Service) recTitleRelations(viewer Viewer, item Item, community bool) ([]relationSpec, error) {
	more, ok, err := s.moreLikeRelation(viewer, item)
	if err != nil || !ok {
		return nil, err
	}
	work, _, err := s.recTitleWork(item)
	if err != nil {
		return nil, err
	}
	out := []relationSpec{more}
	var base *recSession
	session := func() (*recSession, error) {
		if base != nil {
			return base, nil
		}
		// One session (the viewer's taste, libraries, rarity) serves every row.
		x, err := s.recSession(HomeRequest{Viewer: viewer, Profile: viewer.Profile, ViewerFence: viewer.Fence, Libraries: []string{item.LibraryID}, Restrictions: s.recRestrictions})
		base = x
		return x, err
	}
	ranked := func(rank func(*recSession) ([]recCandidate, error)) func(int) (*relationOrdered, error) {
		return func(int) (*relationOrdered, error) {
			x, err := session()
			if err != nil {
				return nil, err
			}
			out, err := rank(x)
			if err != nil {
				return nil, err
			}
			return relationRanked(out), nil
		}
	}
	add := func(relation, provider, evidence, heading string, order func(int) (*relationOrdered, error)) {
		out = append(out, relationSpec{id: relation + ":" + provider + ":" + evidence, relation: relation, provider: provider, evidence: evidence, heading: heading,
			caching: relationContinuation, boundary: true, order: order})
	}
	facets, err := compactcatalog.RecWorkFacets(s.Context(), s.read(), work)
	if err != nil {
		return nil, err
	}
	for _, f := range facets {
		if strings.HasPrefix(f, "cd:creator:") {
			facet := f
			name := recTitleCase(strings.TrimPrefix(f, "cd:creator:"))
			add("creator", "local", f, "From "+name, ranked(func(x *recSession) ([]recCandidate, error) { return s.recFacetTitles(x, work, facet) }))
			break
		}
	}
	if item.Kind == "movie" {
		var provider, person, name string
		err := s.read().QueryRow(`SELECT c.provider,c.provider_person_id,c.credited_name FROM catalog_entities i JOIN catalog_credits c ON c.entity_id=i.id
		 JOIN catalog_credit_labels d ON d.id=c.department_id WHERE i.id=? AND d.label='Acting' AND c.provider_person_id<>'' ORDER BY c.source_ordinal LIMIT 1`, work).Scan(&provider, &person, &name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			facet := "p:" + provider + ":" + person
			add("starring", provider, person, "Starring "+name, ranked(func(x *recSession) ([]recCandidate, error) { return s.recFacetTitles(x, work, facet) }))
		}
	}
	if community {
		add("viewers_also_watched", "local", item.ID, "Viewers also watched", ranked(func(x *recSession) ([]recCandidate, error) {
			return s.recViewersAlsoWatched(x, work, s.recommendationNow(time.Time{}))
		}))
	}
	return out, nil
}
