package assets

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strconv"
	"strings"
	"time"
)

// PlaybackAnalysisRevision invalidates earlier scan results on the next bounded inventory pass.
const PlaybackAnalysisRevision = "playback_facts:4"

func (p Probe) analysisOutput(ctx context.Context, path string, limit int, args ...string) ([]byte, error) {
	if p.ReadGuard != nil {
		if e := p.ReadGuard(ctx, path); e != nil {
			return nil, e
		}
	}
	binary := p.Binary
	if binary == "" {
		binary = mediatools.Resolve("ffprobe")
	}
	args = append([]string{"-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", mediaexec.LocalInputFormats, "-probesize", "8388608", "-analyzeduration", "5000000"}, args...)
	args = append(args, path)
	out := &boundedBuffer{limit: limit}
	var e error
	if handled, err := storage.RunScanCommand(ctx, binary, args, path, out); handled {
		e = err
	} else if p.Supervisor != nil {
		var cmd *exec.Cmd
		if cmd, e = mediaexec.Command(probeJob(binary, args, path)); e == nil {
			e = p.Supervisor.Run(ctx, path, cmd, func(r io.Reader) error { _, e := io.Copy(out, r); return e })
		}
	} else {
		var cmd *exec.Cmd
		if cmd, e = mediaexec.CommandContext(ctx, probeJob(binary, args, path)); e == nil {
			cmd.Stdout = out
			cmd.Stderr = io.Discard
			cmd.WaitDelay = time.Second
			e = cmd.Run()
		}
	}
	return out.data, e
}

// AnalyzePlayback is analysis-lane-only. Missing, timed-out or truncated probes
// remain unobserved; no peak estimate is published from a partial packet pass.
func (p Probe) AnalyzePlayback(ctx context.Context, path string, f *Facts) {
	var video *Stream
	for i := range f.Streams {
		if f.Streams[i].Type == "video" {
			video = &f.Streams[i]
			break
		}
	}
	if video == nil {
		return
	}
	boundedFrames, cancelFrames := context.WithTimeout(ctx, 12*time.Second)
	frames, frameErr := p.analysisOutput(boundedFrames, path, 4<<20, "-select_streams", "v:0", "-read_intervals", "%+#240", "-show_frames", "-show_entries", "frame=best_effort_timestamp_time,interlaced_frame,repeat_pict", "-of", "json")
	cancelFrames()
	if frameErr == nil {
		analyzeFrames(frames, &video.Detail)
	}
	// HDR10+ and, where the container did not carry it, HDR10 static metadata
	// travel in the first picture (SEI in HEVC elementary and MPEG-TS sources).
	if video.Codec == "hevc" || video.Codec == "av1" || video.Codec == "vp9" || video.Detail.DolbyVisionProfile > 0 {
		bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
		raw, e := p.analysisOutput(bounded, path, 1<<20, "-select_streams", "v:0", "-read_intervals", "%+#1", "-show_frames", "-show_entries", "frame=media_type,width,height,side_data_list", "-of", "json")
		cancel()
		if e == nil {
			analyzeDolbyVisionFrames(raw, &video.Detail)
			var doc struct {
				Frames []struct {
					Side []probeSideData `json:"side_data_list"`
				} `json:"frames"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				for _, frame := range doc.Frames {
					for _, side := range frame.Side {
						switch {
						case strings.Contains(side.Type, "SMPTE2094-40"):
							video.Detail.HDR10Plus = true
						case side.Type == "Mastering display metadata" && video.Detail.MasterDisplay == "":
							video.Detail.MasterDisplay = side.masteringDisplay()
						case side.Type == "Content light level metadata" && video.Detail.MaxCLL == "":
							video.Detail.MaxCLL = side.contentLightLevel()
						}
					}
				}
			}
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	raw, e := p.analysisOutput(bounded, path, 32<<20, "-select_streams", "v:0", "-show_packets", "-show_entries", "packet=dts_time,size", "-of", "csv=p=0")
	if e == nil {
		video.Detail.MaxBitRate = packetPeak(raw)
	}
}

// A rolling one-second packet window catches bursts across second boundaries.
func packetPeak(raw []byte) int64 {
	type packet struct {
		time float64
		size int64
	}
	ring := make([]packet, 8192)
	head, count := 0, 0
	var total, peak int64
	last := math.Inf(-1)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	observed := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			return 0
		}
		at, e := strconv.ParseFloat(fields[0], 64)
		if e != nil || math.IsNaN(at) || math.IsInf(at, 0) || at < last {
			return 0
		}
		size, e := strconv.ParseInt(fields[1], 10, 64)
		if e != nil || size < 1 || size > 128<<20 {
			return 0
		}
		for count > 0 && ring[head].time <= at-1 {
			total -= ring[head].size
			head = (head + 1) % len(ring)
			count--
		}
		if count == len(ring) {
			return 0
		}
		ring[(head+count)%len(ring)] = packet{at, size}
		count++
		total += size
		peak = max(peak, total*8)
		last = at
		observed++
	}
	if scanner.Err() != nil || observed < 2 {
		return 0
	}
	return peak
}
