// Package linearbuffer owns closed, immutable HLS segments for one existing
// playback occurrence. It is not a tuner allocator or a playback session owner.
package linearbuffer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrExpired = errors.New("timeshift window expired")
var ErrGap = errors.New("requested position has no retained media")
var ErrClosed = errors.New("linear buffer closed")
var ErrMedia = errors.New("invalid linear output")
var errOutputPending = errors.New("linear output awaiting completed segment")

const maxSegmentBytes int64 = 64 << 20
const maxPending = 8

type Options struct {
	LiveDelay time.Duration
	Parent    string
	OriginMS  int64
	MaxBytes  int64
	Retention time.Duration
	Scheduled bool
}
type Interval struct{ StartUS, EndUS int64 }
type Window struct {
	OriginMS, Revision, StartUS, EndUS, LiveUS int64
	Gaps                                       []Interval
	Ready                                      bool
}
type Producer struct {
	// OnPlayable acknowledges the first accepted nonempty elementary media
	// output, never setup, initialization or transport/PES headers. An error
	// refuses successful output acknowledgement and lets normal producer
	// recovery retry the same real occurrence. It must not call Buffer methods.
	OnPlayable func() error
	ID         int64
	// For scheduled sources, UTC = source PTS + TimelineBaseMS. The decoder
	// preserves the source clock including the server-selected input seek.
	Scheduled      bool
	TimelineBaseMS int64
	EndMS          int64
	// Fragmented producers publish fragmented MP4 (an init segment and .m4s
	// fragments) instead of MPEG-TS, for codecs MPEG-TS cannot carry (FLAC,
	// Opus) and pictures players take only in MP4 (HEVC on Apple, AV1).
	Fragmented bool
}
type segment struct {
	sequence, producer    int64
	fragmented            bool
	path                  string
	startUS, endUS        int64
	bytes                 int64
	discontinuity         bool
	discontinuitiesBefore int64
	// offsetUS is the segment's wall clock minus its media clock: media time
	// plus offsetUS is the moment it shows (microseconds since the epoch).
	offsetUS int64
	readers  int
	expired  bool
}

// initFile is one fragmented producer's init segment: every fragment of that
// producer is decoded with it.
type initFile struct {
	path           string
	bytes          int64
	readers        int
	expired        bool
	hash           string
	clock          fmp4Init
	producer, uses int64
}
type Buffer struct {
	owner                                  *os.File
	mu                                     sync.Mutex
	options                                Options
	directory                              string
	segments                               []*segment
	byID                                   map[int64]*segment
	inits                                  map[int64]*initFile
	next, revision, bytes, discontinuities int64
	// producedUS is the duration of every segment ever appended. Unlike the
	// window, it never shrinks when retention or the byte cap removes media.
	producedUS int64
	readers    int
	closed     bool
}

