// Package mediaengine joins the session layer to the media engine: it turns a session's
// choices into a streaming plan and runs the plan as a stream. The decisions themselves are
// streaming.Plan's; nothing here second-guesses them.
package mediaengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/preferences"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// Options says where the engine works.
type Options struct {
	// Folder is where conversions are written. It is emptied at start.
	Folder string
	// FFmpeg and FFprobe are the tools to run; empty finds the bundled or installed ones.
	FFmpeg, FFprobe string
	// Log is told when ffmpeg cannot be asked what it can do; nil uses the default logger.
	Log *slog.Logger
}

// Engine is the session layer's Planner and Streams, backed by the real media engine.
type Engine struct {
	db      *db.DB
	streams *streaming.Streams
	probe   mediafacts.Probe
	// asked is closed once ffmpeg has said what it can do. Until then converter and
	// fragmented are not to be read: tools waits for it.
	asked chan struct{}
	// converter is false on a server without a working ffmpeg: only original files play.
	// fragmented says its ffmpeg can write fragmented MP4 streams.
	converter, fragmented bool

	mu    sync.Mutex
	facts map[string]mediafacts.Facts // by path, size and modified time
}

// New starts the media engine. Asking ffmpeg what it can do takes several seconds, so it
// happens beside the start and not in it: the server serves at once, and only a play that
// arrives in those first seconds waits for the answer. Run must be called for the engine to
// do its housekeeping.
func New(ctx context.Context, d *db.DB, o Options) (*Engine, error) {
	e := &Engine{db: d, probe: mediafacts.Probe{Binary: o.FFprobe}, facts: map[string]mediafacts.Facts{}, asked: make(chan struct{})}
	ffmpeg := o.FFmpeg
	if ffmpeg == "" {
		ffmpeg = mediatools.Resolve("ffmpeg")
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	go func() {
		defer close(e.asked)
		if _, err := exec.LookPath(ffmpeg); err != nil {
			return
		}
		tools, err := decoder.ProbeToolchain(ctx, ffmpeg)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("ffmpeg could not be asked what it can do, so only original files will play", "error", err)
			}
			return
		}
		decoder.ConfigureToolchain(tools)
		e.converter, e.fragmented = true, tools.Muxers["mp4"] && tools.Muxers["hls"]
	}()
	streams, err := streaming.New(ctx, streaming.Options{Folder: o.Folder, FFmpeg: o.FFmpeg, Libraries: e.folders})
	e.streams = streams
	return e, err
}

// tools waits until ffmpeg has said what it can do, and reports whether this server can
// convert and whether it can write fragmented MP4 streams.
func (e *Engine) tools(ctx context.Context) (converter, fragmented bool, err error) {
	select {
	case <-e.asked:
		return e.converter, e.fragmented, nil
	case <-ctx.Done():
		return false, false, ctx.Err()
	}
}

// Run does the engine's housekeeping until ctx ends. Shutdown stops every stream.
func (e *Engine) Run(ctx context.Context)            { e.streams.Run(ctx) }
func (e *Engine) Shutdown(ctx context.Context) error { return e.streams.Shutdown(ctx) }

// ServeMedia answers one request for a file of a stream, named by the stream's grant.
func (e *Engine) ServeMedia(w http.ResponseWriter, r *http.Request, grant, file string) {
	e.streams.ServeMedia(w, r, grant, file)
}

func (e *Engine) folders() []string {
	folders, err := e.db.LibraryFolders(context.Background())
	if err != nil {
		return nil
	}
	return folders
}

func (e *Engine) CheckCapabilities(document json.RawMessage) error {
	_, err := streaming.ParseCapabilityDocument(document)
	return err
}

// Qualities is the menu for one version: the original, automatic, and the fixed choices no
// taller than its picture; for music the original, CD quality and the lower bitrates.
func (e *Engine) Qualities(version db.PlaybackVersion, kind db.ItemKind, network string) []playback.QualityOption {
	if kind == db.KindTrack || kind == db.KindAudiobook {
		out := []playback.QualityOption{{Mode: playback.QualityOriginal}, {Mode: playback.QualityCD}}
		for _, preset := range qualityladder.Music() {
			if preset.AudioKbps > 0 {
				out = append(out, playback.QualityOption{Mode: playback.QualityLimit, ID: preset.ID, MaxAudioBitrateKbps: preset.AudioKbps,
					DataGBPerHour: preset.DataMBPerHour / 1000})
			}
		}
		return out
	}
	out := []playback.QualityOption{{Mode: playback.QualityOriginal}, {Mode: playback.QualityAutomatic}}
	for _, preset := range qualityladder.Fixed() {
		if version.Height > 0 && preset.Height > version.Height {
			continue
		}
		out = append(out, playback.QualityOption{Mode: playback.QualityFixed, ID: preset.ID, MaxHeight: preset.Height,
			MaxVideoBitrateKbps: preset.VideoBitrateBPS / 1000, MaxAudioBitrateKbps: preset.MaxAudioBitrateBPS / 1000,
			SameAsOriginal: preset.SameAsSource(version.Height, version.Bitrate), DataGBPerHour: preset.DataGBPerHour})
	}
	return out
}

