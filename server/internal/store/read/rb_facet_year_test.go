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
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_EXACT_FACET_JOURNAL
func newRBExactFacetSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET year=CASE WHEN id%4=0 THEN 0 ELSE 2000+id%3 END`)
	return f
}

// END_RB_EXACT_FACET_JOURNAL
func rbExactFacetFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_facet_year_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_EXACT_FACET_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_EXACT_FACET_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func rbExactFacetConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate PHASE2 logical test profile. Prep16MiB is included in non-LIVE
	// demand; M grows20MiB to fund it plus its20% contingency. The four-request
	// LIVE capacity is preserved, and its foreground partition gains16MiB.
	// This arithmetic is not a whole-server/2GB physical qualification.
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 16 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}
func newRBExactFacetFixture(t *testing.T) (*browseFixture, *ExactFacetReader) {
	t.Helper()
	f := composeReadFixture(t, newRBExactFacetSource(t), RBExactFacetDefinitions(), rbExactFacetConfig(), false, rbExactFacetFixtureHash(t))
	r, err := NewExactFacetReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func rbFacetReadRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 398, ID: "exact-numeric-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: rbFacetRaw + 10, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}
func rbFacetPublicationRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 397, ID: "facet-test-discretionary-publication"}, Owner: 2, Class: governor.Background, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: time.Now().Add(time.Second), Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Footprint: []governor.Scope{{Library: 1}}}
}
func TestRBExactNumericFacetNativeHandoff(t *testing.T) {
	synctest.Test(t, testRBExactNumericFacetNativeHandoff)
}
func testRBExactNumericFacetNativeHandoff(t *testing.T) {
	f, r := newRBExactFacetFixture(t)
	job, err := r.BeginNumericFacets(t.Context(), 1, 16<<20, f.input, ExactYear)
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	first, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
	if err != nil || first.Ready || first.RawRows != rbFacetRaw {
		t.Fatal("first full quantum became ready", first, err)
	}
	last, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
	if err != nil || !last.Ready || last.RawRows >= rbFacetRaw {
		t.Fatal("exhausted preparation did not become ready", last, err)
	}
	if d := f.governor.Try(rbFacetPublicationRequest()); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
		t.Fatal("ready-before-poll publication bypass", d)
	}
	var expected []NumericFacetValue
	counts := map[int64]int64{}
	for id := int64(1); id <= 1100; id++ {
		if id%9 != 0 && id != 1040 && id%4 != 0 {
			counts[2000+id%3]++
		}
	}
	var keys []int64
	for key := range counts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		expected = append(expected, NumericFacetValue{key, counts[key]})
	}
	var got []NumericFacetValue
	var after *NumericFacetCursor
	for pageNumber := 0; pageNumber < 4; pageNumber++ {
		q, request, output := liveRequests(fmt.Sprint("numeric-ready-", pageNumber))
		var next *NumericFacetCursor
		var exhausted bool
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			page, cost, e := job.Page(ctx, request, time.Second, out, f.input, after, 1)
			if e != nil {
				return e
			}
			if cost.PublicationStatements != 5 || page.Statements != 5 {
				t.Fatal("ready evidence", page, cost)
			}
			assertLiveUsage(t, f, 1, 0)
			got = append(got, page.Values...)
			next = page.After
			exhausted = page.Exhausted
			if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] < 16<<20 {
				t.Fatal("cache bytes refunded during consumer")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if exhausted {
			break
		}
		if next == nil {
			t.Fatal("lost exact facet continuation")
		}
		after = next
	}
	if !slices.Equal(got, expected) {
		t.Fatal("facet count mismatch", got, expected)
	}
	d := f.governor.Try(rbFacetPublicationRequest())
	if d.Grant == nil {
		t.Fatal("publication did not resume", d.Refusal)
	}
	if err := f.authority.Retired(d.Grant); err != nil {
		t.Fatal(err)
	}
}
func TestRBExactNumericFacetRevocationAndMovingSource(t *testing.T) {
	for _, mode := range []string{"denied", "source"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := newRBExactFacetFixture(t)
				job, err := r.BeginNumericFacets(t.Context(), 1, 16<<20, f.input, ExactDecade)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				for n := 0; n < 3; n++ {
					state, e := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
					if e != nil {
						t.Fatal(e)
					}
					if state.Ready {
						break
					}
				}
				want := ErrStale
				if mode == "denied" {
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
					want = ErrDenied
				} else {
					f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
				}
				q, request, output := liveRequests(mode)
				_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
					page, _, e := job.Page(ctx, request, time.Second, out, f.input, nil, 200)
					if len(page.Values) != 0 || page.ValueMemberships != 0 {
						t.Fatal("invalid prepared values escaped", page)
					}
					return e
				})
				if !errors.Is(err, want) {
					t.Fatal(err, want)
				}
				if !job.entry.closed {
					t.Fatal("invalid prepared job retained its lease")
				}
			})
		})
	}
}
