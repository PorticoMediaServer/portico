package playback

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// HLSSegmentSeconds is the fixed conversion grid. Every session, every window
// and every restart cuts on the same source-relative boundaries, which is what
// lets a seek jump the producer forward without renumbering anything.
const HLSSegmentSeconds = 6

// HLSMaxSegments bounds manifests to seven days (about 5 MiB per rendition).
// Longer items are explicitly refused before publication, never truncated.
const HLSMaxSegments = 100800

func hlsDurationSupported(duration float64) bool {
	return duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) && duration <= HLSMaxSegments*HLSSegmentSeconds
}

var hlsSegmentName = regexp.MustCompile(`^segment-([0-9]{6})\.(?:ts|m4s)$`)

// hlsSegmentIndex returns the grid index of a segment file name.
func hlsSegmentIndex(name string) (int, bool) {
	m := hlsSegmentName.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 || n >= HLSMaxSegments {
		return 0, false
	}
	return n, true
}

func hlsSegmentFile(index int) string { return fmt.Sprintf("segment-%06d.ts", index) }

// hlsSegmentCount is the number of grid slots one duration occupies.
func hlsSegmentCount(duration float64) int {
	if !hlsDurationSupported(duration) {
		return 0
	}
	n := int(math.Ceil(duration / HLSSegmentSeconds))
	if n < 1 {
		n = 1
	}
	return n
}

// hlsTimelineManifest writes the whole title up front, on the fixed grid.
//
// The manifest is authored by the server rather than by the converter, because
// the converter only knows the window it is producing. Publishing the complete
// timeline is what makes a seek a plain segment request: the player asks for the
// slot it wants, and the server decides whether that slot already exists or the
// producer has to be moved. No discontinuity tag is needed, and none is emitted:
// every window writes absolute source timestamps, so all segments share one
// continuous presentation clock however many times conversion restarted.
func hlsTimelineManifest(duration float64) []byte {
	count := hlsSegmentCount(duration)
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:" + strconv.Itoa(HLSSegmentSeconds+1) + "\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	remaining := duration
	for i := 0; i < count; i++ {
		length := float64(HLSSegmentSeconds)
		if remaining < length {
			length = remaining
		}
		if length <= 0 {
			length = float64(HLSSegmentSeconds)
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s\n", length, hlsSegmentFile(i))
		remaining -= HLSSegmentSeconds
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

// writeTimelineManifest publishes the index atomically. The manifest is a pure
// function of the duration, so a re-publication with identical bytes is a no-op
// and a changed duration is a changed session, never a rewritten one.
func writeTimelineManifest(dir string, duration float64) error {
	if !hlsDurationSupported(duration) {
		return errors.New("hls_duration_unsupported")
	}
	path := filepath.Join(dir, "master.m3u8")
	manifest := hlsTimelineManifest(duration)
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(manifest) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(path+".tmp", manifest, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// highestProducedSegment reports the last contiguous slot produced from start.
// Contiguity matters: a player that is handed slot N must be able to read every
// slot between the window start and N without another restart.
func highestProducedSegment(dir string, start int) int {
	highest := start - 1
	for i := start; i < HLSMaxSegments; i++ {
		info, err := os.Stat(filepath.Join(dir, hlsSegmentFile(i)))
		if err != nil || info.Size() == 0 {
			if !segmentReady(filepath.Join(dir, fmt.Sprintf("segment-%06d.m4s", i))) {
				break
			}
		}
		highest = i
	}
	return highest
}

// prunePlayedSegments reclaims slots the viewer has passed. Retention is counted
// backwards from the slot actually served, so a viewer who steps back a little
// still finds the bytes; a viewer who steps back past the retention window pays
// for a restart, which is the trade the setting exists to make.
// from is the floor already reclaimed, so a long title does not re-walk every
// played slot on every segment request; the work per request stays proportional
// to how far the viewer advanced, not to how far they have come.
func prunePlayedSegments(dir string, from, served, retentionSeconds int) (int, int) {
	if retentionSeconds <= 0 || served <= 0 {
		return 0, from
	}
	keep := (retentionSeconds + HLSSegmentSeconds - 1) / HLSSegmentSeconds
	before := served - keep
	if before <= from {
		return 0, from
	}
	removed := 0
	for i := from; i < before; i++ {
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("segment-%06d.m4s", i)))
		if err := os.Remove(filepath.Join(dir, hlsSegmentFile(i))); err == nil {
			removed++
		}
	}
	return removed, before
}

func fragmentedManifest(media []byte) []byte {
	s := strings.ReplaceAll(string(media), "#EXT-X-VERSION:3", "#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"")
	return []byte(strings.ReplaceAll(s, ".ts\n", ".m4s\n"))
}
func publishRenditionInit(dir string, ordinal int, data []byte) error {
	target := filepath.Join(dir, renditionInitFile(ordinal))
	if old, err := readConversionInit(target); err == nil {
		if string(old) != string(data) {
			return errMP4InitChanged
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeNamedManifest(dir, renditionInitFile(ordinal), data)
}

// Init files are only read after a closed media segment proves the muxer has
// finished writing them. Bound reads separately from the owner storage budget.
func readConversionInit(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, errMP4Box
	}
	if _, err = mp4Timescales(data); err != nil {
		return nil, err
	}
	return data, nil
}
