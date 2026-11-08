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

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
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
	return emit(observedCall("CONTROL", start, time.Now()))
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
					for range maxActionObservations + 1 {
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

// A real WC action emits its before-snapshot, ACCEPT, RESOLVE, ACK and
// after-snapshot plus one caller-side bracket. The bound must retain all six,
// while the existing too-many control still stops one record beyond it.
func TestWorkloadActionObservationBound(t *testing.T) {
	config := testConfig()
	config.Duration = 60 * time.Millisecond
	emitted := 0
	action := func(_ context.Context, _ Offer, emit func(Observation) error) error {
		emitted++
		for i := range maxActionObservations {
			if err := emit(Observation{Phase: fmt.Sprintf("CONTROL_%d", i)}); err != nil {
				return err
			}
		}
		return nil
	}
	sink := &memorySink{}
	if err := Run(t.Context(), config, stream(action), sink); err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, r := range sink.records {
		if r.Observation != nil {
			records++
		}
	}
	if emitted == 0 || records != emitted*maxActionObservations {
		t.Fatal(emitted, records)
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
	for _, action := range []Action{Inventory(nil, nil, nil), Effective(nil, nil, nil), Browse(nil, nil, nil, nil, nil), Detail(nil, nil, nil, nil, nil), PlaybackLookup(nil, nil, nil, nil, nil), ContinueWatching(nil, nil, nil, nil, nil), Passive(nil, nil), WAL("", nil)} {
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
	for _, key := range []string{"Evidence", "HoldNS", "CommitNS", "ReadyNS", "Requested", "WallNS", "CallStarted", "CallReturned"} {
		if value, ok := o[key]; !ok || value != nil {
			t.Fatal(key, value)
		}
	}
}

func TestWorkloadTypedPassiveRefusalStopsAndCannotReduce(t *testing.T) {
	// Fabricated Step protocol control: actual GV can return (typed refusal,nil).
	result := governor.CheckpointResult{Refusal: &governor.Refusal{Code: governor.Capacity}}
	start := time.Now()
	var refused Observation
	err := passiveResult(result, nil, start, time.Now(), func(o Observation) error { refused = o; return nil })
	if !errors.Is(err, errActionRefused) || !refused.RefusalError || refused.CallError != "" || refused.Result.(governor.CheckpointResult).Refusal != result.Refusal {
		t.Fatal("typed refusal/native result was lost", refused, err)
	}
	for _, ignoreEmitError := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignore-emit-error-%t", ignoreEmitError), func(t *testing.T) {
			writerStarted, writerReturned := make(chan struct{}), make(chan struct{})
			writerCalls, passiveCalls := 0, 0
			writerAction := func(ctx context.Context, _ Offer, emit func(Observation) error) error {
				writerCalls++
				close(writerStarted)
				<-ctx.Done()
				defer close(writerReturned)
				return emit(Observation{Phase: "CONTROL"})
			}
			passiveAction := func(_ context.Context, _ Offer, emit func(Observation) error) error {
				passiveCalls++
				<-writerStarted
				if ignoreEmitError {
					_ = emit(refused)
					return nil
				}
				return passiveResult(result, nil, time.Now(), time.Now(), emit)
			}
			streams := stream(writerAction)
			streams = append(streams, Stream{Name: "checkpoint", Every: time.Millisecond, Pattern: []Step{{"passive", passiveAction}}})
			sink := &memorySink{}
			err := Run(t.Context(), testConfig(), streams, sink)
			if !errors.Is(err, errActionRefused) || passiveCalls != 1 || writerCalls != 1 || final(t, sink).ScheduleComplete {
				t.Fatal(err, writerCalls, passiveCalls)
			}
			select {
			case <-writerReturned:
			default:
				t.Fatal("refusal returned before active stream joined")
			}
			found := false
			for _, r := range sink.records {
				if r.Observation != nil && r.Observation.Phase == "PASSIVE" {
					found = r.Observation.RefusalError && r.Observation.Result.(governor.CheckpointResult).Refusal == result.Refusal
				}
			}
			if !found {
				t.Fatal("typed PASSIVE refusal missing from raw output")
			}
		})
	}
	// A nominally complete envelope previously hid the nil-error PASSIVE refusal
	// beside a fully projectable writer call. Reduction must flag it independently
	// of Run's final completion flag or native CallError.
	raw := syntheticRun(syntheticObservation(), refused)
	coverage := Reduce(testConfig().Cell, raw, map[string]Classification{raw[1].UnitID: syntheticJoin(1)})
	if coverage.Report != nil || len(coverage.Problems) == 0 || coverage.PhysicalAttempts != 1 {
		t.Fatal(coverage)
	}
}

func TestWorkloadUnmeasuredTimingIsNullAndMeasuredZeroSurvives(t *testing.T) {
	var stub, prepare Observation
	_ = StubCadence("security missing")(t.Context(), Offer{}, func(o Observation) error { stub = o; return nil })
	_ = preparationError(func(o Observation) error { prepare = o; return nil }, errors.New("synthetic prepare failure"))
	now := time.Now()
	measured := observedCall("CONTROL", now, now)
	for _, tc := range []struct {
		name     string
		o        Observation
		measured bool
	}{{"stub", stub, false}, {"prepare", prepare, false}, {"observed-zero", measured, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := (JSONSink{&output}).Append(Record{Observation: &tc.o}); err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(output.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			o := raw["Observation"].(map[string]any)
			for _, key := range []string{"CallStarted", "CallReturned", "WallNS"} {
				value, ok := o[key]
				if !ok || (value != nil) != tc.measured {
					t.Fatal(key, value, tc.measured)
				}
			}
			if tc.measured && o["WallNS"] != float64(0) {
				t.Fatal("measured zero disappeared", o)
			}
		})
	}
}