// planned is what a plan carries back to Start.
type planned struct {
	plan    streaming.DeliveryPlan
	request streaming.PlanRequest
	path    string
	place   db.FileOnDisk
}

// source finds a version's file on disk and what is in it. The file is measured once and
// remembered until it changes.
func (e *Engine) source(ctx context.Context, version db.PlaybackVersion) (string, db.FileOnDisk, mediafacts.Facts, error) {
	place, err := e.db.FileOnDisk(ctx, version.FileID)
	if errors.Is(err, db.ErrNotFound) {
		return "", place, mediafacts.Facts{}, playback.ErrSourceUnavailable
	}
	if err != nil {
		return "", place, mediafacts.Facts{}, err
	}
	path, err := libraryfile.Resolve(place.Path, e.folders())
	if err != nil {
		return "", place, mediafacts.Facts{}, playback.ErrSourceUnavailable
	}
	key := fmt.Sprint(path, "\x00", place.Size, "\x00", place.ModifiedNS)
	e.mu.Lock()
	facts, ok := e.facts[key]
	e.mu.Unlock()
	if ok {
		return path, place, facts, nil
	}
	if facts, err = e.probe.Inspect(ctx, path); err != nil {
		return "", place, facts, playback.ErrSourceUnavailable
	}
	e.mu.Lock()
	if len(e.facts) > 2000 {
		clear(e.facts)
	}
	e.facts[key] = facts
	e.mu.Unlock()
	return path, place, facts, nil
}

var routes = map[streaming.DeliveryStrategy]string{
	streaming.DeliveryOriginal: playback.RouteOriginal, streaming.DeliveryCopyRemux: playback.RouteRepackaged,
	streaming.DeliveryAudioConversion: playback.RouteConverted, streaming.DeliveryVideoConversion: playback.RouteConverted,
}

func action(a string) string {
	switch a {
	case "convert":
		return "transcode"
	case "copy", "":
		return "copy"
	}
	return a
}

// The scanner stores a file's audio tracks in the file's order, and so does a fresh reading
// of the file, but the two number them differently when the file also holds a cover picture
// or a data track. An audio track is therefore matched by its place among the audio tracks.

// fileStream is the file's own number for the audio track a session names.
func fileStream(version db.PlaybackVersion, facts mediafacts.Facts, id string) (int, bool) {
	place := 0
	for _, track := range version.Tracks {
		if track.Kind != db.StreamAudio || track.Sidecar {
			continue
		}
		if playback.TrackID(track) == id {
			for _, s := range facts.Streams {
				if s.Type != "audio" {
					continue
				}
				if place == 0 {
					return s.Index, true
				}
				place--
			}
			return 0, false
		}
		place++
	}
	return 0, false
}

// trackID is the session's name for the audio track the file numbers index, or "".
func trackID(version db.PlaybackVersion, facts mediafacts.Facts, index int) string {
	place := 0
	for _, s := range facts.Streams {
		if s.Type != "audio" {
			continue
		}
		if s.Index == index {
			for _, track := range version.Tracks {
				if track.Kind != db.StreamAudio || track.Sidecar {
					continue
				}
				if place == 0 {
					return playback.TrackID(track)
				}
				place--
			}
			return ""
		}
		place++
	}
	return ""
}

// saved answers the planner's questions about the viewer's settings: what the profile saved,
// or the settings list's default.
type saved map[string]string

func (s saved) value(key string) (any, bool) {
	field, known := preferences.Find(key)
	if !known {
		return nil, false
	}
	var raw any
	if stored, ok := s[key]; ok && json.Unmarshal([]byte(stored), &raw) == nil {
		if value, err := field.Check(raw); err == nil {
			return value, true
		}
	}
	value, _ := field.Check(field.Default)
	return value, false
}

