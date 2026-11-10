package main

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Timing summarises how long a kind of request took, in milliseconds.
type Timing struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50Ms"`
	P90 float64 `json:"p90Ms"`
	P99 float64 `json:"p99Ms"`
	Max float64 `json:"maxMs"`
}

func summarise(samples []time.Duration) Timing {
	if len(samples) == 0 {
		return Timing{}
	}
	slices.Sort(samples)
	ms := func(at int) float64 { return float64(samples[at].Microseconds()) / 1000 }
	n := len(samples)
	return Timing{N: n, P50: ms(n / 2), P90: ms(n * 9 / 10), P99: ms(n * 99 / 100), Max: ms(n - 1)}
}

func sortedKeys(m map[string]Timing) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// workload is the reads and writes of people using the server: what an app sends as someone
// browses libraries, opens titles and marks things watched.
type workload struct {
	d        *db.DB
	viewers  []db.Viewer
	libs     []db.Library
	holders  []string // shows, seasons and albums to open
	leaves   []string // films, episodes, tracks and books to open and mark
	people   []string
	mu       sync.Mutex
	reads    map[string][]time.Duration
	writes   []time.Duration
	failures int
}

// newWorkload reads enough of the library, from different ends of it, to have titles to ask for.
func newWorkload(ctx context.Context, d *db.DB, viewers []db.Viewer) (*workload, error) {
	w := &workload{d: d, viewers: viewers}
	v := viewers[0]
	var err error
	if w.libs, err = d.Libraries(ctx, v); err != nil {
		return nil, err
	}
	for _, lib := range w.libs {
		kinds := []db.ItemKind{seed.LeafKind(lib.Kind)}
		switch lib.Kind {
		case db.LibraryShows:
			kinds = append(kinds, db.KindShow, db.KindSeason)
		case db.LibraryMusic:
			kinds = append(kinds, db.KindAlbum)
		}
		for _, kind := range kinds {
			for _, sort := range []string{db.SortTitle, db.SortAdded, db.SortYear} {
				for _, desc := range []bool{false, true} {
					page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Sort: sort, Desc: desc, Limit: 200})
					if err != nil {
						return nil, err
					}
					for _, item := range page.Items {
						if kind.Playable() {
							w.leaves = append(w.leaves, item.PublicID)
						} else {
							w.holders = append(w.holders, item.PublicID)
						}
					}
				}
			}
		}
	}
	for _, id := range w.leaves[:min(len(w.leaves), 60)] {
		detail, err := d.Item(ctx, v, id)
		if err != nil {
			return nil, err
		}
		for _, credit := range detail.Credits {
			w.people = append(w.people, credit.PersonID)
		}
	}
	if len(w.leaves) == 0 || len(w.holders) == 0 || len(w.people) == 0 {
		return nil, errors.New("the library is too small to drive")
	}
	return w, nil
}

// start runs the readers, and the writer if a rate is given, until the returned function is
// called. That function returns what was measured: reads by class (and "all"), and writes.
func (w *workload) start(ctx context.Context, readers, writesPerSec int) func() (map[string]Timing, Timing) {
	ctx, cancel := context.WithCancel(ctx)
	w.reads, w.writes = map[string][]time.Duration{}, nil
	var wg sync.WaitGroup
	for n := range readers {
		wg.Go(func() { w.read(ctx, rand.New(rand.NewPCG(uint64(n), 99))) })
	}
	if writesPerSec > 0 {
		wg.Go(func() { w.write(ctx, writesPerSec) })
	}
	return func() (map[string]Timing, Timing) {
		cancel()
		wg.Wait()
		out := map[string]Timing{}
		var all []time.Duration
		for class, samples := range w.reads {
			out[class] = summarise(samples)
			if !strings.Contains(class, " (") {
				all = append(all, samples...)
			}
		}
		out["all"] = summarise(all)
		if w.failures > 0 {
			log.Fatalf("%d requests failed during the run", w.failures)
		}
		return out, summarise(w.writes)
	}
}

// record times one request under its class, and again under the class and the kind of
// restricted profile that asked, if one did.
func (w *workload) record(class, who string, started time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil && !errors.Is(err, db.ErrNotFound) && !errors.Is(err, context.Canceled) {
		w.failures++
		println("request failed:", class, err.Error())
	}
	w.reads[class] = append(w.reads[class], time.Since(started))
	if who != "" {
		w.reads[class+who] = append(w.reads[class+who], time.Since(started))
	}
}

func pick[T any](r *rand.Rand, from []T) T { return from[r.IntN(len(from))] }

