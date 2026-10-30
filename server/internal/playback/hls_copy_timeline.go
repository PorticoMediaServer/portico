package playback

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// A converted picture can be cut anywhere, because the encoder is told to put a
// keyframe on every grid line. A copied picture cannot: it can only be cut where
// the source already has a keyframe, and those fall where the person who encoded
// the file put them. Publishing a six-second grid for a copied stream therefore
// publishes a timeline the segments do not follow — segment 10 does not start at
// sixty seconds, the file has fewer segments than the playlist lists, and a
// converter restarted for a seek numbers its output differently from the one that
// started at zero. Players survive that by accident when keyframes happen to sit
// near the grid and fail when they do not.
//
// So a copy session publishes the file's own timeline. The cut points are the
// keyframes at or before each grid line, read from the container's index by
// seeking rather than by reading the file; every window cuts at exactly those
// points whatever position it started from; and the playlist states each
// segment's real length.

// copyTimeline is the cut list of one source: Boundaries[i] is where segment i
// starts, in seconds from the start of the file, and the last entry is the end.
type copyTimeline struct {
	Boundaries []float64 `json:"boundaries"`
}

func (t *copyTimeline) count() int {
	if t == nil || len(t.Boundaries) < 2 {
		return 0
	}
	return len(t.Boundaries) - 1
}

// indexAt is the segment that contains a position.
func (t *copyTimeline) indexAt(seconds float64) int {
	n := t.count()
	if n == 0 || seconds <= 0 {
		return 0
	}
	i := sort.SearchFloat64s(t.Boundaries, seconds+1e-6) - 1
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

const (
	// copyTimelineMaxSegment refuses a source whose keyframes are so sparse that
	// one copied segment would be longer than a player will buffer in one request.
	copyTimelineMaxSegment = 30.0
	// copyTimelineMinSegment is the minimum spacing between keyframe cuts;
	// closer cuts are folded into their neighbor.
	copyTimelineMinSegment = 1.0
	// copyTimelineIntervals bounds one ffprobe invocation's argument length; a
	// long title is indexed in several bounded invocations.
	copyTimelineIntervals = 1000
	copyTimelineTimeout   = 20 * time.Second
	// copyWindowSegments bounds how many cut points one producer run is given, so
	// the argument list stays well inside the Windows command-line limit. A run
	// that reaches the end of its list stops and the next request continues.
	copyWindowSegments = 900
)

var errCopyTimeline = errors.New("source keyframes cannot be indexed for copying")

// buildCopyTimeline validates raw keyframe observations into a cut list.
// keyframes[k] is the keyframe found at or before grid line k (seconds from the
// start of the file); ok[k] is false where the demuxer did not answer with a
// keyframe. Any doubt refuses the whole timeline: a session that cannot be cut
// faithfully is converted instead, which always can be.
func buildCopyTimeline(duration float64, keyframes []float64, ok []bool) (*copyTimeline, error) {
	if !(duration > 0) || len(keyframes) != len(ok) {
		return nil, errCopyTimeline
	}
	boundaries := []float64{0}
	previous := 0.0
	for k := range keyframes {
		if !ok[k] {
			return nil, errCopyTimeline
		}
		at := keyframes[k]
		line := float64(k+1) * HLSSegmentSeconds
		// The answer to "the keyframe at or before this line" is never after it.
		if math.IsNaN(at) || at > line+0.05 {
			return nil, errCopyTimeline
		}
		// Answers for later lines never go backwards, folded or not.
		if at < previous-0.001 {
			return nil, errCopyTimeline
		}
		previous = at
		last := boundaries[len(boundaries)-1]
		// Two grid lines inside one group of pictures share a keyframe, and a cut
		// closer than a second to the previous one or to the end is folded into
		// its neighbour. The first case matters at the start: when the picture
		// begins just after the file does (audio encoder priming puts the file's
		// start a few milliseconds earlier), the first keyframe would otherwise
		// become a cut of its own and publish a segment with no picture in it.
		if at < last+copyTimelineMinSegment || at >= duration-copyTimelineMinSegment {
			continue
		}
		boundaries = append(boundaries, at)
	}
	boundaries = append(boundaries, duration)
	for i := 1; i < len(boundaries); i++ {
		if boundaries[i]-boundaries[i-1] > copyTimelineMaxSegment {
			return nil, errCopyTimeline
		}
	}
	if len(boundaries)-1 > HLSMaxSegments {
		return nil, errCopyTimeline
	}
	return &copyTimeline{Boundaries: boundaries}, nil
}

// timelineProbe reads a bounded index query. Packet scans have a separate
// streaming runner, which is deliberately absent for retained remote windows.
type timelineProbe func(context.Context, ...string) ([]byte, error)
type timelinePacketProbe func(context.Context, io.Writer) error

func localTimelineProbe(ffprobe string, input *assets.PlaybackInput) timelineProbe {
	if ffprobe == "" || input == nil {
		return nil
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return runProbe(ctx, ffprobe, input, args...)
	}
}

// probeCopyTimeline asks the container index where the keyframes are. Each read
// interval seeks to one grid line and reads a single video packet, so the cost is
// one seek per six seconds of title and does not depend on the file's size.
func probeCopyTimeline(ctx context.Context, probe timelineProbe, packets timelinePacketProbe, duration float64) (*copyTimeline, error) {
	if probe == nil || !(duration > 0) {
		return nil, errCopyTimeline
	}
	lines := int(math.Ceil(duration/HLSSegmentSeconds)) - 1
	if lines < 0 {
		lines = 0
	}
	if lines > HLSMaxSegments {
		return nil, errCopyTimeline
	}
	ctx, cancel := context.WithTimeout(ctx, copyTimelineTimeout)
	defer cancel()
	start, err := probeFormatStart(ctx, probe)
	if err != nil {
		return nil, err
	}
	keyframes := make([]float64, lines)
	ok := make([]bool, lines)
	for from := 0; from < lines; from += copyTimelineIntervals {
		to := from + copyTimelineIntervals
		if to > lines {
			to = lines
		}
		intervals := make([]string, 0, to-from)
		for k := from; k < to; k++ {
			intervals = append(intervals, strconv.FormatFloat(float64(k+1)*HLSSegmentSeconds+start, 'f', 6, 64)+"%+#1")
		}
		out, runErr := probe(ctx, "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", "-read_intervals", strings.Join(intervals, ","))
		if runErr != nil {
			if ctx.Err() == nil {
				if packets == nil {
					return nil, errCopyTimeline
				}
				return probePacketTimeline(ctx, packets, start, duration)
			}
			return nil, runErr
		}
		rows := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(rows) != to-from {
			if packets == nil {
				return nil, errCopyTimeline
			}
			return probePacketTimeline(ctx, packets, start, duration)
		}
		for i, row := range rows {
			stamp, flags, _ := strings.Cut(strings.TrimSpace(row), ",")
			value, parseErr := strconv.ParseFloat(stamp, 64)
			if parseErr != nil || !strings.HasPrefix(flags, "K") {
				continue
			}
			keyframes[from+i], ok[from+i] = value-start, true
		}
	}
	timeline, err := buildCopyTimeline(duration, keyframes, ok)
	if err != nil && ctx.Err() == nil {
		if packets == nil {
			return nil, errCopyTimeline
		}
		return probePacketTimeline(ctx, packets, start, duration)
	}
	return timeline, err
}

func probeFormatStart(ctx context.Context, probe timelineProbe) (float64, error) {
	out, err := probe(ctx, "-show_entries", "format=start_time", "-of", "csv=p=0")
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" || text == "N/A" {
		return 0, nil
	}
	start, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(start) || math.Abs(start) > 86400 {
		return 0, errCopyTimeline
	}
	return start, nil
}

