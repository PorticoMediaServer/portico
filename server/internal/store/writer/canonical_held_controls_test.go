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

func TestCanonicalHeldClosedRegistryKeepsAllBindingsExplicit(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range canonicalHeldClasses() {
		if seen[c.name] || c.name == "" || c.owner == "" {
			t.Fatal("duplicate/empty class", c)
		}
		seen[c.name] = true
		want := 1
		if c.name == "inventory_n1" {
			want = 0
		}
		if len(canonicalHeldMissing(c)) != want || (c.run != nil) != (c.name == "inventory_n1") {
			t.Fatal("unmerged seed/Action opened", c)
		}
		if (strings.HasPrefix(c.name, "mp.") || strings.HasPrefix(c.name, "rb.")) && (c.kind != "foreground_read" || c.purpose != nil) {
			t.Fatal("read became writer class", c)
		}
	}
	if len(seen) != 14 {
		t.Fatal(seen)
	}
	if _, err := canonicalHeldClassFor("caller_sql"); err == nil {
		t.Fatal("caller-selected class")
	}
}
func TestCanonicalHeldConfigRejectsSmallSamplingAndIdentityDrift(t *testing.T) {
	c := canonicalHeldConfig{RunID: "CONTROL", HostEpoch: "CONTROL", SourceCommit: strings.Repeat("a", 40), OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Class: "progress", Profile: "standard", Attempts: 10000, ArtifactDirectory: filepath.Join(t.TempDir(), "seed"), ManifestSHA256: strings.Repeat("b", 64)}
	raw, _ := json.Marshal(c)
	if _, err := decodeCanonicalHeldConfig(raw); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*canonicalHeldConfig){func(c *canonicalHeldConfig) { c.Attempts = 64 }, func(c *canonicalHeldConfig) { c.Class = "unknown" }, func(c *canonicalHeldConfig) { c.ManifestSHA256 = "computed from untrusted file" }} {
		bad := c
		mutate(&bad)
		data, _ := json.Marshal(bad)
		if _, err := decodeCanonicalHeldConfig(data); err == nil {
			t.Fatal("accepted invalid target config", bad)
		}
	}
	duplicate := strings.TrimSuffix(string(raw), "}") + `,"attempts":10000}`
	if _, err := decodeCanonicalHeldConfig([]byte(duplicate)); err == nil {
		t.Fatal("duplicate pin/config field")
	}
	extra := strings.TrimSuffix(string(raw), "}") + `,"sql":"SELECT 1"}`
	if _, err := decodeCanonicalHeldConfig([]byte(extra)); err == nil {
		t.Fatal("caller SQL input")
	}
}

func TestCanonicalHeldBlockedPrefixIsDurableAndNotFakeZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "class.ndjson")
	file, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "qualification": false}}
	definition, _ := canonicalHeldClassFor("mp.queue_head")
	err = emitCanonicalHeldPrefix(stream, canonicalHeldConfig{Attempts: 10000}, definition)
	if !errors.Is(err, errCanonicalHeldBinding) {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatal(string(raw))
	}
	var first, last map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatal(err)
	}
	if first["target_kind"] != "foreground_read" || first["normal_hosted_p99_ns"] != nil || first["actual_library_items"] != nil || last["complete"] != false || last["report"] != nil || last["native_calls_started"] != float64(0) {
		t.Fatal(first, last)
	}
}