// read sends one request after another with a short pause, as one busy app would.
func (w *workload) read(ctx context.Context, r *rand.Rand) {
	yes, no := true, false
	var next db.ItemQuery // the list this reader is paging down
	var nextViewer db.Viewer
	var nextWho string
	for ctx.Err() == nil {
		n := r.IntN(len(w.viewers))
		v := w.viewers[n]
		// The seeder restricts the last three profiles, by label, by library and by rating.
		// Their requests are also timed apart, because a restriction is checked row by row.
		who := ""
		if at := n - (len(w.viewers) - 3); at >= 0 && len(w.viewers) > 3 {
			who = []string{" (label)", " (library)", " (rating)"}[at]
		}
		// What a library's own screens list: mostly its titles, sometimes everything under them.
		lib := pick(r, w.libs)
		kind := seed.LeafKind(lib.Kind)
		switch {
		case lib.Kind == db.LibraryShows && r.IntN(2) == 0:
			kind = db.KindShow
		case lib.Kind == db.LibraryMusic && r.IntN(5) < 3:
			kind = pick(r, []db.ItemKind{db.KindAlbum, db.KindAlbum, db.KindArtist})
		}
		video := lib.Kind == db.LibraryMovies || lib.Kind == db.LibraryShows && kind == db.KindEpisode
		leaf := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind},
			Sort: pick(r, []string{db.SortTitle, db.SortTitle, db.SortAdded, db.SortYear, db.SortRelease}), Desc: r.IntN(3) == 0}
		started := time.Now()
		switch n := r.IntN(100); {
		case n < 20: // open a library: the first page and its size
			leaf.WithTotal = true
			page, err := w.d.Items(ctx, v, leaf)
			w.record("first-page", who, started, err)
			leaf.WithTotal, leaf.Cursor = false, page.NextCursor
			next, nextViewer, nextWho = leaf, v, who
		case n < 50: // scroll on
			if next.Cursor == "" {
				continue
			}
			page, err := w.d.Items(ctx, nextViewer, next)
			w.record("next-page", nextWho, started, err)
			next.Cursor = page.NextCursor
		case n < 55:
			leaf.Genre, leaf.WithTotal = pick(r, seed.Genres(lib.Kind)), true
			_, err := w.d.Items(ctx, v, leaf)
			w.record("genre", who, started, err)
		case n < 60:
			if lib.Kind == db.LibraryMusic { // music has no watched state to filter by
				continue
			}
			leaf.Watched = pick(r, []*bool{&yes, &no})
			_, err := w.d.Items(ctx, v, leaf)
			w.record("watched", who, started, err)
		case n < 63:
			leaf.YearFrom, leaf.YearTo, leaf.WithTotal = 1990, 1999, true
			_, err := w.d.Items(ctx, v, leaf)
			w.record("decade", who, started, err)
		case n < 65:
			if !video { // only video libraries offer the resolution filter
				continue
			}
			leaf.Resolution = pick(r, []string{"sd", "720p", "1080p", "4k"})
			_, err := w.d.Items(ctx, v, leaf)
			w.record("resolution", who, started, err)
		case n < 70:
			q := db.ItemQuery{Favorite: &yes}
			if r.IntN(2) == 0 {
				q = db.ItemQuery{InList: &yes}
			}
			_, err := w.d.Items(ctx, v, q)
			w.record("favourites", who, started, err)
		case n < 80:
			_, err := w.d.Items(ctx, v, db.ItemQuery{Parent: pick(r, w.holders)})
			w.record("children", who, started, err)
		case n < 85:
			person := pick(r, w.people)
			_, err := w.d.Person(ctx, v, person)
			if err == nil {
				_, err = w.d.Items(ctx, v, db.ItemQuery{Person: person})
			}
			w.record("person", who, started, err)
		case n < 90: // Home: both rows
			_, err := w.d.ContinueWatching(ctx, v)
			if err == nil {
				_, err = w.d.RecentlyAdded(ctx, v)
			}
			w.record("home", who, started, err)
		case n < 93: // opening the genre filter of a library screen
			leaf.Kinds = []db.ItemKind{seed.TitleKind(lib.Kind)}
			_, err := w.d.Facets(ctx, v, leaf, db.FacetGenre)
			w.record("facets", who, started, err)
		default:
			_, err := w.d.Item(ctx, v, pick(r, w.leaves))
			w.record("detail", who, started, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// write marks titles at a steady rate. Each write is timed from when it was due, so time
// spent waiting behind an import batch and the disk sync are both counted.
func (w *workload) write(ctx context.Context, perSec int) {
	r := rand.New(rand.NewPCG(5, 5))
	yes, no := true, false
	tick := time.NewTicker(time.Second / time.Duration(perSec))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case due := <-tick.C:
			var edit db.StateEdit
			switch r.IntN(4) {
			case 0: // refused for a track, as an app would be
				edit.Watched = pick(r, []*bool{&yes, &no})
			case 1:
				edit.Favorite = pick(r, []*bool{&yes, &no})
			case 2:
				edit.InList = pick(r, []*bool{&yes, &no})
			default:
				position := int64(r.IntN(600_000))
				edit.PositionMS = &position
			}
			_, _, err := w.d.SetState(ctx, pick(r, w.viewers), pick(r, w.leaves), edit)
			w.mu.Lock()
			if err != nil && !errors.Is(err, db.ErrNotFound) && !errors.Is(err, db.ErrInvalid) && !errors.Is(err, context.Canceled) {
				w.failures++
				println("write failed:", err.Error())
			}
			w.writes = append(w.writes, time.Since(due))
			w.mu.Unlock()
		}
	}
}
