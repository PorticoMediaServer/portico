package playback

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHLSTimelineManifestNumbersTheWholeTitle(t *testing.T) {
	// 100 seconds on a six-second grid is 17 slots, the last one short.
	manifest := string(hlsTimelineManifest(100))
	if !strings.HasPrefix(manifest, "#EXTM3U\n") {
		t.Fatal("manifest does not start with the tag")
	}
	for _, tag := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST", "#EXT-X-MEDIA-SEQUENCE:0", "#EXT-X-INDEPENDENT-SEGMENTS", "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES", "#EXT-X-TARGETDURATION:7"} {
		if !strings.Contains(manifest, tag) {
			t.Fatal("manifest is missing", tag)
		}
	}
	// One continuous timeline: restarts never introduce a discontinuity, because
	// every window writes absolute source timestamps.
	if strings.Contains(manifest, "#EXT-X-DISCONTINUITY") {
		t.Fatal("VOD grid published a discontinuity")
	}
	if strings.Contains(manifest, "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatal("grid manifest published as a growing playlist")
	}
	lines := []string{}
	for _, line := range strings.Split(manifest, "\n") {
		if strings.HasPrefix(line, "segment-") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 17 {
		t.Fatal("published", len(lines), "segments for 100 seconds")
	}
	for i, name := range lines {
		if name != hlsSegmentFile(i) {
			t.Fatalf("slot %d is named %q", i, name)
		}
	}
	if !strings.Contains(manifest, "#EXTINF:4.000000,\nsegment-000016.ts") {
		t.Fatal("final short slot was not published at its real length", manifest)
	}
	if hlsSegmentCount(0) != 0 || hlsSegmentCount(-1) != 0 {
		t.Fatal("unknown duration produced a grid")
	}
	if hlsSegmentCount(1) != 1 || hlsSegmentCount(6) != 1 || hlsSegmentCount(7) != 2 {
		t.Fatal("grid boundaries are wrong")
	}
}

func TestHLSSegmentIndexParsing(t *testing.T) {
	for _, name := range []string{"segment-000000.ts", "segment-000017.ts", "segment-007199.ts"} {
		index, ok := hlsSegmentIndex(name)
		if !ok || hlsSegmentFile(index) != name {
			t.Fatal("round trip failed for", name)
		}
	}
	for _, name := range []string{"master.m3u8", "segment-17.ts", "segment-000017.tsx", "../segment-000001.ts", "segment-999999.ts"} {
		if _, ok := hlsSegmentIndex(name); ok {
			t.Fatal("accepted", name)
		}
	}
}

// A seek beyond the converted edge restarts the producer at the requested slot.
// The grid is absolute, so the restarted window numbers its output exactly where
// the player asked for it and everything already converted stays valid.
func TestHLSSeekRestartSegmentNumbering(t *testing.T) {
	dir := t.TempDir()
	write := func(index int) {
		if err := os.WriteFile(filepath.Join(dir, hlsSegmentFile(index)), []byte(strconv.Itoa(index)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A window from slot 0 produced the first four slots.
	for i := 0; i < 4; i++ {
		write(i)
	}
	if got := highestProducedSegment(dir, 0); got != 3 {
		t.Fatal("contiguous edge misread", got)
	}
	// The viewer seeks to 10 minutes: slot 100. Nothing there yet.
	if got := highestProducedSegment(dir, 100); got != 99 {
		t.Fatal("empty window reported production", got)
	}
	// The restarted window writes slot 100 onwards, with no renumbering.
	for i := 100; i < 103; i++ {
		write(i)
	}
	if got := highestProducedSegment(dir, 100); got != 102 {
		t.Fatal("restarted window edge misread", got)
	}
	// The earlier slots are untouched, so seeking back is a file read.
	if got := highestProducedSegment(dir, 0); got != 3 {
		t.Fatal("restart discarded already-converted output", got)
	}
	// A hole stops the contiguous edge: a player must never be handed a slot it
	// cannot reach the previous one from.
	write(105)
	if got := highestProducedSegment(dir, 100); got != 102 {
		t.Fatal("edge jumped a hole", got)
	}
}

func TestHLSPlayedRetentionPrunesBehindThePlayhead(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 60; i++ {
		if err := os.WriteFile(filepath.Join(dir, hlsSegmentFile(i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 300 seconds of retention is 50 slots on a six-second grid; serving slot 55
	// leaves slots 5..59 and reclaims 0..4.
	removed, reclaimed := prunePlayedSegments(dir, 0, 55, 300)
	if removed != 5 || reclaimed != 5 {
		t.Fatal("pruned", removed, "slots to", reclaimed)
	}
	// A second pass from the recorded floor does no work at all.
	if again, _ := prunePlayedSegments(dir, reclaimed, 55, 300); again != 0 {
		t.Fatal("re-walked already reclaimed slots", again)
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, hlsSegmentFile(i))); err == nil {
			t.Fatal("played slot", i, "was retained")
		}
	}
	for i := 5; i < 60; i++ {
		if _, err := os.Stat(filepath.Join(dir, hlsSegmentFile(i))); err != nil {
			t.Fatal("retained slot", i, "was reclaimed")
		}
	}
	// Retention is counted backwards from the slot served, so a viewer who has
	// not reached the window yet loses nothing.
	if inside, _ := prunePlayedSegments(dir, 0, 10, 300); inside != 0 {
		t.Fatal("pruned inside the retention window")
	}
	// Retention off keeps everything.
	if off, _ := prunePlayedSegments(dir, 0, 59, 0); off != 0 {
		t.Fatal("pruned with retention disabled")
	}
}

func TestHLSTimelineManifestIsWrittenAtomicallyAndIdempotently(t *testing.T) {
	dir := t.TempDir()
	if err := writeTimelineManifest(dir, 100); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if err = writeTimelineManifest(dir, 100); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("republication changed an immutable index")
	}
	if _, err = os.Stat(filepath.Join(dir, "master.m3u8.tmp")); err == nil {
		t.Fatal("temporary index was left behind")
	}
}

func TestThrottleAndRetentionDefaults(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	if cfg.ThrottleBufferSeconds != 60 || cfg.PlayedRetentionSeconds != 300 {
		t.Fatal("published defaults changed", cfg.ThrottleBufferSeconds, cfg.PlayedRetentionSeconds)
	}
	// Out-of-range stored values fall back per field rather than failing.
	broken := DeliveryConfiguration{ThrottleBufferSeconds: 2, PlayedRetentionSeconds: -1, HardwareBackend: "nonsense", ToneMapAlgorithm: "nonsense", SoftwarePreset: "nonsense", PlanningPolicy: "nonsense"}.Normalized()
	if broken.ThrottleBufferSeconds != 60 || broken.PlayedRetentionSeconds != 300 || broken.HardwareBackend != "auto" || broken.PlanningPolicy != PlanningMaximumFidelity {
		t.Fatal("invalid configuration was not repaired", broken)
	}
	if deliveryConfiguration(nil).ThrottleBufferSeconds != 60 {
		t.Fatal("nil settings source did not fall back to the defaults")
	}
}
