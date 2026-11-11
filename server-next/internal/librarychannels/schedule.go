package librarychannels

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// State is a channel's schedule_state: where each order had got to when the schedule was
// last written. It never holds a list of titles to play: a shuffle is a seed and a cursor.
type State struct {
	// Orders is each selection's order: "" is the channel's own, a block's id is the block's.
	Orders map[string]*Order `json:"orders,omitempty"`
	// Recent is the titles played last, oldest first, for "Avoid repeats within". At most 1,000.
	Recent []int64 `json:"recent,omitempty"`
	// Shows is the last episode played of each show that is part-way through.
	Shows map[int64]Place `json:"shows,omitempty"`
	// Titles and Unplaceable are how many titles the channel's own selection had to choose
	// from at the last build, and of those how many have no known length, for its row and health.
	Titles      int `json:"titles"`
	Unplaceable int `json:"unplaceable,omitempty"`
}

// Place is one episode in a show's order.
type Place struct {
	Season int   `json:"s"`
	Number int   `json:"n"`
	ID     int64 `json:"i"`
}

// Order is where one selection's play order had got to. Under a shuffle the order is the
// matching titles sorted by a hash of Seed and each id, and Cursor is the last hash played:
// a title added later joins this round if its hash is ahead of the cursor. In order, After is
// the sort key last played.
type Order struct {
	Seed   uint64 `json:"seed"`
	Cursor uint64 `json:"cursor,omitempty"`
	After  string `json:"after,omitempty"`
	Round  int    `json:"round,omitempty"`
	Draws  uint64 `json:"draws,omitempty"`
	// Show and Left are a marathon running: the show and how many episodes are still to come.
	Show int64 `json:"show,omitempty"`
	Left int   `json:"left,omitempty"`
	// Airing is when a block's airing that last played began, so Start again knows a new one.
	Airing int64 `json:"airing,omitempty"`
	// rewound says this build has put back the titles a rebuild threw away.
	rewound bool
}

const recentKept = 1000

// Source is what building a schedule reads. *db.DB is one.
type Source interface {
	ChannelCandidates(ctx context.Context, sel db.ChannelSelection, fn func(db.ChannelCandidate) error) error
	ShowEpisodes(ctx context.Context, showID int64, afterSeason, afterNumber int, afterID int64, limit int) ([]db.Episode, error)
	ShowEpisodeCount(ctx context.Context, showID int64) (int, error)
	ShowEpisodeAt(ctx context.Context, showID int64, place int) (db.Episode, error)
}

// Discarded is a programme a rebuild throws away: its title goes back in its order.
type Discarded struct {
	ItemID, ShowID  int64
	Season, Episode int
}

// Report says how a build went, for the channel's state and its health.
type Report struct {
	Titles      int // titles the channel's own selection can choose from
	Unplaceable int // of those, titles with no known length
	Slate       time.Duration
	Built       time.Duration
	Programmes  int
}

// Word is the channel's state after the build, and the sentence why when it needs attention.
func (r Report) Word() (string, string) {
	switch {
	case r.Titles == 0:
		return db.LibraryChannelAttention, "Nothing matches what this channel plays. Loosen a condition, or choose more libraries."
	case r.Built > 0 && r.Slate*2 > r.Built:
		return db.LibraryChannelAttention, fmt.Sprintf("Only %d titles are left to schedule. Add more titles, or loosen a condition.", r.Titles)
	}
	return db.LibraryChannelOnAir, ""
}

type title struct {
	id     int64
	public string
	show   bool
	ms     int64
	key    string
	weight float64
	hash   uint64
}

// pool is one selection's matching titles, read once per build. It lives only while one
// channel's schedule is built.
type pool struct {
	titles      []title // sorted by key
	byHash      []int   // indexes into titles, sorted by hash for hashSeed
	hashSeed    uint64
	hashed      bool
	unplaceable int
	weights     float64
}

