package streaming

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// LiveInput is a live channel's shared input as one device's conversion reads it (package
// live). Every viewer of the channel reads the one connection to its source; a conversion
// never holds that connection itself, so the input's own rules decide when it ends.
type LiveInput interface {
	// ReadFrom writes the channel from the moment at (zero: live) for as long as it is read,
	// and tells began the moment it really starts with before writing anything.
	ReadFrom(ctx context.Context, at time.Time, w io.Writer, began func(time.Time)) error
	// Window is what can be played: from the oldest kept moment to live.
	Window() (oldest, live time.Time)
	// Stopped says why the source stopped sending, or nil while it sends.
	Stopped() error
}

// LiveRequest is one device's HLS of a live channel: the channel's MPEG-TS repackaged or
// converted for a device that cannot play it as it is.
type LiveRequest struct {
	// SessionID is the live viewing it belongs to; a viewing has one at a time.
	SessionID string
	// Plan is from Plan, made from what was measured of the channel: an HLS plan.
	Plan  DeliveryPlan
	Input LiveInput
}

// LiveStatus is where a live conversion stands.
type LiveStatus struct {
	// Ended says the channel's source stopped and its playlist is closed.
	Ended bool
	// Failure says why the conversion failed, when it did.
	Failure *ConversionFailure
	Plan    DeliveryPlan
}

const (
	// LivePlaylist is the playlist a live conversion is played from.
	LivePlaylist = "index.m3u8"
	// liveReadyWait is how long a playlist request waits for the first segment of a
	// conversion that is getting ready, before it is told to ask again.
	liveReadyWait = 20 * time.Second
	// liveKeepAtLeast segments are kept when the rewind stretch or the disk would leave fewer.
	liveKeepAtLeast = 3
)

// liveIdle is how long a conversion runs with nobody fetching from it. It then stops; the
// viewing stays, and the next fetch starts it again from where it stopped. A variable for
// tests.
var liveIdle = time.Minute

// liveTick is how often a running conversion's new segments are taken up.
var liveTick = 250 * time.Millisecond

// liveConversion is one device's HLS of a live channel. Guarded by HLS.mu.
type liveConversion struct {
	session string
	input   LiveInput
	dir     string
	plan    *DeliveryPlan
	// segments are what can be played now, oldest first; sequence and discontinuity are the
	// playlist's numbers for the first of them.
	segments                []liveSegment
	sequence, discontinuity int
	// runs counts converter runs; the latest is the one running, if any. Each writes files of
	// its own, so nothing a run left behind is ever mistaken for another's.
	runs   int
	cancel context.CancelFunc
	done   chan struct{}
	// asked is the last at= this device asked for. Its player asks the same address again and
	// again; only a new moment restarts the conversion.
	asked   string
	fetched time.Time
	// deaths counts the runs in a row that ended without a new segment.
	deaths  int
	ended   bool
	failure *ConversionFailure
	changed chan struct{}
}

type liveSegment struct {
	name, init string
	run        int
	start      time.Time
	duration   float64
}

func (l *liveConversion) signal() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// resume is the moment a stopped conversion starts again from: the end of what it made.
func (l *liveConversion) resume() time.Time {
	if n := len(l.segments); n > 0 {
		last := l.segments[n-1]
		return last.start.Add(seconds(last.duration))
	}
	return time.Time{}
}

