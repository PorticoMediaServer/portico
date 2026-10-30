// Package vod produces bounded, measured finite windows from an admitted pinned
// descriptor. Scheduling, authorization, publication and descriptor ownership
// remain with the caller. No worker may publish a partially written artifact.
package vod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrUnsupported = errors.New("source timing or format requires preparation")
var ErrChanged = errors.New("source changed since admission")
var ErrBudget = errors.New("finite window resource limit exceeded")

const Policy = "finite_cfr_sdr_grid_v4"
const Interval = 6.0
const EdgeTolerance = 0.050

// IntervalCount shares the immutable layout with scheduling and manifest code.
// A terminal remainder of at most 250ms belongs to the preceding interval.
func IntervalCount(duration float64) int {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7200 {
		return 0
	}
	full := int(math.Floor(duration / Interval))
	remainder := duration - float64(full)*Interval
	if remainder == 0 {
		return full
	}
	if full > 0 && remainder <= .250 {
		return full
	}
	return full + 1
}

// IntervalBounds returns a complete, contiguous source interval, never an
// independently encoded tiny terminal remainder when a full interval precedes it.
func IntervalBounds(duration float64, index int) (start, end float64, ok bool) {
	count := IntervalCount(duration)
	if index < 0 || index >= count {
		return 0, 0, false
	}
	start = float64(index) * Interval
	end = math.Min(start+Interval, duration)
	if index == count-1 {
		end = duration
	}
	return start, end, true
}

type Limits struct {
	AnalyzeTimeout, EncodeTimeout time.Duration
	ProbeBytes, OutputBytes       int64
	MaxPreroll                    float64
}
type Encoder struct {
	ffmpeg, ffprobe string
	limits          Limits
}
type Work struct {
	WallSeconds, CPUSeconds float64
	ProbeOutputBytes        int64
}

func (a Work) plus(b Work) Work {
	return Work{a.WallSeconds + b.WallSeconds, a.CPUSeconds + b.CPUSeconds, a.ProbeOutputBytes + b.ProbeOutputBytes}
}

// Input is exclusively leased to one worker; child processes seek this descriptor.
// Validate must inspect this same descriptor in a bounded isolated child.
type Identity struct{ Size, ModifiedNS int64 }
type Input struct {
	Release  func() error
	File     *os.File
	Identity Identity
	Validate func(context.Context, *os.File, Identity) error
	// Argument is what follows `-i`, and Inherit says the file belongs in the
	// child's ExtraFiles. On Unix that is the inherited descriptor as
	// /dev/fd/3; on Windows the server holds the file open with writers and
	// deleters locked out and the child opens the path by name, because Windows
	// has neither /dev/fd nor ExtraFiles. See assets.PlaybackInput.
	Argument string
	Inherit  bool
}

// Close releases the retained source and reader pins as well as the file.
func (i Input) Close() error {
	if i.Release != nil {
		return i.Release()
	}
	if i.File != nil {
		return i.File.Close()
	}
	return nil
}

// source is what to hand ffmpeg, defaulting to the descriptor convention so a
// caller that has not been updated behaves as it always did.
func (i Input) source() string {
	if i.Argument == "" {
		return "/dev/fd/3"
	}
	return i.Argument
}

// readPaths is what the sandbox must let the child read by name: the source
// path where it is not inherited (Windows).
func (i Input) readPaths() []string {
	if i.Argument == "" || i.Inherit || !filepath.IsAbs(i.Argument) {
		return nil
	}
	return []string{filepath.Clean(i.Argument)}
}

// extraFiles is the slice for exec.Cmd.ExtraFiles.
func (i Input) extraFiles() []*os.File {
	if i.Argument != "" && !i.Inherit {
		return nil
	}
	return []*os.File{i.File}
}

