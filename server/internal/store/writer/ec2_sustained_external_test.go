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

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ec3WriteEvery = flag.Duration("ec3-foreground-write-every", 0, "independent progress/security submissions;0 disables;1ms..1min")
var ec3WritePattern = flag.String("ec3-foreground-write-pattern", "progress_capture,progress_drain,security", "closed progress_capture/progress_drain/security pattern")

var ec2Enabled = flag.Bool("ec2-sustained", false, "opt-in EC2 component; never qualification")
var ec2Every = flag.Duration("ec2-writer-every", time.Second, "declared serial writer cycle spacing,1ms..30s")
var ec2Readers = flag.Duration("ec2-reader-every", 250*time.Millisecond, "one bounded serial foreground worker spacing,>=1ms")

// The adopted keeper offer cadence is 50ms (the cadence lever was dropped from
// the WAL policy); this cell must not run an arbitrary slower sweep. The
// governor's own minimum start spacing still applies.
var ec2Passive = flag.Duration("ec2-passive-every", 50*time.Millisecond, "adopted keeper CP offer cadence,50ms default,>=50ms;0 explicitly omits CP in a subset")
var ec2Recovery = flag.Duration("ec2-recovery-every", time.Second, "requested GV recovery cadence;0 explicitly omits recovery")
var ec2Observe = flag.Duration("ec2-wal-every", time.Second, "advisory observation cadence,>=1ms")
var ec2Pattern = flag.String("ec2-writer-pattern", "inventory,security", "bounded writer pattern; exactly one inventory entry; missing native bindings fail")
var ec2CPMode = flag.String("ec2-checkpoint-mode", "concurrent", "declared serialized or concurrent; no custody enable")
var ec2Warmup = flag.Int("ec2-warmup", 1, "actual retained N1 warmup originals,1..64")
var ec2Cohort = flag.String("ec2-cohort", "timing", "timing or native-counters, never pooled")
var ec2Cadence = flag.String("ec2-cadence", "realistic", "realistic or accelerated declaration")

// A bounded scheduler may miss the paired offer. Never submit a progress phase
// the owner driver would refuse as unresolved work: record the deferral raw and
// do no write. The missed offer stays visible in stream_end and the cell verdict,
// so a deferral never turns a gap into a success.
func deferProgressPhase(phase string, valid func() bool, action workload.Action) workload.Action {
	return func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		if !valid() {
			return emit(workload.Observation{Phase: phase + "_DEFERRED", Notice: "paired progress phase not valid at this bounded offer; no write attempted; the missed offer remains an incomplete schedule"})
		}
		return action(ctx, offer, emit)
	}
}

// Concurrent ingest can change a read's publication fence between its first and
// final window. That is the read contract's stale-continuation deferral, not a
// failed composition: retain the stale attempt raw and retry the offered read
// once. A non-stale error is returned unchanged.
func retryStaleRead(action workload.Action) workload.Action {
	return func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		var buffered []workload.Observation
		err := action(ctx, offer, func(o workload.Observation) error {
			buffered = append(buffered, o)
			return nil
		})
		if !errors.Is(err, read.ErrStale) {
			for _, o := range buffered {
				if emitErr := emit(o); emitErr != nil {
					return errors.Join(err, emitErr)
				}
			}
			return err
		}
		for _, o := range buffered {
			o.CallError = ""
			o.Notice = "read fence changed by concurrent ingest; stale attempt retained raw, offered read retried once"
			if emitErr := emit(o); emitErr != nil {
				return errors.Join(err, emitErr)
			}
		}
		return action(ctx, offer, emit)
	}
}

