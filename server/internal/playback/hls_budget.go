package playback

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// reclaimPlayedForBudget removes only closed segments strictly behind demand.
// It preserves the current segment, every future segment and each init file.
func reclaimPlayedForBudget(dir string, served int, bytes, limit int64, files, fileLimit int) (int64, int) {
	if served <= 0 {
		return bytes, files
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return bytes, files
	}
	type candidate struct {
		name  string
		index int
		size  int64
	}
	candidates := []candidate{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		index, ok := hlsSegmentIndex(entry.Name())
		if !ok {
			if parts := renditionSegmentName.FindStringSubmatch(entry.Name()); parts != nil {
				index, _ = strconv.Atoi(parts[2])
				ok = true
			}
		}
		if !ok || index >= served {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			candidates = append(candidates, candidate{entry.Name(), index, info.Size()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].index < candidates[j].index })
	for _, c := range candidates {
		if bytes <= limit && files <= fileLimit {
			break
		}
		if os.Remove(filepath.Join(dir, c.name)) == nil {
			bytes -= c.size
			files--
		}
	}
	return bytes, files
}

func sessionFileLimit(mib int) int {
	// Retain the original runaway-file bound at the default, scaling it alongside
	// the owner's byte ceiling, with the seven-day manifest bound as the maximum.
	return min(HLSMaxSegments*17, max(audioArtifactFiles, mib))
}

func (h *HLS) highestServed(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	served := -1
	for key, w := range h.windows {
		if producerSession(key) == id {
			served = max(served, w.served)
		}
	}
	return served
}

func renditionPackaging(r AudioRenditionPlan) string {
	if r.Packaging != "" {
		return r.Packaging
	}
	switch r.Codec {
	case "flac", "opus", "truehd":
		return "fmp4_hls"
	}
	return "mpegts_hls"
}
func renditionSegmentFile(r AudioRenditionPlan, index int) string {
	ext := "ts"
	if renditionPackaging(r) == "fmp4_hls" {
		ext = "m4s"
	}
	return fmt.Sprintf("audio-%d-%06d.%s", r.Ordinal, index, ext)
}
func renditionInitFile(ordinal int) string { return fmt.Sprintf("audio-%d-init.mp4", ordinal) }
func isRenditionInit(name string) bool {
	return strings.HasPrefix(name, "audio-") && strings.HasSuffix(name, "-init.mp4")
}
