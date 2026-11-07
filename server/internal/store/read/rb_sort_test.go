package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// BEGIN_RB_SCALAR_JOURNAL
func newRBScalarSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET year=id%3,duration_key=id%3,community_rating_key=id%3,critic_rating_key=id%3,file_size_key=id%3,release_key=CAST(id%3 AS TEXT)`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`)
	// A minimal real hierarchy supplies kind-specific cohorts; no FK bypass.
	for _, kind := range []int64{2, 4, 5, 6, 7} {
		for value := int64(0); value < 3; value++ {
			id := int64(2000) + kind*10 + value
			public := make([]byte, 16)
			binary.LittleEndian.PutUint64(public, uint64(id))
			f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
			var show, artist, album, numbering, episode any
			switch kind {
			case 4:
				show = int64(2020)
				numbering = "seasonal"
				episode = value
			case 6:
				artist = int64(2050)
			case 7:
				album = int64(2060)
			}
			f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,year,duration_key,community_rating_key,critic_rating_key,file_size_key,episode_count,latest_aired_key,release_key,track_key,show_id,artist_id,album_id,numbering,episode_number) VALUES(?,?,1,?,'Source','source','s','source','s',0,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, public, kind, public, public, value, float64(value), float64(value), float64(value), value, value, fmt.Sprint(value), fmt.Sprint(value), value, show, artist, album, numbering, episode)
		}
	}
	return f
}

// END_RB_SCALAR_JOURNAL

func rbScalarFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_sort_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_SCALAR_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_SCALAR_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal missing")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func newRBScalarFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBScalarSource(t), RBBrowseDefinitions(), governor.Config{Now: time.Now, MaxGrantsPerClass: 2, Partitions: [4]governor.Amounts{governor.Foreground: {governor.Reader: 1, governor.MemoryBytes: 20 << 20}}}, true, rbScalarFixtureHash(t))
}
func (f *browseFixture) scalar(t *testing.T, in ScalarInput) (ScalarPage, BrowseReadResult, error) {
	t.Helper()
	request := governor.Request{Key: governor.JobKey{Domain: 93, ID: "scalar"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 100, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	return f.reader.sortedWindow(t.Context(), request, time.Second, f.output, in)
}

func TestRBScalarCapturePopulations(t *testing.T) {
	f := newRBScalarSource(t)
	defs := rbScalarDefinitions()
	if len(defs) != 60 {
		t.Fatal("statement inventory", len(defs))
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
				t.Fatal(d.ID, cohort.Name, "population", present, err)
			}
		}
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, rbScalarFixtureHash(t))
	}
}

func TestRBScalarTraversalIndependentOrder(t *testing.T) {
	f := newRBScalarFixture(t)
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	// Expected keys are reconstructed from the known seed journal, not from
	// the production scalar statements or a mirrored SQL ORDER BY.
	for _, spec := range rbScalarOrders() {
		for _, desc := range []bool{false, true} {
			t.Run(string(spec.id)+fmt.Sprint(desc), func(t *testing.T) {
				in := ScalarInput{View: f.input, Sort: spec.id}
				in.View.Kind = spec.kind
				in.View.Descending = desc
				var expected []int64
				if spec.kind == 1 {
					for id := int64(1); id <= 1100; id++ {
						if id%9 != 0 && id != 1040 {
							expected = append(expected, id)
						}
					}
				} else {
					for n := int64(0); n < 3; n++ {
						expected = append(expected, 2000+spec.kind*10+n)
					}
				}
				slices.SortFunc(expected, func(a, b int64) int {
					ka, kb := a%3, b%3
					if spec.kind != 1 {
						ka, kb = a-(2000+spec.kind*10), b-(2000+spec.kind*10)
					}
					result := 0
					if ka < kb {
						result = -1
					} else if ka > kb {
						result = 1
					} else if a < b {
						result = -1
					} else if a > b {
						result = 1
					}
					if desc {
						return -result
					}
					return result
				})
				var got []int64
				for n := 0; n < 40; n++ {
					page, cost, err := f.scalar(t, in)
					if err != nil {
						t.Fatal(err)
					}
					if page.RawRows > MaxRaw || page.RawPositions > MaxRaw || len(page.Items) > MaxPage || page.Statements != cost.LookupStatements+cost.PublicationStatements || len(cost.LookupWindows) != 1 || len(cost.PublicationWindows) != 1 {
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
						t.Fatal("lost raw continuation")
					}
					in.After = page.Next
				}
				if !slices.Equal(got, expected) {
					t.Fatalf("traversal got%d want%d; head%v", len(got), len(expected), got[:min(5, len(got))])
				}
			})
		}
	}
}