func ec2Bindings(t *testing.T, c *writer.N1PairedComponent, cohort string) workload.EC2Bindings {
	bindings := workload.EC2Bindings{}
	bindings[workload.EC2Inventory] = func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		start := time.Now()
		release, queueErr := c.EnterEC2Writer(ctx)
		if queueErr != nil {
			return queueErr
		}
		defer release()
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
		bindings[route.op] = retryStaleRead(recordPaired(c, pairedAction(c, route.name, nil)))
	}
	bindings[workload.EC2FullSearch] = retryStaleRead(ec2FullSearchBinding(t, c, "harbour"))
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
	if c.EC2Writer() != nil {
		progress := workload.ProgressBinding{Writer: c.EC2Writer(), Driver: c.EC2Progress(), Report: func(context.Context, workload.Offer) (workload.ProgressReport, error) {
			return workload.ProgressReport{Sequence: c.EC2ReportSequence(), PositionSeconds: 5, DurationSeconds: 100, State: "playing"}, nil
		}}
		bindings[workload.EC2ProgressCapture] = deferProgressPhase("PROGRESS_CAPTURE", func() bool { return !c.EC2ProgressPending() }, boundEC2WC(c, writer.ProgressAccept, workload.ProgressCapture(progress)))
		bindings[workload.EC2ProgressDrain] = deferProgressPhase("PROGRESS_DRAIN", c.EC2ProgressPending, boundEC2WC(c, writer.ProgressAccept, workload.ProgressDrain(progress)))
		bindings[workload.EC2Security] = boundEC2WC(c, writer.SecurityAccept, workload.Security(workload.SecurityBinding{Writer: c.EC2Writer(), Driver: c.EC2Security(), Target: func(ctx context.Context, _ workload.Offer) (string, error) { return c.EC2SecurityTarget(t, ctx) }}))
		bindings[workload.EC2Reclaim] = boundEC2WC(c, writer.ProgressReceiptReclaim, workload.Reclaim(workload.ReclaimBinding{Writer: c.EC2Writer(), Driver: c.EC2Reclaim()}))
		for _, operation := range []workload.EC2Operation{workload.EC2Passive, workload.EC2Recovery} {
			op := operation
			bindings[op] = func(ctx context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
				start := time.Now()
				if op == workload.EC2Recovery {
					snapshot, err := c.ObserveEC2WAL(ctx)
					if err != nil {
						return err
					}
					if snapshot.State == nil {
						return errors.New("recovery eligibility snapshot unavailable")
					}
					if snapshot.State.ActiveBytes < (1<<29)-(1<<20) {
						end := time.Now()
						wall := end.Sub(start)
						return emit(workload.Observation{Phase: "RECOVERY_NOT_DUE", CallStarted: &start, CallReturned: &end, WallNS: &wall, Result: snapshot, Notice: "observed active WAL below hard-minus-reserve; native RecoverWAL not attempted, no reset proof; actual space refusals remain raw failures"})
					}
				}
				out, err := c.EC2Checkpoint(ctx, op == workload.EC2Recovery)
				end := time.Now()
				wall := end.Sub(start)
				if errors.Is(err, governor.ErrCheckpointCadence) {
					// The adopted 50ms offer cadence sits exactly on the keeper's minimum
					// start spacing; an early offer is a deferral, not a maintenance or
					// writer failure. Record it raw and keep the schedule moving.
					return emit(workload.Observation{Phase: "CHECKPOINT_DEFERRED", CallStarted: &start, CallReturned: &end, WallNS: &wall, Result: out, Notice: "offer inside the keeper minimum start spacing; no checkpoint started; recorded deferral"})
				}
				observation := workload.Observation{Phase: strings.ToUpper(string(op)), CallStarted: &start, CallReturned: &end, WallNS: &wall, Result: out, RefusalError: out.Refusal != nil}
				if err != nil {
					observation.CallError = err.Error()
				}
				return errors.Join(err, emit(observation))
			}
		}
	}
	// Effective has no trusted binding here and remains an explicit failed STUB.
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
	if *ec2CPMode != "concurrent" {
		t.Fatal("native EC2 keeper requires the declared concurrent checkpoint mode")
	}
	if inventory != 1 {
		t.Fatal("existing progressive fixture requires exactly one inventory entry per pattern")
	}
	// One serial foreground worker. Browse/detail carry the load; search is one
	// offer in nine, well below them, and still runs during scan writes.
	schedule := workload.EC2Schedule{WriterEvery: *ec2Every, ReaderEvery: *ec2Readers, PassiveEvery: *ec2Passive, RecoveryEvery: *ec2Recovery, WALEvery: *ec2Observe, CheckpointMode: *ec2CPMode, WriterPattern: pattern,
		ReaderPattern: []workload.EC2Operation{workload.EC2Browse, workload.EC2Detail, workload.EC2Browse, workload.EC2Detail, workload.EC2Playback, workload.EC2Continue, workload.EC2Browse, workload.EC2Detail, workload.EC2FullSearch}}
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
	c := writer.NewEC2PairedComponent(t, data, *ec2Warmup, *ec2Cohort)
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
	raw, _ := json.Marshal(map[string]any{"method": "ec2-sustained-component-v1", "manifest": manifest, "planned_total_offers": total, "planned_fresh_originals": c.Measured, "planned_writer_slots": c.Measured * len(pattern), "database_id": c.DatabaseID, "wal_bound_coverage": false, "wal_reset_coverage": false, "scope": "same native N1/WC/read/fundedCP/recovery composition; Effective remains unavailable; reset/bound proof depends on actual raw events", "caps": "unchanged existing N1/RD2 synthetic profiles", "wal_method": "typed snapshots outside each measured physical N1 turn plus timer; non-atomic, no reset inference"})
	if err := c.EmitEC2("start", raw); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	cfg := workload.Config{RunID: c.RunID, Duration: duration, Cell: qual.RestartCell{ID: c.RunID, HostID: c.HostEpoch, DatabaseID: c.DatabaseID, Profile: "EC2 existing synthetic N1/RD2 profiles; unqualified", ManifestSHA256: hex.EncodeToString(digest[:]), CacheState: "warm", CadenceMode: *ec2Cadence}}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sink := &pairedSink{c: c}
	runErr := workload.Run(ctx, cfg, streams, sink)
	if runErr == nil && (!sink.finalSeen || !sink.drivers) {
		runErr = errors.New("workload drivers did not return a final; retain all raw prefixes")
	}
	if runErr == nil && writeLedger == nil {
		// The EC2 target is the serial ingest writer. Read, checkpoint and WAL
		// workers are concurrent load; their missed offers are recorded gaps, not
		// an ingest failure. Every action error still fails the run above.
		if gap := sink.streamGap("writer"); gap != "" {
			runErr = errors.New(gap)
		}
	}
	searchRaw, _ := json.Marshal(sink.searchReadReport())
	runErr = errors.Join(runErr, c.EmitEC2("search_read_report", searchRaw))
	if writeLedger != nil {
		expected := int((duration-1) / *ec3WriteEvery) + 1
		fg, fgSeen := sink.progress["foreground-writes"]
		if !fgSeen {
			runErr = errors.Join(runErr, errors.New("foreground-writes stream missing its terminal progress"))
		}
		report := writeLedger.report(expected, int(fg.Missed), int(fg.Unserved), sink.finalSeen && sink.drivers, sink.finalSeen && sink.complete)
		raw, _ := json.Marshal(report)
		runErr = errors.Join(runErr, c.EmitEC2("foreground_write_report", raw))
		if runErr == nil && !report.CoverageComplete {
			runErr = errors.New("foreground write coverage incomplete; retain all raw prefixes")
		}
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

// Pure controls for the bounded-composition wrappers. No database or clock.
func TestEC2StaleReadRetryRetainsStaleAttempt(t *testing.T) {
	calls := 0
	inner := func(_ context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		calls++
		if calls == 1 {
			return errors.Join(read.ErrStale, emit(workload.Observation{Phase: "BROWSE", CallError: read.ErrStale.Error()}))
		}
		return emit(workload.Observation{Phase: "BROWSE"})
	}
	var got []workload.Observation
	err := retryStaleRead(inner)(t.Context(), workload.Offer{}, func(o workload.Observation) error {
		got = append(got, o)
		return nil
	})
	if err != nil || calls != 2 || len(got) != 2 {
		t.Fatal(err, calls, got)
	}
	if got[0].CallError != "" || !strings.Contains(got[0].Notice, "stale attempt retained") || got[1].CallError != "" {
		t.Fatal(got)
	}
	// A non-stale failure is returned unchanged and its observation keeps the error.
	boom := errors.New("control failure")
	failing := func(_ context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		return errors.Join(boom, emit(workload.Observation{Phase: "BROWSE", CallError: boom.Error()}))
	}
	got = nil
	err = retryStaleRead(failing)(t.Context(), workload.Offer{}, func(o workload.Observation) error {
		got = append(got, o)
		return nil
	})
	if !errors.Is(err, boom) || len(got) != 1 || got[0].CallError != boom.Error() {
		t.Fatal(err, got)
	}
}

func TestEC2ProgressPhaseDeferralRecordsWithoutWrite(t *testing.T) {
	valid, called := false, false
	action := func(context.Context, workload.Offer, func(workload.Observation) error) error {
		called = true
		return nil
	}
	var got workload.Observation
	err := deferProgressPhase("PROGRESS_DRAIN", func() bool { return valid }, action)(t.Context(), workload.Offer{}, func(o workload.Observation) error {
		got = o
		return nil
	})
	if err != nil || called || got.Phase != "PROGRESS_DRAIN_DEFERRED" || got.CallError != "" {
		t.Fatal(err, called, got)
	}
	valid = true
	if err := deferProgressPhase("PROGRESS_DRAIN", func() bool { return valid }, action)(t.Context(), workload.Offer{}, func(workload.Observation) error { return nil }); err != nil || !called {
		t.Fatal(err, called)
	}
}