func (i Input) check(ctx context.Context) error {
	if i.File == nil || i.Validate == nil {
		return ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := i.Validate(ctx, i.File, i.Identity)
	// A deadline can expire during descriptor validation, before encoding even
	// begins. Preserve that cause instead of leaking the validator's killed
	// child-process error to the caller.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type Source struct {
	admitted               bool
	AnalysisWork           Work
	Policy                 string
	Duration, FPS          float64
	VideoEnd, AudioEnd     float64
	VideoIndex, AudioIndex int
	AudioCodec             string
	Width, Height          int
	Size, ModifiedNS       int64
	Keyframes              []float64
}
type Request struct {
	Index             int
	GenerationDir     string
	ObserveConversion func(ConversionProgress)
}

// ConversionProgress comes from FFmpeg's machine-readable progress stream.
// It measures media time per wall second; CPU time and output bytes are not speed.
type ConversionProgress struct {
	Speed      float64
	ObservedAt time.Time
}

// ConversionProgressWriter consumes bounded progress lines, including split
// writes. Only a complete FFmpeg report with real output time and finite speed
// can publish. It never retains diagnostics or paths.
type ConversionProgressWriter struct {
	line          []byte
	dropping      bool
	media         int64
	output        bool
	previousMedia int64
	previousAt    time.Time
	observe       func(ConversionProgress)
}

func NewConversionProgressWriter(observe func(ConversionProgress)) *ConversionProgressWriter {
	return &ConversionProgressWriter{observe: observe}
}
func (w *ConversionProgressWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b != '\n' {
			if len(w.line) < 256 && !w.dropping {
				w.line = append(w.line, b)
			} else {
				w.dropping = true
			}
			continue
		}
		line := string(w.line)
		w.line = w.line[:0]
		if w.dropping {
			w.dropping = false
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			n, e := strconv.ParseInt(value, 10, 64)
			w.media, w.output = n, e == nil && n > 0
		case "progress":
			at := time.Now()
			if w.output {
				if !w.previousAt.IsZero() && w.media >= w.previousMedia && at.After(w.previousAt) && w.observe != nil {
					w.observe(ConversionProgress{Speed: float64(w.media-w.previousMedia) / 1e6 / at.Sub(w.previousAt).Seconds(), ObservedAt: at})
				}
				w.previousMedia, w.previousAt = w.media, at
			}
			w.output = false
		}
	}
	return len(p), nil
}

type StreamFacts struct {
	Kind       string
	First, End float64
	Packets    int
}
type Window struct {
	ProbeWork, EncodeWork Work
	Path                  string
	Index                 int
	Start, End            float64
	Streams               []StreamFacts
	Bytes                 int64
	Files                 int
	DecodeFrom            float64
	Policy                string
}

func NewEncoder(ffmpeg, ffprobe string, l Limits) (*Encoder, error) {
	if l.AnalyzeTimeout <= 0 {
		l.AnalyzeTimeout = 30 * time.Second
	}
	if l.EncodeTimeout <= 0 {
		l.EncodeTimeout = 30 * time.Second
	}
	if l.ProbeBytes <= 0 {
		l.ProbeBytes = 96 << 20
	}
	if l.OutputBytes <= 0 {
		l.OutputBytes = 32 << 20
	}
	if l.MaxPreroll <= 0 {
		l.MaxPreroll = 12
	}
	a, e := exec.LookPath(ffmpeg)
	if e != nil {
		return nil, e
	}
	b, e := exec.LookPath(ffprobe)
	if e != nil {
		return nil, e
	}
	return &Encoder{a, b, l}, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	max      int64
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(b.buffer.Len()+len(p)) > b.max {
		b.overflow = true
		return 0, ErrBudget
	}
	return b.buffer.Write(p)
}

