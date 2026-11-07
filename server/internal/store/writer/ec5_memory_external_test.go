package writer_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ec5Every = flag.Duration("ec5-memory-every", 100*time.Millisecond, "process-only sampler spacing,20ms..1s; observer overhead included")

func ec5MemoryStream(c *writer.N1PairedComponent) workload.Stream {
	return workload.Stream{Name: "memory", Every: *ec5Every, Pattern: []workload.Step{{Name: "process_memory", Run: func(ctx context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		start := time.Now()
		sample := c.SampleMemory()
		end := time.Now()
		wall := end.Sub(start)
		return emit(workload.Observation{Phase: "MEMORY", CallStarted: &start, CallReturned: &end, WallNS: &wall, Result: sample, Notice: "process/Go current sample; writer owner native CURRENT samples separate; no HOLD/COMMIT/native LIVE peak"})
	}}}}
}
func TestEC5PairedMemoryMeasurement(t *testing.T) {
	if !writer.EC5MemoryRequested() {
		if os.Getenv("PORTICO_EC5_MEMORY") != "" {
			t.Fatal("EC5 companion flag required")
		}
		t.Skip("opt-in EC5 memory component; use capped runner")
	}
	if os.Getenv("PORTICO_EC5_MEMORY") != "1" {
		t.Fatal("explicit EC5 flag requires PORTICO_EC5_MEMORY=1")
	}
	if !*pairedEnabled || *ec5Every < 20*time.Millisecond || *ec5Every > time.Second {
		t.Fatal("EC5 requires original paired opt-in and bounded sampling")
	}
	TestN1PairedMeasurement(t) // ONE existing scheduler/fixture/consumer path
}
func TestEC5SustainedMemoryMeasurement(t *testing.T) {
	if !writer.EC5MemoryRequested() {
		if os.Getenv("PORTICO_EC5_MEMORY") != "" {
			t.Fatal("EC5 companion flag required")
		}
		t.Skip("opt-in EC5 sustained memory component")
	}
	if os.Getenv("PORTICO_EC5_MEMORY") != "1" || !*ec2Enabled || *ec5Every < 20*time.Millisecond || *ec5Every > time.Second {
		t.Fatal("EC5 requires EC2 opt-in, companion env and bounded sampling")
	}
	TestEC2SustainedMeasurement(t) // merged composer, original actions/errors/gates
}
func TestEC5CgroupFunctional(t *testing.T) {
	if os.Getenv("PORTICO_EC5_FUNCTIONAL") != "1" {
		t.Skip("GitHub ec5-memory functional harness only")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if evidence == "" {
		t.Fatal("functional evidence directory required")
	}
	command := exec.CommandContext(t.Context(), "sudo", "-n", "python3", "../../../../ci/ec5_memory.py", "--binary", binary, "--output", evidence, "--target", "control")
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("cgroup driver: %v\n%s", err, raw)
	}
}
func TestEC5MemoryChildControl(t *testing.T) {
	if os.Getenv("PORTICO_EC5_CHILD") != "1" {
		t.Skip("private capped functional child only")
	}
	// Source-bound real N1 + read; process sampler is independently concurrent.
	path := filepath.Join(os.Getenv("PORTICO_LANE_EVIDENCE_DIR"), "component.ndjson")
	data, _ := json.Marshal(map[string]any{"run_id": "EC5_FUNCTIONAL", "host_epoch": "CONTROL", "source_commit": strings.Repeat("a", 40), "output_path": path, "fixtures": 1})
	c := writer.NewN1PairedComponent(t, data, 1, "native-counters")
	if !c.MemoryEnabled() {
		t.Fatal("memory observer not enabled")
	}
	ctx, cancel := context.WithCancel(t.Context())
	sink := &pairedSink{c: c}
	cfg := workload.Config{RunID: c.RunID, Duration: time.Minute, Cell: pairedMemoryCell(c)}
	done := make(chan error, 1)
	first := make(chan struct{})
	var once sync.Once
	sampler := ec5MemoryStream(c)
	original := sampler.Pattern[0].Run
	sampler.Pattern[0].Run = func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		return original(ctx, offer, func(o workload.Observation) error {
			if o.HoldNS != nil || o.CommitNS != nil || o.Evidence != nil {
				t.Error("sampler invented owner evidence")
			}
			err := emit(o)
			once.Do(func() { close(first) })
			return err
		})
	}
	go func() { done <- workload.Run(ctx, cfg, []workload.Stream{sampler}, sink) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	select {
	case <-first:
	case err := <-done:
		joined = true
		t.Fatal("sampler failed before first observation", err)
	}
	callErr := c.RunOriginal(t, t.Context(), time.Now(), false, "native-counters")
	if callErr == nil {
		callErr = recordPaired(c, pairedAction(c, "browse", nil))(t.Context(), workload.Offer{Stream: "foreground", Operation: "browse", Slot: 1, ScheduledAt: time.Now()}, func(workload.Observation) error { return nil })
	}
	cancel()
	sampleErr := <-done
	joined = true
	if !errors.Is(sampleErr, context.Canceled) {
		t.Fatal(sampleErr)
	}
	if err := c.Close(callErr); err != nil {
		t.Fatal(err)
	}
}

func pairedMemoryCell(c *writer.N1PairedComponent) qual.RestartCell {
	return qual.RestartCell{ID: c.RunID, HostID: c.HostEpoch, DatabaseID: c.DatabaseID, Profile: "existing synthetic N1/RD2; EC5 component only", ManifestSHA256: strings.Repeat("a", 64), CacheState: "warm", CadenceMode: "accelerated"}
}
