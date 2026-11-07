package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_RESOLUTION_JOURNAL
func newRBResolutionSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET resolution_key=CASE WHEN id%5=0 THEN NULL WHEN id%5=1 THEN 0 WHEN id%5=2 THEN 720 WHEN id%5=3 THEN 1080 ELSE 2160 END`)
	return f
}

// END_RB_RESOLUTION_JOURNAL

func rbResolutionFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_resolution_test.go")
	if e != nil {
		t.Fatal(e)
	}
	start := strings.Index(string(data), "// BEGIN_RB_RESOLUTION_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_RESOLUTION_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("source journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func rbResolutionFixture(t *testing.T, live bool) *browseFixture {
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	cfg.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	if live {
		cfg = readfixture.LiveConfig(time.Now)
	}
	return composeReadFixture(t, newRBResolutionSource(t), RBResolutionDefinitions(), cfg, !live, rbResolutionFixtureHash(t))
}
func rbResolutionRequest() governor.Request {
	r := readfixture.ReadRequest()
	r.Budget.RawRows = 2*MaxRaw + 5
	r.Budget.VMOps = 100000
	return r
}
func TestRBResolutionNullZeroTraversal(t *testing.T) {
	synctest.Test(t, testRBResolutionNullZeroTraversal)
}
func testRBResolutionNullZeroTraversal(t *testing.T) {
	f := rbResolutionFixture(t, false)
	for _, desc := range []bool{false, true} {
		in := ResolutionInput{View: f.input}
		in.View.Limit = 17
		in.View.Descending = desc
		var got, want []int64
		for n := 0; ; n++ {
			if n > 100 {
				t.Fatal("unending cursor")
			}
			page, cost, e := f.reader.resolutionWindow(t.Context(), rbResolutionRequest(), time.Second, f.output, in)
			if e != nil {
				t.Fatal(e)
			}
			if page.RawRows > 2*MaxRaw || page.RawPositions > MaxRaw || page.RawPositions > page.RawRows || cost.LookupStatements > 8 || cost.PublicationStatements != 5 {
				t.Fatal("unbounded resolution merge", page, cost)
			}
			for _, item := range page.Items {
				got = append(got, item.ID)
			}
			if page.Exhausted {
				if page.Next != nil {
					t.Fatal("terminal cursor")
				}
				break
			}
			if page.Next == nil {
				t.Fatal("lost raw cursor")
			}
			in.After = page.Next
		}
		for _, fact := range f.facts {
			if !fact.retired && fact.id != 1040 {
				want = append(want, fact.id)
			}
		}
		key := func(id int64) int64 {
			switch id % 5 {
			case 2:
				return 720
			case 3:
				return 1080
			case 4:
				return 2160
			}
			return 0
		}
		sort.Slice(want, func(i, j int) bool {
			a, b := key(want[i]), key(want[j])
			if a == b {
				return want[i] < want[j]
			}
			return a < b
		})
		if desc {
			slices.Reverse(want)
		}
		if !slices.Equal(got, want) {
			t.Fatal("NULL/zero tie order or traversal differs", desc, got, want)
		}
	}
}
func TestRBResolutionRawHiddenAndFence(t *testing.T) {
	synctest.Test(t, testRBResolutionRawHiddenAndFence)
}
func testRBResolutionRawHiddenAndFence(t *testing.T) {
	f := rbResolutionFixture(t, false)
	f.exec(t, `UPDATE catalog_entities SET retired=1`)
	in := ResolutionInput{View: f.input}
	page, _, e := f.reader.resolutionWindow(t.Context(), rbResolutionRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatal("rejected rows lost continuation", page, e)
	}
	in.After = page.Next
	f.exec(t, `UPDATE libraries SET data_order_revision=data_order_revision+1 WHERE id='movies'`)
	page, _, e = f.reader.resolutionWindow(t.Context(), rbResolutionRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || len(page.Items) != 0 || page.Next != nil {
		t.Fatal("stale cursor output", page, e)
	}
}
func TestRBResolutionLIVEAndCurrentDenial(t *testing.T) {
	synctest.Test(t, testRBResolutionLIVEAndCurrentDenial)
}
func testRBResolutionLIVEAndCurrentDenial(t *testing.T) {
	f := rbResolutionFixture(t, true)
	in := ResolutionInput{View: f.input}
	q, request, output := liveRequests("resolution-ready")
	request.Budget.RawRows = 2*MaxRaw + 5
	if _, _, e := f.reader.ResolutionWindow(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(e, ErrInput) {
		t.Fatal("LIVE bypass", e)
	}
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, cost, e := f.reader.ResolutionWindow(ctx, request, time.Second, out, in)
		if e == nil && (len(page.Items) != in.View.Limit) {
			t.Fatal("LIVE evidence", page, cost)
		}
		assertLiveUsage(t, f, 1, 0)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	q, request, output = liveRequests("resolution-denied")
	request.Budget.RawRows = 2*MaxRaw + 5
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, e := f.reader.ResolutionWindow(ctx, request, time.Second, out, in)
		if len(page.Items) != 0 || page.Next != nil {
			t.Fatal("revoked result escaped", page)
		}
		return e
	})
	if !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}