type probeStream struct {
	Index         int    `json:"index"`
	Type          string `json:"codec_type"`
	Codec         string `json:"codec_name"`
	Width, Height int
	Pixel         string `json:"pix_fmt"`
	Rate          string `json:"r_frame_rate"`
	Average       string `json:"avg_frame_rate"`
	Start         string `json:"start_time"`
	Transfer      string `json:"color_transfer"`
}
type packet struct {
	Index    int    `json:"stream_index"`
	PTS      string `json:"pts_time"`
	DTS      string `json:"dts_time"`
	Duration string `json:"duration_time"`
	Flags    string `json:"flags"`
}
type probe struct {
	Work   Work `json:"-"`
	Format struct {
		Duration string `json:"duration"`
		Name     string `json:"format_name"`
	} `json:"format"`
	Streams []probeStream `json:"streams"`
	Packets []packet      `json:"packets"`
}

func number(s string) (float64, bool) {
	v, e := strconv.ParseFloat(s, 64)
	return v, e == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func rate(s string) (float64, bool) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return 0, false
	}
	a, ok := number(parts[0])
	b, bok := number(parts[1])
	return a / b, ok && bok && b > 0
}
func (e *Encoder) probe(ctx context.Context, input Input, interval string) (probe, error) {
	var p probe
	started := time.Now()
	b := &boundedBuffer{max: e.limits.ProbeBytes}
	ctx, progress := progressio.New(ctx, e.limits.AnalyzeTimeout, 4*time.Hour)
	defer progress.Close()
	args := []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-probesize", "8388608", "-analyzeduration", "3000000", "-show_streams", "-show_format", "-show_entries", "format=duration,format_name:stream=index,codec_type,codec_name,width,height,pix_fmt,r_frame_rate,avg_frame_rate,start_time,color_transfer:packet=stream_index,pts_time,dts_time,duration_time,flags", "-of", "json"}
	if interval != "metadata" {
		args = append(args, "-show_packets")
		if interval != "" {
			args = append(args, "-read_intervals", interval)
		}
	}
	args = append(args, input.source())
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: e.ffprobe, Args: args, Files: input.extraFiles(), ReadPaths: input.readPaths()})
	if err != nil {
		return p, err
	}
	cmd.Stdout = progress.Writer(b)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := childprocess.Run(ctx, cmd); err != nil {
		if b.overflow {
			return p, ErrBudget
		}
		if ctx.Err() != nil {
			return p, context.Cause(ctx)
		}
		return p, ErrUnsupported
	}
	if err := json.Unmarshal(b.buffer.Bytes(), &p); err != nil {
		return p, ErrUnsupported
	}
	p.Work = Work{WallSeconds: time.Since(started).Seconds(), CPUSeconds: (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds(), ProbeOutputBytes: int64(b.buffer.Len())}
	return p, nil
}
func (e *Encoder) Analyze(ctx context.Context, input Input) (*Source, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := input.check(ctx); err != nil {
		return nil, err
	}
	p, err := e.probe(ctx, input, "metadata")
	if err != nil {
		return nil, err
	}
	s := &Source{Policy: Policy, AudioIndex: -1, VideoIndex: -1, Size: input.Identity.Size, ModifiedNS: input.Identity.ModifiedNS}
	for _, v := range p.Streams {
		switch v.Type {
		case "video":
			if s.VideoIndex >= 0 || v.Width <= 0 || v.Height <= 0 || v.Width > 1920 || v.Height > 1080 || v.Pixel != "yuv420p" || (v.Transfer != "" && v.Transfer != "unknown" && v.Transfer != "bt709") {
				return nil, fmt.Errorf("video geometry/pixel/color or multiple video: %w", ErrUnsupported)
			}
			fps, ok := rate(v.Rate)
			avg, aok := rate(v.Average)
			start, sok := number(v.Start)
			if !ok || !aok || !sok || math.Abs(start) > .00001 || math.Abs(fps-avg) > .01 || (fps != 24 && fps != 25 && fps != 30 && math.Abs(fps-30000.0/1001) > .00001 && math.Abs(fps-24000.0/1001) > .00001) {
				return nil, fmt.Errorf("video origin or nominal rate: %w", ErrUnsupported)
			}
			s.VideoIndex = v.Index
			s.FPS = fps
			s.Width = v.Width
			s.Height = v.Height
		case "audio":
			start, ok := number(v.Start)
			if s.AudioIndex >= 0 || (v.Codec != "aac" && v.Codec != "opus") || !ok || math.Abs(start) > EdgeTolerance {
				return nil, fmt.Errorf("audio codec/origin or multiple audio: %w", ErrUnsupported)
			}
			s.AudioIndex = v.Index
			s.AudioCodec = v.Codec
		default:
			return nil, fmt.Errorf("additional stream type: %w", ErrUnsupported)
		}
	}
	if s.VideoIndex < 0 {
		return nil, fmt.Errorf("missing video: %w", ErrUnsupported)
	}
	duration, ok := number(p.Format.Duration)
	if !ok || duration < 1.0/30 || duration > 7200 || (!strings.Contains(p.Format.Name, "mov") && !strings.Contains(p.Format.Name, "matroska")) {
		return nil, fmt.Errorf("container or duration: %w", ErrUnsupported)
	}
	s.Duration = duration
	head, err := e.probe(ctx, input, "0%2")
	if err != nil {
		return nil, err
	}
	tailStart := math.Max(0, duration-2)
	// Every interval names its end. ffprobe leaves an open interval's end fields
	// unset (fftools/ffprobe.c parse_read_interval, through n8.1.2), so "START%"
	// can read heap garbage as an end offset and return no packets at all; the
	// production bundle does. The file's end comes first anyway.
	tail, err := e.probe(ctx, input, fmt.Sprintf("%.9f%%+3600", tailStart))
	if err != nil {
		return nil, err
	}
	audioFirst, audioEnd := math.Inf(1), math.Inf(-1)
	videoFirst, videoEnd := math.Inf(1), 0.0
	for n, chunk := range []probe{head, tail} {
		seen := map[int]bool{}
		for _, packet := range chunk.Packets {
			pts, ok := number(packet.PTS)
			d, dok := number(packet.Duration)
			if !ok || !dok || d <= 0 {
				return nil, fmt.Errorf("packet timestamp/duration: %w", ErrUnsupported)
			}
			if n == 1 && pts < tailStart-e.limits.MaxPreroll {
				return nil, fmt.Errorf("tail preroll: %w", ErrUnsupported)
			}
			if packet.Index == s.VideoIndex {
				frame := int(math.Round(pts * s.FPS))
				if frame < 0 || seen[frame] || math.Abs(pts-float64(frame)/s.FPS) > .0011 || math.Abs(d-1/s.FPS) > .0011 {
					return nil, fmt.Errorf("sampled video grid: %w", ErrUnsupported)
				}
				seen[frame] = true
				videoFirst = math.Min(videoFirst, pts)
				videoEnd = math.Max(videoEnd, pts+d)
				if n == 0 && strings.Contains(packet.Flags, "K") {
					s.Keyframes = append(s.Keyframes, pts)
				}
			} else if packet.Index == s.AudioIndex {
				audioFirst = math.Min(audioFirst, pts)
				audioEnd = math.Max(audioEnd, pts+d)
			}
		}
	}
	if math.Abs(videoFirst) > .0011 || len(s.Keyframes) == 0 || math.Abs(s.Keyframes[0]) > .0011 {
		return nil, fmt.Errorf("video extent/access point first=%f end=%f metadata=%f keys=%v: %w", videoFirst, videoEnd, duration, s.Keyframes, ErrUnsupported)
	}
	// Metadata extent is admitted only after the bounded tail reaches matching
	// packet extents. Every subsequently requested region is checked separately.
	if s.AudioIndex >= 0 && (math.Abs(audioFirst) > EdgeTolerance || math.Abs(audioEnd-videoEnd) > .250) {
		return nil, fmt.Errorf("audio extent: %w", ErrUnsupported)
	}
	s.VideoEnd = videoEnd
	s.Duration = videoEnd
	if s.AudioIndex >= 0 {
		s.AudioEnd = audioEnd
		s.Duration = math.Max(videoEnd, audioEnd)
	}
	if math.Abs(s.Duration-duration) > EdgeTolerance {
		return nil, fmt.Errorf("measured maximum extent: %w", ErrUnsupported)
	}
	if err := input.check(ctx); err != nil {
		return nil, err
	}
	s.admitted = true
	s.AnalysisWork = p.Work.plus(head.Work).plus(tail.Work)
	return s, nil
}
func (e *Encoder) Encode(ctx context.Context, s *Source, input Input, r Request) (Window, error) {
	ctx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	w := Window{Index: r.Index, Policy: Policy}
	if s == nil || !s.admitted || s.Policy != Policy || r.Index < 0 {
		return w, ErrUnsupported
	}
	var valid bool
	w.Start, w.End, valid = IntervalBounds(s.Duration, r.Index)
	if !valid {
		return w, ErrUnsupported
	}
	if input.Identity.Size != s.Size || input.Identity.ModifiedNS != s.ModifiedNS {
		return w, ErrChanged
	}
	if err := input.check(ctx); err != nil {
		return w, err
	}
	// Seek to an inspected keyframe before the private decoder warmup interval.
	seekTarget := math.Max(0, w.Start-2.1)
	local, err := e.probe(ctx, input, fmt.Sprintf("%.9f%%%.9f", seekTarget, w.End+.1))
	if err != nil {
		return w, err
	}
	w.ProbeWork = local.Work
	found := false
	frames := map[int]bool{}
	for _, packet := range local.Packets {
		if packet.Index != s.VideoIndex {
			continue
		}
		pts, ok := number(packet.PTS)
		d, dok := number(packet.Duration)
		if !ok || !dok || math.Abs(pts*s.FPS-math.Round(pts*s.FPS)) > .04 || math.Abs(d-1/s.FPS) > .0011 {
			return w, ErrUnsupported
		}
		frame := int(math.Round(pts * s.FPS))
		if frames[frame] || pts < w.Start-e.limits.MaxPreroll {
			return w, ErrUnsupported
		}
		frames[frame] = true
		if strings.Contains(packet.Flags, "K") && pts <= seekTarget+.0011 && pts >= w.Start-e.limits.MaxPreroll {
			w.DecodeFrom = pts
			found = true
		}
	}
	for frame := int(math.Ceil(w.Start * s.FPS)); frame < int(math.Floor(math.Min(w.End, s.VideoEnd)*s.FPS)); frame++ {
		if !frames[frame] {
			return w, ErrUnsupported
		}
	}
	if !found || w.Start-w.DecodeFrom > e.limits.MaxPreroll {
		return w, ErrUnsupported
	}
	if err = os.Mkdir(r.GenerationDir, 0700); err != nil {
		return w, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(r.GenerationDir)
		}
	}()
	temp := filepath.Join(r.GenerationDir, "segment.partial.ts")
	encoded := temp
	if s.AudioIndex >= 0 {
		encoded = filepath.Join(r.GenerationDir, "encoded.ts")
	}
	output := filepath.Join(r.GenerationDir, "segment.ts")
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-ss", fmt.Sprintf("%.9f", w.DecodeFrom), "-copyts", "-t", fmt.Sprintf("%.9f", w.End-w.DecodeFrom+.25), "-i", input.source(), "-map", fmt.Sprintf("0:%d", s.VideoIndex), "-vf", fmt.Sprintf("tpad=stop_mode=clone:stop_duration=0.250,trim=start=%.9f:end=%.9f,fps=30:start_time=%.9f", w.Start, w.End, w.Start), "-c:v", "libx264", "-preset", "veryfast", "-crf", "21", "-pix_fmt", "yuv420p", "-profile:v", "high", "-level:v", "4.0", "-bf", "0", "-g", "180", "-keyint_min", "180", "-sc_threshold", "0", "-maxrate:v", "3M", "-bufsize:v", "6M", "-threads:v", "2"}
	if s.AudioIndex >= 0 {
		audioStartSamples := int64(math.Ceil(math.Max(0, w.Start-2)*48000/1024)) * 1024
		audioEndSamples := (int64(math.Ceil(w.End*48000/1024)) + 2) * 1024
		args = append(args, "-map", fmt.Sprintf("0:%d", s.AudioIndex), "-af", fmt.Sprintf("aresample=48000,aresample=48000:first_pts=%d,apad=pad_dur=0.250,atrim=start=%.9f:end=%.9f,asettb=1/48000,asetpts=N+%d", audioStartSamples, float64(audioStartSamples)/48000, float64(audioEndSamples)/48000, audioStartSamples), "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-ar", "48000", "-threads:a", "1")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-sn", "-dn", "-avoid_negative_ts", "disabled", "-mpegts_copyts", "1", "-muxdelay", "0", "-f", "mpegts", encoded)
	work, err := e.runWindow(ctx, args, input.extraFiles(), []string{encoded}, r.ObserveConversion)
	w.EncodeWork = work
	if err != nil {
		return w, err
	}
	if s.AudioIndex >= 0 {
		// Select closed packets on a global AAC lattice. The source decoder and
		// encoder have already warmed up; priming is private to encoded.ts.
		copyArgs := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe", "-copyts", "-i", encoded, "-ss", fmt.Sprintf("%.9f", w.Start), "-to", fmt.Sprintf("%.9f", w.End), "-map", "0", "-c", "copy", "-avoid_negative_ts", "disabled", "-mpegts_copyts", "1", "-output_ts_offset", fmt.Sprintf("%.9f", w.Start), "-muxdelay", "0", "-f", "mpegts", temp}
		copyWork, copyErr := e.runWindow(ctx, copyArgs, nil, []string{encoded, temp})
		w.EncodeWork = w.EncodeWork.plus(copyWork)
		if copyErr != nil {
			return w, copyErr
		}
		if err = os.Remove(encoded); err != nil {
			return w, err
		}
	}
	f, err := os.Open(temp)
	if err != nil {
		return w, err
	}
	// A file this process just wrote and opened itself: no session pin, no
	// share-mode fence, and the descriptor convention is correct on every
	// platform because the child inherits what we opened.
	p, err := e.probe(ctx, Input{File: f, Inherit: true}, "")
	f.Close()
	if err != nil {
		return w, err
	}
	w.ProbeWork = w.ProbeWork.plus(p.Work)
	expectedStreams := 1
	if s.AudioIndex >= 0 {
		expectedStreams++
	}
	if len(p.Streams) != expectedStreams {
		return w, ErrUnsupported
	}
	facts := map[int]*StreamFacts{}
	for _, v := range p.Streams {
		if (v.Type != "video" || v.Codec != "h264") && (v.Type != "audio" || v.Codec != "aac" || s.AudioIndex < 0) {
			return w, ErrUnsupported
		}
		facts[v.Index] = &StreamFacts{Kind: v.Type, First: math.Inf(1)}
	}
	for _, p := range p.Packets {
		v := facts[p.Index]
		if v == nil {
			return w, ErrUnsupported
		}
		pts, ok := number(p.PTS)
		d, dok := number(p.Duration)
		if !ok || !dok {
			return w, ErrUnsupported
		}
		if v.Kind == "video" && v.Packets == 0 && !strings.Contains(p.Flags, "K") {
			return w, ErrUnsupported
		}
		packetDuration := 1.0 / 30
		if v.Kind == "audio" {
			packetDuration = 1024.0 / 48000
		}
		if math.Abs(d-packetDuration) > 0.0000223 || (v.Packets > 0 && math.Abs(pts-v.End) > 0.0000223) {
			return w, ErrUnsupported
		}
		v.First = math.Min(v.First, pts)
		v.End = math.Max(v.End, pts+d)
		v.Packets++
	}
	for _, v := range facts {
		if v.Packets == 0 || math.Abs(v.First-w.Start) > EdgeTolerance || math.Abs(v.End-w.End) > EdgeTolerance {
			return w, ErrUnsupported
		}
		if v.Kind == "audio" {
			firstPacket := math.Ceil(w.Start*48000/1024 - 1e-9)
			endPacket := math.Ceil(w.End*48000/1024 - 1e-9)
			if v.Packets != int(endPacket-firstPacket) || math.Abs(v.First-firstPacket*1024/48000) > 0.0000223 || math.Abs(v.End-endPacket*1024/48000) > 0.0000223 {
				return w, ErrUnsupported
			}
		}
		w.Streams = append(w.Streams, *v)
	}
	sort.Slice(w.Streams, func(i, j int) bool { return w.Streams[i].Kind < w.Streams[j].Kind })
	st, err := os.Stat(temp)
	if err != nil {
		return w, err
	}
	if st.Size() > e.limits.OutputBytes {
		return w, ErrBudget
	}
	w.Bytes = st.Size()
	w.Files = 1
	if err := input.check(ctx); err != nil {
		return w, err
	}
	if err = os.Chmod(temp, 0400); err != nil {
		return w, err
	}
	if err = os.Rename(temp, output); err != nil {
		return w, err
	}
	w.Path = output
	success = true
	return w, nil
}