func TestRBScalarCursorRefusalsAndRawProgress(t *testing.T) {
	f := newRBScalarFixture(t)
	in := ScalarInput{View: f.input, Sort: SortYear}
	page, _, err := f.scalar(t, in)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	for _, change := range []func(*ScalarInput){func(v *ScalarInput) { v.Sort = SortDuration }, func(v *ScalarInput) { v.View.Descending = true }, func(v *ScalarInput) { v.View.Database = "new" }, func(v *ScalarInput) { v.View.KeepArticles = true }} {
		next := in
		next.After = page.Next
		change(&next)
		p, _, err := f.scalar(t, next)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(p, ScalarPage{}) {
			t.Fatal("query cursor leaked", p, err)
		}
	}
	f.exec(t, `UPDATE libraries SET data_order_revision=data_order_revision+1 WHERE id='movies'`)
	in.After = page.Next
	if p, _, err := f.scalar(t, in); !errors.Is(err, ErrStale) || len(p.Items) != 0 {
		t.Fatal(p, err)
	}
	in.After = nil
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1 AND kind=1`)
	page, _, err = f.scalar(t, in)
	if err != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatal("hidden window became complete empty result", page, err)
	}
	in.After = page.Next
	next, _, err := f.scalar(t, in)
	if err != nil || next.RawPositions != MaxRaw || next.Next == nil || (next.Next.key == page.Next.key && next.Next.id <= page.Next.id) {
		t.Fatal("raw continuation did not advance", next, err)
	}
}

func TestRBScalarOwnerAndUnsupportedPolicy(t *testing.T) {
	f := newRBScalarFixture(t)
	in := ScalarInput{View: f.input, Sort: SortFileSize}
	if p, _, err := f.scalar(t, in); !errors.Is(err, ErrDenied) || len(p.Items) != 0 {
		t.Fatal("owner-only source sort", p, err)
	}
	in.Sort = SortEpisodeCount
	if _, _, err := f.scalar(t, in); !errors.Is(err, ErrUnqualified) {
		t.Fatal("unsupported kind", err)
	}
	in.Sort = "duration; DROP TABLE catalog_entities"
	if _, cost, err := f.scalar(t, in); !errors.Is(err, ErrInput) || cost.LookupStatements != 0 {
		t.Fatal("request SQL shape", err)
	}
	in.Sort = SortYear
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('p','["blocked"]')`)
	if p, _, err := f.scalar(t, in); !errors.Is(err, ErrUnqualified) || len(p.Items) != 0 {
		t.Fatal("unsupported labels", p, err)
	}
}

func TestRBScalarPublicLIVE(t *testing.T) {
	source := newRBScalarSource(t)
	f := composeReadFixture(t, source, RBBrowseDefinitions(), readfixture.LiveConfig(time.Now), false, rbScalarFixtureHash(t))
	q, request, output := liveRequests("scalar")
	in := ScalarInput{View: f.input, Sort: SortYear}
	if p, cost, err := f.reader.SortedWindow(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) || len(p.Items) != 0 || cost.LookupStatements != 0 {
		t.Fatal("bypassed LIVE", err)
	}
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, cost, err := f.reader.SortedWindow(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if len(page.Items) != 40 || cost.AdmissionElapsed <= 0 {
			t.Fatal(page, cost)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