func New(o Options) (*Buffer, error) {
	if o.OriginMS <= 0 || o.MaxBytes < maxSegmentBytes || o.MaxBytes > 16<<30 || o.Retention < 12*time.Second || o.Retention > 24*time.Hour || !filepath.IsAbs(o.Parent) {
		return nil, ErrMedia
	}
	canonical, e := filepath.EvalSymlinks(o.Parent)
	if e != nil || canonical != o.Parent {
		return nil, ErrMedia
	}
	info, e := os.Lstat(o.Parent)
	if e != nil || !info.IsDir() {
		return nil, ErrMedia
	}
	dir, owner, e := createOwnedDirectory(o.Parent)
	if e != nil {
		return nil, e
	}
	return &Buffer{options: o, directory: dir, owner: owner, byID: map[int64]*segment{}, inits: map[int64]*initFile{}, next: 1}, nil
}
func (b *Buffer) Directory() string { return b.directory }
func (b *Buffer) SetLiveDelay(seconds int) error {
	if seconds < 0 || seconds > 120 {
		return ErrMedia
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.options.LiveDelay = time.Duration(seconds) * time.Second
	return nil
}
func (b *Buffer) Publisher(p Producer, check func() error) (*Publisher, error) {
	if p.ID < 1 || check == nil || p.Scheduled && (p.TimelineBaseMS <= 0 || p.EndMS <= p.TimelineBaseMS) {
		return nil, ErrMedia
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	// One playlist cannot switch between MPEG-TS and fragmented MP4: an init
	// segment applies to everything after it.
	if len(b.segments) > 0 && b.segments[len(b.segments)-1].fragmented != p.Fragmented {
		return nil, ErrMedia
	}
	return &Publisher{buffer: b, producer: p, check: check, pending: map[int64]pending{}, hashes: map[int64]string{}, last: -1, failed: make(chan struct{}), changed: make(chan struct{})}, nil
}
func (b *Buffer) windowLocked(now int64) Window {
	w := Window{OriginMS: b.options.OriginMS, Revision: b.revision, Gaps: []Interval{}}
	if b.closed || len(b.segments) == 0 {
		return w
	}
	origin := b.options.OriginMS * 1000
	w.StartUS = b.segments[0].startUS - origin
	w.EndUS = b.segments[len(b.segments)-1].endUS - origin
	if b.options.Scheduled && w.EndUS > now-origin {
		w.EndUS = now - origin
	}
	w.Ready = w.EndUS > w.StartUS && b.producedUS >= b.options.LiveDelay.Microseconds()
	w.LiveUS = max(w.StartUS, w.EndUS-b.options.LiveDelay.Microseconds())
	for n := 1; n < len(b.segments); n++ {
		a, z := b.segments[n-1], b.segments[n]
		if z.startUS > a.endUS+1000 {
			w.Gaps = append(w.Gaps, Interval{a.endUS - origin, z.startUS - origin})
		}
	}
	return w
}

// Produced is the total duration of media producers have appended, including
// what has since been evicted. The difference between two readings is what was
// added in between; gaps between segments are not media and are not counted.
func (b *Buffer) Produced() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Duration(b.producedUS) * time.Microsecond
}
func (b *Buffer) Window() Window {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.windowLocked(time.Now().UnixMicro())
}
func (b *Buffer) Resolve(position *int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.windowLocked(time.Now().UnixMicro())
	if !w.Ready {
		return 0, ErrExpired
	}
	target := w.LiveUS - 2_000_000
	if b.options.Scheduled {
		target = w.LiveUS - 100_000
	}
	if target < w.StartUS {
		target = w.StartUS
	}
	if position != nil {
		target = *position
	}
	if target < w.StartUS || target >= w.EndUS {
		return 0, ErrExpired
	}
	absolute := target + b.options.OriginMS*1000
	for _, s := range b.segments {
		if absolute >= s.startUS && absolute < s.endUS {
			return target, nil
		}
	}
	return 0, ErrGap
}
func (b *Buffer) Producers() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := map[int64]bool{}
	for _, s := range b.segments {
		set[s.producer] = true
	}
	ids := []int64{}
	for n := range set {
		ids = append(ids, n)
	}
	return ids
}
func (b *Buffer) Playlist() ([]byte, error) { return b.PlaylistFrom(nil) }