// holds says the moment at can be played from what is made: no new conversion is needed.
func (l *liveConversion) holds(at time.Time) bool {
	if len(l.segments) == 0 {
		return false
	}
	return !at.Before(l.segments[0].start) && !at.After(l.resume().Add(2*HLSSegmentSeconds*time.Second))
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// StartLive starts one device's HLS of a live channel. It is held to the owner's limits on
// conversions as any conversion is, and refused with ErrConversionCapacity when they are
// reached. It does not wait for the first segment: the playlist does.
func (s *Streams) StartLive(ctx context.Context, r LiveRequest) error {
	h := s.hls
	if h.binary == "" {
		return ErrNoConverter
	}
	if r.SessionID == "" || r.Input == nil || r.Plan.Mode != "hls" {
		return errors.New("a live conversion needs its session, its input and an HLS plan")
	}
	s.StopLive(r.SessionID)
	plan := r.Plan
	h.selectOutputBackend(ctx, "", &plan)
	if !diskspace.Room(h.root, diskspace.ProducerFloor) {
		return ErrConversionSpace
	}
	dir := filepath.Join(h.root, ".live-"+token())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ErrConversionSpace
	}
	l := &liveConversion{session: r.SessionID, input: r.Input, dir: dir, plan: &plan, fetched: time.Now(), changed: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.ctx.Err()
	if err == nil && s.live[r.SessionID] != nil {
		err = errors.New("this live viewing was started again")
	}
	if err == nil {
		err = s.admitLocked(&plan)
	}
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	if s.live == nil {
		s.live = map[string]*liveConversion{}
	}
	s.live[r.SessionID] = l
	s.runLiveLocked(l, time.Time{})
	return nil
}

// StopLive ends a device's live conversion and removes what it made. Stopping one that is
// gone succeeds.
func (s *Streams) StopLive(sessionID string) {
	h := s.hls
	h.mu.Lock()
	l := s.live[sessionID]
	delete(s.live, sessionID)
	var done chan struct{}
	if l != nil {
		if l.cancel != nil {
			l.cancel()
		}
		done = l.done
		l.signal()
	}
	h.mu.Unlock()
	if l == nil {
		return
	}
	go func() {
		if done != nil {
			<-done
		}
		os.RemoveAll(l.dir)
	}()
}

// LiveStatusOf says where a device's live conversion stands.
func (s *Streams) LiveStatusOf(sessionID string) (LiveStatus, bool) {
	h := s.hls
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.live[sessionID]
	if l == nil {
		return LiveStatus{}, false
	}
	return LiveStatus{Ended: l.ended, Failure: l.failure, Plan: *l.plan}, true
}

// runLiveLocked starts a converter run from the moment from (zero: live). Caller holds h.mu.
func (s *Streams) runLiveLocked(l *liveConversion, from time.Time) {
	h := s.hls
	ctx, cancel := context.WithCancel(h.ctx)
	l.runs++
	run, done, plan := l.runs, make(chan struct{}), *l.plan
	l.cancel, l.done = cancel, done
	h.producers.Add(1)
	supervise.Go("playback.live.run", func() {
		defer h.producers.Done()
		defer close(done)
		defer cancel()
		s.liveRun(ctx, l, run, from, &plan)
	})
}

// liveRun runs the converter once and decides what follows: the end of the channel, another
// run (a converter that died, a hardware encoder that failed), or the conversion's failure.
func (s *Streams) liveRun(ctx context.Context, l *liveConversion, run int, from time.Time, plan *DeliveryPlan) {
	h := s.hls
	e, diagnostic, made := s.liveConvert(ctx, l, run, from, plan)
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil || s.live[l.session] != l || l.runs != run {
		return // stopped here: the viewing ended, nobody fetched, or another moment was asked for
	}
	l.cancel = nil
	defer l.signal()
	if made > 0 {
		l.deaths = 0
	}
	switch {
	case e == nil && l.input.Stopped() != nil:
		// The source stopped and everything it sent is converted.
		l.ended = true
		return
	case e != nil && plan.VideoAction == "convert" && plan.HardwareBackend != "" && plan.HardwareBackend != string(decoder.BackendSoftware) &&
		!converterStoppedOutside(e):
		// The hardware encoder refused the channel, or was full: software carries on from
		// where it got to, and later runs stay in software.
		software := *plan
		software.HardwareBackend, software.HardwareNative, software.HardwareDevice = string(decoder.BackendSoftware), false, ""
		if !errors.Is(e, errHardwarePermitUnavailable) {
			software.ReasonCodes = append(append([]string{}, plan.ReasonCodes...), ReasonHardwareFailedSoftware)
			h.hardwareFailures.Add(1)
		}
		l.plan = &software
		s.runLiveLocked(l, l.resumeOr(from))
		return
	case e != nil && !converterKilled(e):
		l.failure = &ConversionFailure{Code: classifyConverterFailure(diagnostic), Diagnostic: diagnostic, At: time.Now(), Strategy: string(plan.Strategy),
			Backend: plan.HardwareBackend, Video: plan.OutputVideoCodec, Audio: plan.OutputAudioCodec}
		log.Printf("Live conversion of %s failed (%s)", l.session, l.failure.Code)
		return
	}
	// The converter stopped from outside, crashed or ended with the source still sending: it
	// starts again where it got to, a few times in a row without progress, and then the
	// conversion fails, as a title's does.
	l.deaths++
	if l.deaths >= converterDeaths {
		l.failure = &ConversionFailure{Code: FailureConverterStopped, Diagnostic: diagnostic, At: time.Now(), Strategy: string(plan.Strategy),
			Backend: plan.HardwareBackend, Video: plan.OutputVideoCodec, Audio: plan.OutputAudioCodec}
		log.Printf("Live conversion of %s failed (%s)", l.session, l.failure.Code)
		return
	}
	log.Printf("Live conversion of %s: the converter stopped; it starts again", l.session)
	s.runLiveLocked(l, l.resumeOr(from))
}

func (l *liveConversion) resumeOr(from time.Time) time.Time {
	if at := l.resume(); !at.IsZero() {
		return at
	}
	return from
}

// liveConvert runs one converter on the channel from the moment from, taking up each segment
// it finishes, until it exits. It says how many segments it made.
func (s *Streams) liveConvert(ctx context.Context, l *liveConversion, run int, from time.Time, plan *DeliveryPlan) (error, string, int) {
	h := s.hls
	args, stages, err := liveArgs(plan, run, l.dir)
	if err != nil {
		return err, sanitizeConverterOutput(err.Error(), h.root), 0
	}
	if hardwareEncodeStage(stages) {
		cell := decoder.PermitCell{Identity: decoder.PermitIdentity(h.binary), Backend: decoder.HardwareBackend(plan.HardwareBackend), Device: plan.HardwareDevice}
		release, reserved := decoder.HardwarePermits().Reserve(cell)
		if !reserved {
			return errHardwarePermitUnavailable, "", 0
		}
		defer release()
	}
	// The channel reaches the converter through a pipe, as descriptor 3: it never opens the
	// source or the input's files itself.
	reader, writer, err := os.Pipe()
	if err != nil {
		return err, "", 0
	}
	began := make(chan time.Time, 1)
	read, stopRead := context.WithCancel(ctx)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		l.input.ReadFrom(read, from, writer, func(at time.Time) { began <- at })
		writer.Close()
	}()
	collect := s.liveCollector(l, run, began)
	watch, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	supervise.Go("playback.live.watch", func() {
		defer close(watchDone)
		tick := time.NewTicker(liveTick)
		defer tick.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-tick.C:
			}
			collect()
			h.mu.Lock()
			idle := s.live[l.session] == l && l.runs == run && time.Since(l.fetched) >= liveIdle
			if idle && l.cancel != nil {
				l.cancel()
				l.cancel = nil
			}
			h.mu.Unlock()
		}
	})
	job := mediaexec.Job{Executable: h.binary, Args: args, Files: []*os.File{reader}, WriteDirs: []string{l.dir},
		Hardware: conversionJobHardware(plan, stages)}
	var diagnostic string
	if h.execute != nil {
		err, diagnostic = h.execute(ctx, job)
	} else if cmd, cerr := mediaexec.CommandContext(ctx, job); cerr != nil {
		err, diagnostic = &notStarted{cerr}, "the media sandbox refused to start the converter: "+cerr.Error()
	} else {
		cmd.WaitDelay = 2 * time.Second
		tail := &tailWriter{limit: 4096}
		cmd.Stderr = tail
		err = childprocess.Run(ctx, cmd)
		diagnostic = sanitizeConverterOutput(tail.String(), h.root, h.memoryRoot)
	}
	// The converter is gone: closing its end lets a reading blocked on a full pipe return.
	reader.Close()
	stopRead()
	<-readDone
	stopWatch()
	<-watchDone
	made := collect()
	return err, diagnostic, made
}

