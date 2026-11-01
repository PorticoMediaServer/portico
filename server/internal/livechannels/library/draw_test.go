package librarychannels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// syntheticPool adds n movie-shaped candidates to rule "main" of generation
// gen, after any it has: items numbered from 1000000, minutes long, with the
// weight the SQL expression weight gives ordinal i. Large pools are built here
// rather than as catalog items, which would take minutes to scan in.
func syntheticPool(t testing.TB, f *fixtureLibrary, gen string, n, minutes int, weight string) {
	t.Helper()
	f.exec(t, `WITH RECURSIVE k(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM k WHERE i+1<?),
 base(o) AS (SELECT COALESCE(max(ordinal)+1,0) FROM lc_candidates WHERE generation_id=? AND rule_id='main')
 INSERT INTO lc_candidates SELECT ?,'main',1000000+o+i,o+i,'movies','synthetic','synthetic',?,'Synthetic '||(o+i),0,0,0,`+weight+`,printf('%09d',o+i) FROM k,base`, n, gen, gen, minutes*60000)
}

// syntheticGeneration is a week-long scheduling generation over a pool of
// one-minute synthetic items, outside any channel's lineup.
func syntheticGeneration(t testing.TB, f *fixtureLibrary, r Rule, n int, weight string) *generation {
	t.Helper()
	c := Config{ID: "synthetic", Seed: "seed-synthetic", Timezone: "UTC", DefaultRuleID: "main", Rules: []Rule{r}}
	start := f.now.UnixMilli()
	end := f.now.Add(7 * 24 * time.Hour).UnixMilli()
	f.exec(t, `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('synthetic',1,'{}',0,0,'synthetic','preview')`)
	f.exec(t, `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('synthetic','synthetic',1,'{}','',?,'preview','scheduling','',?,?,?,?,?)`, c.Seed, start, end, start, start, start)
	syntheticPool(t, f, "synthetic", n, 1, weight)
	return &generation{ID: "synthetic", ChannelID: "synthetic", Revision: 1, Config: c, Seed: c.Seed, Phase: "scheduling", Start: start, End: end, Boundary: start, Cursor: start, Created: start}
}

