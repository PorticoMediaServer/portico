package writer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// B-READS budgets for the complete search route: 200ms ready p95 at 1M is the
// formal target; 50ms first-ready is the working target. Neither is a
// qualification verdict for this component cell.
const (
	searchFormalReadyP95 = 200 * time.Millisecond
	searchWorkingReady   = 50 * time.Millisecond
)

// ec2FullSearchBinding composes RD's native reader with the common foreground
// lifecycle. The query and preview limits are the owning recipe's; this driver
// adds no SQL, bank, plan or cursor.
func ec2FullSearchBinding(t *testing.T, c *writer.N1PairedComponent, term string) workload.Action {
	t.Helper()
	reader, err := read.NewNativeFullSearchReader(c.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	query, err := search.PlanQuery(term, false)
	if err != nil {
		t.Fatal(err)
	}
	return workload.FullSearch(reader, c.Governor, c.Authority, c.Snapshots, func(_ context.Context, offer workload.Offer) (workload.FullSearchRequest, error) {
		live, request, output := readfixture.Requests(fmt.Sprintf("%s/full-search/%d", c.DatabaseID, offer.Slot), offer.ScheduledAt)
		request.Budget.RawRows = 4096
		view := read.BrowseInput{Database: c.DatabaseID, TokenHash: "token", Library: c.View.Library, Kind: 0, Limit: 20, Now: offer.ScheduledAt}
		return workload.FullSearchRequest{
			Request: request, Budget: time.Second, Foreground: live, OutputRequest: output,
			View:    read.FullSearchInput{View: view, Preview: true, CandidateBudget: read.NativePreviewCandidates},
			Query:   query,
			Consume: encodeFullSearch,
		}, nil
	})
}

func encodeFullSearch(ctx context.Context, page read.FullSearchPage) error {
	summary := struct {
		Hits       int
		Candidates int
		Exhausted  bool
		Partial    bool
	}{len(page.Hits), page.NativeCandidates, page.Exhausted, page.Partial}
	return errors.Join(json.NewEncoder(io.Discard).Encode(summary), ctx.Err())
}

type searchReadyDistribution struct {
	Count               int
	P50NS, P99NS, MaxNS time.Duration
}

func searchReadyStats(xs []time.Duration) searchReadyDistribution {
	if len(xs) == 0 {
		return searchReadyDistribution{}
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	rank99 := (99*len(xs) + 99) / 100
	return searchReadyDistribution{len(xs), xs[(len(xs)+1)/2-1], xs[rank99-1], xs[len(xs)-1]}
}

type searchReadReport struct {
	Method                  string
	Offers                  int
	WithHits                int
	Stamped                 int
	Unavailable             int
	Partial                 int
	Errors                  int
	Ready                   searchReadyDistribution
	FormalP95NS             time.Duration
	WorkingNS               time.Duration
	ObservedP99WithinFormal *bool
	Qualification           bool
	Scope                   string
}

func (s *pairedSink) searchReadReport() searchReadReport {
	r := searchReadReport{Method: "ec2-full-search-read-v1", FormalP95NS: searchFormalReadyP95, WorkingNS: searchWorkingReady, Scope: "independent search worker well below browse/detail; ReadyNS is the whole returned read including LIVE admission, both windows and joined consumer; directional component evidence only", Qualification: false}
	var ready []time.Duration
	for _, o := range s.search {
		r.Offers++
		if o.CallError != "" {
			r.Errors++
			continue
		}
		if o.ReadyNS != nil {
			ready = append(ready, *o.ReadyNS)
		}
		if out, ok := o.Result.(workload.ForegroundResult[workload.FullSearchSummary]); ok && out.Summary != nil {
			if out.Summary.Hits > 0 {
				r.WithHits++
			}
			if out.Summary.Stamps {
				r.Stamped++
			}
			if out.Summary.Unavailable {
				r.Unavailable++
			}
			if out.Summary.Partial {
				r.Partial++
			}
		}
	}
	r.Ready = searchReadyStats(ready)
	if r.Ready.Count > 0 {
		ok := r.Ready.P99NS <= searchFormalReadyP95
		r.ObservedP99WithinFormal = &ok
	}
	return r
}

// The positive control seeds one real v1 bank and proves the native reader
// executes, returns hits and joins a stamp. It is functional only.
func TestEC2NativeFullSearchFunctional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search-control.ndjson")
	data, _ := json.Marshal(map[string]any{"run_id": "SEARCH_CONTROL", "host_epoch": "CONTROL", "source_commit": strings.Repeat("a", 40), "output_path": path, "fixtures": 1})
	c := writer.NewN1PairedComponent(t, data, 1, "timing")
	// Entity 1 carries the fixture's large credit cohort, which the complete
	// cohort recipe correctly refuses; 1100 has the same title shape with none.
	c.SeedNativeSearchBankForTest(t, 1100, "Title 1100")
	// Complete the measured N1 cell so the shared fixture close has its whole-cell
	// evidence; the search read is the control under test.
	if err := c.RunEC2Original(t, t.Context(), time.Now(), "timing"); err != nil {
		t.Fatal(err)
	}
	action := ec2FullSearchBinding(t, c, "1100")
	var observations []workload.Observation
	if err := action(t.Context(), workload.Offer{Stream: "search", Slot: 1, ScheduledAt: time.Now()}, func(o workload.Observation) error {
		observations = append(observations, o)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].CallError != "" {
		t.Fatal(observations)
	}
	out, ok := observations[0].Result.(workload.ForegroundResult[workload.FullSearchSummary])
	if !ok || out.Summary == nil || out.Summary.Hits == 0 || !out.Summary.Stamps || out.Summary.Unavailable {
		t.Fatal(out)
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
}

// The negative control proves the binding fails closed when the native
// composition (reader/prepare) is absent, and that the constructor refuses a
// nil pool rather than inventing a reader.
func TestEC2FullSearchRefusesWithoutComposition(t *testing.T) {
	if _, err := read.NewNativeFullSearchReader(nil); !errors.Is(err, read.ErrInput) {
		t.Fatal(err)
	}
	var got workload.Observation
	err := workload.FullSearch(nil, nil, nil, nil, nil)(t.Context(), workload.Offer{Stream: "search", Slot: 1}, func(o workload.Observation) error {
		got = o
		return nil
	})
	if err == nil || got.Phase != "PREPARE" || got.CallError == "" || got.Stub {
		t.Fatal(err, got)
	}
}
