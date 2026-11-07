package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// BEGIN_RB_RANDOM_JOURNAL
func newRBRandomSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET random_key=(id%4)*1431655765`)
	return f
}

// END_RB_RANDOM_JOURNAL
func rbRandomFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_random_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_RANDOM_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_RANDOM_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func newRBRandomFixture(t *testing.T) *browseFixture {
	t.Helper()
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	cfg.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	return composeReadFixture(t, newRBRandomSource(t), RBRandomDefinitions(), cfg, true, rbRandomFixtureHash(t))
}
func (f *browseFixture) random(t *testing.T, in RandomInput) (RandomPage, BrowseReadResult, error) {
	t.Helper()
	request := governor.Request{Key: governor.JobKey{Domain: 95, ID: "random"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 100, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	return f.reader.randomWindow(t.Context(), request, time.Second, f.output, in)
}
func TestRBRandomCapturePopulations(t *testing.T) {
	f := newRBRandomSource(t)
	defs := rbRandomDefinitions()
	if len(defs) != 10 {
		t.Fatal("inventory", len(defs))
	}
	for _, d := range defs {
		for _, cohort := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (cohort.Name == "present") {
				t.Fatal(d.ID, cohort.Name, present, err)
			}
		}
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, rbRandomFixtureHash(t))
	}
}
func TestRBRandomTraversalRotationAndBoundarySeeds(t *testing.T) {
	f := newRBRandomFixture(t)
	for _, seed := range []uint32{0, 2000000000, ^uint32(0)} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprint(seed, desc), func(t *testing.T) {
				in := RandomInput{View: f.input, Seed: seed}
				in.View.Descending = desc
				var want []int64
				for id := int64(1); id <= 1100; id++ {
					if id%9 != 0 && id != 1040 {
						want = append(want, id)
					}
				}
				key := func(id int64) int64 { return id % 4 * 1431655765 }
				phase := func(id int64) int {
					if !desc && key(id) < int64(seed) || desc && key(id) > int64(seed) {
						return 1
					}
					return 0
				}
				slices.SortFunc(want, func(a, b int64) int {
					if phase(a) < phase(b) {
						return -1
					}
					if phase(a) > phase(b) {
						return 1
					}
					v := 0
					if key(a) < key(b) {
						v = -1
					} else if key(a) > key(b) {
						v = 1
					} else if a < b {
						v = -1
					} else if a > b {
						v = 1
					}
					if desc {
						return -v
					}
					return v
				})
				var got []int64
				for n := 0; n < 40; n++ {
					page, cost, err := f.random(t, in)
					if err != nil {
						t.Fatal(err)
					}
					if page.RawRows > MaxRaw || page.RawPositions > MaxRaw || len(page.Items) > MaxPage || cost.LookupStatements > 8 || page.Statements > 13 {
						t.Fatal("bounds/evidence", page, cost)
					}
					for _, item := range page.Items {
						got = append(got, item.ID)
					}
					if page.Exhausted {
						if page.Next != nil {
							t.Fatal("exhausted cursor")
						}
						break
					}
					if page.Next == nil {
						t.Fatal("lost continuation")
					}
					in.After = page.Next
				}
				if !slices.Equal(got, want) {
					t.Fatalf("rotation got%d want%d head%v", len(got), len(want), got[:min(5, len(got))])
				}
			})
		}
	}
}
func TestRBRandomCursorAndRawRejection(t *testing.T) {
	f := newRBRandomFixture(t)
	in := RandomInput{View: f.input, Seed: 2000000000}
	page, _, err := f.random(t, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	next := in
	next.Seed++
	next.After = page.Next
	if p, _, err := f.random(t, next); !errors.Is(err, ErrStale) || len(p.Items) != 0 {
		t.Fatal("seed cursor not bound", p, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1`)
	page, _, err = f.random(t, in)
	if err != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatal("raw hidden run truncated", page, err)
	}
	in.After = page.Next
	p, _, err := f.random(t, in)
	if err != nil || p.Next == nil || p.Next.id <= page.Next.id {
		t.Fatal(p, err)
	}
}
func TestRBRandomPublicLIVE(t *testing.T) {
	f := composeReadFixture(t, newRBRandomSource(t), RBRandomDefinitions(), readfixture.LiveConfig(time.Now), false, rbRandomFixtureHash(t))
	q, request, output := liveRequests("random")
	in := RandomInput{View: f.input, Seed: 2000000000}
	if p, cost, err := f.reader.RandomWindow(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) || len(p.Items) != 0 || cost.LookupStatements != 0 {
		t.Fatal("bypassed LIVE", err)
	}
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := f.reader.RandomWindow(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if len(page.Items) != 40 {
			t.Fatal(page)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