// PlaylistFrom is the playlist starting at the segment holding a channel
// position (microseconds from the origin): a player that cannot seek in a live
// playlist (mpv) reopens it there instead. nil is the whole window.
func (b *Buffer) PlaylistFrom(position *int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.segments) == 0 {
		return nil, ErrExpired
	}
	segments := b.segments
	if position != nil {
		at := b.options.OriginMS*1000 + *position
		first := len(segments) - 1
		for i, s := range segments {
			if s.endUS > at {
				first = i
				break
			}
		}
		segments = segments[first:]
	}
	var out strings.Builder
	target := 1
	for _, s := range segments {
		n := int(math.Ceil(float64(s.endUS-s.startUS) / 1e6))
		if n > target {
			target = n
		}
	}
	first := segments[0]
	version := 3
	if first.fragmented {
		version = 7
	}
	fmt.Fprintf(&out, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", version, target, first.sequence, first.discontinuitiesBefore)
	if b.options.LiveDelay > 0 && position == nil {
		window := b.windowLocked(time.Now().UnixMicro())
		offset := max(window.StartUS, window.LiveUS-2_000_000) - window.EndUS
		fmt.Fprintf(&out, "#EXT-X-START:TIME-OFFSET=%.6f,PRECISE=YES\n", float64(offset)/1e6)
	}
	// A channel change/restart changes buffer generation, but a programme change
	// stays on this resource with an explicit decoder-clock discontinuity.
	mapped := int64(0)
	for _, s := range segments {
		if s.discontinuity {
			out.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		extension := "ts"
		if s.fragmented {
			extension = "m4s"
			// Each producer's fragments are decoded with that producer's init.
			if s.producer != mapped {
				fmt.Fprintf(&out, "#EXT-X-MAP:URI=\"i%d.mp4\"\n", s.producer)
				mapped = s.producer
			}
		}
		fmt.Fprintf(&out, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.6f,\ns%d.%s\n", time.UnixMicro(s.startUS).UTC().Format(time.RFC3339Nano), float64(s.endUS-s.startUS)/1e6, s.sequence, extension)
	}
	return []byte(out.String()), nil
}

type Reader struct {
	File     *os.File
	Producer int64
	Size     int64
	// ContentType is the object's media type: MPEG-TS, an MP4 fragment or an
	// MP4 init segment.
	ContentType string
	once        sync.Once
	release     func()
}

func (r *Reader) Close() error {
	var e error
	r.once.Do(func() { e = r.File.Close(); r.release() })
	return e
}

var publicName = regexp.MustCompile(`^s([1-9][0-9]{0,18})\.(ts|m4s)$`)
var publicInit = regexp.MustCompile(`^i([1-9][0-9]{0,18})\.mp4$`)

func (b *Buffer) Open(name string) (*Reader, error) {
	if match := publicInit.FindStringSubmatch(name); match != nil {
		return b.openInit(match[1])
	}
	match := publicName.FindStringSubmatch(name)
	if match == nil {
		return nil, ErrExpired
	}
	id, e := strconv.ParseInt(match[1], 10, 64)
	if e != nil {
		return nil, ErrExpired
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	s := b.byID[id]
	if s == nil || s.expired || s.fragmented != (match[2] == "m4s") {
		b.mu.Unlock()
		return nil, ErrExpired
	}
	file, e := os.Open(s.path)
	if e != nil {
		b.mu.Unlock()
		return nil, e
	}
	s.readers++
	b.readers++
	b.mu.Unlock()
	kind := "video/mp2t"
	if s.fragmented {
		kind = "video/iso.segment"
	}
	return &Reader{File: file, Producer: s.producer, Size: s.bytes, ContentType: kind, release: func() {
		b.mu.Lock()
		s.readers--
		b.readers--
		remove := s.expired && s.readers == 0
		all := b.closed && b.readers == 0
		b.mu.Unlock()
		if remove {
			_ = os.Remove(s.path)
		}
		if all {
			_ = b.removeDirectory()
		}
	}}, nil
}
func (b *Buffer) append(s *segment) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if len(b.segments) > 0 {
		last := b.segments[len(b.segments)-1]
		if s.startUS < last.endUS-250_000 {
			return ErrMedia
		}
		if s.producer != last.producer || s.startUS > last.endUS+50_000 {
			s.discontinuity = true
		}
	}
	if s.fragmented {
		init := b.inits[s.producer]
		if init == nil || init.expired {
			return ErrMedia
		}
		init.uses++
	}
	s.sequence = b.next
	b.next++
	s.discontinuitiesBefore = b.discontinuities
	if s.discontinuity {
		b.discontinuities++
	}
	b.segments = append(b.segments, s)
	b.byID[s.sequence] = s
	b.bytes += s.bytes
	b.producedUS += s.endUS - s.startUS
	b.revision++
	threshold := s.endUS - b.options.Retention.Microseconds()
	for len(b.segments) > 1 && (b.bytes > b.options.MaxBytes || b.segments[0].endUS < threshold) {
		old := b.segments[0]
		b.segments = b.segments[1:]
		delete(b.byID, old.sequence)
		old.expired = true
		b.bytes -= old.bytes
		if old.readers == 0 {
			_ = os.Remove(old.path)
		}
		if init := b.inits[old.producer]; init != nil && old.fragmented {
			init.uses--
			if init.uses <= 0 {
				b.retireInitLocked(old.producer)
			}
		}
	}
	return nil
}
func (b *Buffer) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for _, s := range b.segments {
		s.expired = true
		if s.readers == 0 {
			_ = os.Remove(s.path)
		}
	}
	b.byID = map[int64]*segment{}
	b.segments = nil
	for producer := range b.inits {
		b.retireInitLocked(producer)
	}
	all := b.readers == 0
	b.mu.Unlock()
	if all {
		return b.removeDirectory()
	}
	return nil
}

type pending struct {
	path, hash string
	bytes      int64
}
type Publisher struct {
	changed                                         chan struct{}
	final                                           bool
	manifest                                        []byte
	manifestLast                                    int64
	failure                                         error
	failed                                          chan struct{}
	mu                                              sync.Mutex
	buffer                                          *Buffer
	producer                                        Producer
	check                                           func() error
	pending                                         map[int64]pending
	hashes                                          map[int64]string
	last                                            int64
	anchored                                        bool
	anchorPTS, anchorUS, previousPTS, previousEndUS int64
	closed                                          bool
	playableReported                                bool
	// init is the producer's accepted init segment (fragmented producers).
	init *initFile
}

var privateName = regexp.MustCompile(`^segment-([0-9]{9,19})\.ts$`)
var privateFragment = regexp.MustCompile(`^segment-([0-9]{9,19})\.m4s$`)

func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "PUT" || r.URL.RawQuery != "" || strings.Contains(r.URL.Path, "/") {
		http.Error(w, "invalid output", 400)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.notify()
	if p.closed || p.failure != nil || p.check() != nil {
		http.Error(w, "retired output", 410)
		return
	}
	var e error
	if r.URL.Path == "index.m3u8" {
		var raw []byte
		raw, e = io.ReadAll(io.LimitReader(r.Body, 256*1024+1))
		if e == nil && len(raw) > 256*1024 {
			e = ErrMedia
		}
		if e == nil {
			e = p.acceptManifest(raw)
		}
	} else if r.URL.Path == "init.mp4" && p.producer.Fragmented {
		e = p.storeInit(r.Body)
		if e == nil {
			e = p.flushManifest()
		}
	} else {
		e = p.store(r.URL.Path, r.Body)
		if e == nil {
			e = p.flushManifest()
		}
	}
	if e != nil {
		p.failure = e
		close(p.failed)
		http.Error(w, "output rejected", 422)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
func (p *Publisher) store(name string, body io.Reader) error {
	pattern := privateName
	if p.producer.Fragmented {
		pattern = privateFragment
	}
	match := pattern.FindStringSubmatch(name)
	if match == nil {
		return ErrMedia
	}
	number, e := strconv.ParseInt(match[1], 10, 64)
	if e != nil {
		return ErrMedia
	}
	if len(p.pending) >= maxPending {
		return ErrMedia
	}
	file, e := os.CreateTemp(p.buffer.directory, fmt.Sprintf("p%d-", p.producer.ID))
	if e != nil {
		return e
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, h), io.LimitReader(body, maxSegmentBytes+1))
	if e != nil || n < 188 && !p.producer.Fragmented || n < 16 || n > maxSegmentBytes {
		return ErrMedia
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if old, ok := p.hashes[number]; ok {
		if old != hash {
			return ErrMedia
		}
		return nil
	}
	if number <= p.last {
		return ErrExpired
	}
	if old, ok := p.pending[number]; ok {
		if old.hash != hash {
			return ErrMedia
		}
		return nil
	}
	if e = file.Close(); e != nil {
		return e
	}
	keep = true
	p.pending[number] = pending{path, hash, n}
	return nil
}

type manifestEntry struct{ number, durationUS int64 }

func parseManifest(raw []byte, fragmented bool) ([]manifestEntry, error) {
	name := privateName
	if fragmented {
		name = privateFragment
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	if !scanner.Scan() || scanner.Text() != "#EXTM3U" {
		return nil, ErrMedia
	}
	values := []manifestEntry{}
	duration := int64(0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			if duration != 0 {
				return nil, ErrMedia
			}
			text, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			v, e := strconv.ParseFloat(text, 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 60 {
				return nil, ErrMedia
			}
			duration = int64(math.Round(v * 1e6))
			continue
		}
		if strings.HasPrefix(line, "#") {
			// The only addressed tag a producer writes is its own init segment.
			if fragmented && line == `#EXT-X-MAP:URI="init.mp4"` {
				continue
			}
			if strings.HasPrefix(line, "#EXT-X-KEY") || strings.HasPrefix(line, "#EXT-X-MAP") || strings.Contains(line, "URI=") {
				return nil, ErrMedia
			}
			continue
		}
		match := name.FindStringSubmatch(line)
		if duration == 0 || match == nil || len(values) >= 32 {
			return nil, ErrMedia
		}
		n, e := strconv.ParseInt(match[1], 10, 64)
		if e != nil || len(values) > 0 && n <= values[len(values)-1].number {
			return nil, ErrMedia
		}
		values = append(values, manifestEntry{n, duration})
		duration = 0
	}
	if scanner.Err() != nil || duration != 0 || len(values) == 0 {
		return nil, ErrMedia
	}
	return values, nil
}

// FFmpeg can close one PUT and start the next before the server has finished
// its previous handler. Retain the newest bounded manifest until all referenced
// immutable uploads arrive; a pending manifest is not published media.
func (p *Publisher) acceptManifest(raw []byte) error {
	entries, e := parseManifest(raw, p.producer.Fragmented)
	if e != nil {
		return e
	}
	last := entries[len(entries)-1].number
	if last < p.manifestLast {
		return nil
	}
	p.manifestLast = last
	p.final = p.final || strings.Contains("\n"+string(raw)+"\n", "\n#EXT-X-ENDLIST\n")
	p.manifest = append([]byte(nil), raw...)
	return p.flushManifest()
}
func (p *Publisher) flushManifest() error {
	if len(p.manifest) == 0 {
		return nil
	}
	e := p.publish(p.manifest)
	if errors.Is(e, errOutputPending) {
		return nil
	}
	if e == nil {
		p.manifest = nil
	}
	return e
}
func (p *Publisher) notify() { close(p.changed); p.changed = make(chan struct{}) }

// Finish waits for the final ENDLIST PUT and every closed media object after
// physical decoder exit. FFmpeg does not await all HTTP handlers before exiting.
func (p *Publisher) Finish(ctx context.Context) error {
	for {
		p.mu.Lock()
		failure, closed, complete, changed := p.failure, p.closed, p.final && len(p.manifest) == 0 && len(p.pending) == 0, p.changed
		p.mu.Unlock()
		if failure != nil {
			return failure
		}
		if closed {
			return ErrClosed
		}
		if complete {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrMedia, ctx.Err())
		case <-changed:
		}
	}
}
func (p *Publisher) Failed() <-chan struct{} { return p.failed }
func (p *Publisher) Result() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		return p.failure
	}
	if len(p.manifest) > 0 {
		return ErrMedia
	}
	return nil
}
func (p *Publisher) publish(raw []byte) error {
	entries, e := parseManifest(raw, p.producer.Fragmented)
	if e != nil {
		return e
	}
	if p.producer.Fragmented && p.init == nil {
		// Fragments cannot be dated, or played, before their init arrives.
		return errOutputPending
	}
	for _, entry := range entries {
		if entry.number <= p.last {
			continue
		}
		value, ok := p.pending[entry.number]
		if !ok {
			return errOutputPending
		}
		file, e := os.Open(value.path)
		if e != nil {
			return e
		}
		var clock Clock
		if p.producer.Fragmented {
			clock, e = ReadFragmentClock(file, p.init.clock)
		} else {
			clock, e = ReadPTS(file)
		}
		playable := false
		if e == nil && p.producer.OnPlayable != nil && !p.playableReported {
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				e = seekErr
			} else {
				playable = hasPlayablePayload(file, p.producer.Fragmented)
			}
		}
		_ = file.Close()
		if e != nil {
			return e
		}
		minimum, maximum := clock.Min, clock.Max
		if p.anchored {
			minimum = unwrap(minimum, p.previousPTS)
			maximum = unwrap(maximum, minimum)
		}
		if maximum < minimum {
			maximum += ptsWrap
		}
		span := (maximum - minimum) * 1000000 / 90000
		if span > entry.durationUS+500_000 || entry.durationUS > span+2_000_000 {
			return ErrMedia
		}
		discontinuity := false
		if !p.anchored {
			p.anchorPTS = minimum
			p.anchorUS = time.Now().UnixMicro() - entry.durationUS
			p.buffer.mu.Lock()
			if len(p.buffer.segments) > 0 {
				p.anchorUS = max(p.anchorUS, p.buffer.segments[len(p.buffer.segments)-1].endUS)
			}
			p.buffer.mu.Unlock()
			if p.producer.Scheduled {
				p.anchorUS = p.producer.TimelineBaseMS*1000 + minimum*1000000/90000
			}
			p.anchored = true
		}
		start := p.anchorUS + (minimum-p.anchorPTS)*1000000/90000
		if p.producer.Scheduled {
			start = p.producer.TimelineBaseMS*1000 + minimum*1000000/90000
		}
		if p.previousEndUS != 0 && start < p.previousEndUS-50_000 {
			if p.producer.Scheduled {
				return ErrMedia
			}
			// A genuinely restarted source clock gets a new explicit epoch anchor.
			// Duration still comes from the muxed clock, never from network receipts.
			discontinuity = true
			p.anchorPTS = minimum
			p.anchorUS = max(p.previousEndUS, time.Now().UnixMicro()-entry.durationUS)
			start = p.anchorUS
		}
		end := start + entry.durationUS
		if p.producer.Scheduled && end > p.producer.EndMS*1000+100_000 {
			return ErrMedia
		}
		if e = p.check(); e != nil {
			return e
		}
		e = p.buffer.append(&segment{producer: p.producer.ID, fragmented: p.producer.Fragmented, path: value.path, startUS: start, endUS: end, bytes: value.bytes, discontinuity: discontinuity, offsetUS: start - minimum*1000000/90000})
		if e != nil {
			return e
		}
		delete(p.pending, entry.number)
		p.hashes[entry.number] = value.hash
		p.last = entry.number
		p.previousPTS = maximum
		p.previousEndUS = end
		if playable && !p.playableReported && p.producer.OnPlayable != nil {
			if e := p.producer.OnPlayable(); e != nil {
				return e
			}
			p.playableReported = true
		}
		for old := range p.hashes {
			if old < entry.number-32 {
				delete(p.hashes, old)
			}
		}
	}
	return nil
}
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.notify()
	var errs []error
	keys := []int64{}
	for k := range p.pending {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		errs = append(errs, os.Remove(p.pending[k].path))
		delete(p.pending, k)
	}
	return errors.Join(errs...)
}