// A single actor runs at most one encoder/copy child. All private output paths
// share one temporary byte budget, including a closed intermediate during mux.
func (e *Encoder) runWindow(ctx context.Context, args []string, inherited []*os.File, paths []string, observe ...func(ConversionProgress)) (Work, error) {
	ctx, progress := progressio.New(ctx, e.limits.EncodeTimeout, 4*time.Hour)
	defer progress.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Writes only into the folders of the window's own output paths, each file
	// bounded by the window's byte budget (mediaexec).
	dirs := []string{}
	for _, path := range paths {
		if dir := filepath.Dir(path); len(dirs) == 0 || dirs[len(dirs)-1] != dir {
			dirs = append(dirs, dir)
		}
	}
	if len(observe) > 0 && observe[0] != nil {
		args = append([]string{"-progress", "pipe:1", "-stats_period", "1", "-nostats"}, args...)
	}
	job := mediaexec.Job{Executable: e.ffmpeg, Args: args, Files: inherited, WriteDirs: dirs}
	if e.limits.OutputBytes > 0 {
		job.MaxFileBytes = e.limits.OutputBytes
	}
	cmd, buildErr := mediaexec.CommandContext(ctx, job)
	if buildErr != nil {
		if ctx.Err() != nil {
			return Work{}, context.Cause(ctx)
		}
		return Work{}, buildErr
	}
	cmd.Stderr = io.Discard
	if len(observe) > 0 && observe[0] != nil {
		cmd.Stdout = NewConversionProgressWriter(observe[0])
	}
	cmd.WaitDelay = time.Second
	started := time.Now()
	if err := childprocess.Start(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return Work{}, context.Cause(ctx)
		}
		return Work{}, err
	}
	done := make(chan error, 1)
	supervise.Go("playback.vod.wait", func() { done <- cmd.Wait() })
	var producedBytes int64
	overBudget := func() bool {
		var bytes int64
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil {
				bytes += info.Size()
			}
		}
		if bytes > producedBytes {
			progress.Ping()
			producedBytes = bytes
		}
		return bytes > e.limits.OutputBytes
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var err error
selectLoop:
	for {
		select {
		case err = <-done:
			break selectLoop
		case <-tick.C:
			if overBudget() {
				cancel()
				<-done
				err = ErrBudget
				break selectLoop
			}
		}
	}
	work := Work{WallSeconds: time.Since(started).Seconds(), CPUSeconds: (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()}
	if errors.Is(err, ErrBudget) || overBudget() {
		return work, ErrBudget
	}
	if ctx.Err() != nil {
		return work, context.Cause(ctx)
	}
	if err != nil {
		return work, ErrUnsupported
	}
	return work, nil
}
