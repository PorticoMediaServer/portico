package writer

import (
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalPublisherRecipeKeepsPopulationsDistinct(t *testing.T) {
	cfg := canonicalHeldConfig{RunID: "CONTROL", SourceCommit: strings.Repeat("a", 40), Profile: "standard", OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Attempts: 10000}
	for _, class := range []string{"publisher_fragment", "publisher_flip"} {
		cfg.Class = class
		c, recipe, err := canonicalPublisherConfig(cfg)
		if err != nil || recipe == "" || c.FreshCount != 10000 || c.WarmupCount != 8 || c.BackgroundCount != 0 {
			t.Fatal("implicit or conflated population", c, recipe, err)
		}
		definition, _ := canonicalHeldClassFor(class)
		if _, ok := definition.run.(canonicalFixtureRun); !ok {
			t.Fatal("constructor ownership runner missing")
		}
		if definition.purpose == nil || *definition.purpose != c.purpose() {
			t.Fatal("class/purpose mismatch")
		}
	}
	cfg.Attempts = 1000000
	if _, _, err := canonicalPublisherConfig(cfg); err == nil {
		t.Fatal("silently exceeded owner's fresh+warmup bound")
	}
	if !errors.Is(canonicalPublisherSustainedGate(nil), errCanonicalPublisherSustained) {
		t.Fatal("absent keeper opened a sustained target")
	}
}

func TestCanonicalPublisherFundedKeeperFunctional(t *testing.T) {
	if value := os.Getenv("PORTICO_CANONICAL_PB_FUNCTIONAL"); value == "" {
		t.Skip("opt-in real-clock publisher/funded-keeper functional control")
	} else if value != "1" {
		t.Fatal("PORTICO_CANONICAL_PB_FUNCTIONAL must be1")
	}
	// Tiny real-clock functional component, not a1M/10k/profile measurement.
	// Actual refusals/errors remain nonpassing; no default wall-clock assertion.
	dir, pin := canonicalN1ControlArtifact(t)
	for _, command := range []string{"SearchFragmentStage", "SearchHeadPublish"} {
		t.Run(command, func(owner *testing.T) {
			path := filepath.Join(owner.TempDir(), "raw.ndjson")
			file, err := openFirstLightOutput(path)
			if err != nil {
				owner.Fatal(err)
			}
			defer file.Close()
			stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "qualification": false, "process_epoch": "FUNCTIONAL_ONLY/" + command}}
			manifest := UseQualificationPopulation(owner, dir, pin) // actual constructor's EXACT t
			c := publisherMeasurementConfig{RunID: "FUNCTIONAL_ONLY", SourceCommit: strings.Repeat("c", 40), HostProfile: "functional-only", OutputPath: path, Command: command, FreshCount: 2, WarmupCount: 1, BackgroundCount: 1}
			raw, _ := json.Marshal(c)
			result := canonicalPublisherSummary{}
			err = runCanonicalPublisherOwner(owner, c, "FUNCTIONAL_ONLY", manifest, stream, raw, "functional-only", "not-a-source-qualification", &result)
			if err != nil || !result.Complete || result.Calls.FreshCompleted != 2 || result.Calls.CallsAttempted != 9 || result.NativeJoined == nil || !*result.NativeJoined || result.CheckpointCalls == nil || *result.CheckpointCalls < 1 || result.CheckpointComplete == nil || *result.CheckpointComplete != *result.CheckpointCalls {
				owner.Fatal("real original/keeper chain incomplete", result, err)
			}
			if err := file.Close(); err != nil {
				owner.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				owner.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) < 21 {
				owner.Fatal("missing real call/CP prefixes", len(lines))
			}
			var population struct {
				Kind       string           `json:"kind"`
				Seed       int              `json:"canonical_manifest_residents"`
				Before     map[string]int64 `json:"native_census_before_graphs"`
				After      map[string]int64 `json:"native_census_after_graphs"`
				Measured   int              `json:"prepared_measured_graphs"`
				Warmup     int              `json:"prepared_warmup_graphs"`
				Background int              `json:"prepared_background_graphs"`
			}
			if err := json.Unmarshal([]byte(lines[1]), &population); err != nil {
				owner.Fatal(err)
			}
			if population.Kind != "population_attached" || population.Seed != 16 || population.Before["catalog_entities"] < 16 || population.After["catalog_entities"] != population.Before["catalog_entities"]+4 || population.Measured != 2 || population.Warmup != 1 || population.Background != 1 {
				owner.Fatal("seed/overlay counts conflated", population)
			}
			calls, starts, checkpoints := 0, 0, 0
			for _, line := range lines {
				var value struct {
					Kind      string                      `json:"kind"`
					Physical  *int                        `json:"actual_physical_attempt_ordinal"`
					Publisher *publisherMeasurementRecord `json:"publisher"`
				}
				if err := json.Unmarshal([]byte(line), &value); err != nil {
					owner.Fatal(err)
				}
				switch value.Kind {
				case "attempt_start":
					starts++
				case "checkpoint":
					checkpoints++
				case "attempt":
					calls++
					if value.Physical == nil || *value.Physical != calls || value.Publisher == nil || !value.Publisher.Complete || value.Publisher.Evidence == nil || value.Publisher.PhysicalHoldNS == nil || value.Publisher.CallError != "" {
						owner.Fatal("unbound/partial real call", line)
					}
				case "failed_binding":
					owner.Fatal("keeper not attached", line)
				}
			}
			if calls != 9 || starts != 9 || checkpoints != *result.CheckpointCalls {
				owner.Fatal(calls, starts, checkpoints, result)
			}
		})
	}
}

func TestCanonicalPublisherMaintenancePreservesCaps(t *testing.T) {
	original := publisherFixtureWALPolicy()
	m := canonicalPublisherMaintenance()
	comparable := m.policy
	comparable.ConcurrentPassive = false
	comparable.CheckpointMaxHold = 0
	if comparable != original || !m.policy.ConcurrentPassive || m.policy.CheckpointMaxHold != 5*time.Second {
		t.Fatal("publisher WAL caps changed", m)
	}
	owner := canonicalN1MaintenanceOwner{shape: canonicalCheckpointShape{SearchFragmentStage, "canonical-publisher-cp", m.policy.HardBytes}, opening: &scanOpening{binding: &scanFileBinding{device: 7}}}
	request := owner.request("passive")
	if request.Need[governor.Reader] != 1 || request.Need[governor.IOBytes] != 2*m.policy.HardBytes || request.MaxHold != 5*time.Second || request.Budget.Pages != uint32(2*(m.policy.HardBytes/4120)+1) || m.need[governor.IOBytes] != 4*m.policy.HardBytes || m.need[governor.Reader] != 2 {
		t.Fatal("keeper/active/shutdown whole-prefix funding missing", request, m)
	}
}
