package qual

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestP99UpperRank(t *testing.T) {
	// Independent 70-digit decimal binomial recurrence reference values.
	// 298 samples cannot supply 95% confidence, even at the observed maximum.
	for _, tt := range []struct{ n, rank int }{
		{0, 0}, {1, 0}, {298, 0}, {299, 299}, {1000, 996},
		{10_000, 9917}, {100_000, 99052}, {1_000_000, 990164},
	} {
		if got := p99UpperRank(tt.n); got != tt.rank {
			t.Errorf("n=%d: rank=%d, want %d", tt.n, got, tt.rank)
		}
	}
}

func TestSummarizeNearestRankAndRetainedTail(t *testing.T) {
	samples := make([]time.Duration, 1000)
	for i := range samples {
		samples[i] = time.Duration(1000-i) * time.Microsecond
	}
	samples[0] = time.Second
	original := slices.Clone(samples)
	d, err := Summarize(samples)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 1000 || d.P50 != 500*time.Microsecond || d.P95 != 950*time.Microsecond ||
		d.P99 != 990*time.Microsecond || d.P999 != 999*time.Microsecond || d.Max != time.Second {
		t.Fatalf("wrong distribution: %+v", d)
	}
	if d.P99Upper95 == nil || d.P99Upper95.Rank != 996 || d.P99Upper95.Value != 996*time.Microsecond {
		t.Fatalf("wrong bound: %+v", d.P99Upper95)
	}
	if !slices.Equal(original, samples) {
		t.Fatal("summary changed raw samples")
	}
}

func TestSummaryInvalidAndInsufficient(t *testing.T) {
	for _, samples := range [][]time.Duration{nil, {0}, {time.Second, -1}} {
		if _, err := Summarize(samples); err == nil {
			t.Fatalf("accepted invalid population %v", samples)
		}
	}
	d, err := Summarize([]time.Duration{time.Millisecond})
	if err != nil || d.P99Upper95 != nil {
		t.Fatalf("insufficient sample has finite bound: %+v, %v", d, err)
	}
	b, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(b), `"p99_upper_95":null`) {
		t.Fatalf("unavailable bound must serialize as null: %s, %v", b, err)
	}
}

func host(id string, hold time.Duration) HostHolds {
	xs := make([]time.Duration, Phase1HoldsPerHost)
	for i := range xs {
		xs[i] = hold
	}
	return HostHolds{HostID: id, CompleteHolds: xs}
}

func TestHostedHoldsPassKeepsDirectionalMaximum(t *testing.T) {
	hosts := []HostHolds{host("vm-a", time.Millisecond), host("vm-b", 2*time.Millisecond), host("vm-c", 3*time.Millisecond)}
	// One long hosted hold is retained and reported. Hosted max is directional;
	// quiet-machine maximum is an independent gate.
	hosts[0].CompleteHolds[99] = time.Second
	r := EvaluateHostedHolds(hosts)
	if r.Verdict != Pass || r.Split || r.MedianRatio != 3 || r.Hosts[0].Holds.Max != time.Second {
		t.Fatalf("unexpected report: %+v", r)
	}
}

func TestHostedHoldsDoesNotPoolAwaySlowHost(t *testing.T) {
	hosts := []HostHolds{host("a", 8*time.Millisecond), host("b", 8*time.Millisecond), host("c", 8*time.Millisecond)}
	// 1% of the third host exceeds the budget. Pooled across all hosts this
	// would look like 0.33%; the individual upper confidence bound must fail.
	for i := range 100 {
		hosts[2].CompleteHolds[i] = 11 * time.Millisecond
	}
	r := EvaluateHostedHolds(hosts)
	if r.Verdict != Fail || r.Hosts[2].Verdict != Fail || r.Hosts[2].Holds.P99Upper95.Value != 11*time.Millisecond {
		t.Fatalf("slow host lost: %+v", r)
	}
}

func TestHostedHoldsSplitRequiresFreshCohort(t *testing.T) {
	for _, slow := range []time.Duration{4 * time.Millisecond, 40 * time.Millisecond} {
		r := EvaluateHostedHolds([]HostHolds{host("a", time.Millisecond), host("b", time.Millisecond), host("c", slow)})
		if r.Verdict != Inconclusive || !r.Split || len(r.Hosts) != 3 {
			t.Fatalf("fourfold split was waived: %+v", r)
		}
		if slow > WriterP99Budget && r.Hosts[2].Verdict != Fail {
			t.Fatal("split hid the individual failure")
		}
	}
}

func TestHostedHoldsInsufficientSamplesAndVMs(t *testing.T) {
	for _, n := range []int{0, 2, 3} {
		hosts := []HostHolds{host("a", time.Millisecond), host("b", time.Millisecond), host("c", time.Millisecond)}[:n]
		if n == 3 {
			hosts[2].CompleteHolds = hosts[2].CompleteHolds[:9999]
		}
		if r := EvaluateHostedHolds(hosts); r.Verdict != Inconclusive {
			t.Fatalf("incomplete cohort qualified: %+v", r)
		}
	}
}

func TestHostedHoldsRetainsInvalidAndFailedHosts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*HostHolds)
		want   Verdict
	}{
		{"wrong-engine", func(h *HostHolds) { h.ProtocolErrors = []string{"wrong engine"} }, Invalid},
		{"crash", func(h *HostHolds) { h.ProtocolErrors = []string{"crash; raw evidence retained"} }, Invalid},
		{"failed-command", func(h *HostHolds) { h.FailedCommands = 1 }, Fail},
		{"negative-count", func(h *HostHolds) { h.Refusals = -1 }, Invalid},
		{"missing-host", func(h *HostHolds) { h.HostID = "" }, Invalid},
		{"duplicate-host", func(h *HostHolds) { h.HostID = "a" }, Invalid},
		{"zero-hold", func(h *HostHolds) { h.CompleteHolds[0] = 0 }, Invalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hosts := []HostHolds{host("a", time.Millisecond), host("b", time.Millisecond), host("c", time.Millisecond)}
			tt.change(&hosts[2])
			r := EvaluateHostedHolds(hosts)
			if r.Verdict != tt.want || len(r.Hosts) != 3 || r.Hosts[2].Verdict != tt.want || len(r.Hosts[2].Reasons) == 0 {
				t.Fatalf("bad host disappeared or passed: %+v", r)
			}
		})
	}
}

func TestRefusalsAreReportedOutsideHoldPopulation(t *testing.T) {
	hosts := []HostHolds{host("a", time.Millisecond), host("b", time.Millisecond), host("c", time.Millisecond)}
	hosts[0].Refusals = 7
	r := EvaluateHostedHolds(hosts)
	if r.Verdict != Pass || r.Hosts[0].Refusals != 7 || r.Hosts[0].Holds.Count != Phase1HoldsPerHost {
		t.Fatalf("refusal counted as a hold or hidden: %+v", r)
	}
	// This passes only the hold gate; admission/foreground/receipt gates must
	// independently evaluate whether those refusals are legal.
}