type build struct {
	ctx   context.Context
	src   Source
	b     *Builder
	loc   *time.Location
	st    *State
	now   time.Time
	pools map[string]*pool
	// base is the seed new orders are made from: a hash of the builder, so a preview and the
	// save of the same document make the same order.
	base      uint64
	discarded map[int64]bool
	// thrownAway is the programmes a rebuild threw away, in time order.
	thrownAway []Discarded
	report     Report
	rows       []db.ScheduleRow
}

// Build fills a channel's schedule from from until it reaches until, carrying st forward.
// discarded are the programmes a rebuild throws away.
func Build(ctx context.Context, src Source, b *Builder, loc *time.Location, st *State, from, until, now time.Time,
	discarded []Discarded) ([]db.ScheduleRow, Report, error) {
	if st.Orders == nil {
		st.Orders = map[string]*Order{}
	}
	if st.Shows == nil {
		st.Shows = map[int64]Place{}
	}
	bd := &build{ctx: ctx, src: src, b: b, loc: loc, st: st, now: now, pools: map[string]*pool{}, discarded: map[int64]bool{}, base: seedOf(b)}
	bd.putBack(discarded)
	main, err := bd.pool("", &b.Selection)
	if err != nil {
		return nil, Report{}, err
	}
	bd.report.Titles, bd.report.Unplaceable = len(main.titles)+main.unplaceable, main.unplaceable
	if err := bd.run(from, until); err != nil {
		return nil, Report{}, err
	}
	bd.report.Programmes = len(bd.rows)
	return bd.rows, bd.report, nil
}

// putBack undoes what thrown-away programmes did to the state: each show goes back to the
// first of its episodes that was thrown away, and the titles leave Recent. Orders are put
// back as each is first used.
func (bd *build) putBack(discarded []Discarded) {
	bd.thrownAway = discarded
	for _, d := range discarded {
		if d.ItemID == 0 {
			continue
		}
		bd.discarded[d.ItemID] = true
		if d.ShowID != 0 {
			bd.discarded[d.ShowID] = true
			before := Place{d.Season, d.Episode, d.ItemID - 1}
			if place, ok := bd.st.Shows[d.ShowID]; !ok || placeLess(before, place) {
				bd.st.Shows[d.ShowID] = before
			}
		}
	}
	if len(bd.discarded) > 0 {
		bd.st.Recent = slices.DeleteFunc(bd.st.Recent, func(id int64) bool { return bd.discarded[id] })
		for _, o := range bd.st.Orders {
			o.Show, o.Left = 0, 0
		}
	}
}

func placeLess(a, b Place) bool {
	if a.Season != b.Season {
		return a.Season < b.Season
	}
	if a.Number != b.Number {
		return a.Number < b.Number
	}
	return a.ID < b.ID
}

// seedOf is a hash of a builder document.
func seedOf(b *Builder) uint64 {
	raw, _ := json.Marshal(b)
	h := fnv.New64a()
	h.Write(raw)
	return h.Sum64()
}

func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func hashOf(seed uint64, id int64) uint64 { return mix(seed ^ mix(uint64(id))) }

