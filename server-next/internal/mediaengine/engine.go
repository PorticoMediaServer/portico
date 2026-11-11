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

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/preferences"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// Options says where the engine works.
type Options struct {
	// Folder is where conversions are written. It is emptied at start.
	Folder string
	// FFmpeg and FFprobe are the tools to run; empty finds the bundled or installed ones.
	FFmpeg, FFprobe string
	// Kept is a file in which what ffmpeg can do is kept between starts, so only the first
	// start with a program asks it; empty keeps nothing.
	Kept string
	// Log is told when ffmpeg cannot be asked what it can do; nil uses the default logger.
	Log *slog.Logger
	// Remote opens a remote source by its id, behind its throttle (remote.Registry.Open).
	// With none, a file on remote storage does not play.
	Remote func(ctx context.Context, sourceID int64) (remote.Storage, time.Duration, error)
}

// Engine is the session layer's Planner and Streams, backed by the real media engine.
type Engine struct {
	db      *db.DB
	streams *streaming.Streams
	probe   mediafacts.Probe
	// asked is closed once ffmpeg has said what it can do. Until then converter and
	// fragmented are not to be read: tools waits for it.
	asked chan struct{}
	// remote opens remote sources; processes supervises the probes of remote files, which
	// run on the decoder's isolated bridge, and the streams' converters.
	remote    func(ctx context.Context, sourceID int64) (remote.Storage, time.Duration, error)
	processes *processpool.Supervisor
	// converter is false on a server without a working ffmpeg: only original files play.
	// fragmented says its ffmpeg can write fragmented MP4 streams.
	converter, fragmented bool
	// ffmpeg is the converter's path, and hardware what has been found out about this
	// machine's hardware encoders. Both are set before asked is closed.
	ffmpeg   string
	hardware *decoder.HardwareDetector

	mu    sync.Mutex
	facts map[string]mediafacts.Facts // by path, size and modified time
	music map[string]*measuredMusic   // the same, for the exact measurement of a music file
}

// New starts the media engine. Asking ffmpeg what it can do takes several seconds, so it
// happens beside the start and not in it: the server serves at once, and only a play that
// arrives in those first seconds waits for the answer. Run must be called for the engine to
// do its housekeeping.
func New(ctx context.Context, d *db.DB, o Options) (*Engine, error) {
	e := &Engine{db: d, probe: mediafacts.Probe{Binary: o.FFprobe}, facts: map[string]mediafacts.Facts{}, remote: o.Remote, asked: make(chan struct{}),
		// No limit of its own: a limit here would refuse the fifth remote film being converted
		// at once, since a converter is not made to wait for a slot.
		processes: &processpool.Supervisor{}}
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
		tools, err := decoder.ProbeToolchainKept(ctx, ffmpeg, o.Kept)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("ffmpeg could not be asked what it can do, so only original files will play", "error", err)
			}
			return
		}
		decoder.ConfigureToolchain(tools)
		e.converter, e.fragmented = true, tools.Muxers["mp4"] && tools.Muxers["hls"]
		// The hardware encoders are tried out now, beside the start, so no play waits for
		// that: until it is done a conversion is planned in software.
		if path, err := exec.LookPath(ffmpeg); err == nil {
			e.ffmpeg, e.hardware = path, &decoder.HardwareDetector{}
			if libraries, err := decoder.ResolveLibraries(path, mediatools.Resolve("ffprobe"), nil); err == nil {
				e.hardware.Libraries = libraries
			}
			e.hardware.SelectCached(path, "auto", "")
		}
	}()
	streams, err := streaming.New(ctx, streaming.Options{Folder: o.Folder, FFmpeg: o.FFmpeg, Libraries: e.folders, Processes: e.processes})
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
	// remoteID is the remote source the file is on, with path its path within the source, or
	// 0 for a file on a disk here.
	remoteID int64
	// text is the version's text subtitles, which are served beside the stream.
	text []textTrack
	// nudgeMs is the viewer's subtitle timing nudge. sent says the server moves it into the
	// subtitles it sends; otherwise the app is told it and applies it.
	nudgeMs int64
	sent    bool
	// burn is the subtitle that is drawn into the picture, when one is.
	burn *burnTrack
	// audio is a planned music stream, which replaces plan and request, and index the
	// file's seek index for it, when it has one.
	audio *streaming.AudioPlan
	index *audiofacts.SeekIndex
}

