package writer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ec3WriteEvery = flag.Duration("ec3-foreground-write-every", 0, "independent progress/security submissions;0 disables;1ms..1min")
var ec3WritePattern = flag.String("ec3-foreground-write-pattern", "progress_drain,security", "closed progress_capture/progress_drain/security pattern")

var ec2Enabled = flag.Bool("ec2-sustained", false, "opt-in EC2 component; never qualification")
var ec2Every = flag.Duration("ec2-writer-every", time.Second, "declared serial writer cycle spacing,1ms..30s")
var ec2Readers = flag.Duration("ec2-reader-every", 250*time.Millisecond, "one bounded serial foreground worker spacing,>=1ms")
var ec2Passive = flag.Duration("ec2-passive-every", 250*time.Millisecond, "requested CP cadence,>=50ms;0 explicitly omits CP in a subset")
var ec2Recovery = flag.Duration("ec2-recovery-every", time.Second, "requested GV recovery cadence;0 explicitly omits recovery")
var ec2Observe = flag.Duration("ec2-wal-every", time.Second, "advisory observation cadence,>=1ms")
var ec2Pattern = flag.String("ec2-writer-pattern", "inventory,security", "bounded writer pattern; exactly one inventory entry; missing native bindings fail")
var ec2CPMode = flag.String("ec2-checkpoint-mode", "concurrent", "declared serialized or concurrent; no custody enable")
var ec2Warmup = flag.Int("ec2-warmup", 1, "actual retained N1 warmup originals,1..64")
var ec2Cohort = flag.String("ec2-cohort", "timing", "timing or native-counters, never pooled")
var ec2Cadence = flag.String("ec2-cadence", "realistic", "realistic or accelerated declaration")

func ec2Bindings(t *testing.T, c *writer.N1PairedComponent, cohort string) workload.EC2Bindings {
	bindings := workload.EC2Bindings{}
	bindings[workload.EC2Inventory] = func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		start := time.Now()
		err := c.RunEC2Original(t, ctx, offer.ScheduledAt, cohort)
		end := time.Now()
		wall := end.Sub(start)
		o := workload.Observation{Phase: "COMPONENT_N1_CHAIN", CallStarted: &start, CallReturned: &end, WallNS: &wall, Notice: "three physical turns in owner ledger; chain wall is not HOLD or COMMIT"}
		if err != nil {
			o.CallError = err.Error()
		}
		return errors.Join(err, emit(o))
	}
	for _, route := range []struct {
		op   workload.EC2Operation
		name string
	}{{workload.EC2Browse, "browse"}, {workload.EC2Detail, "detail"}, {workload.EC2Playback, "playback"}, {workload.EC2Continue, "continue"}} {
		bindings[route.op] = recordPaired(c, pairedAction(c, route.name, nil))
	}
	bindings[workload.EC2WAL] = func(ctx context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		start := time.Now()
		snapshot, err := c.ObserveEC2WAL(ctx)
		end := time.Now()
		wall := end.Sub(start)
		o := workload.Observation{Phase: "WAL", Result: snapshot, CallStarted: &start, CallReturned: &end, WallNS: &wall}
		if err != nil {
			o.CallError = err.Error()
		}
		return errors.Join(err, emit(o))
	}
	// No callback/flag grants native WC/Effective/CP custody. Their same-owner
	// bindings land separately. Requested operations remain explicit failed STUBs.
	return bindings
}

