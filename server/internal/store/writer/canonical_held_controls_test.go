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
		ready := c.name == "inventory_n1" || c.name == "basic_accept" || c.name == "staged_enrich" || c.name == "progress" || c.name == "security" || c.name == "wc_reclaim" || c.name == "publisher_fragment" || c.name == "publisher_flip"
		want := 1
		if ready {
			want = 0
		}
		if len(canonicalHeldMissing(c)) != want || (c.run != nil) != ready {
			t.Fatal("unmerged seed/Action opened", c)
		}
		if c.name == "mp.queue_repeat" {
			if c.kind != "normal_write" || c.purpose == nil || *c.purpose != MPQueueRepeat {
				t.Fatal("repeat754 writer binding changed", c)
			}
		} else if (strings.HasPrefix(c.name, "mp.") || strings.HasPrefix(c.name, "rb.") || strings.HasPrefix(c.name, "rs.") || strings.HasPrefix(c.name, "uo.") || strings.HasPrefix(c.name, "IA-membership-")) && (c.kind != "foreground_read" || c.purpose != nil) {
			t.Fatal("read became writer class", c)
		}
	}
	if len(seen) != 28 {
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
	for _, class := range []string{"mp.queue_head", "uo.personal", "rb.numeric_advance", "rb.numeric_first_page", "rb.numeric_cached_page", "rb.group_window", "rb.relationship_exact", "rb.resolution", "rs.listening", "IA-membership-audio-artist", "IA-membership-audio-album", "IA-membership-audio-song", "IA-membership-audiobook-book", "IA-membership-audiobook-file"} {
		t.Run(class, func(t *testing.T) { canonicalBlockedReadPrefix(t, class) })
	}
}
func canonicalBlockedReadPrefix(t *testing.T, class string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "class.ndjson")
	file, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "class": class, "qualification": false}}
	definition, _ := canonicalHeldClassFor(class)
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
	var first, blocked, last map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked["kind"] != "failed_binding" || blocked["hold_ns"] != nil || blocked["commit_ns"] != nil {
		t.Fatal(blocked)
	}
	if canonicalReadRecipeFor(class) != nil {
		var start struct {
			ReadRecipe *canonicalReadRecipe `json:"read_recipe_intent"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
			t.Fatal(err)
		}
		if start.ReadRecipe == nil || start.ReadRecipe.SourceHead == "" || start.ReadRecipe.Qualification || len(start.ReadRecipe.Contracts) == 0 {
			t.Fatal("missing intent / accidental qualification", start)
		}
	}
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatal(err)
	}
	if first["class"] != class || first["target_kind"] != "foreground_read" || first["normal_hosted_p99_ns"] != nil || first["actual_library_items"] != nil || last["complete"] != false || last["report"] != nil || last["native_calls_started"] != float64(0) {
		t.Fatal(first, last)
	}
}
