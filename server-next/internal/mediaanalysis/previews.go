package mediaanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// Seek previews: the sprite sheets a player shows while scrubbing, one picture
// per chapter, and one picture for an episode that has none from a provider.
// Everything here decodes and encodes; where the results are kept is the
// caller's business, through PreviewSink.
const (
	TrickplaySheetKind      = "trickplay_tiles"
	TrickplayDescriptorKind = "trickplay_set"
	ChapterImageKind        = "chapter_images"
	maxTrickplaySheetBytes  = 4 << 20
	// A sheet stays inside decoder-friendly dimensions and one screenful of
	// frames, so a scrub preview never downloads an unbounded image.
	maxSheetPixels = 4096
	maxSheetFrames = 100
)

// ErrInput says a request's numbers do not describe a title.
var ErrInput = errors.New("Analysis input is not valid.")

// ErrConflict says the decoder produced something the title's facts rule out,
// such as a frame past its end: the facts are stale.
var ErrConflict = errors.New("Analysis no longer matches the media it was planned for.")

// PreviewSink receives each produced file as soon as it is complete: its kind,
// content type, position in its set, the span of the title it covers in
// microseconds and its pixel size. The caller records it (the media_artifacts
// table) and returns an error to stop production.
type PreviewSink func(kind, contentType string, index int, startUS, endUS int64, width, height int, data []byte) error

// TrickplaySettings is the owner's choice for one library; zero values take
// the defaults.
type TrickplaySettings struct {
	IntervalSeconds int `json:"intervalSeconds"`
	TileWidth       int `json:"tileWidth"`
	MaxTiles        int `json:"maxTiles"`
}

const (
	DefaultTrickplayIntervalSeconds = 10
	DefaultTrickplayTileWidth       = 320
	DefaultTrickplayMaxTiles        = 1080
	// MaxTrickplayTiles is the most frames one library may ask for.
	MaxTrickplayTiles = 4096
)

// Normalized returns the effective production geometry. Callers never see a
// zero, a negative, or an unbounded frame budget.
func (t TrickplaySettings) Normalized() TrickplaySettings {
	if t.IntervalSeconds == 0 {
		t.IntervalSeconds = DefaultTrickplayIntervalSeconds
	}
	if t.TileWidth == 0 {
		t.TileWidth = DefaultTrickplayTileWidth
	}
	if t.MaxTiles == 0 {
		t.MaxTiles = DefaultTrickplayMaxTiles
	}
	t.IntervalSeconds = min(600, max(1, t.IntervalSeconds))
	t.TileWidth = min(640, max(64, t.TileWidth/2*2))
	t.MaxTiles = min(MaxTrickplayTiles, max(16, t.MaxTiles))
	return t
}