func TestEC2SustainedMeasurement(t *testing.T) {
	if !*ec2Enabled {
		if os.Getenv("PORTICO_WRITER_EC2") != "" {
			t.Fatal("EC2 companion flag required")
		}
		t.Skip("opt-in EC2 sustained component")
	}
	if os.Getenv("PORTICO_WRITER_EC2") != "1" {
		t.Fatal("explicit EC2 flag requires PORTICO_WRITER_EC2=1")
	}
	if *ec2Every < time.Millisecond || *ec2Every > 30*time.Second || (*ec2Cadence != "realistic" && *ec2Cadence != "accelerated") {
		t.Fatal("invalid EC2 writer cadence")
	}
	pattern := []workload.EC2Operation{}
	inventory := 0
	for _, name := range strings.Split(*ec2Pattern, ",") {
		op := workload.EC2Operation(name)
		pattern = append(pattern, op)
		if op == workload.EC2Inventory {
			inventory++
		}
	}
	if inventory != 1 {
		t.Fatal("existing progressive fixture requires exactly one inventory entry per pattern")
	}
	schedule := workload.EC2Schedule{WriterEvery: *ec2Every, ReaderEvery: *ec2Readers, PassiveEvery: *ec2Passive, RecoveryEvery: *ec2Recovery, WALEvery: *ec2Observe, CheckpointMode: *ec2CPMode, WriterPattern: pattern,
		ReaderPattern: []workload.EC2Operation{workload.EC2Browse, workload.EC2Detail, workload.EC2Playback, workload.EC2Continue}}
	if _, _, err := workload.EC2Streams(schedule, nil); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(os.Getenv("PORTICO_WRITER_EC2_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 16385))
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	c := writer.NewN1PairedComponent(t, data, *ec2Warmup, *ec2Cohort)
	streams, manifest, err := workload.EC2Streams(schedule, ec2Bindings(t, c, *ec2Cohort))
	if err != nil {
		t.Fatal(err)
	}
	if c.MemoryEnabled() {
		streams = append(streams, ec5MemoryStream(c))
	}
	var writeLedger *foregroundWriteLedger
	if *ec3WriteEvery != 0 {
		for _, op := range pattern {
			if op != workload.EC2Inventory && op != workload.EC2Effective {
				t.Fatal("foreground request mode requires scan-only writer pattern; no duplicate WC worker/receipt source")
			}
		}
		var phases []workload.EC2Operation
		for _, name := range strings.Split(*ec3WritePattern, ",") {
			phases = append(phases, workload.EC2Operation(name))
		}
		foreground, ledger, err := foregroundWriteStream(*ec3WriteEvery, phases, ec2Bindings(t, c, *ec2Cohort))
		if err != nil {
			t.Fatal(err)
		}
		writeLedger = ledger
		streams = append(streams, foreground)
	}
	duration := time.Duration(c.Measured) * time.Duration(len(pattern)) * schedule.WriterEvery
	// Bound file/observer offer volume too, without widening any owner recipe.
	total := uint64(0)
	for _, stream := range streams {
		total += uint64((duration-1)/stream.Every) + 1
	}
	if writeLedger != nil && int((duration-1) / *ec3WriteEvery)+1 > foregroundWriteLimit {
		t.Fatal("foreground write planned ledger exceeds4096; no dropped requests")
	}
	if duration > 2*time.Hour || total > 65536 {
		t.Fatal("EC2 declared schedule exceeds bounded duration/offer census")
	}
	raw, _ := json.Marshal(map[string]any{"method": "ec2-sustained-component-v1", "manifest": manifest, "planned_total_offers": total, "planned_fresh_originals": c.Measured, "planned_writer_slots": c.Measured * len(pattern), "database_id": c.DatabaseID, "wal_bound_coverage": false, "wal_reset_coverage": false, "scope": "N1/read/WAL subset only until actual WC, Effective and GV bindings land; whole EC2 remains unavailable", "caps": "unchanged existing N1/RD2 synthetic profiles", "wal_method": "typed snapshots outside each measured physical N1 turn plus timer; non-atomic, no reset inference"})
	if err := c.EmitEC2("start", raw); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	cfg := workload.Config{RunID: c.RunID, Duration: duration, Cell: qual.RestartCell{ID: c.RunID, HostID: c.HostEpoch, DatabaseID: c.DatabaseID, Profile: "EC2 existing synthetic N1/RD2 profiles; unqualified", ManifestSHA256: hex.EncodeToString(digest[:]), CacheState: "warm", CadenceMode: *ec2Cadence}}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sink := &pairedSink{c: c}
	runErr := workload.Run(ctx, cfg, streams, sink)
	if runErr == nil && (!sink.finalSeen || !sink.complete) {
		runErr = errors.New("incomplete EC2 offered schedule; retain all missed/unserved/error prefixes")
	}
	if writeLedger != nil {
		expected := int((duration-1) / *ec3WriteEvery) + 1
		raw, _ := json.Marshal(writeLedger.report(expected, sink.finalSeen && sink.complete))
		runErr = errors.Join(runErr, c.EmitEC2("foreground_write_report", raw))
	}
	if err := c.Close(runErr); err != nil {
		t.Fatal(err)
	}
}

// Tiny functional control on a disposable DB, not an opt-in timing workload.
// CI runs this on Go1.26.10; no sleeps, SQL, replacement identities or retries.
func TestEC2N1WALComposition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ec2.ndjson")
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(evidence, fmt.Sprintf("ec2-functional-%d.ndjson", time.Now().UnixNano()))
	}
	data, _ := json.Marshal(map[string]any{"run_id": "EC2_CONTROL", "host_epoch": "CONTROL", "source_commit": strings.Repeat("a", 40), "output_path": path, "fixtures": 1})
	c := writer.NewN1PairedComponent(t, data, 1, "timing")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	bindings := ec2Bindings(t, c, "timing")
	offer := workload.Offer{Stream: "writer", Operation: "inventory", Slot: 1, ScheduledAt: time.Now()}
	if err := bindings[workload.EC2Inventory](ctx, offer, func(o workload.Observation) error {
		if o.Evidence != nil || o.HoldNS != nil || o.CommitNS != nil || o.CallError != "" {
			return errors.New("chain wall became physical evidence")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bindings[workload.EC2Browse](ctx, workload.Offer{Stream: "foreground", Operation: "browse", Slot: 1, ScheduledAt: time.Now()}, func(o workload.Observation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_, manifest, err := workload.EC2Streams(workload.EC2Schedule{WriterEvery: time.Second, WALEvery: time.Second, WriterPattern: []workload.EC2Operation{workload.EC2Inventory, workload.EC2Security}, CheckpointMode: "concurrent", PassiveEvery: time.Second, RecoveryEvery: time.Second}, bindings)
	if err != nil || manifest.CompleteCapabilities || len(manifest.Missing) != 3 {
		t.Fatal(manifest, err)
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	roles := map[string]bool{}
	var original *writer.ReceiptReference
	previous := 0
	for {
		var row map[string]json.RawMessage
		if err := decoder.Decode(&row); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		var kind string
		_ = json.Unmarshal(row["kind"], &kind)
		if kind != "ec2_wal_turn" {
			continue
		}
		var wrapper struct {
			Record struct {
				Role          string `json:"turn_role"`
				ID            string `json:"physical_attempt_id"`
				Ordinal       *int   `json:"physical_attempt_ordinal"`
				Before, After *workload.WALSnapshot
				Reference     *writer.ReceiptReference `json:"original_reference"`
			}
		}
		if err := json.Unmarshal(row["record"], &wrapper.Record); err != nil {
			t.Fatal(err)
		}
		r := wrapper.Record
		if r.ID == "" || r.Ordinal == nil || r.Before == nil || r.After == nil || r.Before.State == nil || r.After.State == nil || r.Before.TokenBefore == nil || r.After.TokenAfter == nil || r.After.FileAfter == nil {
			t.Fatal("missing actual physical WAL join", string(row["record"]))
		}
		if r.Reference == nil || r.Reference.Identity.Sequence <= 0 {
			t.Fatal("lost original", r)
		}
		if original == nil {
			original = r.Reference
		} else if *original != *r.Reference {
			t.Fatal("changed original across physical chain")
		}
		if previous != 0 && *r.Ordinal != previous+1 {
			t.Fatal("renumbered physical stream", r)
		}
		previous = *r.Ordinal
		if roles[r.Role] {
			t.Fatal("duplicate physical role", r.Role)
		}
		roles[r.Role] = true
	}
	if len(roles) != 3 {
		t.Fatal(fmt.Sprintf("physical role joins=%v, want BEGIN/Resolve/ACK", roles))
	}
}