// textTrack is one text subtitle: in a file beside the video, or track number index of
// the video file itself.
type textTrack struct {
	track    db.Track
	format   string
	index    int
	selected bool
}

// subtitleFormat is the format a subtitle track is written in, or "" when it is not one
// the server knows. The scanner records a file beside the video by its format and a track
// inside the video by ffprobe's name for it.
func subtitleFormat(track db.Track) string {
	if subtitleoffer.ValidFormat(track.Codec) {
		return track.Codec
	}
	return subtitles.CodecFormat(track.Codec)
}

// source finds a version's file, on a disk here or on remote storage, and what is in it. The
// file is measured once and remembered until it changes. A file on remote storage is measured
// through its source, by range, and its remote source id is returned with its path there.
func (e *Engine) source(ctx context.Context, version db.PlaybackVersion) (string, db.FileOnDisk, int64, mediafacts.Facts, error) {
	place, err := e.db.FileOnDisk(ctx, version.FileID)
	var path string
	var remoteID int64
	switch {
	case errors.Is(err, db.ErrNotFound) && e.remote != nil:
		if remoteID, place, err = e.db.RemoteFileOf(ctx, version.FileID); errors.Is(err, db.ErrNotFound) {
			return "", place, 0, mediafacts.Facts{}, playback.ErrSourceUnavailable
		}
		path = place.Path
	case errors.Is(err, db.ErrNotFound):
		return "", place, 0, mediafacts.Facts{}, playback.ErrSourceUnavailable
	case err == nil:
		if path, err = libraryfile.Resolve(place.Path, e.folders()); err != nil {
			return "", place, 0, mediafacts.Facts{}, playback.ErrSourceUnavailable
		}
	}
	if err != nil {
		return "", place, 0, mediafacts.Facts{}, err
	}
	if scan.DiscTitle(place.Path) {
		// A film disc kept as a folder. The file the scanner lists for it is the disc's index,
		// which ffprobe cannot read, and no build ships a disc reader yet: say so plainly.
		return "", place, 0, mediafacts.Facts{}, playback.ErrDisc
	}
	key := fmt.Sprint(remoteID, "\x00", path, "\x00", place.Size, "\x00", place.ModifiedNS)
	e.mu.Lock()
	facts, ok := e.facts[key]
	e.mu.Unlock()
	if ok {
		return path, place, remoteID, facts, nil
	}
	read := func() (mediafacts.Facts, error) {
		if remoteID != 0 {
			return e.inspectRemote(ctx, remoteID, path, place.Size)
		}
		return e.probe.Inspect(ctx, path)
	}
	if facts, err = read(); err != nil && ctx.Err() == nil {
		// The reader itself can die (stopped from outside, a helper that crashed). One more
		// try, and only one, tells that from a file that cannot be read.
		facts, err = read()
	}
	if err != nil {
		return "", place, 0, facts, playback.ErrSourceUnavailable
	}
	e.mu.Lock()
	if len(e.facts) > 2000 {
		clear(e.facts)
	}
	e.facts[key] = facts
	e.mu.Unlock()
	if !place.Read {
		// A scan has listed this file and not yet read it. What was just measured is stored
		// the way the scan would store it, so the title's length and its audio and subtitle
		// lists are there at once. It is one background write, and playing does not wait for it.
		found := scan.FromMedia(facts)
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			err := e.db.StoreProbes(ctx, []db.Probe{{FileID: version.FileID, Size: place.Size, ModifiedNS: place.ModifiedNS, Container: found.Container,
				DurationMS: found.DurationMS, Bitrate: found.Bitrate, Streams: found.Streams, Chapters: found.Chapters}})
			if err != nil {
				slog.Warn("what was measured at play time was not stored", "error", err)
			}
		}()
	}
	return path, place, remoteID, facts, nil
}

// inspectRemote reads a remote file's facts through its source, so a file on Just list the
// files is read when someone first plays it.
func (e *Engine) inspectRemote(ctx context.Context, sourceID int64, path string, size int64) (mediafacts.Facts, error) {
	storage, _, err := e.remote(ctx, sourceID)
	if err != nil {
		return mediafacts.Facts{}, err
	}
	file, err := storage.Open(ctx, path)
	if err != nil {
		return mediafacts.Facts{}, err
	}
	defer file.Close()
	if file.Size() != size {
		return mediafacts.Facts{}, streaming.ErrSourceChanged
	}
	return inputbridge.Inspect(ctx, e.processes, e.probe.Binary, file, size)
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
	return fileTrack(version, facts, db.StreamAudio, "audio", id)
}

