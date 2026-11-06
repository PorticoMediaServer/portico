package writer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func n1ControlStream(t *testing.T) *n1Stream {
	t.Helper()
	f, err := openFirstLightOutput(filepath.Join(t.TempDir(), "raw.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return &n1Stream{file: f, origin: time.Now(), base: map[string]any{"schema": "portico-n1-component-v1", "label": "N1 COMPONENT DIRECTIONAL", "qualification": false, "run_id": "CONTROL", "process_epoch": t.Name()}}
}

func TestN1ComponentControlMatrix(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	for _, banks := range []string{"active", "dual"} {
		for _, age := range []string{"current", "dirty"} {
			for _, bucket := range []string{"present", "absent"} {
				cell := n1Case{banks, age, bucket, "fixture"}
				if banks == "dual" && age == "dirty" && bucket == "absent" {
					cell.Input = "escaped"
				}
				stream := n1ControlStream(t)
				protocolClock := time.Now().Add(time.Hour)
				var record *n1Attempt
				if !t.Run(banks+"/"+age+"/"+bucket, func(t *testing.T) {
					runN1Attempt(t, t.Context(), stream, 1, cell, "native-counters", "", &record, n1AttemptOptions{population: "first-use-reopen", clock: func() time.Time { protocolClock = protocolClock.Add(time.Microsecond); return protocolClock }})
				}) {
					continue
				}
				if !record.Complete || record.Clock.HoldNS == nil || *record.Clock.HoldNS <= 0 || record.Clock.CommitNS == nil || *record.Clock.CommitNS <= 0 {
					t.Fatalf("missing actual physical sample: outcome=%s error=%s units=%d", record.Outcome, record.Error, len(record.Units))
				}
				if record.Settings.Actual.Engine == "" || !*record.Settings.PolicyVerified || record.Settings.Actual.Synchronous != 1 || !*record.NativeJoined || !*record.ConnectionReturned || !*record.Retired {
					t.Fatal("policy/retirement missing")
				}
				if record.Profile == nil || record.Profile.ActualAgeCurrent == nil || *record.Profile.ActualAgeCurrent != (age == "current") || record.Profile.AgeBucketRows == nil || (*record.Profile.AgeBucketRows == 1) != (bucket == "present") {
					t.Fatal("profile lost actual native branch readback", record.Profile)
				}
				uses := map[stmt.ID]int{}
				domainCalls := 0
				for i, unit := range record.Units {
					if unit.Order != i+1 || unit.StartedNS == nil || unit.ReturnedNS == nil {
						t.Fatal("unit order/interval missing")
					}
					if unit.ID != "" {
						domainCalls++
						uses[unit.ID]++
						if unit.UseIndex != uses[unit.ID] || uses[unit.ID] > inventoryFreshMaxUses(unit.ID) {
							t.Fatal("ID/use mismatch")
						}
						if !unit.RowsClosed || unit.VMOps.Value == nil || unit.RowsExamined != nil || unit.RowsRejected != nil {
							t.Fatal("native rows/counter scope misrepresented", unit.ID)
						}
					}
				}
				want := 41
				if banks == "dual" {
					want = 44
				}
				if domainCalls != want {
					t.Fatalf("domain uses=%d, want%d", domainCalls, want)
				}
				if record.WALBefore.State == nil || record.WALAfter.State == nil || record.WALBefore.Token != nil || record.NativeAfter.WALIO == nil {
					t.Fatal("WAL evidence missing or invented")
				}
				if *record.NativeAfter.VMOps.Value <= *record.NativeBefore.VMOps.Value {
					t.Fatal("native counter did not advance")
				}
			}
		}
	}
}

func TestN1ComponentTimingOmitsNativeCounters(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	stream := n1ControlStream(t)
	var record *n1Attempt
	if !t.Run("attempt", func(t *testing.T) {
		runN1Attempt(t, t.Context(), stream, 1, n1Case{"active", "current", "present", "fixture"}, "timing", "", &record)
	}) {
		return
	}
	if !record.Complete || len(record.Units) == 0 || record.Clock.HoldNS == nil || record.Clock.BeginNS == nil {
		t.Fatal("timing lost bounded request observations/owner HOLD")
	}
	for _, unit := range record.Units {
		if unit.Before != nil || unit.After != nil || unit.VMOps.Value != nil {
			t.Fatal("timing cohort sampled native counters inside HOLD")
		}
	}
	if record.Profile == nil || len(record.Profile.PerID) == 0 || record.Profile.ProfileReady {
		t.Fatal("missing per-ID arrays or guessed profile readiness")
	}

}

func TestN1ComponentFailureRetainsOriginalAndRollback(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	stream := n1ControlStream(t)
	var query string
	for _, d := range scanInventoryFreshDefinitions() {
		if d.ID == scanFreshRun {
			query = d.SQL
		}
	}
	var record *n1Attempt
	if !t.Run("attempt", func(t *testing.T) {
		runN1Attempt(t, t.Context(), stream, 7, n1Case{"active", "current", "present", "fixture"}, "native-counters", query, &record)
	}) {
		return
	}
	if record.Complete || record.Ordinal != 7 || record.Reference == nil || !strings.Contains(record.Error, "injected N1") || record.Outcome != "rolled_back" {
		t.Fatal("partial failure was lost or renumbered")
	}
	if record.Clock.CommitNotAttempted == nil || !*record.Clock.CommitNotAttempted || record.Clock.CommitNS != nil || record.Clock.RollbackNS == nil || record.Clock.HoldNS == nil || !*record.NativeJoined || !*record.Retired {
		t.Fatal("failure terminal/retirement evidence lost")
	}
	raw, err := os.ReadFile(stream.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatal("incremental start/partial record missing")
	}
	for i, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["sequence"] != float64(i+1) || decoded["attempt_ordinal"] != float64(7) {
			t.Fatal("sequence/original attempt ordinal lost")
		}
	}
}

func TestN1ComponentLedgerOverflowIsExplicit(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	trace := newN1Trace(time.Now(), true)
	trace.armed, trace.used = true, n1UnitLimit
	c := &n1MeasuredConn{trace: trace}
	if c.start("not executed") != nil || !trace.overflow || trace.used != n1UnitLimit {
		t.Fatal("ledger overflow silently dropped")
	}
	for _, value := range []string{"active/current/present/fixture", "dual/dirty/absent/escaped"} {
		if _, err := parseN1Case(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"", "active/current/present", "active/current/present/MAX", "other/current/present/fixture"} {
		if _, err := parseN1Case(value); err == nil {
			t.Fatal("arbitrary case input accepted")
		}
	}
}

func TestN1ComponentEarlyFailureChild(t *testing.T) {
	path := os.Getenv("PORTICO_TEST_N1_EARLY_OUTPUT")
	if !*n1EnabledFlag || path == "" {
		return
	}
	f, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	stream := &n1Stream{file: f, origin: time.Now(), base: map[string]any{"process_epoch": "EARLY_CONTROL"}}
	var record *n1Attempt
	runN1Attempt(t, t.Context(), stream, 9, n1Case{"active", "current", "present", "fixture"}, "native-counters", "PRAGMA foreign_keys", &record)
	t.Fatal("setup fault did not stop the existing helper")
}

func TestN1ComponentEarlyFailureKeepsNullOwnerAndJoins(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "early.ndjson")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestN1ComponentEarlyFailureChild$", "-n1-component")
	cmd.Env = append(os.Environ(), "PORTICO_TEST_N1_EARLY_OUTPUT="+path)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("negative control unexpectedly passed: %s", output)
	}
	if ctx.Err() != nil {
		t.Fatal("early-failure child did not join", ctx.Err())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatal("partial attempt prefix missing")
	}
	var envelope struct {
		Kind    string    `json:"kind"`
		Attempt n1Attempt `json:"attempt"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope.Attempt
	if envelope.Kind != "attempt" || r.Ordinal != 9 || r.Reference == nil || r.Complete || r.Error == "" || r.Settings.AssertError == "" {
		t.Fatal("early failure/identity lost")
	}
	if r.Clock.HoldNS != nil || r.Clock.CommitNotAttempted != nil || r.Settings.Actual != nil || r.Settings.PolicyVerified != nil {
		t.Fatal("owner evidence manufactured after helper failure")
	}
	if r.NativeJoined == nil || !*r.NativeJoined {
		t.Fatal("early-failure native pool did not close")
	}
}

func TestN1ComponentExplicitOptInRequiresCompanions(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component controls")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestN1ComponentMeasurement$", "-n1-component")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PORTICO_WRITER_N1_COMPONENT=") && !strings.HasPrefix(entry, "PORTICO_WRITER_N1_COMPONENT_CONFIG=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "missing PORTICO_WRITER_N1_COMPONENT=1") || strings.Contains(string(output), "SKIP") {
		t.Fatalf("explicit opt-in did not fail for missing companion env: %v %s", err, output)
	}
}