func (s saved) Bool(key string) bool   { v, _ := s.value(key); b, _ := v.(bool); return b }
func (s saved) Int(key string) int     { v, _ := s.value(key); n, _ := v.(int64); return int(n) }
func (s saved) Text(key string) string { v, _ := s.value(key); t, _ := v.(string); return t }
func (s saved) List(key string) []string {
	v, _ := s.value(key)
	list, _ := v.([]string)
	return list
}

// PreferenceSource tells the planner a default from a choice somebody made.
func (s saved) PreferenceSource(key string) string {
	if _, stored := s.value(key); stored {
		return preferences.ScopeProfile
	}
	return "default"
}

// Plan asks the media engine how the version reaches the device.
func (e *Engine) Plan(r playback.PlanRequest) (playback.Plan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, place, facts, err := e.source(ctx, r.Version)
	if err != nil {
		return playback.Plan{}, err
	}
	converter, fragmented, err := e.tools(ctx)
	if err != nil {
		return playback.Plan{}, err
	}
	request := streaming.PlanRequest{
		File: streaming.FileFacts{ID: r.Version.PublicID, Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
			Duration: facts.Duration, Size: place.Size, Width: facts.Width, Height: facts.Height,
			DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true, Streams: facts.Streams},
		Settings: streaming.DefaultDeliveryConfiguration(), TranscodingEnabled: true, FragmentedOutput: fragmented, NoConverter: !converter,
		Connection:  streaming.DeliveryContext{NetworkClass: streaming.NetworkRemote},
		Preferences: saved(r.Preferences),
	}
	if r.Network == playback.NetworkLocal {
		request.Connection.NetworkClass = streaming.NetworkLocal
	}
	if len(r.Capabilities) > 0 {
		device, err := streaming.ParseCapabilityDocument(r.Capabilities)
		if err != nil {
			return playback.Plan{}, playback.ErrUpdateRequired
		}
		request.Device = &device
	}
	choice := &request.Choice
	choice.AssetID, choice.SubtitleOff = r.Version.PublicID, r.Choice.SubtitlesOff
	if index, ok := fileStream(r.Version, facts, r.Choice.AudioTrackID); ok {
		choice.AudioStream = &index
	}
	q := r.Choice.Quality
	choice.QualityID = playback.QualityOriginal
	switch q.Mode {
	case playback.QualityCD:
		choice.QualityID, choice.AudioCDQuality = playback.QualityCD, true
	case playback.QualityFixed, playback.QualityAutomatic, playback.QualityLimit:
		choice.QualityID = q.Mode
		if q.Mode == playback.QualityFixed {
			choice.QualityID = q.ID
		} else if q.Mode == playback.QualityAutomatic && q.ID != "" {
			choice.QualityID = q.Mode + ":" + q.ID
		}
		if q.MaxVideoBitrateKbps > 0 || q.MaxHeight > 0 || q.MaxAudioBitrateKbps > 0 {
			choice.Limit = true
			choice.MaxVideoBitrateBPS, choice.MaxAudioBitrateBPS, choice.MaxVideoHeight = q.MaxVideoBitrateKbps*1000, q.MaxAudioBitrateKbps*1000, q.MaxHeight
		}
	}
	for _, failure := range r.Failures {
		for strategy, route := range routes {
			// A failed "converted" is remembered against the cheaper of the two conversions.
			if route != failure.Route || strategy == streaming.DeliveryVideoConversion {
				continue
			}
			if request.RouteFailures == nil {
				request.RouteFailures = map[string]map[streaming.DeliveryStrategy]streaming.RouteRejection{}
			}
			if request.RouteFailures[failure.Player] == nil {
				request.RouteFailures[failure.Player] = map[streaming.DeliveryStrategy]streaming.RouteRejection{}
			}
			request.RouteFailures[failure.Player][strategy] = streaming.RouteRejection{Code: failure.Code, Detail: failure.Detail}
		}
	}
	plan, err := streaming.Plan(request)
	switch {
	case errors.Is(err, streaming.ErrTranscodingDisabled):
		return playback.Plan{}, playback.ErrConversionOff
	case errors.Is(err, streaming.ErrQualityUnavailable):
		return playback.Plan{}, &playback.Invalid{Field: "quality", Message: "This is not a quality this title offers."}
	case err != nil:
		return playback.Plan{}, playback.ErrUnsupported
	}

	out := playback.Plan{Mode: "stream", Player: plan.Engine, Route: routes[plan.Strategy], Container: plan.OutputContainer,
		BitrateKbps: (plan.VideoBitrateBPS + plan.AudioBitrateBPS) / 1000, Quality: q,
		Audio:  &playback.Decision{Action: action(plan.AudioAction), Reasons: plan.ReasonCodes, To: plan.OutputAudioCodec, Channels: plan.AudioChannels},
		Detail: &planned{plan: plan, request: request, path: path, place: place}}
	if plan.Strategy == streaming.DeliveryOriginal {
		out.Mode, out.Audio.Action = "direct", "direct"
		out.Audio.Reasons, out.Audio.To = nil, ""
	}
	out.AudioTrackID = trackID(r.Version, facts, plan.AudioStream)
	if facts.VideoCodec != "" {
		out.Video = &playback.Decision{Action: action(plan.VideoAction), To: plan.OutputVideoCodec}
		if out.Mode == "direct" {
			out.Video = &playback.Decision{Action: "direct"}
		}
		if plan.ToneMap {
			out.HDR = &playback.HDR{ToneMapped: true}
		}
	}
	// A subtitle is only in force where the player can draw it from the file it is playing.
	// Subtitles served beside a stream, and burned into it, wait on the engine.
	if r.Choice.SubtitleTrackID != "" && !r.Choice.SubtitlesOff && out.Mode == "direct" {
		for _, track := range r.Version.Tracks {
			if track.Kind == db.StreamSubtitle && !track.Sidecar && playback.TrackID(track) == r.Choice.SubtitleTrackID {
				out.Subtitle = &playback.SubtitleInForce{TrackID: r.Choice.SubtitleTrackID, Kind: track.Codec, Delivery: "device"}
			}
		}
	}
	// Two plans that would produce the same stream share a key, so a change that alters
	// nothing in the stream does not interrupt it.
	raw, _ := json.Marshal(plan)
	sum := sha256.Sum256(append(raw, []byte(path)...))
	out.Key = hex.EncodeToString(sum[:8])
	return out, nil
}