// drawSlots schedules n slots of g in one transaction and reports what each
// marked used.
func drawSlots(t testing.TB, f *fixtureLibrary, g *generation, n int) []usedMark {
	t.Helper()
	out := make([]usedMark, 0, n)
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		q := g.statements(tx)
		for len(out) < n {
			var w slotWrites
			if e := planSlot(context.Background(), q, g, &w); e != nil {
				return e
			}
			if w.used == nil {
				return fmt.Errorf("slot at %d drew nothing: %+v", g.Cursor, w.entry)
			}
			if e := applySlot(context.Background(), q, g, &w); e != nil {
				return e
			}
			out = append(out, *w.used)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func rule(mode string) Rule {
	return Rule{ID: "main", Mode: mode, EpisodeMode: "none", Exhaustion: "loop", MaxConsecutive: 100}
}

// A shuffle bag plays every candidate once, in a shuffled order, before any
// plays again.
func TestShuffleBagDrawsEachCandidateOncePerCycle(t *testing.T) {
	f := openFixture(t)
	const n = 300
	g := syntheticGeneration(t, f, rule("shuffle-bag"), n, "1.0")
	seen, inPlace := map[int64]bool{}, 0
	for k, u := range drawSlots(t, f, g, n) {
		if u.cycle != 0 || seen[u.item] || u.item < 1000000 || u.item >= 1000000+n {
			t.Fatalf("draw %d: %+v (seen %v)", k, u, seen[u.item])
		}
		seen[u.item] = true
		if u.item == 1000000+int64(k) {
			inPlace++
		}
	}
	if inPlace > 5 {
		t.Fatalf("%d of %d draws in ordinal order: not shuffled", inPlace, n)
	}
	if u := drawSlots(t, f, g, 1)[0]; u.cycle != 1 {
		t.Fatalf("draw after the bag emptied: %+v", u)
	}
}

// Weighted random draws with replacement in proportion to weight: here odd
// ordinals weigh 4 and even ones 1, so odd ones are four fifths of the plays.
func TestWeightedDrawsFollowTheWeights(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := openFixture(t)
	r := rule("weighted-random")
	r.Weights = []ItemWeight{{ItemID: "heavy", Weight: 4}}
	g := syntheticGeneration(t, f, r, 200, "CASE WHEN i%2=1 THEN 4.0 ELSE 1.0 END")
	const draws = 2000
	heavy, plays := 0, map[int64]int{}
	for _, u := range drawSlots(t, f, g, draws) {
		plays[u.item]++
		if u.item%2 == 1 {
			heavy++
		}
	}
	// Binomial standard deviation is about 18 plays; allow over three.
	if share := float64(heavy) / draws; share < 0.77 || share > 0.83 {
		t.Fatalf("heavy share %.3f, want 0.8", share)
	}
	if len(plays) < 190 {
		t.Fatalf("only %d of 200 candidates ever played", len(plays))
	}
}

// When eligible candidates are too rare for proposals to find (here one left
// in a bag of 5000), the draw counts them instead: still exact, and the bag
// still ends and starts again.
func TestRareEligibleCandidatesAreCounted(t *testing.T) {
	f := openFixture(t)
	g := syntheticGeneration(t, f, rule("shuffle-bag"), 5000, "1.0")
	f.exec(t, `INSERT INTO lc_used SELECT generation_id,rule_id,item_id,0 FROM lc_candidates WHERE generation_id='synthetic' AND item_id<>1003210`)
	if u := drawSlots(t, f, g, 2); u[0].item != 1003210 || u[0].cycle != 0 || u[1].cycle != 1 {
		t.Fatalf("the last in the bag, then the next bag: %+v", u)
	}
}

// Shuffled episodes still play in order within each show, and every one
// plays once before the bag starts again.
func TestShuffledEpisodesKeepTheirOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := demoShapedLibrary(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("tv", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "shuffle-bag", "in-order")
	if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save", Config: c}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 1000; n++ {
		if worked, err := s.RunBatch(context.Background()); err != nil {
			t.Fatal(err)
		} else if !worked {
			break
		}
	}
	rows, err := f.db.Query(`SELECT c.show_id,c.season,c.episode,c.item_id FROM lc_entries e JOIN lc_generations g ON g.id=e.generation_id AND g.status='published' JOIN lc_candidates c ON c.generation_id=e.generation_id AND c.item_id=e.item_id ORDER BY e.start_ms LIMIT 128`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	last, seen, shows := map[int64]int{}, map[int64]bool{}, map[int64]bool{}
	k := 0
	for ; rows.Next(); k++ {
		var show, item int64
		var season, episode int
		if err = rows.Scan(&show, &season, &episode, &item); err != nil {
			t.Fatal(err)
		}
		if order := season*1000 + episode; order <= last[show] || seen[item] {
			t.Fatalf("entry %d: show %d S%dE%d after %d (repeat %v)", k, show, season, episode, last[show], seen[item])
		} else {
			last[show] = order
		}
		seen[item], shows[show] = true, true
	}
	if k != 128 || len(shows) != 4 {
		t.Fatalf("%d entries over %d shows", k, len(shows))
	}
}

// WJ1: a draw used to count the whole eligible pool, and every slot of a
// 64-slot batch ran in one write transaction. With about 100k candidates the
// batch outran the worker's deadline, rolled back, and was retried from the
// same cursor forever, holding the writer each time. Now each slot commits on
// its own and a draw costs a few seeks: a 100k shuffle pool keeps scheduling
// under the worker's three-second budget, and even a deadline far too short for
// a batch keeps the slots it made.
func TestLargeShufflePoolKeepsScheduling(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 100k-candidate pool")
	}
	f := openFixture(t)
	for i := 0; i < 4; i++ {
		f.movie(t, fixtureMovie{id: fmt.Sprintf("m%d", i), title: fmt.Sprintf("Movie %d", i), minutes: 95})
	}
	f.settle(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("big", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "shuffle-bag", "none")
	c.Rules[0].DeduplicationWindow = 20
	if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save", Config: c}); err != nil {
		t.Fatal(err)
	}
	var gen, phase string
	for n := 0; phase != "scheduling"; n++ {
		if n == 100 {
			t.Fatalf("never reached scheduling (phase %q)", phase)
		}
		if _, err := s.RunBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT id,phase FROM lc_generations WHERE channel_id='big' AND status='pending'`).Scan(&gen, &phase); err != nil {
			t.Fatal(err)
		}
	}
	syntheticPool(t, f, gen, 100000, 95, "1.0")
	entries := func() (n int) {
		if err := f.db.QueryRow(`SELECT count(*) FROM lc_entries WHERE generation_id=?`, gen).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A deadline too short for a whole batch still keeps the slots made within it. A loaded
	// shared runner can spend the first short deadline before the first slot (the batch reads
	// the 100k pool first), so the deadline doubles until slots are kept; what this asserts is
	// that an interrupted batch keeps its work, not how fast a runner is.
	for wait := 500 * time.Millisecond; ; wait *= 2 {
		short, cancel := context.WithTimeout(context.Background(), wait)
		_, err := s.RunBatch(short)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline") {
			t.Fatalf("short deadline %s: %v", wait, err)
		}
		if entries() > 0 {
			break
		}
		if wait >= 8*time.Second {
			t.Fatalf("deadlines up to %s kept nothing (%v)", wait, err)
		}
	}

	// The worker's own budget schedules the whole week, a pass at a time.
	for n := 0; ; n++ {
		if n == 50 {
			t.Fatalf("still pending after %d passes (%d entries)", n, entries())
		}
		before := entries()
		budget, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		started := time.Now()
		worked, err := s.RunBatch(budget)
		cancel()
		if err != nil {
			t.Fatalf("pass %d after %s: %v", n, time.Since(started), err)
		}
		if !worked {
			break
		}
		if err = f.db.QueryRow(`SELECT phase FROM lc_generations WHERE id=?`, gen).Scan(&phase); err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		if phase == "scheduling" && entries() == before {
			t.Fatalf("pass %d scheduled nothing", n)
		}
	}
	var published string
	if err := f.db.QueryRow(`SELECT status FROM lc_generations WHERE id=?`, gen).Scan(&published); err != nil || published != "published" {
		t.Fatalf("generation %s: %v", published, err)
	}
}

// A slot is committed only by the pass whose lease it planned under, and only
// if the generation's cursor is where the plan found it.
func TestSlotWriteRechecksTheLease(t *testing.T) {
	f := openFixture(t)
	f.movie(t, fixtureMovie{id: "m", title: "Movie", minutes: 95})
	f.settle(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("one", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "shuffle-bag", "none")
	if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save", Config: c}); err != nil {
		t.Fatal(err)
	}
	var g generation
	for n := 0; g.Phase != "scheduling"; n++ {
		if n == 100 {
			t.Fatal("never reached scheduling")
		}
		if _, err := s.RunBatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			var id string
			if e := tx.QueryRow(`SELECT id FROM lc_generations WHERE channel_id='one' AND status='pending'`).Scan(&id); e != nil {
				return e
			}
			var e error
			g, e = loadGeneration(context.Background(), tx, id)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.exec(t, `UPDATE lc_generations SET lease_token='mine',lease_until_ms=? WHERE id=?`, f.now.Add(time.Minute).UnixMilli(), g.ID)
	if err := s.scheduleSlot(context.Background(), dbwork.ClassBackgroundMedia, &g, "theirs"); !errors.Is(err, errMoved) {
		t.Fatalf("someone else's lease: %v", err)
	}
	stale := g
	if err := s.scheduleSlot(context.Background(), dbwork.ClassBackgroundMedia, &g, "mine"); err != nil {
		t.Fatal(err)
	}
	if err := s.scheduleSlot(context.Background(), dbwork.ClassBackgroundMedia, &stale, "mine"); !errors.Is(err, errMoved) {
		t.Fatalf("a plan from before the last slot: %v", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM lc_entries WHERE generation_id=?`, g.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d entries: %v", n, err)
	}
}

// A proposal is one seek on (generation, rule, ordinal), with the proposals
// outermost: never a scan of the pool.
func TestProposalIsAnIndexSeek(t *testing.T) {
	f := openFixture(t)
	where := "c.generation_id=? AND c.rule_id=? AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=c.generation_id AND u.rule_id=c.rule_id AND u.item_id=c.item_id AND u.cycle=?)" + inOrderWalkSQL
	rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+proposalSQL+where+` ORDER BY j.key LIMIT 1`, "[[1,0]]", "g", "main", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if len(plan) < 2 || !strings.HasPrefix(plan[0], "SCAN j ") || !strings.Contains(plan[1], "SEARCH c USING INDEX") || !strings.Contains(plan[1], "(generation_id=? AND rule_id=? AND ordinal=?)") {
		t.Fatalf("proposal plan:\n%s", strings.Join(plan, "\n"))
	}
}