func (b *Buffer) ProducerAt(position int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, ErrClosed
	}
	at := b.options.OriginMS*1000 + position
	for _, s := range b.segments {
		if at >= s.startUS && at < s.endUS {
			return s.producer, nil
		}
	}
	return 0, ErrExpired
}

func (b *Buffer) removeDirectory() error {
	return errors.Join(os.RemoveAll(b.directory), b.owner.Close())
}

// storeInit accepts the producer's init segment. FFmpeg writes it once, before
// the first fragment; an identical repeat is accepted, a different one is a
// failed producer (its fragments could no longer all be decoded with one init).
func (p *Publisher) storeInit(body io.Reader) error {
	raw, e := io.ReadAll(io.LimitReader(body, maxInitBytes+1))
	if e != nil || len(raw) > maxInitBytes || len(raw) < 16 {
		return ErrMedia
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if p.init != nil {
		if p.init.hash != hash {
			return ErrMedia
		}
		return nil
	}
	clock, ok := parseInit(raw)
	if !ok {
		return ErrMedia
	}
	file, e := os.CreateTemp(p.buffer.directory, fmt.Sprintf("i%d-", p.producer.ID))
	if e != nil {
		return e
	}
	path := file.Name()
	if _, e = file.Write(raw); e == nil {
		e = file.Close()
	} else {
		_ = file.Close()
	}
	if e != nil {
		_ = os.Remove(path)
		return e
	}
	init := &initFile{path: path, bytes: int64(len(raw)), hash: hash, clock: clock, producer: p.producer.ID}
	b := p.buffer
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.inits[p.producer.ID] != nil {
		_ = os.Remove(path)
		return ErrMedia
	}
	b.inits[p.producer.ID] = init
	p.init = init
	return nil
}

func (b *Buffer) openInit(producerText string) (*Reader, error) {
	producer, e := strconv.ParseInt(producerText, 10, 64)
	if e != nil {
		return nil, ErrExpired
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	init := b.inits[producer]
	if init == nil || init.expired {
		b.mu.Unlock()
		return nil, ErrExpired
	}
	file, e := os.Open(init.path)
	if e != nil {
		b.mu.Unlock()
		return nil, e
	}
	init.readers++
	b.readers++
	b.mu.Unlock()
	return &Reader{File: file, Producer: producer, Size: init.bytes, ContentType: "video/mp4", release: func() {
		b.mu.Lock()
		init.readers--
		b.readers--
		remove := init.expired && init.readers == 0
		all := b.closed && b.readers == 0
		b.mu.Unlock()
		if remove {
			_ = os.Remove(init.path)
		}
		if all {
			_ = b.removeDirectory()
		}
	}}, nil
}

// retireInitLocked forgets a producer's init once nothing retained uses it;
// a reader still holding it keeps the file until it is done.
func (b *Buffer) retireInitLocked(producer int64) {
	init := b.inits[producer]
	if init == nil {
		return
	}
	delete(b.inits, producer)
	init.expired = true
	if init.readers == 0 {
		_ = os.Remove(init.path)
	}
}

// Packaging reports what the retained media is: held is false for an empty
// buffer, and fragmented says whether it holds fragmented MP4.
func (b *Buffer) Packaging() (held, fragmented bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.segments) == 0 {
		return false, false
	}
	return true, b.segments[len(b.segments)-1].fragmented
}

// ClockEpoch is a stretch of the window over which one media clock maps to the
// channel's: channel position = media time + OffsetUS (all microseconds,
// positions from the origin). A new producer or a restarted source clock
// starts a new epoch.
type ClockEpoch struct{ StartUS, EndUS, OffsetUS int64 }

// maxClockEpochs bounds what a snapshot carries; older epochs drop first.
const maxClockEpochs = 32

// Clocks lists the window's clock epochs, oldest first. A player that reports
// media time rather than program dates (mpv) places itself on the channel with
// them.
func (b *Buffer) Clocks() []ClockEpoch {
	b.mu.Lock()
	defer b.mu.Unlock()
	origin := b.options.OriginMS * 1000
	out := []ClockEpoch{}
	for _, s := range b.segments {
		offset := s.offsetUS - origin
		if n := len(out); n > 0 && !s.discontinuity && abs64(out[n-1].OffsetUS-offset) <= 1000 && s.startUS-origin <= out[n-1].EndUS+50_000 {
			out[n-1].EndUS = s.endUS - origin
			continue
		}
		out = append(out, ClockEpoch{StartUS: s.startUS - origin, EndUS: s.endUS - origin, OffsetUS: offset})
	}
	if len(out) > maxClockEpochs {
		out = out[len(out)-maxClockEpochs:]
	}
	return out
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
