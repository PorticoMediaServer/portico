package db

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSearchCost is a measurement, not a check: the load workflow runs it against a seeded
// database to learn what each part of a wide search costs, and what a count costs when it is
// exact, when it stops at the cap, and when it is narrowed from the index side. Without
// PORTICO_LOAD_DB it is skipped.
func TestSearchCost(t *testing.T) {
	source := os.Getenv("PORTICO_LOAD_DB")
	if source == "" {
		t.Skip("set PORTICO_LOAD_DB to a seeded database to measure")
	}
	ctx := context.Background()
	d, err := Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}

	// The profiles: one that sees everything, the seeder's three with one restriction each,
	// and one with all three at once, as a child's profile has.
	seeded, err := d.SeedViewers(ctx)
	if err != nil || len(seeded) < 10 {
		t.Fatalf("seeded profiles: %d, %v", len(seeded), err)
	}
	libraries, _ := d.AllLibraries(ctx)
	labels, _ := d.Tags(ctx, TagLabel)
	age := 13
	child := SeedProfile{Name: fmt.Sprint("child-", time.Now().UnixNano()), MaxRatingAge: &age, BlockedTags: []string{labels[0].PublicID}}
	for _, lib := range libraries {
		if lib.Kind != LibraryBooks {
			child.Libraries = append(child.Libraries, lib.ID)
		}
	}
	combined, err := d.CreateSeedProfile(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	// The seeder makes ten profiles and restricts the last three, by label, library and rating.
	viewers := []struct {
		name string
		v    Viewer
	}{{"unrestricted", seeded[0]}, {"label", seeded[7]}, {"library", seeded[8]}, {"rating", seeded[9]}, {"all three", combined}}

	const runs = 15
	timed := func(fn func()) (median, slowest float64) {
		var took []time.Duration
		for range runs {
			start := time.Now()
			fn()
			took = append(took, time.Since(start))
		}
		slices.Sort(took)
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		return ms(took[runs/2]), ms(took[runs-1])
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	line := func(text, who, what string, median, slowest float64, note string) {
		t.Logf("SEARCH COST %-10q %-13s %-34s median %9.2f ms  slowest %9.2f ms  %s", text, who, what, median, slowest, note)
	}

	for _, text := range []string{"t", "s", "the", "a", "ka", "the ka", "kalo"} {
		terms, err := parseSearch(text)
		must(err)
		for _, viewer := range viewers {
			open := func() (*searcher, func()) {
				s, done, err := d.searcher(ctx, viewer.v, terms)
				must(err)
				return s, done
			}
			// The whole request, as an app sends it.
			var matched int
			median, slowest := timed(func() {
				found, err := d.Search(ctx, viewer.v, SearchQuery{Text: text})
				must(err)
				matched = 0
				for _, g := range found.Groups {
					matched += g.Total
				}
			})
			line(text, viewer.name, "every kind, whole request", median, slowest, fmt.Sprint("total ", matched))
			median, slowest = timed(func() {
				_, err := d.Search(ctx, viewer.v, SearchQuery{Text: text, Kind: "episode"})
				must(err)
			})
			line(text, viewer.name, "episodes first page, whole request", median, slowest, "")
			median, slowest = timed(func() {
				_, err := d.SearchSuggestions(ctx, viewer.v, text, 8)
				must(err)
			})
			line(text, viewer.name, "suggestions", median, slowest, "")

			// Its parts.
			var totals map[int64]searchTotal
			median, slowest = timed(func() {
				s, done := open()
				defer done()
				totals, err = s.totals()
				must(err)
			})
			capped := ""
			for _, total := range totals {
				if total.minimum {
					capped = "some totals are minimums"
				}
			}
			line(text, viewer.name, fmt.Sprint("totals, cap ", searchCheckCap), median, slowest, capped)

			var shown []int64
			median, slowest = timed(func() {
				s, done := open()
				defer done()
				shown = shown[:0]
				for _, g := range searchGroups {
					rows, _, err := s.best(s.match.tiers, g.code, searchCursor{}, searchGroupSize, -1)
					must(err)
					shown = append(shown, rows...)
				}
			})
			line(text, viewer.name, "first pages of every kind", median, slowest, fmt.Sprint(len(shown), " rows"))
			var items map[int64]Item
			median, slowest = timed(func() {
				s, done := open()
				defer done()
				items, _, err = s.load(shown)
				must(err)
			})
			line(text, viewer.name, "loading those rows", median, slowest, "")
			median, slowest = timed(func() {
				s, done := open()
				defer done()
				list := make([]Item, 0, len(items))
				for _, item := range items {
					list = append(list, item)
				}
				must(loadLeafCounts(ctx, s.tx, s.v, s.acc, list))
			})
			line(text, viewer.name, "of which counting what holders hold", median, slowest, "")
			if viewer.name == "unrestricted" {
				continue
			}

			// An exact count the plain way: every match checked against main.db.
			var items0, people0 int
			exact := map[int64]int{}
			checkAll := func(people bool) {
				s, done := open()
				defer done()
				for _, g := range searchGroups {
					if (g.code == searchPeople) != people {
						continue
					}
					for after := int64(0); ; {
						rows, err := s.candidates(s.match.all, g.code, after, 20000)
						must(err)
						if len(rows) == 0 {
							break
						}
						after = rows[len(rows)-1]
						seen, err := s.check(rows)
						must(err)
						exact[g.code] += len(seen)
						if people {
							people0 += len(rows)
						} else {
							items0 += len(rows)
						}
					}
				}
			}
			median, slowest = timed(func() { items0 = 0; clear(exact); checkAll(false) })
			itemsExact := 0
			for _, count := range exact {
				itemsExact += count
			}
			line(text, viewer.name, "exact count of titles, every match", median, slowest, fmt.Sprintf("%d of %d matches seen", itemsExact, items0))
			median, slowest = timed(func() { people0 = 0; clear(exact); checkAll(true) })
			line(text, viewer.name, "exact count of people, every match", median, slowest, fmt.Sprintf("%d of %d matches seen", exact[searchPeople], people0))

			// An exact count of titles from the index side: narrowed to the libraries and
			// ratings the profile may see by the facts in the index, less what its blocked
			// labels hide, found from the labels' side in main.db.
			s, done := open()
			acc := s.acc
			done()
			narrowed := s.match.all
			if !acc.allLibraries {
				keys := make([]string, len(acc.libraries))
				for n, id := range acc.libraries {
					keys[n] = `"` + searchLibrary(id) + `"`
				}
				narrowed += " AND (" + strings.Join(keys, " OR ") + ")"
			}
			if acc.maxAge != nil {
				var keys []string
				for age := 0; age <= *acc.maxAge; age++ {
					keys = append(keys, `"`+searchRating(int64(age))+`"`)
				}
				narrowed += " AND (" + strings.Join(keys, " OR ") + ")"
			}
			var fromIndex int
			median, slowest = timed(func() {
				s, done := open()
				defer done()
				fromIndex = 0
				rows, err := s.index.QueryContext(ctx, `SELECT rowid >> ?, count(*) FROM search_titles WHERE search_titles MATCH ? AND rowid >> ? != ? GROUP BY 1`,
					searchIDBits, narrowed, searchIDBits, searchPeople)
				must(err)
				for rows.Next() {
					var code int64
					var count int
					must(rows.Scan(&code, &count))
					fromIndex += count
				}
				must(rows.Close())
				if len(acc.blockedTags) == 0 {
					return
				}
				tags := intList(acc.blockedTags)
				hidden, err := ids(ctx, s.tx, `SELECT item_id FROM item_tags WHERE tag_id IN `+tags+`
					UNION SELECT c.id FROM item_tags h CROSS JOIN items c WHERE h.tag_id IN `+tags+` AND c.parent_id = h.item_id
					UNION SELECT c.id FROM item_tags h CROSS JOIN items c WHERE h.tag_id IN `+tags+` AND c.root_id = h.item_id`)
				must(err)
				var lost int
				must(s.index.QueryRowContext(ctx, `SELECT count(*) FROM search_titles WHERE search_titles MATCH ? AND rowid >> ? != ?
					AND (rowid & ?) IN (SELECT value FROM json_each(?))`, narrowed, searchIDBits, searchPeople, int64(1)<<searchIDBits-1, idsJSON(hidden)).Scan(&lost))
				fromIndex -= lost
			})
			agrees := "agrees with the plain count"
			if fromIndex != itemsExact {
				agrees = "DISAGREES with the plain count of " + strconv.Itoa(itemsExact)
			}
			line(text, viewer.name, "exact count of titles, index side", median, slowest, fmt.Sprintf("%d, %s", fromIndex, agrees))
		}
	}
}