// TrickplayDescriptor is the durable geometry of one produced set. It is
// handed to the sink once, as JSON, after the last sheet.
type TrickplayDescriptor struct {
	Version    int    `json:"version"`
	TileWidth  int    `json:"tileWidth"`
	TileHeight int    `json:"tileHeight"`
	Columns    int    `json:"columns"`
	Rows       int    `json:"rows"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	IntervalUS string `json:"intervalUS"`
	FrameCount int    `json:"frameCount"`
	TileCount  int    `json:"tileCount"`
	DurationUS string `json:"durationUS"`
}

func (d TrickplayDescriptor) interval() int64 { n, _ := parseUS(d.IntervalUS); return n }
func (d TrickplayDescriptor) duration() int64 { n, _ := parseUS(d.DurationUS); return n }
func (d TrickplayDescriptor) perSheet() int   { return d.Columns * d.Rows }
func (d TrickplayDescriptor) valid() bool {
	if d.Version != 1 || d.TileWidth < 16 || d.TileHeight < 16 || d.Columns < 1 || d.Rows < 1 {
		return false
	}
	if d.Width != d.Columns*d.TileWidth || d.Height != d.Rows*d.TileHeight || d.Width > maxSheetPixels || d.Height > maxSheetPixels {
		return false
	}
	if d.FrameCount < 1 || d.FrameCount > 65536 || d.perSheet() > maxSheetFrames {
		return false
	}
	if d.TileCount != (d.FrameCount+d.perSheet()-1)/d.perSheet() {
		return false
	}
	return d.interval() > 0 && d.duration() > 0
}

type trickplayLayout struct {
	TileWidth, TileHeight   int
	Columns, Rows, PerSheet int
	IntervalUS              int64
	MaxFrames               int
}

// trickplayGeometry folds the owner's library policy into the operator's frame
// budget. The smaller bound always wins; neither is inferred from the media.
func trickplayGeometry(policy TrickplaySettings, budget int) trickplayLayout {
	p := policy.Normalized()
	width := p.TileWidth
	height := width * 9 / 16
	if height%2 != 0 {
		height++
	}
	columns := max(1, min(10, maxSheetPixels/width))
	rows := max(1, min(10, maxSheetPixels/height))
	for columns*rows > maxSheetFrames {
		if rows > 1 {
			rows--
			continue
		}
		columns--
	}
	frames := p.MaxTiles
	if budget > 0 {
		frames = min(frames, budget)
	}
	return trickplayLayout{TileWidth: width, TileHeight: height, Columns: columns, Rows: rows, PerSheet: columns * rows, IntervalUS: int64(p.IntervalSeconds) * 1000000, MaxFrames: max(1, frames)}
}
func (g trickplayLayout) descriptor(frames int, interval, duration int64) TrickplayDescriptor {
	return TrickplayDescriptor{Version: 1, TileWidth: g.TileWidth, TileHeight: g.TileHeight, Columns: g.Columns, Rows: g.Rows,
		Width: g.Columns * g.TileWidth, Height: g.Rows * g.TileHeight, IntervalUS: fmt.Sprint(interval),
		FrameCount: frames, TileCount: (frames + g.PerSheet - 1) / g.PerSheet, DurationUS: fmt.Sprint(duration)}
}

type trickplaySheet struct {
	layout trickplayLayout
	canvas *image.RGBA
	frames int
}

func newTrickplaySheet(g trickplayLayout) *trickplaySheet {
	return &trickplaySheet{layout: g, canvas: image.NewRGBA(image.Rect(0, 0, g.Columns*g.TileWidth, g.Rows*g.TileHeight))}
}
func (s *trickplaySheet) width() int  { return s.layout.Columns * s.layout.TileWidth }
func (s *trickplaySheet) height() int { return s.layout.Rows * s.layout.TileHeight }
func (s *trickplaySheet) place(frame image.Image) {
	column, row := s.frames%s.layout.Columns, s.frames/s.layout.Columns
	at := image.Rect(column*s.layout.TileWidth, row*s.layout.TileHeight, (column+1)*s.layout.TileWidth, (row+1)*s.layout.TileHeight)
	draw.Draw(s.canvas, at, frame, image.Point{}, draw.Src)
	s.frames++
}
func (s *trickplaySheet) reset() {
	draw.Draw(s.canvas, s.canvas.Bounds(), image.Black, image.Point{}, draw.Src)
	s.frames = 0
}
func (s *trickplaySheet) encode() ([]byte, error) {
	var data bytes.Buffer
	if e := jpeg.Encode(&data, s.canvas, &jpeg.Options{Quality: 75}); e != nil {
		return nil, e
	}
	if data.Len() > maxTrickplaySheetBytes {
		return nil, ErrBudget
	}
	return data.Bytes(), nil
}

func trickplayCue(us int64) string {
	ms := us / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
func trickplayVTT(d TrickplayDescriptor, start, end int64) string {
	var out strings.Builder
	out.WriteString("WEBVTT\n")
	interval, duration := d.interval(), d.duration()
	for i := 0; i < d.FrameCount; i++ {
		from, to := int64(i)*interval, min(duration, int64(i+1)*interval)
		if to <= start || from >= end || to <= from {
			continue
		}
		tile, within := i/d.perSheet(), i%d.perSheet()
		x, y := (within%d.Columns)*d.TileWidth, (within/d.Columns)*d.TileHeight
		out.WriteString("\n")
		out.WriteString(trickplayCue(max(from, start) - start))
		out.WriteString(" --> ")
		out.WriteString(trickplayCue(min(to, end) - start))
		out.WriteString("\n")
		fmt.Fprintf(&out, "tiles/%d.jpg#xywh=%d,%d,%d,%d\n", tile, x, y, d.TileWidth, d.TileHeight)
	}
	return out.String()
}

// Tile delivers one sprite sheet. The set is re-resolved inside the read so a
// retired result or replaced inventory cannot be served from a stale handle.

func parseUS(s string) (int64, error) {
	if s == "" || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, ErrInput
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInput
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 || n > (1<<53)-1 {
		return 0, ErrInput
	}
	return n, nil
}

// FileDecode returns a Decode that runs the confined decoder over one source
// the server holds open. The decoder reads it through a private loopback
// bridge and never sees a path.
func FileDecode(supervisor *processpool.Supervisor, ffmpeg string, libraries []string, source io.ReaderAt, size int64) Decode {
	return func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
		bridge, err := inputbridge.Open(ctx, source, size)
		if err != nil {
			return err
		}
		defer bridge.Close()
		reservation, err := bridge.Reserve()
		if err != nil {
			return err
		}
		return decoder.RunAnalysis(ctx, supervisor, key, ffmpeg, bridge.URL(), reservation, spec, consume, libraries...)
	}
}

// TrickplayRequest is one title to make scrub previews for.
type TrickplayRequest struct {
	DurationUS  int64
	SourceVideo decoder.PreviewVideoFacts
	Settings    TrickplaySettings
	// MaxFrames is the server's own bound on frames per title; zero is none
	// beyond the library's setting.
	MaxFrames int
}

// MakeTrickplay decodes one small frame per interval and hands the sink each
// sprite sheet as it fills, then the set's descriptor. A long title never
// holds more than one sheet in memory.
func MakeTrickplay(ctx context.Context, decode Decode, key string, r TrickplayRequest, add PreviewSink) (TrickplayDescriptor, error) {
	if decode == nil || add == nil || r.DurationUS <= 0 || r.DurationUS > (1<<53)-1 {
		return TrickplayDescriptor{}, ErrInput
	}
	geometry := trickplayGeometry(r.Settings, r.MaxFrames)
	spec := decoder.AnalysisSpec{Kind: "trickplay", MaxDurationUS: r.DurationUS, SourceVideo: r.SourceVideo}
	spec.MaxFrames = geometry.MaxFrames
	spec.Width, spec.Height = geometry.TileWidth, geometry.TileHeight
	spec.IntervalUS = max(geometry.IntervalUS, (r.DurationUS+int64(spec.MaxFrames)-1)/int64(spec.MaxFrames))
	count := 0
	// A sheet is filled in decode order and sealed the moment it is complete,
	// so a long source never holds more than one sprite sheet in memory.
	sheet := newTrickplaySheet(geometry)
	flush := func() error {
		if sheet.frames == 0 {
			return nil
		}
		data, e := sheet.encode()
		if e != nil {
			return e
		}
		index := (count - 1) / geometry.PerSheet
		start := int64(index*geometry.PerSheet) * spec.IntervalUS
		end := min(r.DurationUS, int64(count)*spec.IntervalUS)
		if e = add(TrickplaySheetKind, "image/jpeg", index, start, end, sheet.width(), sheet.height(), data); e != nil {
			return e
		}
		sheet.reset()
		return nil
	}
	err := decode(ctx, key, spec, func(frames io.Reader) error {
		frame := make([]byte, geometry.TileWidth*geometry.TileHeight*3)
		for {
			_, e := io.ReadFull(frames, frame)
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			if count >= spec.MaxFrames {
				return ErrBudget
			}
			img := image.NewRGBA(image.Rect(0, 0, geometry.TileWidth, geometry.TileHeight))
			for p := 0; p < geometry.TileWidth*geometry.TileHeight; p++ {
				img.SetRGBA(p%geometry.TileWidth, p/geometry.TileWidth, color.RGBA{frame[p*3], frame[p*3+1], frame[p*3+2], 255})
			}
			if int64(count)*spec.IntervalUS >= r.DurationUS {
				return ErrConflict
			}
			sheet.place(img)
			count++
			if count%geometry.PerSheet == 0 {
				if e = flush(); e != nil {
					return e
				}
			}
		}
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return TrickplayDescriptor{}, err
	}
	if count == 0 {
		return TrickplayDescriptor{}, ErrUnsupported
	}
	descriptor := geometry.descriptor(count, spec.IntervalUS, r.DurationUS)
	if !descriptor.valid() {
		return TrickplayDescriptor{}, ErrUnsupported
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return TrickplayDescriptor{}, err
	}
	end := min(r.DurationUS, max(1, int64(count)*spec.IntervalUS))
	return descriptor, add(TrickplayDescriptorKind, "application/json", 0, 0, end, descriptor.Width, descriptor.Height, raw)
}

// TrickplayVTT is the WebVTT thumbnail track for a produced set: one cue per
// frame, pointing into its sheet as tiles/<n>.jpg#xywh=...
func TrickplayVTT(d TrickplayDescriptor) string { return trickplayVTT(d, 0, d.duration()) }

// Chapter is one chapter's span in microseconds.
type Chapter struct{ StartUS, EndUS int64 }

// MakeChapterImages decodes one 320x180 picture at the start of each chapter.
func MakeChapterImages(
	ctx context.Context, decode Decode, key string, durationUS int64, video decoder.PreviewVideoFacts, chapters []Chapter, add PreviewSink,
) error {
	if decode == nil || add == nil || durationUS <= 0 {
		return ErrInput
	}
	spec := decoder.AnalysisSpec{Kind: "image", MaxDurationUS: durationUS, SourceVideo: video}
	for i, c := range chapters {
		if c.StartUS < 0 || c.EndUS > durationUS {
			return ErrConflict
		}
		spec.StartUS = c.StartUS
		var data []byte
		if err := decode(ctx, key+":"+fmt.Sprint(i), spec, func(r io.Reader) error {
			var e error
			data, e = io.ReadAll(io.LimitReader(r, (1<<20)+1))
			if len(data) > 1<<20 {
				return ErrBudget
			}
			return e
		}); err != nil {
			return err
		}
		cfg, e := jpeg.DecodeConfig(bytes.NewReader(data))
		if e != nil || cfg.Width != 320 || cfg.Height != 180 {
			return ErrUnsupported
		}
		if err := add(ChapterImageKind, "image/jpeg", i, c.StartUS, c.EndUS, 320, 180, data); err != nil {
			return err
		}
	}
	return nil
}

// Episode thumbnail modes: the library's setting for episodes without a picture.
const (
	EpisodeThumbnailDefaultMode  = "provider_then_made"
	EpisodeThumbnailProviderMode = "provider"
	EpisodeThumbnailOffMode      = "off"
)

// EpisodeThumbnailRequest asks for one episode's picture. ProviderStill says a
// provider's picture is already available; Manual is the owner asking for a
// made one whatever the mode says.
type EpisodeThumbnailRequest struct {
	Mode          string
	Manual        bool
	ProviderStill bool
	DurationUS    int64
	StartUS       int64
	EndUS         int64
	SourceVideo   decoder.PreviewVideoFacts
}

// EpisodeThumbnailResult is the picture, or where it should come from instead:
// Origin is "generated", "provider", or "fallback" when there is none.
type EpisodeThumbnailResult struct {
	Origin        string
	JPEG          []byte
	Width, Height int
	PositionUS    int64
}

// MakeEpisodeThumbnail produces exactly one bounded picture, or chooses an
// already available provider still for automatic work. Manual generation ignores
// the automatic mode and provider presence. Nothing is decoded when the mode or
// a provider still answers the request.
func MakeEpisodeThumbnail(ctx context.Context, decode Decode, r EpisodeThumbnailRequest) (EpisodeThumbnailResult, error) {
	out := EpisodeThumbnailResult{Origin: "fallback"}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	switch r.Mode {
	case EpisodeThumbnailOffMode, EpisodeThumbnailProviderMode, EpisodeThumbnailDefaultMode:
	default:
		return out, ErrInput
	}
	if !r.Manual {
		if r.Mode == EpisodeThumbnailOffMode {
			return out, nil
		}
		if r.ProviderStill {
			out.Origin = "provider"
			return out, nil
		}
		if r.Mode == EpisodeThumbnailProviderMode {
			return out, nil
		}
	}
	if decode == nil || r.DurationUS <= 0 || r.DurationUS > (1<<53)-1 || r.StartUS < 0 || r.EndUS <= r.StartUS || r.EndUS > r.DurationUS {
		return out, ErrInput
	}
	// Ten percent avoids most opening cards; a short episode still has a valid
	// seek strictly inside its duration.
	position := min(r.EndUS-1, r.StartUS+(r.EndUS-r.StartUS)/10)
	spec := decoder.AnalysisSpec{Kind: "image", StartUS: position, MaxDurationUS: r.DurationUS, SourceVideo: r.SourceVideo}
	var data []byte
	err := decode(ctx, "episode_thumbnail", spec, func(reader io.Reader) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if len(data) > 1<<20 {
			return ErrBudget
		}
		return err
	})
	if err = errors.Join(err, ctx.Err()); err != nil {
		return out, err
	}
	shape, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || shape.Width != 320 || shape.Height != 180 {
		return out, ErrUnsupported
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		return out, ErrUnsupported
	}
	return EpisodeThumbnailResult{Origin: "generated", JPEG: data, Width: 320, Height: 180, PositionUS: position}, nil
}