// liveCollector takes up the segments one run has finished, as its own playlist lists them,
// each placed on the channel's clock from the moment the run started with. It trims what is
// older than the channel's rewind stretch, or everything but the newest when the disk is
// short. It returns how many segments the run has made so far.
func (s *Streams) liveCollector(l *liveConversion, run int, began <-chan time.Time) func() int {
	h := s.hls
	playlist := filepath.Join(l.dir, "run-"+strconv.Itoa(run)+".m3u8")
	var start time.Time
	var offset float64
	seen, made := -1, 0
	return func() int {
		if start.IsZero() {
			select {
			case start = <-began:
			default:
				return made
			}
		}
		found := liveRunSegments(playlist)
		h.mu.Lock()
		defer h.mu.Unlock()
		if s.live[l.session] != l || l.runs != run {
			return made
		}
		added := false
		for _, seg := range found {
			if seg.number <= seen {
				continue
			}
			seen = seg.number
			l.segments = append(l.segments, liveSegment{name: seg.name, init: seg.init, run: run, start: start.Add(seconds(offset)), duration: seg.duration})
			offset += seg.duration
			made++
			added = true
		}
		oldest, _ := l.input.Window()
		short := !diskspace.Room(l.dir, diskspace.ProducerFloor)
		for len(l.segments) > liveKeepAtLeast && (short || l.segments[0].start.Add(seconds(l.segments[0].duration)).Before(oldest)) {
			l.dropFirst()
			added = true
		}
		if added {
			l.signal()
		}
		return made
	}
}

