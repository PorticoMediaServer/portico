package librarychannels

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// cpuTime is this process's user plus system CPU time: on a one-core host it,
// not wall time on a fast disk, is what a generation costs everyone else.
func cpuTime() time.Duration {
	var u syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

// demoShapedLibrary mirrors the demo server's catalog (23 Sep): 80 movies of
// 90–120 minutes and 128 episodes across four shows, all schedulable.
func demoShapedLibrary(t testing.TB) *fixtureLibrary {
	f := openFixture(t)
	for i := 0; i < 80; i++ {
		f.movie(t, fixtureMovie{id: fmt.Sprintf("dm%03d", i), title: fmt.Sprintf("Demo Movie %d", i), overview: "A story about people.", year: 1970 + i%50, rating: 5 + float64(i%40)/10, minutes: 90 + (i*7)%31})
	}
	for i := 0; i < 4; i++ {
		f.show(t, fmt.Sprintf("ds%d", i), fmt.Sprintf("Demo Show %d", i), 2, 16)
	}
	f.settle(t)
	return f
}

func demoShapedChannels() []Config {
	movies := channel("movies", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "shuffle-bag", "none")
	movies.Rules[0].DeduplicationWindow = 20
	weighted := channel("weighted", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "rating"}, "weighted-random", "none")
	weighted.Rules[0].DeduplicationWindow = 10
	tv := channel("tv", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "sequential", "in-order")
	marathon := channel("marathon", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "shuffle-bag", "marathon")
	marathon.Rules[0].MaxConsecutive = 4
	return []Config{movies, weighted, tv, marathon}
}

func median(v []time.Duration) time.Duration {
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v[len(v)/2]
}

// The generator's cost on a demo-shaped catalog: a full builder preview (first
// day scheduled) and a background generation (Save, then batches until the
// seven-day schedule is published). Timings are logged; set
// PORTICO_LC_COST=1 to run it (it is a measurement, not a gate).
func TestGeneratorCostOnADemoShapedCatalog(t *testing.T) {
	if os.Getenv("PORTICO_LC_COST") == "" {
		t.Skip("set PORTICO_LC_COST=1 to measure")
	}
	const rounds = 5
	for _, c := range demoShapedChannels() {
		var previews, generations, previewCPU, generationCPU []time.Duration
		var entries, batches int
		for round := 0; round < rounds; round++ {
			f := demoShapedLibrary(t)
			s, _ := New(f.db)
			s.now = func() time.Time { return f.now }
			started, cpu := time.Now(), cpuTime()
			p, err := s.Preview(context.Background(), ownerAuthority(nil), c)
			if err != nil || !p.Complete || len(p.FirstDay) == 0 {
				t.Fatalf("%s preview: %v complete=%v entries=%d", c.ID, err, p.Complete, len(p.FirstDay))
			}
			previews, previewCPU = append(previews, time.Since(started)), append(previewCPU, cpuTime()-cpu)
			started, cpu = time.Now(), cpuTime()
			if _, err = s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save-" + c.ID, Config: c}); err != nil {
				t.Fatal(err)
			}
			batches = 0
			for ; batches < 10000; batches++ {
				worked, err := s.RunBatch(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !worked {
					break
				}
			}
			generations, generationCPU = append(generations, time.Since(started)), append(generationCPU, cpuTime()-cpu)
			if err = f.db.QueryRow(`SELECT count(*) FROM lc_entries e JOIN lc_generations g ON g.id=e.generation_id WHERE g.channel_id=? AND g.status='published'`, c.ID).Scan(&entries); err != nil || entries == 0 {
				t.Fatalf("%s generation published %d entries: %v", c.ID, entries, err)
			}
		}
		ms := func(v []time.Duration) time.Duration { return median(v).Round(time.Millisecond) }
		t.Logf("%-9s preview %6s wall %6s CPU   generation %6s wall %6s CPU   (%d entries, %d batches)", c.ID, ms(previews), ms(previewCPU), ms(generations), ms(generationCPU), entries, batches)
	}
}

// Scheduling reads the recent history for every slot. Its show lookup must be
// an index seek: a scan of the generation's candidates made each slot cost the
// whole pool (demo-shaped TV channel: 0.85 ms per slot, 7x the seek).
func TestHistoryLookupIsAnIndexSeek(t *testing.T) {
	f := openFixture(t)
	rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+historySQL, "g", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "COVERING INDEX lc_candidate_item (generation_id=? AND item_id=?)") {
		t.Fatalf("history lookup plan:\n%s", plan)
	}
}

// Draws use the per-show heads form of the in-order rule and ordered walks use
// the per-candidate form: both must admit exactly the same candidates, at every
// point of a schedule, whatever has been used so far.
func TestInOrderFormsAdmitTheSameCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := demoShapedLibrary(t)
	s, _ := New(f.db)
	s.now = func() time.Time { return f.now }
	c := channel("marathon", Query{LibraryIDs: []string{"tv"}, Kinds: []string{"episode"}, Order: "episode"}, "shuffle-bag", "marathon")
	c.Rules[0].MaxConsecutive = 3
	if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save", Config: c}); err != nil {
		t.Fatal(err)
	}
	eligible := func(gen string, cycle int64, form string) []string {
		// In-order rules always draw from the bag: a candidate used in this cycle is out.
		args := []any{gen, cycle, cycle}
		if form == inOrderHeadsSQL {
			args = append(args, gen, "main")
		}
		rows, err := f.db.Query(`SELECT c.item_id FROM lc_candidates c WHERE c.generation_id=? AND c.rule_id='main' AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=c.generation_id AND u.rule_id=c.rule_id AND u.item_id=c.item_id AND u.cycle=?)`+form+` ORDER BY c.item_id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	checked := 0
	for batch := 0; batch < 100; batch++ {
		var gen, phase string
		if err := f.db.QueryRow(`SELECT id,phase FROM lc_generations WHERE status='pending'`).Scan(&gen, &phase); err != nil {
			break
		}
		if phase == "scheduling" {
			for cycle := int64(0); cycle < 2; cycle++ {
				walk, heads := eligible(gen, cycle, inOrderWalkSQL), eligible(gen, cycle, inOrderHeadsSQL)
				if strings.Join(walk, ",") != strings.Join(heads, ",") {
					t.Fatalf("batch %d cycle %d: walk %v, heads %v", batch, cycle, walk, heads)
				}
				checked += len(walk)
			}
		}
		if worked, err := s.RunBatch(context.Background()); err != nil || !worked {
			break
		}
	}
	if checked == 0 {
		t.Fatal("never compared a scheduling state")
	}
}
