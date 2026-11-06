package workload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

// All actions/evidence here are fabricated controls, never native measurements.
type memorySink struct {
	records []Record
	failAt  int
	notify  chan Record
}

func (s *memorySink) Append(r Record) error {
	if s.failAt > 0 && len(s.records)+1 == s.failAt {
		return errors.New("synthetic disk full")
	}
	s.records = append(s.records, r)
	if s.notify != nil {
		s.notify <- r
	}
	return nil
}
func testConfig() Config {
	return Config{RunID: "control", Duration: 100 * time.Millisecond, Cell: qual.RestartCell{
		ID: "cell", HostID: "synthetic", Profile: "directional-reference", DatabaseID: "one-db", ManifestSHA256: strings.Repeat("a", 64), CacheState: "warm", CadenceMode: "accelerated"}}
}
func control(_ context.Context, _ Offer, emit func(Observation) error) error {
	start := time.Now()
	return emit(Observation{Phase: "CONTROL", CallStarted: start, CallReturned: time.Now()})
}
func stream(action Action) []Stream {
	return []Stream{{Name: "writer", Every: 10 * time.Millisecond, Pattern: []Step{{"control", action}}}}
}
func final(t *testing.T, s *memorySink) Record {
	t.Helper()
	if len(s.records) == 0 || s.records[len(s.records)-1].Kind != "final" {
		t.Fatal("missing final")
	}
	for i, r := range s.records {
		if r.Version != Version || r.Label != "DIRECTIONAL" || r.Qualification || r.Sequence != uint64(i+1) || r.UnitID != fmt.Sprintf("control:%d", i+1) {
			t.Fatalf("bad envelope: %+v", r)
		}
	}
	return s.records[len(s.records)-1]
}

func TestWorkloadMissedSlots(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		elapsed time.Duration
		want    uint64
	}{{-1, 0}, {0, 0}, {1, 1}, {time.Millisecond, 1}, {time.Millisecond + 1, 2}, {10 * time.Millisecond, 10}} {
		if got := missedSlots(now, now.Add(tc.elapsed), time.Millisecond); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestWorkloadCancellationWaitsForActionAndRetainsPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, physicalReturn := make(chan struct{}), make(chan struct{})
	sink := &memorySink{notify: make(chan Record, 16)}
	done := make(chan error, 1)
	action := func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		if err := emit(Observation{Phase: "ACCEPT"}); err != nil {
			return err
		}
		close(started)
		<-physicalReturn // models unpreemptible native work; cancellation is no join
		if err := emit(Observation{Phase: "RESOLVE", CallError: "synthetic cancellation"}); err != nil {
			return err
		}
		return ctx.Err()
	}
	go func() { done <- Run(ctx, testConfig(), stream(action), sink) }()
	<-started
	cancel()
	// ACCEPT must already be in the sink before the blocked next phase returns.
	found := false
	for !found {
		r := <-sink.notify
		found = r.Observation != nil && r.Observation.Phase == "ACCEPT"
	}
	select {
	case <-done:
		t.Fatal("returned before action physically returned")
	default:
	}
	close(physicalReturn)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f := final(t, sink)
	if !f.DriversReturned || f.ScheduleComplete {
		t.Fatal(f)
	}
}

func TestWorkloadPairedReadRunsWhileWriterBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writerStarted, readServed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	slow := func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		close(writerStarted)
		<-readServed
		cancel()
		return emit(Observation{Phase: "ACCEPT"})
	}
	browse := func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		<-writerStarted
		once.Do(func() { close(readServed) })
		return emit(Observation{Phase: "BROWSE"})
	}
	streams := stream(slow)
	streams = append(streams, Stream{Name: "foreground", Every: time.Millisecond, Pattern: []Step{{"browse", browse}}})
	sink := &memorySink{}
	if err := Run(ctx, testConfig(), streams, sink); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	found := false
	for _, r := range sink.records {
		found = found || r.Observation != nil && r.Observation.Phase == "BROWSE"
	}
	if !found {
		t.Fatal("foreground did not run")
	}
}

