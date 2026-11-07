package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func canonicalN1ControlArtifact(t *testing.T) (string, string) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "artifact")
	cfg := qualificationPopulationExportConfig{OutputDir: output, SourceCommit: strings.Repeat("c", 40), Seed: 0, Residents: 16}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "export.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTICO_QUAL_FIXTURE", "1")
	t.Setenv("PORTICO_QUAL_FIXTURE_CONFIG", path)
	TestExportQualificationPopulation(t)
	raw, err = os.ReadFile(filepath.Join(output, qualificationPopulationManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(raw)
	return output, hex.EncodeToString(pin[:])
}

func TestCanonicalN1PopulationAndFundedKeeper(t *testing.T) {
	dir, pin := canonicalN1ControlArtifact(t)
	path := filepath.Join(t.TempDir(), "raw.ndjson")
	file, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "process_epoch": "FUNCTIONAL_ONLY", "qualification": false}}
	manifest := UseQualificationPopulation(t, dir, pin) // same exact test owns next constructor
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 3, 1, "timing", n1SteadyControls{clock: time.Now, canonical: &manifest}, summary)
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if !summary.Complete || summary.ActualLibraryItems == nil || *summary.ActualLibraryItems != 16 || summary.Measured.FreshBegins != 3 || summary.KnownPhysicalTurns != 12 || summary.CheckpointCalls < 1 || summary.CheckpointCalls != summary.CheckpointComplete || summary.CheckpointOverflow || summary.NativeJoined == nil || !*summary.NativeJoined {
		t.Fatalf("native canonical composition: %+v", summary)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	attempts, starts, checkpoints := 0, 0, 0
	refs := map[int]ReceiptReference{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var record struct {
			Kind       string                 `json:"kind"`
			Attempt    *n1Attempt             `json:"attempt"`
			Checkpoint *canonicalN1Checkpoint `json:"checkpoint"`
		}
		if err = json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		switch record.Kind {
		case "attempt_start":
			starts++
		case "checkpoint":
			checkpoints++
			if record.Checkpoint == nil || !record.Checkpoint.Result.DatabaseMatched || !record.Checkpoint.Result.PhysicalSafe || record.Checkpoint.Result.Settings.Engine == "" {
				t.Fatal("unproven maintenance", string(line))
			}
		case "attempt":
			attempts++
			a := record.Attempt
			if a == nil || !a.Complete || a.Outcome != "committed" || a.Reference == nil || a.Clock.HoldNS == nil || a.Settings.Actual == nil || a.Settings.ConnectionID == nil || a.Settings.Actual.Synchronous != 1 || a.PhysicalAttemptOrdinal == nil || *a.PhysicalAttemptOrdinal != attempts || a.Reference.Identity.Sequence != int64(18+*a.OriginalOrdinal) {
				t.Fatal("missing actual physical/reference facts", string(line))
			}
			ordinal := *a.OriginalOrdinal
			if prior, ok := refs[ordinal]; ok && prior != *a.Reference {
				t.Fatal("Resolve/ACK changed original")
			}
			refs[ordinal] = *a.Reference
			if a.TurnRole == "ack" && (a.CurrentSnapshot == nil || a.AcknowledgedThrough == nil || a.CurrentSnapshot.LastSequence != *a.AcknowledgedThrough) {
				t.Fatal("fake ACK", string(line))
			}
		}
	}
	if attempts != 12 || starts != 12 || checkpoints != summary.CheckpointCalls || len(refs) != 4 {
		t.Fatal(attempts, starts, checkpoints, refs)
	}
	// Re-open the unchanged artifact only through its manifest reader, no DB open.
	original, err := readQualificationPopulation(dir, pin)
	if err != nil || original.DatabaseSHA256 != manifest.DatabaseSHA256 {
		t.Fatal("artifact mutated", err)
	}
}

func TestCanonicalN1MaintenanceKeepsCommandCaps(t *testing.T) {
	m := canonicalN1Maintenance()
	if m.policy.HardBytes != 1<<29 || m.policy.ReserveBytes != 1<<20 || m.policy.SoftDebtBytes != 1<<26 || m.policy.ActivePressureBytes != 1<<28 || !m.policy.ConcurrentPassive || m.policy.CheckpointMaxHold != 5*time.Second {
		t.Fatal(m)
	}
	// Requested sample count is a streaming census; never a per-command cap.
	c, _ := canonicalHeldClassFor("inventory_n1")
	if c.run == nil || len(canonicalHeldMissing(c)) != 0 {
		t.Fatal("merged binding still closed")
	}
	for _, name := range []string{"progress", "security", "wc_reclaim"} {
		c, _ = canonicalHeldClassFor(name)
		if c.run != nil || len(canonicalHeldMissing(c)) != 1 {
			t.Fatal("unreviewed WC path opened", name)
		}
	}
}

func TestCanonicalN1CheckpointClockSharesOriginAndPreservesMissing(t *testing.T) {
	origin := time.Now() // carries the real monotonic origin, like the stream
	record := canonicalN1Checkpoint{Result: governor.CheckpointResult{
		Started: origin, NativeStarted: origin.Add(7 * time.Nanosecond),
		NativeEnded: origin.Add(11 * time.Nanosecond), PhysicalReturned: origin.Add(13 * time.Nanosecond), RetiredAt: origin.Add(17 * time.Nanosecond),
	}}
	record.observeClock(origin)
	for i, actual := range []*int64{record.Clock.StartedNS, record.Clock.NativeStartedNS, record.Clock.NativeEndedNS, record.Clock.PhysicalReturnedNS, record.Clock.RetiredNS} {
		want := []int64{0, 7, 11, 13, 17}[i]
		if actual == nil || *actual != want {
			t.Fatalf("phase%d origin join: %v want%d", i, actual, want)
		}
	}
	// A refused/partial call keeps reached phases and nulls missing phases. An
	// observed0 at the origin must survive JSON as0, never null/omitted.
	record.Result.NativeStarted = time.Time{}
	record.Result.NativeEnded = time.Time{}
	record.Result.PhysicalReturned = time.Time{}
	record.Result.RetiredAt = time.Time{}
	record.observeClock(origin)
	raw, err := json.Marshal(record.Clock)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]*int64
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || fields["started_ns"] == nil || *fields["started_ns"] != 0 {
		t.Fatal(string(raw))
	}
	for _, key := range []string{"native_started_ns", "native_ended_ns", "physical_returned_ns", "retired_ns"} {
		value, present := fields[key]
		if !present || value != nil {
			t.Fatal("missing phase became measured", string(raw))
		}
	}
}