// dropFirst removes the oldest segment, and its run's init file once nothing uses it.
func (l *liveConversion) dropFirst() {
	first := l.segments[0]
	l.segments = l.segments[1:]
	l.sequence++
	os.Remove(filepath.Join(l.dir, first.name))
	if len(l.segments) > 0 && l.segments[0].run != first.run {
		l.discontinuity++
		if first.init != "" {
			os.Remove(filepath.Join(l.dir, first.init))
		}
	}
}

// clear removes every segment, for a conversion that starts again from another moment.
func (l *liveConversion) clear() {
	for len(l.segments) > 0 {
		last := len(l.segments) == 1
		first := l.segments[0]
		l.dropFirst()
		if last {
			l.discontinuity++
			if first.init != "" {
				os.Remove(filepath.Join(l.dir, first.init))
			}
		}
	}
}

type runSegment struct {
	name, init string
	number     int
	duration   float64
}

// liveRunSegments reads the segments a run's own playlist lists, in order.
func liveRunSegments(path string) []runSegment {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var out []runSegment
	var duration float64
	init := ""
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:URI="):
			init = strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`)
		case strings.HasPrefix(line, "#EXTINF:"):
			duration, _ = strconv.ParseFloat(strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(line, "#EXTINF:"), ",", 2)[0], ","), 64)
		case line != "" && !strings.HasPrefix(line, "#"):
			name := filepath.Base(line)
			dash := strings.LastIndexByte(name, '-')
			dot := strings.LastIndexByte(name, '.')
			if dash < 0 || dot < dash {
				continue
			}
			number, err := strconv.Atoi(name[dash+1 : dot])
			if err != nil {
				continue
			}
			out = append(out, runSegment{name: name, init: init, number: number, duration: duration})
		}
	}
	return out
}

// liveArgs is the converter for one run: the channel's MPEG-TS on descriptor 3, converted or
// repackaged as the plan says, into HLS segments of the run's own.
func liveArgs(plan *DeliveryPlan, run int, dir string) ([]string, []decoder.HardwareStage, error) {
	graph, err := plan.conversionGraph(nil)
	if err != nil {
		return nil, nil, err
	}
	// A broadcast carries damaged packets now and then: they are dropped, not fatal.
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts+discardcorrupt"}
	args = append(args, graph.Input...)
	args = append(args, "-protocol_whitelist", "pipe", "-f", "mpegts", "-i", "pipe:3")
	videoMap := "0:v:0"
	if graph.VideoMap != "" {
		videoMap = graph.VideoMap
	}
	audioMap := "0:a:0?"
	if plan.AudioStream >= 0 {
		audioMap = "0:" + strconv.Itoa(plan.AudioStream)
	}
	switch {
	case plan.VideoAction == "none":
		args = append(args, "-map", strings.TrimSuffix(audioMap, "?"), "-vn", "-sn", "-dn")
	case plan.AudioAction == "none":
		args = append(args, "-map", videoMap, "-an", "-sn", "-dn")
	default:
		args = append(args, "-map", videoMap, "-map", audioMap, "-sn", "-dn")
	}
	args = append(args, graph.Video...)
	args = append(args, graph.Audio...)
	fragmented := plan.OutputContainer == "fmp4_hls"
	packaging := "mpegts_hls"
	if fragmented {
		packaging = "fmp4_hls"
		args = append(args, "-map_metadata", "-1")
	}
	videoCodec, copiedAudio := "", ""
	if plan.VideoAction != "none" {
		videoCodec = plan.OutputVideoCodec
	}
	if plan.AudioAction == "copy" {
		copiedAudio = plan.OutputAudioCodec
	}
	args = append(args, decoder.HLSMuxArgs(fragmented, videoCodec, copiedAudio, 0)...)
	args = append(args, "-max_muxing_queue_size", "1024")
	name := strconv.Itoa(run)
	packagingArgs, ext, err := decoder.HLSPackagingArgs(packaging, "init-"+name+".mp4")
	if err != nil {
		return nil, nil, err
	}
	args = append(args, packagingArgs...)
	// The run's own playlist keeps its last few segments: the conversion's playlist is made
	// from it as segments finish, and segment files are removed only by the rewind stretch.
	args = append(args, "-f", "hls", "-hls_time", strconv.Itoa(HLSSegmentSeconds), "-hls_list_size", "10",
		"-hls_flags", "independent_segments+temp_file", "-hls_segment_filename", filepath.Join(dir, "segment-"+name+"-%06d."+ext),
		filepath.Join(dir, "run-"+name+".m3u8"))
	return args, graph.Stages, nil
}

// ServeLive answers one request for a device's live conversion: its playlist, or a segment
// or init file the playlist names. The playlist may ask for a moment with at= (RFC 3339):
// one outside what is made starts the conversion again from there (a moment older than the
// rewind stretch starts at its oldest); one inside it is played from what is there.
func (s *Streams) ServeLive(w http.ResponseWriter, r *http.Request, sessionID, file string) {
	h := s.hls
	if file != LivePlaylist {
		h.mu.Lock()
		l := s.live[sessionID]
		path := ""
		if l != nil {
			l.fetched = time.Now()
			for _, seg := range l.segments {
				if seg.name == file || seg.init == file {
					path = filepath.Join(l.dir, file)
				}
			}
		}
		h.mu.Unlock()
		if path == "" {
			mediaError(w, ErrGrantEnded)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			mediaError(w, ErrGrantEnded)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			mediaError(w, ErrGrantEnded)
			return
		}
		switch filepath.Ext(file) {
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case ".m4s":
			w.Header().Set("Content-Type", "video/iso.segment")
		default:
			w.Header().Set("Content-Type", "video/mp2t")
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeContent(w, r, file, info.ModTime(), f)
		return
	}
	asked := r.URL.Query().Get("at")
	var at time.Time
	if asked != "" {
		var err error
		if at, err = time.Parse(time.RFC3339Nano, asked); err != nil {
			writeMediaError(w, http.StatusBadRequest, "invalid_field", "Use a time such as 2026-11-10T20:00:00Z for at.", false)
			return
		}
	}
	wait, cancel := context.WithTimeout(r.Context(), liveReadyWait)
	defer cancel()
	h.mu.Lock()
	l := s.live[sessionID]
	if l == nil {
		h.mu.Unlock()
		mediaError(w, ErrGrantEnded)
		return
	}
	l.fetched = time.Now()
	if asked != "" && asked != l.asked {
		l.asked = asked
		if !l.holds(at) && !l.ended && l.failure == nil {
			// Another moment: this device's conversion starts again from there.
			if l.cancel != nil {
				l.cancel()
				l.cancel = nil
			}
			l.clear()
			s.runLiveLocked(l, at)
		}
	}
	if l.cancel == nil && !l.ended && l.failure == nil {
		// Stopped while nobody fetched: it carries on from where it stopped.
		s.runLiveLocked(l, l.resume())
	}
	for len(l.segments) == 0 && !l.ended && l.failure == nil && s.live[sessionID] == l {
		changed := l.changed
		h.mu.Unlock()
		select {
		case <-wait.Done():
			if r.Context().Err() != nil {
				return
			}
			mediaError(w, ErrSegmentPreparing)
			return
		case <-changed:
		}
		h.mu.Lock()
	}
	var body []byte
	failed := l.failure != nil
	if !failed && s.live[sessionID] == l {
		body = l.playlist()
	}
	h.mu.Unlock()
	switch {
	case failed:
		mediaError(w, errMediaConversionFailed)
	case body == nil:
		mediaError(w, ErrGrantEnded)
	default:
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Write(body)
	}
}

// playlist is the conversion's live playlist: every segment kept, each with its moment on the
// channel's clock, and the end once the channel has stopped. Caller holds h.mu.
func (l *liveConversion) playlist() []byte {
	target := HLSSegmentSeconds
	for _, seg := range l.segments {
		target = max(target, int(math.Ceil(seg.duration)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n#EXT-X-INDEPENDENT-SEGMENTS\n",
		target, l.sequence, l.discontinuity)
	for i, seg := range l.segments {
		changed := i > 0 && seg.run != l.segments[i-1].run
		if changed {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if seg.init != "" && (i == 0 || changed) {
			fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"%s\"\n", seg.init)
		}
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.6f,\n%s\n", seg.start.UTC().Format("2006-01-02T15:04:05.000Z07:00"), seg.duration, seg.name)
	}
	if l.ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return []byte(b.String())
}
