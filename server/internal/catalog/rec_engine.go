package catalog

import (
	"encoding/json"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The recommendation engine (Recommendations — Plan.md). The expensive parts
// are kept as the library and the profile change: each work's facets, best-first
// facet lists per library with a vote-weighted quality, facet rarity
// (compactcatalog domain 32), provider similar-title links, and each profile's
// taste at two horizons (compactcatalog/rec_profile.go). A request only reads
// them: the heads of the profile's strongest facet lists, the similar titles of
// its recent favorites, and the best of each library, a few thousand index rows
// whatever the library's size; then it rescores the best few hundred in full,
// keeps what the viewer may see and hasn't engaged with, and diversifies.
//
// Freshness: nothing is cached by time. Jobs the taste worker hasn't reached
// are overlaid with the same computation, and a result is reused only under a
// key of every revision it read (catalogue, viewer, taste, restriction, day).

const (
	recTasteFacets  = 24  // strongest taste facets read per request
	recPostingsHead = 150 // head of each facet list read, shared among libraries
	recPostingsMin  = 40
	recQualityFill  = 60  // best titles per library, for fill and cold starts
	recSeeds        = 20  // recent favorites whose similar titles are read
	recRescore      = 400 // candidates rescored in full
	recRanked       = 300 // ranked works kept for a row's pages
)

// recFacetWeight is how much sharing one facet of a type says about taste.
// Franchises and series say most; a decade or a studio, least.
func recFacetWeight(f string) float64 {
	prefix, rest, _ := strings.Cut(f, ":")
	switch prefix {
	case "c", "k":
		return 3
	case "a", "b":
		return 2.5
	case "cd":
		department, _, _ := strings.Cut(rest, ":")
		switch department {
		case "directing", "creator":
			return 2.5
		case "writing":
			return 1.8
		case "acting":
			return 1.2
		}
		return 1
	case "p":
		return 1.2
	case "p5": // the first five billed also carry 'p:': a lead weighs 2.2
		return 1
	case "x":
		return 1.2
	case "x3": // a strength-3 dataset tag also carries 'x:': 2.0 in all
		return 0.8
	case "g":
		return 1
	case "t":
		return 0.8
	case "n":
		return 0.8
	case "s":
		return 0.6
	case "e":
		return 0.4
	}
	return 0
}

type recTaste struct {
	values      map[string]float64 // today's value of each facet
	engaged     map[int64]bool
	hidden      map[int64]bool
	loaded      map[int64]bool // only candidate IDs, plus exact pending overlays
	stored      bool           // the profile has a persisted taste epoch
	bulkPending bool           // pending jobs exceed the exact overlay bound
	seeds       []recSeed
	// revision is the taste revision the values were read at; pending is
	// true when unprocessed jobs were overlaid (then nothing is memoised).
	revision int64
	pending  bool
}

type recSeed struct {
	work   int64
	weight float64
}

const recSeedSQL = `SELECT work_id,long,short FROM rec_profile_signals INDEXED BY rec_profile_signals_positive_recent WHERE profile_id=? AND weight>0 ORDER BY at DESC LIMIT ?`

// recLoadTaste reads the profile's taste and recent favorites, and
// overlays jobs the worker hasn't processed yet.
func (s *Service) recLoadTaste(profile string, now time.Time) (recTaste, error) {
	t := recTaste{values: map[string]float64{}, engaged: map[int64]bool{}, hidden: map[int64]bool{}, loaded: map[int64]bool{}}
	if profile == "" {
		return t, nil
	}
	ctx, q := s.Context(), s.read()
	day := compactcatalog.UnixDays(now)
	epoch, revision, known, err := compactcatalog.RecEpoch(ctx, q, profile)
	if err != nil {
		return t, err
	}
	t.revision, t.stored = revision, known
	dl, ds := 0.0, 0.0
	if known {
		dl, ds = compactcatalog.RecDecay(epoch, day, compactcatalog.RecLongHalfLife), compactcatalog.RecDecay(epoch, day, compactcatalog.RecShortHalfLife)
		rows, err := q.Query(`SELECT facet,long,short FROM rec_profile_taste WHERE profile_id=?`, profile)
		if err != nil {
			return t, err
		}
		for rows.Next() {
			var f string
			var long, short float64
			if err = rows.Scan(&f, &long, &short); err != nil {
				rows.Close()
				return t, err
			}
			t.values[f] = long*dl + short*ds
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return t, err
		}
		rows, err = q.Query(recSeedSQL, profile, recSeeds)
		if err != nil {
			return t, err
		}
		for rows.Next() {
			var sd recSeed
			var long, short float64
			if err = rows.Scan(&sd.work, &long, &short); err != nil {
				rows.Close()
				return t, err
			}
			sd.weight = long*dl + short*ds
			t.seeds = append(t.seeds, sd)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return t, err
		}
	}
	// Abandonment is decided by time passing, not by a write: something
	// started, under a quarter watched and untouched for three weeks counts
	// mildly against what it's like (never shown; the title itself is
	// engaged, so it isn't recommended either). A work the profile otherwise
	// enjoyed (a show with finished episodes) isn't counted against.
	if err := s.recAbandoned(profile, now, t.values); err != nil {
		return t, err
	}
	// Overlay the jobs not processed yet with the worker's own computation.
	rows, err := q.Query(`SELECT work_id FROM rec_profile_jobs WHERE profile_id=? ORDER BY work_id LIMIT `+strconv.Itoa(recOverlayJobs+1), profile)
	if err != nil {
		return t, err
	}
	var jobs []int64
	for rows.Next() {
		var work int64
		if err = rows.Scan(&work); err != nil {
			rows.Close()
			return t, err
		}
		jobs = append(jobs, work)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return t, err
	}
	// A normal backlog (a few recent actions) is overlaid exactly. A bulk one,
	// such as an imported watch history, would cost a read per job on every
	// request: its works are left out of recommendations until the worker has
	// taken them into taste (they are recommended neither early nor wrongly),
	// and the ranking catches up then. The memo keys on pending work either way.
	if len(jobs) > recOverlayJobs {
		t.pending, t.bulkPending = true, true
		return t, nil
	}
	for _, work := range jobs {
		t.pending = true
		next, err := compactcatalog.RecSignalFor(ctx, q, profile, work, now)
		if err != nil {
			return t, err
		}
		old, _, err := compactcatalog.RecStored(ctx, q, profile, work)
		if err != nil {
			return t, err
		}
		if !known {
			epoch, known = math.Floor(next.At), true
			dl, ds = compactcatalog.RecDecay(epoch, day, compactcatalog.RecLongHalfLife), compactcatalog.RecDecay(epoch, day, compactcatalog.RecShortHalfLife)
		}
		long := next.Weight * math.Exp2((next.At-epoch)/compactcatalog.RecLongHalfLife)
		short := next.Weight * math.Exp2((next.At-epoch)/compactcatalog.RecShortHalfLife)
		delta := (long-old.Long)*dl + (short-old.Short)*ds
		if delta != 0 {
			facets, err := compactcatalog.RecWorkFacets(ctx, q, work)
			if err != nil {
				return t, err
			}
			for _, f := range facets {
				t.values[f] += delta
			}
		}
		t.engaged[work], t.hidden[work], t.loaded[work] = next.Engaged, next.Hidden, true
		// The work's stored seed is replaced: an undone favorite or a new
		// dislike stops promoting its similar titles at once.
		kept := t.seeds[:0]
		for _, sd := range t.seeds {
			if sd.work != work {
				kept = append(kept, sd)
			}
		}
		t.seeds = kept
		if next.Weight > 0 {
			t.seeds = append([]recSeed{{work, long*dl + short*ds}}, t.seeds...)
		}
	}
	return t, nil
}

const recSignalsSQL = `SELECT j.value,COALESCE(s.engaged,0),COALESCE(s.hidden,0),
	 CASE WHEN ? THEN EXISTS(SELECT 1 FROM rec_profile_jobs p WHERE p.profile_id=? AND p.work_id=j.value) ELSE 0 END
	 FROM json_each(?) j LEFT JOIN rec_profile_signals s ON s.profile_id=? AND s.work_id=j.value`

// recLoadSignals reads only the IDs a row may use. Profile history and a bulk
// import can grow independently of that candidate set; neither is hydrated
// into request-sized maps. Exact job overlays already loaded in recLoadTaste
// win over stored signals, including an undone watch or dislike.
func (s *Service) recLoadSignals(profile string, t *recTaste, works []int64) error {
	if profile == "" || len(works) == 0 {
		return nil
	}
	missing := make([]int64, 0, len(works))
	for _, work := range works {
		if !t.loaded[work] {
			missing = append(missing, work)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	storedProfile := profile
	if !t.stored {
		storedProfile = ""
	}
	rows, err := s.read().Query(recSignalsSQL, t.bulkPending, profile, idsJSON64(missing), storedProfile)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var work int64
		var engaged, hidden, pending bool
		if err = rows.Scan(&work, &engaged, &hidden, &pending); err != nil {
			return err
		}
		t.engaged[work], t.hidden[work], t.loaded[work] = engaged || pending, hidden || pending, true
	}
	return rows.Err()
}

// recCandidateWorks supplies IDs to the bounded signal reader even when a
// ranking came from the memo and rank did not run during this request.
func recCandidateWorks(ranked []recCandidate) []int64 {
	works := make([]int64, 0, len(ranked))
	for _, c := range ranked {
		if work, err := strconv.ParseInt(c.Work, 10, 64); err == nil {
			works = append(works, work)
		}
	}
	return works
}

// recOverlayJobs bounds the pending profile jobs a request overlays exactly.
const recOverlayJobs = 256

const (
	recAbandonAfter    = 21 // days untouched
	recAbandonFraction = 0.25
	recAbandonWeight   = -0.5
)

func (s *Service) recAbandoned(profile string, now time.Time, values map[string]float64) error {
	cutoff := now.AddDate(0, 0, -recAbandonAfter).UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.read().Query(`SELECT a.item_id,a.updated_at,COALESCE((SELECT show_id FROM catalog_episodes WHERE entity_id=a.item_id),(SELECT album_id FROM catalog_songs WHERE entity_id=a.item_id),(SELECT book_id FROM catalog_book_files WHERE entity_id=a.item_id),a.item_id),COALESCE((SELECT weight FROM rec_profile_signals signal WHERE signal.profile_id=a.profile_id AND signal.work_id=COALESCE((SELECT show_id FROM catalog_episodes WHERE entity_id=a.item_id),(SELECT album_id FROM catalog_songs WHERE entity_id=a.item_id),(SELECT book_id FROM catalog_book_files WHERE entity_id=a.item_id),a.item_id)),0)
	 FROM (SELECT profile_id,item_id,updated_at,state FROM progress_activity INDEXED BY progress_home_profile_recent
 WHERE profile_id=? AND state!='ended' AND updated_at<? ORDER BY updated_at DESC LIMIT 200) a
 CROSS JOIN progress p ON p.profile_id=a.profile_id AND p.item_id=a.item_id
 WHERE a.state!='ended' AND p.completed=0 AND p.position>0
	 AND p.position<?*1000*(SELECT max(x.duration) FROM catalog_asset_links l CROSS JOIN catalog_assets x ON x.id=l.asset_id WHERE l.entity_id=a.item_id)
	 ORDER BY a.updated_at DESC LIMIT 200`, profile, cutoff, recAbandonFraction)
	if err != nil {
		return err
	}
	type abandoned struct {
		work int64
		at   float64
	}
	var list []abandoned
	for rows.Next() {
		var item, work int64
		var updated string
		var storedWeight float64
		if err = rows.Scan(&item, &updated, &work, &storedWeight); err != nil {
			rows.Close()
			return err
		}
		if t, e := time.Parse(time.RFC3339Nano, updated); e == nil && storedWeight <= 0 {
			list = append(list, abandoned{work, compactcatalog.UnixDays(t)})
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	day := compactcatalog.UnixDays(now)
	contributions := make([][2]any, len(list))
	for i, a := range list {
		contributions[i] = [2]any{a.work, recAbandonWeight * math.Exp2(-(day-a.at)/compactcatalog.RecLongHalfLife)}
	}
	raw, err := json.Marshal(contributions)
	if err != nil {
		return err
	}
	facets, err := s.read().Query(`SELECT f.facet,json_extract(j.value,'$[1]') FROM json_each(?) j
 CROSS JOIN catalog_rec_facets f ON f.entity_id=CAST(json_extract(j.value,'$[0]') AS INTEGER)`, string(raw))
	if err != nil {
		return err
	}
	defer facets.Close()
	for facets.Next() {
		var facet string
		var weight float64
		if err = facets.Scan(&facet, &weight); err != nil {
			return err
		}
		values[facet] += weight
	}
	return facets.Err()
}

type recLibraries struct {
	ids    []int64
	public map[int64]string
}

func (s *Service) recLibraries(libraries []string) (recLibraries, error) {
	out := recLibraries{public: map[int64]string{}}
	rows, err := s.read().Query(`SELECT id,library_id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?)) AND retired=0 ORDER BY id`, idsJSON(libraries))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var public string
		if err = rows.Scan(&id, &public); err != nil {
			return out, err
		}
		out.ids = append(out.ids, id)
		out.public[id] = public
	}
	return out, rows.Err()
}

// recRarity reads each facet's work count and the number of works.
func (s *Service) recRarity(facets []string) (map[string]float64, error) {
	keys := make([]string, 0, len(facets)+1)
	seen := make(map[string]bool, len(facets)+1)
	for _, facet := range facets {
		if !seen[facet] {
			seen[facet] = true
			keys = append(keys, facet)
		}
	}
	if !seen[compactcatalog.RecAllFacet] {
		keys = append(keys, compactcatalog.RecAllFacet)
	}
	raw, _ := json.Marshal(keys)
	rows, err := s.read().Query(`SELECT facet,works FROM catalog_rec_df WHERE facet IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	df := map[string]float64{}
	for rows.Next() {
		var f string
		var n float64
		if err = rows.Scan(&f, &n); err != nil {
			return nil, err
		}
		df[f] = n
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	total := math.Max(df[compactcatalog.RecAllFacet], 1)
	idf := map[string]float64{}
	for _, f := range facets {
		idf[f] = math.Log(1 + total/math.Max(df[f], 1))
	}
	return idf, nil
}

type recScored struct {
	work     int64
	partial  float64
	quality  float64
	votes    int64
	similar  float64
	facets   []string
	match    bool    // shares a taste facet beyond its decade
	duration float64 // longest playable member, seconds
	score    float64
	id       string
	kind     string
	added    string
}

// recSession is one request's view of the engine: the profile's taste, the
// viewer's libraries and the rarity of the taste's facets, read once and
// shared by every row the request ranks.
type recSession struct {
	s         *Service
	r         HomeRequest
	now       time.Time
	taste     recTaste
	libraries recLibraries
	idf       map[string]float64
	strongest []string
	key       string // every revision the session reads, for the memo
	// discover is the library kind when the session ranks one library's
	// Discover view (which adds kind-specific rows), "" on Home.
	discover  string
	hydration *recHydration // immutable source reads shared only within this request snapshot
}

func (s *Service) recSession(r HomeRequest) (*recSession, error) {
	if err := s.compactProjectionReady(18, 32); err != nil {
		return nil, err
	}
	x := &recSession{s: s, r: r, now: s.recommendationNow(r.Now), hydration: s.recHydrationFor(r)}
	var err error
	if x.taste, err = s.recLoadTaste(r.Profile, x.now); err != nil {
		return nil, err
	}
	revision, err := s.homeRevision(homeUnique(r.Libraries), r.Profile)
	if err != nil {
		return nil, err
	}
	var data int64
	if err = s.read().QueryRow(`SELECT revision FROM rec_data_revision WHERE id=1`).Scan(&data); err != nil {
		return nil, err
	}
	x.key = strings.Join([]string{r.Profile, RestrictionFence(r.Restrictions), idsJSON(homeUnique(r.Libraries)), itoa(revision.Catalog), itoa(revision.Viewer), itoa(x.taste.revision), itoa(data), x.day()}, "\x00")
	if x.libraries, err = s.recLibraries(r.Libraries); err != nil {
		return nil, err
	}
	facets := make([]string, 0, len(x.taste.values))
	for f := range x.taste.values {
		if recFacetWeight(f) > 0 {
			facets = append(facets, f)
		}
	}
	if x.idf, err = x.recRarity(facets); err != nil {
		return nil, err
	}
	for _, f := range facets {
		if x.taste.values[f] > 0 {
			x.strongest = append(x.strongest, f)
		}
	}
	sort.Slice(x.strongest, func(i, j int) bool {
		if a, b := x.strength(x.strongest[i]), x.strength(x.strongest[j]); a != b {
			return a > b
		}
		return x.strongest[i] < x.strongest[j]
	})
	if len(x.strongest) > recTasteFacets {
		x.strongest = x.strongest[:recTasteFacets]
	}
	return x, nil
}

func (x *recSession) day() string { return x.now.In(time.Local).Format("2006-01-02") }

// strength is how much a facet says about this profile: taste × rarity × type.
func (x *recSession) strength(f string) float64 {
	return recFacetWeight(f) * x.idf[f] * x.taste.values[f]
}

// memo runs compute once per key of every revision read; a session with
// unprocessed taste jobs overlaid always computes (its key can't name them).
func (x *recSession) memo(row string, compute func() ([]recCandidate, error)) ([]recCandidate, error) {
	key := x.key + "\x00" + row
	if !x.taste.pending {
		if cached, ok := recMemo.get(x.s.state, key); ok {
			return cached, nil
		}
	}
	out, err := compute()
	if err == nil && !x.taste.pending {
		recMemo.put(x.s.state, key, out)
	}
	return out, err
}

// recOptions shape one ranking: where its candidates come from and what it
// keeps. The zero value is Recommended's.
type recOptions struct {
	facets         []string // retrieval facets; nil: the strongest taste facets
	head           int      // posting head per facet (shared among libraries)
	works          []int64  // explicit candidates, besides retrieval
	noFill         bool     // skip the best-of-library fill
	noSimilar      bool     // skip favorites' similar titles
	includeEngaged bool     // keep works the profile engaged with (Rediscover)
	anyMatch       bool     // don't require a taste match
	keep           func(c *recScored) bool
	diversify      bool
	all            bool // rank every candidate (no rescoring cut, no length cap)
}

// rank retrieves, rescores, filters and orders one ranking.
func (x *recSession) rank(o recOptions) ([]recCandidate, error) {
	if len(x.libraries.ids) == 0 {
		return nil, nil
	}
	pool := map[int64]*recScored{}
	candidate := func(work int64) *recScored {
		c := pool[work]
		if c == nil {
			c = &recScored{work: work, quality: -1}
			pool[work] = c
		}
		return c
	}
	facets := o.facets
	if facets == nil {
		facets = x.strongest
	}
	head := o.head
	if head == 0 {
		head = max(recPostingsMin, recPostingsHead/len(x.libraries.ids))
	}
	for _, f := range facets {
		strength := x.strength(f)
		for _, library := range x.libraries.ids {
			if err := x.recPostings(f, library, head, func(work int64, quality float64) {
				c := candidate(work)
				c.partial += strength
				c.quality = quality
			}); err != nil {
				return nil, err
			}
		}
	}
	if !o.noFill {
		for _, library := range x.libraries.ids {
			if err := x.recPostings(compactcatalog.RecAllFacet, library, recQualityFill, func(work int64, quality float64) {
				candidate(work).quality = quality
			}); err != nil {
				return nil, err
			}
		}
	}
	if !o.noSimilar {
		if err := x.recSimilar(func(work int64, weight float64) { candidate(work).similar += weight }); err != nil {
			return nil, err
		}
	}
	for _, work := range o.works {
		candidate(work)
	}
	works := make([]int64, 0, len(pool))
	for work := range pool {
		works = append(works, work)
	}
	if err := x.s.recLoadSignals(x.r.Profile, &x.taste, works); err != nil {
		return nil, err
	}
	// Drop what the profile has engaged with or hidden before spending the
	// full rescoring on it.
	var scored []*recScored
	maxSimilar := 0.0
	for work, c := range pool {
		if x.taste.hidden[work] || x.taste.engaged[work] && !o.includeEngaged {
			continue
		}
		scored = append(scored, c)
		maxSimilar = math.Max(maxSimilar, c.similar)
	}
	prelim := func(c *recScored) float64 {
		v := c.partial
		if maxSimilar > 0 {
			v += 0.5 * c.similar / maxSimilar * math.Max(1, v)
		}
		if c.quality >= 0 {
			v += 0.01 * c.quality
		}
		return v
	}
	sort.Slice(scored, func(i, j int) bool {
		if a, b := prelim(scored[i]), prelim(scored[j]); a != b {
			return a > b
		}
		return scored[i].work < scored[j].work
	})
	if len(scored) > recRescore && !o.all {
		scored = scored[:recRescore]
	}
	if err := x.recRescore(scored, maxSimilar); err != nil {
		return nil, err
	}
	// With taste, a recommendation must match it (beyond the decade) or be a
	// similar title of a favorite: a well-rated film the profile has shown no
	// taste for isn't a recommendation. A cold start ranks by quality.
	kept := scored[:0]
	for _, c := range scored {
		if (o.anyMatch || len(x.strongest) == 0 || c.match || c.similar > 0) && (o.all || !recPoorlyRated(c)) {
			kept = append(kept, c)
		}
	}
	scored, err := x.s.recEligible(x.r, x.libraries, kept)
	if err != nil {
		return nil, err
	}
	// A row's own condition applies once eligibility has read what it may
	// need (a duration, the added date).
	if o.keep != nil {
		filtered := scored[:0]
		for _, c := range scored {
			if o.keep(c) {
				filtered = append(filtered, c)
			}
		}
		scored = filtered
	}
	fresh := x.now.AddDate(0, 0, -30).UTC().Format("2006-01-02")
	for _, c := range scored {
		if c.added >= fresh {
			c.score += 0.05
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].work < scored[j].work
	})
	out := make([]recCandidate, 0, len(scored))
	for _, c := range scored {
		out = append(out, recCandidate{ID: c.id, Work: itoa(c.work), Kind: c.kind, Added: c.added, Facets: c.facets, Score: c.score})
	}
	if o.diversify {
		out = recDiversify(out, x.r.Profile+"\x00"+x.day())
	}
	if len(out) > recRanked && !o.all {
		out = out[:recRanked]
	}
	return out, nil
}

// recPoorlyRated is the quality floor for recommendations: a title many
// people rated poorly isn't recommended however well it matches (unrated and
// barely rated titles stay).
func recPoorlyRated(c *recScored) bool { return c.votes >= 50 && c.quality >= 0 && c.quality < 5.5 }

// recRank is Recommended: the profile's whole taste over every library.
func (s *Service) recRank(r HomeRequest) ([]recCandidate, error) {
	x, err := s.recSession(r)
	if err != nil {
		return nil, err
	}
	return x.memo("recommended", func() ([]recCandidate, error) { return x.rank(recOptions{diversify: true}) })
}

// recPostings reads the head of one facet's best-first list in one library.
func (s *Service) recPostings(facet string, library int64, limit int, each func(work int64, quality float64)) error {
	rows, err := s.read().Query(`SELECT entity_id,quality FROM catalog_rec_postings WHERE facet=? AND library_id=? ORDER BY quality DESC,entity_id DESC LIMIT `+strconv.Itoa(limit), facet, library)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var work int64
		var quality float64
		if err = rows.Scan(&work, &quality); err != nil {
			return err
		}
		each(work, quality)
	}
	return rows.Err()
}

// recSimilar reads the similar-title links of the recent favorites, resolved
// to library works through their provider ids; a nearer rank weighs more.
func (s *Service) recSimilar(seeds []recSeed, each func(work int64, weight float64)) error {
	if len(seeds) == 0 {
		return nil
	}
	weights := map[int64]float64{}
	ids := make([]int64, 0, len(seeds))
	for _, sd := range seeds {
		if _, ok := weights[sd.work]; !ok {
			ids = append(ids, sd.work)
		}
		weights[sd.work] += sd.weight
	}
	raw, _ := json.Marshal(ids)
	rows, err := s.read().Query(`SELECT sim.entity_id,sim.rank,x.entity_id FROM json_each(?) seed
	 CROSS JOIN catalog_similar sim ON sim.entity_id=seed.value
	 CROSS JOIN catalog_external_ids x INDEXED BY catalog_external_ids_lookup ON x.provider=sim.provider AND x.provider_kind=sim.provider_kind AND x.provider_id=sim.provider_id`, string(raw))
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[[2]int64]bool{}
	for rows.Next() {
		var seed, target int64
		var rank int
		if err = rows.Scan(&seed, &rank, &target); err != nil {
			return err
		}
		if target == seed || seen[[2]int64{seed, target}] {
			continue
		}
		seen[[2]int64{seed, target}] = true
		each(target, weights[seed]/(1+0.15*float64(rank-1)))
	}
	return rows.Err()
}

// recRescore scores each candidate over all of its facets: taste times rarity
// times type weight, normalised by the candidate's own facet mass (so a title
// with forty keywords doesn't win by count) and the taste's, plus quality and
// similar-title evidence.
func (x *recSession) recRescore(scored []*recScored, maxSimilar float64) error {
	if len(scored) == 0 {
		return nil
	}
	if err := x.hydrateScored(scored); err != nil {
		return err
	}
	taste, idf := x.taste, x.idf
	norm := recTasteNorm(taste.values, idf)
	for _, c := range scored {
		num, mass := 0.0, 0.0
		for _, f := range c.facets {
			w := recFacetWeight(f) * idf[f]
			num += w * taste.values[f]
			mass += w * w
			if taste.values[f] > 0 && !strings.HasPrefix(f, "e:") {
				c.match = true
			}
		}
		content := 0.0
		if mass > 0 && norm > 0 {
			content = num / (math.Sqrt(mass) * norm)
		}
		similar := 0.0
		if maxSimilar > 0 {
			similar = c.similar / maxSimilar
		}
		quality := c.quality
		if quality < 0 {
			quality = 6
		}
		c.score = content + 0.35*similar + 0.06*(quality-6.3)
	}
	return nil
}

// recEligible keeps the candidates the viewer may see now: available (an
// item itself; a show, album or book through any available member), in an
// allowed library, inside the viewer's restriction and not marked watched as a
// whole (a show or album marked watched is engaged too).
func (s *Service) recEligible(r HomeRequest, libraries recLibraries, scored []*recScored) ([]*recScored, error) {
	if len(scored) == 0 {
		return scored, nil
	}
	index := map[int64]*recScored{}
	ids := make([]int64, 0, len(scored))
	for _, c := range scored {
		index[c.work] = c
		ids = append(ids, c.work)
	}
	allowed, _ := json.Marshal(libraries.ids)
	restriction, args := EntityRestrictionSQL("e.id", r.Restrictions)
	if restriction != "1" {
		// A work its library's class does not hold is refused by one key
		// lookup, before the live restriction walks its members.
		published, err := s.publishedClassesSQL("e.id", r.Libraries, r.Restrictions)
		if err != nil {
			return nil, err
		}
		restriction = published + " AND " + restriction
	}
	bind := []any{idsJSON64(ids), string(allowed), r.Profile}
	bind = append(bind, args...)
	rows, err := s.read().Query(`SELECT e.id,pid(e.public_id),br.kind,COALESCE(br.recent_text,br.added_text,''),COALESCE(br.duration_max,0)
	 FROM json_each(?) j CROSS JOIN catalog_entities e ON e.id=j.value CROSS JOIN catalog_browse_rows br ON br.entity_id=e.id
	 WHERE e.retired=0 AND br.library_id IN(SELECT value FROM json_each(?))
	 AND (br.available=1 OR br.item_id IS NULL AND EXISTS(SELECT 1 FROM catalog_browse_memberships m CROSS JOIN catalog_item_availability v ON v.entity_id=m.item_id WHERE m.entity_id=e.id AND v.available=1))
	 AND NOT EXISTS(SELECT 1 FROM container_personal_state c WHERE c.profile_id=? AND c.kind=CASE br.kind WHEN 2 THEN 'show' WHEN 6 THEN 'album' WHEN 8 THEN 'book' END AND c.container_id=e.id AND c.watched=1)
	 AND `+restriction, bind...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*recScored, 0, len(scored))
	for rows.Next() {
		var work int64
		var id, added string
		var kind int
		var duration float64
		if err = rows.Scan(&work, &id, &kind, &added, &duration); err != nil {
			return nil, err
		}
		c := index[work]
		c.id, c.kind, c.added, c.duration = id, recKindName(kind), added, duration
		out = append(out, c)
	}
	return out, rows.Err()
}

func recKindName(kind int) string {
	switch kind {
	case 1:
		return "movie"
	case 2:
		return "show"
	case 6:
		return "album"
	case 8:
		return "book"
	}
	return strconv.Itoa(kind)
}

// recDiversify orders the ranking page by page (a Home row shows 12 at a
// time): within each page at most two works of one franchise or series and
// three of one director, creator, artist or author, and a small penalty for
// each repeat of a person or a genre, so a page isn't one shelf of the same
// thing. The penalties are on the engine's score scale (content −1…1).
//
// Exploration: every eighth of the first 48 places goes to a work from further
// down that scores at least 60% of the best choice and fits the page's caps,
// picked by seed (profile and day), so rows vary daily but never with filler.
//
// Bounded: the first recDiverseWindow places are diversified, each choosing
// among the next recDiverseReach candidates by score; the rest follow in score
// order. Each candidate's capped and penalised facets are resolved once.
const (
	recDiverseWindow = 60
	recDiverseReach  = 100
)

type recDiverseFacet struct {
	id      int
	cap     int
	penalty float64
}

func recDiversify(in []recCandidate, seed string) []recCandidate {
	const page, explore, reach = 12, 48, 60
	ids := map[string]int{}
	facets := make([][]recDiverseFacet, len(in))
	for i, c := range in {
		for _, f := range c.Facets {
			limit, penalty := recCap(f), recRepeatPenalty(f)
			if limit == 0 && penalty == 0 {
				continue
			}
			id, ok := ids[f]
			if !ok {
				id = len(ids)
				ids[f] = id
			}
			facets[i] = append(facets[i], recDiverseFacet{id, limit, penalty})
		}
	}
	type entry struct {
		c      recCandidate
		facets []recDiverseFacet
	}
	rest := make([]entry, len(in))
	for i := range in {
		rest[i] = entry{in[i], facets[i]}
	}
	out := make([]recCandidate, 0, len(in))
	used := make([]int, len(ids))
	adjusted := func(e entry) (float64, bool) {
		score := e.c.Score
		for _, f := range e.facets {
			if f.cap > 0 && used[f.id] >= f.cap {
				return 0, false
			}
			score -= float64(used[f.id]) * f.penalty
		}
		return score, true
	}
	for len(rest) > 0 && len(out) < recDiverseWindow {
		if len(out)%page == 0 {
			clear(used)
		}
		best, bestScore := -1, math.Inf(-1)
		for i := 0; i < len(rest) && i < recDiverseReach; i++ {
			if score, ok := adjusted(rest[i]); ok && score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			best = 0 // everything in reach is capped: keep score order
		} else if pos := len(out); pos%8 == 7 && pos < explore && bestScore > 0 {
			var eligible []int
			for i := best + 1; i < len(rest) && i < reach; i++ {
				if score, ok := adjusted(rest[i]); ok && score >= 0.6*bestScore && i >= page/2 {
					eligible = append(eligible, i)
				}
			}
			if len(eligible) > 0 {
				h := fnv.New64a()
				h.Write([]byte(seed + "\x00" + strconv.Itoa(pos)))
				best = eligible[h.Sum64()%uint64(len(eligible))]
			}
		}
		e := rest[best]
		out = append(out, e.c)
		for _, f := range e.facets {
			used[f.id]++
		}
		rest = append(rest[:best], rest[best+1:]...)
	}
	for _, e := range rest {
		out = append(out, e.c)
	}
	return out
}

// recCap is how many works sharing a facet one page may hold (0: no cap).
func recCap(f string) int {
	prefix, rest, _ := strings.Cut(f, ":")
	switch prefix {
	case "c", "k":
		return 2
	case "a", "b":
		return 3
	case "cd":
		if d, _, _ := strings.Cut(rest, ":"); d == "directing" || d == "creator" {
			return 3
		}
	}
	return 0
}

func recRepeatPenalty(f string) float64 {
	prefix, _, _ := strings.Cut(f, ":")
	switch prefix {
	case "c", "k", "a", "b":
		return 0.12
	case "cd", "p":
		return 0.03
	case "g", "t", "x":
		return 0.01
	}
	return 0
}

func idsJSON64(ids []int64) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// recMemo reuses a ranking only under a key of every revision it was read at,
// so it can never serve a stale row: it saves recomputing an identical answer.
// Callers get their own copy (row composition filters in place).
var recMemo recMemoCache

type recMemoCache struct{}

const recMemoEntries = 256

func (recMemoCache) get(state *serviceState, key string) ([]recCandidate, bool) {
	state.recMu.Lock()
	defer state.recMu.Unlock()
	v, ok := state.recMemo[key]
	return append([]recCandidate(nil), v...), ok
}

func (recMemoCache) put(state *serviceState, key string, value []recCandidate) {
	state.recMu.Lock()
	defer state.recMu.Unlock()
	// The cap is a memory bound: keys carry every revision, so clearing is safe.
	if state.recMemo == nil || len(state.recMemo) >= recMemoEntries {
		state.recMemo = map[string][]recCandidate{}
	}
	state.recMemo[key] = append([]recCandidate(nil), value...)
}
