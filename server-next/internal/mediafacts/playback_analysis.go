package mediafacts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
)

// PlaybackAnalysisRevision invalidates earlier scan results on the next bounded inventory pass.
const PlaybackAnalysisRevision = "playback_headers:1"
const PlaybackFramesRevision = "playback_frames:1"
const PacketPeakRevision = "packet_peak:1"

var ErrPlaybackObservation = errors.New("incomplete playback frame observation")

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
	if handled, err := p.runScan(ctx, binary, args, path, ScanFile, out); handled {
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

// ApplyFrameCadence merges only observed decoded facts into retained headers.
func ApplyFrameCadence(raw []byte, d *StreamDetail) error {
	if err := validateFrameObservation(raw); err != nil {
		return err
	}
	analyzeFrames(raw, d)
	return nil
}

func NeedsFrameHDR(s Stream) bool {
	return s.Codec == "hevc" || s.Codec == "av1" || s.Codec == "vp9" || s.Detail.DolbyVisionProfile > 0
}

// ApplyFrameHDR never treats a picture without side data as proof of absence.
func ApplyFrameHDR(raw []byte, d *StreamDetail) error {
	if err := validateFrameObservation(raw); err != nil {
		return err
	}
	analyzeDolbyVisionFrames(raw, d)
	var doc struct {
		Frames []struct {
			Side []probeSideData `json:"side_data_list"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ErrPlaybackObservation
	}
	for _, frame := range doc.Frames {
		for _, side := range frame.Side {
			switch {
			case strings.Contains(side.Type, "SMPTE2094-40"):
				d.HDR10Plus = true
			case side.Type == "Mastering display metadata" && d.MasterDisplay == "":
				d.MasterDisplay = side.masteringDisplay()
			case side.Type == "Content light level metadata" && d.MaxCLL == "":
				d.MaxCLL = side.contentLightLevel()
			}
		}
	}
	return nil
}

func validateFrameObservation(raw []byte) error {
	var doc struct {
		Frames []map[string]json.RawMessage `json:"frames"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Frames) == 0 || len(doc.Frames) > 240 {
		return ErrPlaybackObservation
	}
	for _, frame := range doc.Frames {
		if frame == nil {
			return ErrPlaybackObservation
		}
	}
	return nil
}

// A rolling one-second packet window catches bursts across second boundaries.
func PacketPeak(raw []byte) int64 {
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