// Start runs a plan as a stream. It does not wait for the converter: Ready does.
func (e *Engine) Start(ctx context.Context, r playback.StartRequest) (playback.Stream, error) {
	p, ok := r.Plan.Detail.(*planned)
	if !ok {
		return playback.Stream{}, errors.New("the plan was not made by this engine")
	}
	request := p.request
	start := streaming.StartRequest{SessionID: r.SessionID, Plan: p.plan, Request: &request, Path: p.path, Size: p.place.Size,
		Modified: time.Unix(0, p.place.ModifiedNS), PositionMs: r.PositionMs}
	if r.Replaced != nil {
		start.Replaced = func(s streaming.Stream) { r.Replaced(stream(s)) }
	}
	s, err := e.streams.Start(ctx, start)
	switch {
	case errors.Is(err, streaming.ErrConversionCapacity), errors.Is(err, streaming.ErrConversionSpace):
		// The owner's limit on conversions: the start waits and is tried again by itself.
		return playback.Stream{}, playback.ErrBusy
	case errors.Is(err, streaming.ErrSourceUnavailable), errors.Is(err, streaming.ErrSourceChanged):
		return playback.Stream{}, playback.ErrSourceUnavailable
	case errors.Is(err, streaming.ErrNoConverter):
		return playback.Stream{}, playback.ErrConversionOff
	case errors.Is(err, streaming.ErrIncompatible):
		return playback.Stream{}, playback.ErrUnsupported
	}
	return stream(s), err
}

func stream(s streaming.Stream) playback.Stream {
	mode := "stream"
	if s.Mode == "direct" {
		mode = "direct"
	}
	return playback.Stream{Grant: s.Grant, URL: s.URL, Mode: mode}
}

// Stop ends a session's stream; its address stops working.
func (e *Engine) Stop(sessionID string) { e.streams.Stop(sessionID) }

// Ready returns once the stream has something to play. A converter that needs longer is
// "still preparing": the session layer answers the start with "try again".
func (e *Engine) Ready(ctx context.Context, sessionID string) error {
	err := e.streams.Ready(ctx, sessionID)
	switch {
	case err == nil, ctx.Err() != nil, errors.Is(err, streaming.ErrGrantEnded):
		return err
	case errors.Is(err, streaming.ErrSegmentPreparing):
		return context.DeadlineExceeded
	}
	// The conversion failed before it produced anything: this way of playing does not work here.
	return fmt.Errorf("%w: %v", playback.ErrUnsupported, err)
}
