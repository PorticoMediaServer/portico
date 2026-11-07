package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

// BEGIN_RB_CHRONOLOGY_JOURNAL
func newRBChronologySource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET added_at=CASE id%4 WHEN 0 THEN NULL ELSE printf('2026-%02d-01T00:00:00.000000000Z',id%4) END,last_content_added_at=CASE id%4 WHEN 0 THEN NULL ELSE printf('2026-%02d-01T00:00:00.000000000Z',4-id%4) END`)
	for n := int64(0); n < 4; n++ {
		id := int64(2000) + n
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		var added, content any
		if n > 0 {
			added = "2026-01-01T00:00:00.000000000Z"
			content = fmt.Sprintf("2026-%02d-01T00:00:00.000000000Z", 4-n)
		}
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,2)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,added_at,last_content_added_at) VALUES(?,?,1,2,'Show','show','s','show','s',0,?,?,?,?)`, id, public, public, public, added, content)
	}
	return f
}

// END_RB_CHRONOLOGY_JOURNAL

func rbChronologyFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_chronology_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_CHRONOLOGY_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_CHRONOLOGY_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func newRBChronologyFixture(t *testing.T) *browseFixture {
	t.Helper()
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	cfg.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	return composeReadFixture(t, newRBChronologySource(t), RBChronologyDefinitions(), cfg, true, rbChronologyFixtureHash(t))
}
func (f *browseFixture) chronology(t *testing.T, in ChronologyInput) (ChronologyPage, BrowseReadResult, error) {
	t.Helper()
	request := governor.Request{Key: governor.JobKey{Domain: 94, ID: "chronology"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 100, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	return f.reader.chronologyWindow(t.Context(), request, time.Second, f.output, in)
}
func TestRBChronologyCapturePopulations(t *testing.T) {
	f := newRBChronologySource(t)
	defs := rbChronologyDefinitions()
	if len(defs) != 24 {
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
		captureBrowse(t, f, defs, stats, rbChronologyFixtureHash(t))
	}
}
func TestRBChronologyTraversalNullAndContentOrder(t *testing.T) {
	f := newRBChronologyFixture(t)
	for _, kind := range []int64{1, 2} {
		for _, clock := range []Chronology{DateAdded, ContentAdded} {
			for _, desc := range []bool{false, true} {
				t.Run(fmt.Sprint(kind, clock, desc), func(t *testing.T) {
					in := ChronologyInput{View: f.input, Clock: clock}
					in.View.Kind = kind
					in.View.Descending = desc
					var want []int64
					if kind == 1 {
						for id := int64(1); id <= 1100; id++ {
							if id%9 != 0 && id != 1040 {
								want = append(want, id)
							}
						}
					} else {
						want = []int64{2000, 2001, 2002, 2003}
					}
					key := func(id int64) int64 {
						v := id % 4
						if kind == 2 && v != 0 {
							if clock == ContentAdded {
								v = 4 - v
							} else {
								v = 1
							}
						}
						return v
					}
					slices.SortFunc(want, func(a, b int64) int {
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
						page, cost, err := f.chronology(t, in)
						if err != nil {
							t.Fatal(err)
						}
						if page.RawRows > MaxRaw || page.RawPositions > MaxRaw || len(page.Items) > MaxPage || cost.LookupStatements > 8 || page.Statements > 13 {
							t.Fatal("quantum/evidence", page, cost)
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
						t.Fatalf("order got%d want%d head%v", len(got), len(want), got[:min(5, len(got))])
					}
				})
			}
		}
	}
}
func TestRBChronologyCursorAndRawBounds(t *testing.T) {
	f := newRBChronologyFixture(t)
	in := ChronologyInput{View: f.input, Clock: DateAdded}
	page, _, err := f.chronology(t, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	next := in
	next.Clock = ContentAdded
	next.After = page.Next
	if p, _, err := f.chronology(t, next); !errors.Is(err, ErrStale) || len(p.Items) != 0 {
		t.Fatal("clock cursor not bound", p, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=1 AND library_id=1`)
	page, _, err = f.chronology(t, in)
	if err != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatal("raw null bucket lost continuation", page, err)
	}
	in.After = page.Next
	p, _, err := f.chronology(t, in)
	if err != nil || p.Next == nil || p.Next.id <= page.Next.id {
		t.Fatal(p, err)
	}
}
func TestRBChronologyPublicLIVE(t *testing.T) {
	f := composeReadFixture(t, newRBChronologySource(t), RBChronologyDefinitions(), readfixture.LiveConfig(time.Now), false, rbChronologyFixtureHash(t))
	q, request, output := liveRequests("chronology")
	in := ChronologyInput{View: f.input, Clock: DateAdded}
	if p, cost, err := f.reader.ChronologyWindow(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) || len(p.Items) != 0 || cost.LookupStatements != 0 {
		t.Fatal("bypassed LIVE", err)
	}
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := f.reader.ChronologyWindow(ctx, request, time.Second, out, in)
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