// fileTrack is the file's own number for a track of one kind that a session names.
func fileTrack(version db.PlaybackVersion, facts mediafacts.Facts, kind db.StreamKind, kindInFile, id string) (int, bool) {
	place := 0
	for _, track := range version.Tracks {
		if track.Kind != kind || track.Sidecar {
			continue
		}
		if playback.TrackID(track) == id {
			for _, s := range facts.Streams {
				if s.Type != kindInFile {
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
	path, place, remoteID, facts, err := e.source(ctx, r.Version)
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
	if e.hardware != nil {
		// Only what has already been measured: a play never waits for a hardware encoder
		// to be tried out.
		request.Hardware = e.hardware.SelectCached(e.ffmpeg, request.Settings.HardwareBackend, "")
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
	if music := e.musicPlan(ctx, r, request, path, place, remoteID, facts, converter); music != nil {
		return *music, nil
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
	// The chosen subtitle is drawn into the picture when the app asks for that by name (a
	// screen that shows the video alone: Cast, AirPlay, picture in picture), and by itself
	// when it is made of pictures and nothing on the device would draw it: see burning.
	burn, err := burning(r, facts)
	if err != nil {
		return playback.Plan{}, err
	}
	if burn != nil {
		if burn.picture {
			choice.SubtitleKind = burn.kind
		}
		if r.Choice.SubtitleDelivery == "burn" {
			request.BurnIn = &streaming.BurnInSpec{ResourceID: r.Choice.SubtitleTrackID, Revision: 1, Format: burn.format}
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

	if burn != nil && request.BurnIn == nil {
		// Nobody asked for burning. A subtitle made of pictures is burned all the same when
		// the player this plan chose does not draw that kind, or would be handed a stream
		// (a picture subtitle cannot be sent beside one). Text never is: it is sent beside.
		device := streaming.BaselineCapabilityDocument()
		if request.Device != nil {
			device = *request.Device
		}
		if burn.picture && streaming.SubtitleNeedsBurnIn(device.Profile(plan.Engine), burn.kind, plan.Mode, false) {
			request.BurnIn = &streaming.BurnInSpec{ResourceID: r.Choice.SubtitleTrackID, Revision: 1, Format: burn.format}
			if plan, err = streaming.Plan(request); err != nil {
				return playback.Plan{}, playback.ErrUnsupported
			}
		} else {
			burn = nil
		}
	}

	out := playback.Plan{Mode: "stream", Player: plan.Engine, Route: routes[plan.Strategy], Container: plan.OutputContainer,
		BitrateKbps: (plan.VideoBitrateBPS + plan.AudioBitrateBPS) / 1000, Quality: q,
		Audio:  &playback.Decision{Action: action(plan.AudioAction), Reasons: plan.ReasonCodes, To: plan.OutputAudioCodec, Channels: plan.AudioChannels},
		Detail: &planned{plan: plan, request: request, path: path, place: place, remoteID: remoteID}}
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
	// Every text subtitle of the version is served beside the stream as WebVTT, whichever
	// way the title plays, so changing subtitles never interrupts it. The chosen one is in
	// force: drawn by the player itself from a file it plays directly, or fetched beside the
	// stream. A subtitle made of pictures on a converted stream waits on burning in.
	detail := out.Detail.(*planned)
	if detail.burn = burn; burn != nil {
		out.Subtitle = &playback.SubtitleInForce{TrackID: r.Choice.SubtitleTrackID, Kind: burn.kind, Delivery: "burn"}
	}
	for _, track := range r.Version.Tracks {
		format := subtitleFormat(track)
		if track.Kind != db.StreamSubtitle {
			continue
		}
		id := playback.TrackID(track)
		chosen := id == r.Choice.SubtitleTrackID && !r.Choice.SubtitlesOff
		text := textTrack{track: track, format: format, selected: chosen}
		served := subtitleoffer.TextFormat(format)
		if served && !track.Sidecar {
			text.index, served = fileTrack(r.Version, facts, db.StreamSubtitle, "subtitle", id)
		}
		if served {
			detail.text = append(detail.text, text)
		}
		switch {
		case !chosen || burn != nil:
		case out.Mode == "direct" && !track.Sidecar:
			out.Subtitle = &playback.SubtitleInForce{TrackID: id, Kind: track.Codec, Delivery: "device"}
		case served:
			out.Subtitle = &playback.SubtitleInForce{TrackID: id, Kind: "text", Delivery: "sidecar"}
		}
	}
	// The viewer's timing nudge. An app that fetches subtitles to draw them itself (it asked
	// for "sidecar") is told the nudge in force when its stream starts and applies any
	// change itself, at once, with nothing restarted. For
	// every other player the server moves the nudge into the subtitles it sends, because a
	// player that only follows the playlist cannot: a change of nudge is then a new stream,
	// which takes a few seconds, as any change that alters the stream does.
	detail.nudgeMs, detail.sent = r.Choice.SubtitleOffsetMs, r.Choice.SubtitleDelivery != "sidecar"
	// Two plans that would produce the same stream share a key, so a change that alters
	// nothing in the stream does not interrupt it.
	raw, _ := json.Marshal(plan)
	if detail.sent && detail.nudgeMs != 0 && out.Subtitle != nil && out.Subtitle.Delivery == "sidecar" {
		raw = append(raw, fmt.Sprint("nudge ", detail.nudgeMs)...)
	}
	sum := sha256.Sum256(append(raw, []byte(path)...))
	out.Key = hex.EncodeToString(sum[:8])
	if burn != nil && burn.nudgeMs != 0 {
		// A burned subtitle's timing is in the picture: another nudge is another stream.
		out.Key += fmt.Sprint("+", burn.nudgeMs)
	}
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
	if p.audio != nil {
		start.Request, start.Audio, start.SeekIndex = nil, p.audio, p.index
	}
	for _, text := range p.text {
		track := text.track
		rendition := streaming.TextRenditionPlan{Resource: playback.TrackID(track), Revision: 1, Language: track.Language, Label: track.Title,
			Selected: text.selected, Forced: track.Forced, HearingImpaired: track.HearingImpaired, Format: text.format, Track: text.index}
		if p.sent {
			rendition.OffsetUS = p.nudgeMs * 1000
		}
		if track.Sidecar {
			// A subtitle file the scanner no longer has is left out; the film still plays.
			beside, err := e.db.FileOnDisk(ctx, track.FileID)
			if err != nil {
				continue
			}
			rendition.Path, rendition.Size, rendition.Modified = beside.Path, beside.Size, time.Unix(0, beside.ModifiedNS)
		}
		if text.selected {
			start.Subtitles = append([]streaming.TextRenditionPlan{rendition}, start.Subtitles...)
		} else {
			start.Subtitles = append(start.Subtitles, rendition)
		}
	}
	if p.burn != nil {
		// Read when the converter first needs it, beside the start: see StartRequest.BurnLoad.
		start.BurnLoad = e.burnLoader(p)
	}
	// stream adds the addresses of the subtitles to what the session is told.
	stream := func(s streaming.Stream) playback.Stream {
		out := stream(s)
		for _, text := range start.Subtitles {
			file := playback.SubtitleFile{TrackID: text.Resource, Format: "vtt", URL: streaming.TextURL(s.Grant, text.Resource, text.Revision)}
			if !p.sent {
				file.OffsetMs = p.nudgeMs
			}
			out.Subtitles = append(out.Subtitles, file)
		}
		return out
	}
	if p.remoteID != 0 {
		// A file on remote storage is read through its source, by range, never by path.
		storage, _, err := e.remote(ctx, p.remoteID)
		if err != nil {
			return playback.Stream{}, playback.ErrSourceUnavailable
		}
		start.Path, start.Source = "", streaming.RemoteFile(storage, p.path, p.place.Size, start.Modified)
	}
	if r.Replaced != nil {
		start.Replaced = func(s streaming.Stream) { r.Replaced(stream(s)) }
	}
	if r.Failed != nil {
		start.Failed = func(s streaming.Stream) { r.Failed(stream(s)) }
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
	out := playback.Stream{Grant: s.Grant, URL: s.URL, Mode: mode}
	if s.Audio != nil {
		out.AudioRender, _ = json.Marshal(s.Audio)
		if s.Audio.Mode == "direct" {
			out.Mode = "direct"
		}
	}
	return out
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