func runProbe(ctx context.Context, ffprobe string, input *assets.PlaybackInput, args ...string) ([]byte, error) {
	ctx, releaseInput := input.BorrowContext(ctx)
	defer releaseInput()
	full := append([]string{"-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", mediaexec.LocalInputFormats}, args...)
	full = append(full, input.Options...)
	full = append(full, input.Argument)
	// Where /dev/fd shares this process's file offset, the previous child left it
	// wherever it stopped reading.
	if input.File != nil {
		_, _ = input.File.Seek(0, io.SeekStart)
	}
	if input.Validate != nil {
		if err := validateOwnedInput(ctx, input.Validate); err != nil {
			return nil, err
		}
	}
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffprobe, Args: full, Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths()})
	if err != nil {
		return nil, errCopyTimeline
	}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{buffer: &out, limit: 1 << 20}
	if err := childprocess.Run(ctx, cmd); err != nil {
		return nil, errCopyTimeline
	}
	if input.Validate != nil {
		if err := validateOwnedInput(ctx, input.Validate); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

type limitedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > w.limit {
		return 0, errors.New("output exceeds limit")
	}
	return w.buffer.Write(p)
}

// copyTimelineManifest publishes a copied title's whole timeline with each
// segment's real length. Version 3 allows fractional durations; the target
// duration is the longest segment rounded up, as the specification requires.
func copyTimelineManifest(t *copyTimeline) []byte {
	longest := 1.0
	for i := 0; i < t.count(); i++ {
		if d := t.Boundaries[i+1] - t.Boundaries[i]; d > longest {
			longest = d
		}
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:" + strconv.Itoa(int(math.Ceil(longest))) + "\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < t.count(); i++ {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s\n", t.Boundaries[i+1]-t.Boundaries[i], hlsSegmentFile(i))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

func writeManifest(dir string, manifest []byte) error {
	path := filepath.Join(dir, "master.m3u8")
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, manifest) {
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

// loadCopyTimeline reads a cached cut list for exactly this file identity.
func loadCopyTimeline(ctx context.Context, db *sql.DB, asset string, size, modified int64) (*copyTimeline, error) {
	var raw string
	err := dbwork.QueryRow(ctx, db, `SELECT boundaries FROM playback_copy_timelines WHERE asset_id=? AND size=? AND modified_ns=?`, asset, size, modified).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t copyTimeline
	if json.Unmarshal([]byte(raw), &t) != nil || t.count() == 0 {
		return nil, nil
	}
	return &t, nil
}

func storeCopyTimeline(ctx context.Context, db *sql.DB, asset string, size, modified int64, t *copyTimeline) {
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > 1<<20 {
		return
	}
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = dbwork.ExecWrite(write, db, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_copy_timelines(asset_id,size,modified_ns,boundaries,created_at_ms) VALUES(?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,boundaries=excluded.boundaries,created_at_ms=excluded.created_at_ms`, asset, size, modified, string(raw), time.Now().UnixMilli())
}

// copySegmentArgs is the muxer half of a copy window. The segment muxer is used
// instead of the HLS muxer because only it takes an explicit cut list; the HLS
// muxer cuts relative to wherever the run began, which is what made a restarted
// window disagree with the first one. Output goes to a staging directory and is
// promoted segment by segment, because the segment muxer has no temporary-file
// mode and a half-written segment must never be served.
func copySegmentArgs(t *copyTimeline, from int, staging string, fragmented ...bool) []string {
	last := from + copyWindowSegments
	if last > t.count() {
		last = t.count()
	}
	// Cut times are measured from the start of the run, and the absolute source
	// clock is restored inside each segment by the inner muxer. The segment muxer
	// has compared its cut list against run-relative time in some FFmpeg versions
	// and against output time in others; keeping the outer clock at zero makes the
	// two the same thing, so this does not depend on which one is running.
	origin := t.Boundaries[from]
	times := make([]string, 0, last-from)
	for i := from + 1; i < last; i++ {
		times = append(times, strconv.FormatFloat(t.Boundaries[i]-origin, 'f', 6, 64))
	}
	args := []string{"-f", "segment", "-segment_format", "mpegts", "-segment_format_options", "output_ts_offset=" + strconv.FormatFloat(origin, 'f', 6, 64), "-segment_start_number", strconv.Itoa(from), "-segment_time_delta", "0.05", "-reset_timestamps", "0", "-break_non_keyframes", "0"}
	if len(times) > 0 {
		args = append(args, "-segment_times", strings.Join(times, ","))
	} else {
		// One segment left: a cut time beyond the end keeps it in one piece.
		args = append(args, "-segment_time", "86400")
	}
	if len(fragmented) > 0 && fragmented[0] {
		args[3] = "mp4"
		args[5] = "movflags=+frag_keyframe+empty_moov+default_base_moof:avoid_negative_ts=disabled:output_ts_offset=" + strconv.FormatFloat(origin, 'f', 6, 64)
		return append(args, filepath.Join(staging, "segment-%06d.mp4"))
	}
	return append(args, filepath.Join(staging, "segment-%06d.ts"))
}

// promoteCopySegments moves finished segments from a window's staging directory
// into the session directory. A segment is finished when its successor exists,
// or when the run that wrote it has ended cleanly (final). Returns the highest
// index promoted, or from-1.
func promoteCopySegments(staging, dir string, from int, final bool) int {
	highest := from - 1
	for i := from; i < HLSMaxSegments; i++ {
		name := hlsSegmentFile(i)
		source := filepath.Join(staging, name)
		info, err := os.Stat(source)
		if err != nil {
			// Already promoted on an earlier tick, or not written yet.
			if _, done := os.Stat(filepath.Join(dir, name)); done == nil && i == highest+1 {
				highest = i
				continue
			}
			break
		}
		if !final {
			if _, next := os.Stat(filepath.Join(staging, hlsSegmentFile(i+1))); next != nil {
				break
			}
		}
		if info.Size() == 0 {
			break
		}
		if os.Rename(source, filepath.Join(dir, name)) != nil {
			break
		}
		highest = i
	}
	return highest
}