func TestWorkloadSlowCallsDoNotCatchUp(t *testing.T) {
	config := testConfig()
	config.Duration = 20 * time.Millisecond
	action := func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		<-ctx.Done()
		return emit(Observation{Phase: "CONTROL"})
	}
	sink := &memorySink{}
	if err := Run(t.Context(), config, stream(action), sink); err != nil {
		t.Fatal(err)
	}
	offers, missed := uint64(0), uint64(0)
	for _, r := range sink.records {
		if r.Progress != nil {
			offers += r.Progress.Offered
			missed += r.Progress.Missed + r.Progress.Unserved
		}
	}
	if offers > 1 || offers+missed != 2 || final(t, sink).ScheduleComplete {
		t.Fatal(offers, missed, sink.records)
	}
}

func TestWorkloadErrorsAndOutputFailuresStop(t *testing.T) {
	for _, which := range []string{"error", "panic", "empty", "too-many", "output"} {
		t.Run(which, func(t *testing.T) {
			calls := 0
			action := func(_ context.Context, _ Offer, emit func(Observation) error) error {
				calls++
				switch which {
				case "error":
					_ = emit(Observation{Phase: "ACCEPT", CallError: "failure"})
					return errors.New("failure")
				case "panic":
					_ = emit(Observation{Phase: "ACCEPT"})
					panic("synthetic")
				case "empty":
					return nil
				case "too-many":
					for range 4 {
						_ = emit(Observation{Phase: "CONTROL"})
					}
					return nil
				default:
					return emit(Observation{Phase: "ACCEPT"})
				}
			}
			sink := &memorySink{}
			if which == "output" {
				sink.failAt = 2
			}
			if err := Run(t.Context(), testConfig(), stream(action), sink); err == nil || calls > 1 {
				t.Fatal(err, calls)
			}
			if which != "output" && final(t, sink).ScheduleComplete {
				t.Fatal("error marked complete")
			}
		})
	}
}

func TestWorkloadStubAndInvalidComposition(t *testing.T) {
	sink := &memorySink{}
	config := testConfig()
	config.Duration = 10 * time.Millisecond
	if err := Run(t.Context(), config, stream(StubCadence("security FULL unavailable")), sink); err != nil {
		t.Fatal(err)
	}
	if final(t, sink).ScheduleComplete {
		t.Fatal("stub became complete")
	}
	for _, action := range []Action{Inventory(nil, nil, nil), Effective(nil, nil, nil), Browse(nil, nil), Passive(nil, nil), WAL("", nil)} {
		sink = &memorySink{}
		if err := Run(t.Context(), config, stream(action), sink); err == nil {
			t.Fatal("unavailable composition ran")
		}
		if final(t, sink).ScheduleComplete {
			t.Fatal("unavailable composition completed")
		}
	}
	for _, mutate := range []func(*Config, []Stream){
		func(c *Config, _ []Stream) { c.Duration = 0 }, func(c *Config, _ []Stream) { c.Cell.ManifestSHA256 = "bad" },
		func(_ *Config, s []Stream) { s[0].Every = 0 }, func(_ *Config, s []Stream) { s[0].Pattern[0].Run = nil },
	} {
		c := testConfig()
		s := stream(control)
		mutate(&c, s)
		sink = &memorySink{}
		if err := Run(t.Context(), c, s, sink); err == nil || len(sink.records) != 0 {
			t.Fatal("invalid setup emitted records")
		}
	}
}

func TestWorkloadJSONNulls(t *testing.T) {
	var output bytes.Buffer
	sink := JSONSink{&output}
	if err := sink.Append(Record{Observation: &Observation{Phase: "ACCEPT"}}); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(output.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	o := raw["Observation"].(map[string]any)
	for _, key := range []string{"Evidence", "HoldNS", "CommitNS", "ReadyNS", "Requested"} {
		if value, ok := o[key]; !ok || value != nil {
			t.Fatal(key, value)
		}
	}
}