func words(text string) []string {
	return strings.FieldsFunc(sorttext.Fold(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func sortKey(sortBy string, c db.ChannelCandidate) string {
	var value string
	switch sortBy {
	case "release":
		value = c.Release
		if value == "" {
			value = "9999"
		}
	case "year":
		value = fmt.Sprintf("%04d", c.Year)
		if c.Year == 0 {
			value = "9999"
		}
	case "recent":
		value = fmt.Sprintf("%015d", int64(1e15)-c.AddedAt)
	case "firstAdded":
		value = fmt.Sprintf("%015d", c.AddedAt)
	case "rating":
		value = fmt.Sprintf("%06d", int((10-c.Rating)*1000))
	case "plays":
		value = fmt.Sprintf("%012d", c.Plays)
	default:
		value = c.SortTitle
	}
	return value + "\x00" + fmt.Sprintf("%020d", c.ID)
}

// maxProgramme and minProgramme are the longest and shortest titles a channel schedules.
const (
	maxProgramme = 24 * time.Hour
	minProgramme = time.Minute
)

func (bd *build) pool(key string, sel *Selection) (*pool, error) {
	if p := bd.pools[key]; p != nil {
		return p, nil
	}
	query := db.ChannelSelection{Libraries: sel.Libraries, Match: sel.Match, Conditions: sel.Conditions, Always: sel.Always, Never: sel.Never,
		OnlyAlways: sel.OnlyAlways,
		Films:      slices.Contains(sel.Kinds, "movie"), Shows: slices.Contains(sel.Kinds, "show"), Text: sel.Mentions != "",
		Plays: sel.Order == "leastPlayed"}
	if sel.AddedWithinDays > 0 {
		query.AddedSince = bd.now.AddDate(0, 0, -sel.AddedWithinDays)
	}
	mentions := words(sel.Mentions)
	weights := map[string]float64{}
	for _, w := range sel.Weights {
		weights[w.ID] = w.Weight
	}
	sortBy := sel.Sort
	switch {
	case sel.Order == "leastPlayed":
		sortBy = "plays"
	case sel.Episodes == "rotate":
		sortBy = "title"
	}
	p := &pool{}
	pinned := map[string]bool{}
	for _, id := range sel.Always {
		pinned[id] = true
	}
	var shows []db.ChannelCandidate
	err := bd.src.ChannelCandidates(bd.ctx, query, func(c db.ChannelCandidate) error {
		if len(mentions) > 0 && !pinned[c.PublicID] {
			have := map[string]bool{}
			for _, w := range words(c.Title + " " + c.Summary) {
				have[w] = true
			}
			for _, w := range mentions {
				if !have[w] {
					return nil
				}
			}
		}
		if c.Show && sel.Episodes == "mixed" {
			shows = append(shows, c)
			return nil
		}
		if !c.Show && !playable(c.DurationMS) {
			p.unplaceable++
			return nil
		}
		weight := weights[c.PublicID]
		if weight == 0 {
			weight = 1
		}
		p.titles = append(p.titles, title{id: c.ID, public: c.PublicID, show: c.Show, ms: c.DurationMS, key: sortKey(sortBy, c), weight: weight})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Mixed with everything else: each episode is picked like a film.
	for _, show := range shows {
		weight := weights[show.PublicID]
		if weight == 0 {
			weight = 1
		}
		after := Place{}
		for {
			episodes, err := bd.src.ShowEpisodes(bd.ctx, show.ID, after.Season, after.Number, after.ID, 500)
			if err != nil {
				return nil, err
			}
			for _, e := range episodes {
				if !playable(e.DurationMS) {
					p.unplaceable++
					continue
				}
				p.titles = append(p.titles, title{id: e.ID, ms: e.DurationMS, weight: weight,
					key: fmt.Sprintf("%s\x00%04d%05d\x00%020d", show.SortTitle, e.Season, e.Number, e.ID)})
			}
			if len(episodes) < 500 {
				break
			}
			last := episodes[len(episodes)-1]
			after = Place{last.Season, last.Number, last.ID}
		}
	}
	sort.Slice(p.titles, func(i, j int) bool { return p.titles[i].key < p.titles[j].key })
	for _, t := range p.titles {
		p.weights += t.weight
	}
	bd.pools[key] = p
	return p, nil
}

func (p *pool) hashFor(seed uint64) {
	if p.hashed && p.hashSeed == seed {
		return
	}
	p.byHash = p.byHash[:0]
	for n := range p.titles {
		p.titles[n].hash = hashOf(seed, p.titles[n].id)
		p.byHash = append(p.byHash, n)
	}
	sort.Slice(p.byHash, func(i, j int) bool { return p.titles[p.byHash[i]].hash < p.titles[p.byHash[j]].hash })
	p.hashSeed, p.hashed = seed, true
}

func (bd *build) order(key string) *Order {
	o := bd.st.Orders[key]
	if o == nil {
		h := fnv.New64a()
		h.Write([]byte(key))
		o = &Order{Seed: mix(bd.base ^ h.Sum64())}
		bd.st.Orders[key] = o
	}
	return o
}

// rewind puts an order back to just before the first title a rebuild threw away, so the
// rebuilt schedule plays what was thrown away again rather than skipping it. A shuffle that
// has started a new round since keeps its place: what was thrown away plays in the next round.
func (bd *build) rewind(o *Order, p *pool) {
	if o.rewound {
		return
	}
	o.rewound = true
	if len(bd.thrownAway) == 0 {
		return
	}
	index := make(map[int64]int, len(p.titles))
	for n, t := range p.titles {
		index[t.id] = n
	}
	for _, d := range bd.thrownAway {
		n, ok := index[d.ItemID]
		if !ok {
			n, ok = index[d.ShowID]
		}
		if !ok {
			continue
		}
		t := p.titles[n]
		if o.After != "" {
			o.After = t.key[:len(t.key)-1]
		}
		if hash := hashOf(o.Seed, t.id); o.Cursor != 0 && hash <= o.Cursor {
			o.Cursor = hash - 1
		}
		return
	}
}

func (bd *build) recent(id int64) bool {
	avoid := bd.b.AvoidWithin()
	from := max(len(bd.st.Recent)-avoid, 0)
	return avoid > 0 && slices.Contains(bd.st.Recent[from:], id)
}

func (bd *build) played(ids ...int64) {
	bd.st.Recent = append(bd.st.Recent, ids...)
	if extra := len(bd.st.Recent) - recentKept; extra > 0 {
		bd.st.Recent = slices.Delete(bd.st.Recent, 0, extra)
	}
}

// pick chooses the next title of a selection's order, or false when the order has nothing
// more to give (everything has played and the channel shows the slate, or nothing matches).
func (bd *build) pick(o *Order, p *pool, sel *Selection, room int64) (title, bool) {
	if len(p.titles) == 0 {
		return title{}, false
	}
	avoid := len(p.titles) > bd.b.AvoidWithin()
	fits := func(t title) bool { return room <= 0 || t.show || t.ms <= room }
	if sel.Order == "random" {
		for try := 0; try < 20; try++ {
			o.Draws++
			r := float64(mix(o.Seed^o.Draws)>>11) / (1 << 53) * p.weights
			for _, t := range p.titles {
				if r -= t.weight; r < 0 {
					if (!avoid || !bd.recent(t.id)) && fits(t) {
						return t, true
					}
					break
				}
			}
		}
		for _, t := range p.titles {
			if fits(t) {
				return t, true
			}
		}
		return title{}, false
	}
	shuffled := sel.Order == "shuffle" || sel.Order == "" || sel.Order == "inOrderThenShuffle" && o.Round > 0
	if sel.Episodes == "rotate" {
		shuffled = false
	}
	for round := 0; round < 2; round++ {
		if shuffled {
			p.hashFor(o.Seed)
			start := sort.Search(len(p.byHash), func(i int) bool { return p.titles[p.byHash[i]].hash > o.Cursor })
			for _, n := range p.byHash[start:] {
				t := p.titles[n]
				o.Cursor = t.hash
				if (!avoid || !bd.recent(t.id)) && fits(t) {
					return t, true
				}
			}
		} else {
			start := sort.Search(len(p.titles), func(i int) bool { return p.titles[i].key > o.After })
			for _, t := range p.titles[start:] {
				o.After = t.key
				if (!avoid || !bd.recent(t.id)) && fits(t) {
					return t, true
				}
			}
		}
		// Everything has played.
		if sel.WhenDone == "slate" && sel.Episodes != "rotate" {
			return title{}, false
		}
		o.Round++
		o.Cursor, o.After = 0, ""
		if shuffled || sel.Order == "inOrderThenShuffle" {
			o.Seed, shuffled = mix(o.Seed+1), true
		}
	}
	return title{}, false
}

// playable says a title or episode of this length can be scheduled: a known length of at
// least a minute (shorter ones, stray clips or bad data, would fill a week with rows) and at
// most a day.
func playable(ms int64) bool {
	return ms >= minProgramme.Milliseconds() && ms <= maxProgramme.Milliseconds()
}

// episode is the episode of show to play next under the selection. An episode that is placed
// moves the show's place; one that does not fit room leaves the place where it is, so it airs
// later. Episodes that can never be scheduled are passed over for good.
func (bd *build) episode(show int64, sel *Selection, o *Order, room int64) (db.Episode, bool, error) {
	fits := func(e db.Episode) bool { return room <= 0 || e.DurationMS <= room }
	if sel.Episodes == "random" {
		count, err := bd.src.ShowEpisodeCount(bd.ctx, show)
		if err != nil || count == 0 {
			return db.Episode{}, false, err
		}
		o.Draws++
		e, err := bd.src.ShowEpisodeAt(bd.ctx, show, int(mix(o.Seed^o.Draws)%uint64(count)))
		if err == db.ErrNotFound {
			return db.Episode{}, false, nil
		}
		return e, err == nil && playable(e.DurationMS) && fits(e), err
	}
	for try := 0; try < 50; try++ {
		place := bd.st.Shows[show]
		next, err := bd.src.ShowEpisodes(bd.ctx, show, place.Season, place.Number, place.ID, 1)
		if err != nil {
			return db.Episode{}, false, err
		}
		if len(next) == 0 {
			if place == (Place{}) {
				return db.Episode{}, false, nil // a show with no episodes to play
			}
			delete(bd.st.Shows, show) // finished: the show starts again from its first episode
			continue
		}
		e := next[0]
		if !playable(e.DurationMS) {
			bd.st.Shows[show] = Place{e.Season, e.Number, e.ID}
			continue
		}
		if !fits(e) {
			return db.Episode{}, false, nil
		}
		bd.st.Shows[show] = Place{e.Season, e.Number, e.ID}
		return e, true, nil
	}
	return db.Episode{}, false, nil
}

// runLength is how many more episodes of a show play in a row after the one just placed: the
// rest of the marathon, or under In order by "Show then episode", every episode it has left.
func (bd *build) runLength(sel *Selection, show int64) (int, error) {
	if sel.Episodes == "marathon" {
		if sel.Marathon == 0 {
			return 11, nil
		}
		return sel.Marathon - 1, nil
	}
	place := bd.st.Shows[show]
	left, err := bd.src.ShowEpisodes(bd.ctx, show, place.Season, place.Number, place.ID, 5000)
	return len(left), err
}

// inRuns says a show's episodes play in a row: a marathon, or In order by show then episode.
func inRuns(sel *Selection) bool {
	return sel.Episodes == "marathon" || sel.Order == "inOrder" && sel.Sort == "episode" && sel.Episodes != "mixed"
}

// next is the next programme of a selection: its item and length, or false for the slate.
// room, when above 0, is how long the programme may run.
func (bd *build) next(poolKey, orderKey string, sel *Selection, room int64) (int64, int64, bool, error) {
	p, err := bd.pool(poolKey, sel)
	if err != nil {
		return 0, 0, false, err
	}
	o := bd.order(orderKey)
	bd.rewind(o, p)
	if inRuns(sel) && o.Show != 0 && o.Left > 0 {
		e, ok, err := bd.episode(o.Show, sel, o, room)
		if err != nil {
			return 0, 0, false, err
		}
		if ok {
			o.Left--
			bd.played(e.ID)
			return e.ID, e.DurationMS, true, nil
		}
		o.Show, o.Left = 0, 0 // the next episode does not fit here: it stays next for its show
	}
	for try := 0; try < 20; try++ {
		t, ok := bd.pick(o, p, sel, room)
		if !ok {
			return 0, 0, false, nil
		}
		if !t.show {
			bd.played(t.id)
			return t.id, t.ms, true, nil
		}
		e, ok, err := bd.episode(t.id, sel, o, room)
		if err != nil {
			return 0, 0, false, err
		}
		if !ok {
			continue
		}
		if inRuns(sel) {
			n, err := bd.runLength(sel, t.id)
			if err != nil {
				return 0, 0, false, err
			}
			o.Show, o.Left = t.id, n
		}
		bd.played(t.id, e.ID)
		return e.ID, e.DurationMS, true, nil
	}
	return 0, 0, false, nil
}

// airing is one weekly block's stretch of time.
type airing struct {
	block      *Block
	start, end time.Time
}

// airingsFrom lists the blocks' airings that begin on the local days from day-1 to day+days.
func (bd *build) airingsFrom(t time.Time, days int) []airing {
	local := t.In(bd.loc)
	var out []airing
	for d := -1; d <= days; d++ {
		date := time.Date(local.Year(), local.Month(), local.Day()+d, 0, 0, 0, 0, bd.loc)
		day := weekdays[date.Weekday()]
		for n := range bd.b.Blocks {
			blk := &bd.b.Blocks[n]
			if !slices.Contains(blk.Days, day) {
				continue
			}
			from, _ := clock(blk.From)
			to, _ := clock(blk.To)
			start := time.Date(date.Year(), date.Month(), date.Day(), from/60, from%60, 0, 0, bd.loc)
			endDay := date.Day()
			if to <= from {
				endDay++
			}
			end := time.Date(date.Year(), date.Month(), endDay, to/60, to%60, 0, 0, bd.loc)
			out = append(out, airing{blk, start, end})
		}
	}
	return out
}

func (bd *build) add(start time.Time, ms int64, item int64) time.Time {
	end := start.Add(time.Duration(ms) * time.Millisecond)
	bd.rows = append(bd.rows, db.ScheduleRow{StartsAt: start, EndsAt: end, ItemID: item})
	bd.report.Built += end.Sub(start)
	if item == 0 {
		bd.report.Slate += end.Sub(start)
	}
	return end
}

func (bd *build) slate(start, end time.Time) time.Time {
	length := min(max(end.Sub(start), time.Minute), maxProgramme)
	return bd.add(start, length.Milliseconds(), 0)
}

func (bd *build) run(from, until time.Time) error {
	t := from
	var airings []airing
	var airingsAt time.Time
	for t.Before(until) {
		if err := bd.ctx.Err(); err != nil {
			return err
		}
		if len(bd.b.Blocks) > 0 && (airingsAt.IsZero() || t.Sub(airingsAt) > 24*time.Hour) {
			airings, airingsAt = bd.airingsFrom(t, 8), t
		}
		var now *airing
		next := t.Add(maxProgramme)
		for n := range airings {
			a := &airings[n]
			if !t.Before(a.start) && t.Before(a.end) {
				now = a
			}
			if a.start.After(t) && a.start.Before(next) {
				next = a.start
			}
		}
		if now == nil {
			// Outside every block a programme always finishes, so a block can start late.
			item, ms, ok, err := bd.next("", "", &bd.b.Selection, 0)
			if err != nil {
				return err
			}
			if !ok {
				t = bd.slate(t, next)
				continue
			}
			t = bd.add(t, ms, item)
			continue
		}
		blk := now.block
		o := bd.order(blk.ID)
		if blk.Resume == "restart" && o.Airing != now.start.UnixMilli() {
			o.Cursor, o.After, o.Round, o.Show, o.Left = 0, "", 0, 0, 0
		}
		o.Airing = now.start.UnixMilli()
		poolKey, sel := blk.ID, blk.Selection
		if sel == nil {
			poolKey, sel = "", &bd.b.Selection
		}
		var room int64
		if blk.Overrun == "fit" {
			room = now.end.Sub(t).Milliseconds()
		}
		item, ms, ok, err := bd.next(poolKey, blk.ID, sel, room)
		if err == nil && !ok && blk.IfEmpty != "slate" && blk.Selection != nil {
			item, ms, ok, err = bd.next("", "", &bd.b.Selection, room)
		}
		if err != nil {
			return err
		}
		if !ok {
			t = bd.slate(t, now.end)
			continue
		}
		if blk.Overrun == "cut" {
			ms = min(ms, now.end.Sub(t).Milliseconds())
		}
		t = bd.add(t, ms, item)
	}
	return nil
}
