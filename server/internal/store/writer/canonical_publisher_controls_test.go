package writer

import (
	"encoding/json"
	"errors"
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

func TestCanonicalPublisherSameOwnerPopulationAndSustainedGate(t *testing.T) {
	// Tiny canonical transport control, not a1M or timing measurement. No accepting
	// command runs: the real, currently missing keeper binding must stop it.
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
			if !errors.Is(err, errCanonicalPublisherSustained) || result.Complete || result.Calls.CallsAttempted != 0 || result.NativeJoined == nil || !*result.NativeJoined {
				owner.Fatal("gate/join falsely passed", result, err)
			}
			if err := file.Close(); err != nil {
				owner.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				owner.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 3 {
				owner.Fatal("lost constructor prefix/census/gate", len(lines))
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
			var gate map[string]any
			if err := json.Unmarshal([]byte(lines[2]), &gate); err != nil {
				owner.Fatal(err)
			}
			if gate["kind"] != "failed_binding" || gate["native_calls_started"] != float64(0) || gate["hold_ns"] != nil || gate["commit_ns"] != nil {
				owner.Fatal("fake zero measurement", gate)
			}
		})
	}
}
