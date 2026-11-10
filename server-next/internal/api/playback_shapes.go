package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// CapabilityDocument is what a device's players can do, exactly as the app wrote it. It can
// describe more than one player (mpv and the platform's own); the server stores it and plans
// from it without reshaping it.
type CapabilityDocument struct{ raw json.RawMessage }

func (c *CapabilityDocument) UnmarshalJSON(b []byte) error {
	c.raw = append(c.raw[:0], b...)
	return nil
}

func (c CapabilityDocument) MarshalJSON() ([]byte, error) {
	if len(c.raw) == 0 {
		return []byte("{}"), nil
	}
	return c.raw, nil
}

// PlaybackVideo, PlaybackAudio and PlaybackSubtitle are the tracks of a version. id is what
// a start or a change names; apps never parse it.
type PlaybackVideo struct {
	ID          string  `json:"id"`
	Codec       string  `json:"codec"`
	Profile     string  `json:"profile,omitempty"`
	BitDepth    int     `json:"bitDepth,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
	HDR         string  `json:"hdr,omitempty"`
	BitrateKbps int64   `json:"bitrateKbps,omitempty"`
}

type PlaybackAudio struct {
	ID         string `json:"id"`
	Codec      string `json:"codec"`
	Profile    string `json:"profile,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Layout     string `json:"layout,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	BitDepth   int    `json:"bitDepth,omitempty"`
	Language   string `json:"language,omitempty"`
	Title      string `json:"title,omitempty"`
	Default    bool   `json:"default"`
	Commentary bool   `json:"commentary"`
}

type PlaybackSubtitle struct {
	ID     string `json:"id"`
	Format string `json:"format"`
	// Source is embedded (in the file) or sidecar (a file beside it).
	Source   string `json:"source"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Forced   bool   `json:"forced"`
	SDH      bool   `json:"sdh"`
	Default  bool   `json:"default"`
}

// PlaybackVersion is one version of a title with the tracks that can be chosen.
type PlaybackVersion struct {
	ID          string             `json:"id"`
	Label       string             `json:"label"`
	Edition     string             `json:"edition,omitempty"`
	Container   string             `json:"container,omitempty"`
	SizeBytes   int64              `json:"sizeBytes,omitempty"`
	DurationMs  int64              `json:"durationMs"`
	BitrateKbps int64              `json:"bitrateKbps,omitempty"`
	Video       []PlaybackVideo    `json:"video"`
	Audio       []PlaybackAudio    `json:"audio"`
	Subtitles   []PlaybackSubtitle `json:"subtitles"`
}

// PlaybackMarker is a span the player can offer to skip. automaticSafe says it is trusted (in
// the file, from a provider, approved by the owner, or confirmed across a show's episodes),
// so it may be skipped without asking for a viewer who chose that.
type PlaybackMarker struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	StartMs       int64  `json:"startMs"`
	EndMs         int64  `json:"endMs"`
	Confidence    string `json:"confidence"`
	AutomaticSafe bool   `json:"automaticSafe"`
}

type PlaybackChapter struct {
	StartMs int64  `json:"startMs"`
	Title   string `json:"title"`
}

// PlaybackResume is where a start that names no position begins.
type PlaybackResume struct {
	PositionMs int64 `json:"positionMs"`
}

// PlaybackContainer is what the title plays within: an episode's show, a song's album.
type PlaybackContainer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// PlaybackPreferred is what a start that names nothing would play.
type PlaybackPreferred struct {
	VersionID string `json:"versionId"`
	AudioID   string `json:"audioId,omitempty"`
	// SubtitleID is absent when no subtitle would be shown.
	SubtitleID string           `json:"subtitleId,omitempty"`
	Quality    playback.Quality `json:"quality"`
}

// PlaybackDecisions says what happens to each kind of track.
type PlaybackDecisions struct {
	Video     *playback.Decision `json:"video,omitempty"`
	Audio     *playback.Decision `json:"audio,omitempty"`
	Subtitles *playback.Decision `json:"subtitles,omitempty"`
}

// PlaybackPlan is how the preferred choices would be delivered to this device.
type PlaybackPlan struct {
	VersionID string `json:"versionId"`
	// Mode is direct (the file itself) or stream.
	Mode string `json:"mode"`
	// Engine is the player the device would use: native or mpv.
	Engine string `json:"engine"`
	// Route is original, repackaged or converted.
	Route                string            `json:"route"`
	Decision             PlaybackDecisions `json:"decision"`
	EstimatedBitrateKbps int               `json:"estimatedBitrateKbps,omitempty"`
	Quality              playback.Quality  `json:"quality"`
}

// PlaybackOptions is what a title can be played as on the device asking.
type PlaybackOptions struct {
	ItemID       string                   `json:"itemId"`
	Kind         string                   `json:"kind"`
	Resume       *PlaybackResume          `json:"resume,omitempty"`
	Container    *PlaybackContainer       `json:"container,omitempty"`
	Versions     []PlaybackVersion        `json:"versions"`
	Chapters     []PlaybackChapter        `json:"chapters"`
	Markers      []PlaybackMarker         `json:"markers"`
	Qualities    []playback.QualityOption `json:"qualities"`
	Preferred    PlaybackPreferred        `json:"preferred"`
	Plan         PlaybackPlan             `json:"plan"`
	Alternatives []playback.Alternative   `json:"alternatives"`
}

// PlaybackLease says how long the session lives without a word from its device, and how
// often the device should report.
type PlaybackLease struct {
	ExpiresAt     time.Time `json:"expiresAt"`
	ReportEveryMs int64     `json:"reportEveryMs"`
	// Generation is the session's current presentation.generation. An app that is playing
	// an earlier one reads the session and loads the new address.
	Generation int `json:"generation,omitempty"`
}

// Presentation is what the device plays now. A change of quality, audio or version replaces
// it: the address changes, generation goes up by one, and the old address stops working.
type Presentation struct {
	Generation      int    `json:"generation"`
	Mode            string `json:"mode"`
	URL             string `json:"url"`
	StartPositionMs int64  `json:"startPositionMs"`
	// SeekID is the id of the last seek asked for through the session.
	SeekID       string                    `json:"seekId,omitempty"`
	Engine       string                    `json:"engine"`
	Route        string                    `json:"route"`
	Container    string                    `json:"container,omitempty"`
	BitrateKbps  int                       `json:"bitrateKbps,omitempty"`
	Quality      playback.Quality          `json:"quality"`
	AudioTrackID string                    `json:"audioTrackId,omitempty"`
	Subtitle     *playback.SubtitleInForce `json:"subtitle,omitempty"`
	Subtitles    []playback.SubtitleFile   `json:"subtitles"`
	Decision     PlaybackDecisions         `json:"decision"`
	HDR          *playback.HDR             `json:"hdr,omitempty"`
	AudioRender  JSON                      `json:"audioRender,omitempty"`
}

// SessionEnd says why a session ended and what the viewer is told.
type SessionEnd struct {
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// PlaybackSession is one device playing one title.
type PlaybackSession struct {
	ID string `json:"id"`
	// Kind is vod or audio.
	Kind string `json:"kind"`
	// State is playing, paused or ended.
	State        string              `json:"state"`
	ItemID       string              `json:"itemId"`
	VersionID    string              `json:"versionId"`
	Queue        *playback.QueueLink `json:"queue,omitempty"`
	Lease        PlaybackLease       `json:"lease"`
	Presentation Presentation        `json:"presentation"`
	PlaybackInfo JSON                `json:"playbackInfo,omitempty"`
	End          *SessionEnd         `json:"end,omitempty"`
	// RetryAfterMs is set on a 202: the stream needs a moment longer. Send the same start
	// again after this long.
	RetryAfterMs int64 `json:"retryAfterMs,omitempty"`

	status int
}

func (p PlaybackSession) reply(http.Header) int { return p.status }

// AudioPick and SubtitlePick name a track. A subtitle pick with no trackId turns subtitles off.
type AudioPick struct {
	TrackID string `json:"trackId"`
}

type SubtitlePick struct {
	TrackID *string `json:"trackId"`
	// Delivery is auto (the default), embeddedClient (the player draws the track from the
	// file it is playing) or burn (into the picture). sidecar, a subtitle file served beside
	// the stream, is not available yet and is refused with 501 subtitle_delivery_unavailable.
	Delivery string `json:"delivery,omitempty"`
}

// PlaybackStart asks to play a title. Send an Idempotency-Key: the same key and body is the
// same start, however often it is sent.
type PlaybackStart struct {
	// ItemID is the title to play, or Queue the queue entry: one of the two.
	ItemID    string              `json:"itemId,omitempty"`
	Queue     *playback.QueueLink `json:"queue,omitempty"`
	VersionID string              `json:"versionId,omitempty"`
	// StartPositionMs starts there. Without it StartFrom decides: resume (the default) or beginning.
	StartPositionMs *int64 `json:"startPositionMs,omitempty"`
	StartFrom       string `json:"startFrom,omitempty"`
	// State is playing (the default) or paused.
	State     string            `json:"state,omitempty"`
	Quality   *playback.Quality `json:"quality,omitempty"`
	Audio     *AudioPick        `json:"audio,omitempty"`
	Subtitles *SubtitlePick     `json:"subtitles,omitempty"`
	// Network is cellular when the app knows it is on mobile data. The server tells home from away.
	Network        string `json:"network,omitempty"`
	ThroughputKbps int    `json:"throughputKbps,omitempty"`
	// DiscTitle names one title of a disc. Disc playback is not available yet: a start that
	// names one is refused with 501 disc_playback_unavailable.
	DiscTitle JSON `json:"discTitle,omitempty"`
}

type PlaybackSeek struct {
	PositionMs int64  `json:"positionMs"`
	ID         string `json:"id"`
}

// PlaybackChange changes a running session. Only what is sent changes.
type PlaybackChange struct {
	State            string            `json:"state,omitempty"`
	VersionID        string            `json:"versionId,omitempty"`
	Quality          *playback.Quality `json:"quality,omitempty"`
	Audio            *AudioPick        `json:"audio,omitempty"`
	Subtitles        *SubtitlePick     `json:"subtitles,omitempty"`
	SubtitleOffsetMs *int64            `json:"subtitleOffsetMs,omitempty"`
	Seek             *PlaybackSeek     `json:"seek,omitempty"`
	// PositionMs is where the player is now: a new stream starts there.
	PositionMs     *int64 `json:"positionMs,omitempty"`
	ThroughputKbps int    `json:"throughputKbps,omitempty"`
	// Replan plans again from the device's current capability document.
	Replan bool `json:"replan,omitempty"`
	// Failure says the player could not play what it was given. The server remembers that
	// for this file on this device and moves the session to the next way of playing it.
	Failure *playback.Failure `json:"failure,omitempty"`
}

// PlaybackProgress is a device's report of where it is.
type PlaybackProgress struct {
	// State is playing, paused, buffering, ended or error.
	State      string `json:"state"`
	PositionMs int64  `json:"positionMs"`
	// PlayClockMs is how long the title has actually played in this session.
	PlayClockMs int64 `json:"playClockMs,omitempty"`
}

// PlaybackStop is the optional body of a stop: the device's final position.
type PlaybackStop struct {
	PositionMs *int64 `json:"positionMs,omitempty"`
}

// AccountStream is one thing the account is playing. A title the asking profile may not see
// has no itemId and no title.
type AccountStream struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	ItemID     string    `json:"itemId,omitempty"`
	Title      string    `json:"title,omitempty"`
	PositionMs int64     `json:"positionMs"`
	DurationMs int64     `json:"durationMs,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	// Current marks what the device asking is playing.
	Current bool `json:"current"`
}

// AccountPlayback is what the account is playing on all its devices, and its stream limit
// if the owner set one.
type AccountPlayback struct {
	Items []AccountStream `json:"items"`
	Limit int             `json:"limit,omitempty"`
}

// StreamLimits are the owner's caps on streams. Absent or 0 is no limit.
type StreamLimits struct {
	Total     int `json:"total,omitempty"`
	PerPerson int `json:"perPerson,omitempty"`
	// People gives one account its own number, by account id.
	People map[string]int `json:"people,omitempty"`
	// EndPausedVideoAfterMs ends video left paused this long. Music is never ended this way.
	EndPausedVideoAfterMs int64 `json:"endPausedVideoAfterMs,omitempty"`
}

// RememberedFailure is one "this way of playing this file failed on this device".
type RememberedFailure struct {
	ID         string `json:"id"`
	DeviceName string `json:"deviceName"`
	ItemID     string `json:"itemId,omitempty"`
	Title      string `json:"title,omitempty"`
	FileName   string `json:"fileName"`
	// Player is native or mpv; Route is original, repackaged or converted.
	Player   string    `json:"player"`
	Route    string    `json:"route"`
	Code     string    `json:"code"`
	Detail   string    `json:"detail,omitempty"`
	FailedAt time.Time `json:"failedAt"`
	// ExpiresAt is when that way is tried again by itself.
	ExpiresAt time.Time `json:"expiresAt"`
}

func sessionKind(kind db.ItemKind) string {
	if kind == db.KindTrack || kind == db.KindAudiobook {
		return "audio"
	}
	return "vod"
}

func decisions(p playback.Plan) PlaybackDecisions {
	return PlaybackDecisions{Video: p.Video, Audio: p.Audio, Subtitles: p.Subtitles}
}

func sessionOut(v playback.Session) PlaybackSession {
	out := PlaybackSession{ID: v.ID, Kind: sessionKind(v.Item.Kind), State: v.State, ItemID: v.Item.PublicID, VersionID: v.VersionID,
		Queue: v.Queue, Lease: PlaybackLease{ExpiresAt: v.LeaseUntil.UTC(), ReportEveryMs: v.ReportEvery().Milliseconds()},
		PlaybackInfo: v.Plan.PlaybackInfo}
	subtitles := v.Stream.Subtitles
	if subtitles == nil {
		subtitles = []playback.SubtitleFile{}
	}
	out.Presentation = Presentation{Generation: v.Generation, Mode: v.Plan.Mode, URL: v.Stream.URL, StartPositionMs: v.StartPositionMs,
		SeekID: v.SeekID, Engine: v.Plan.Player, Route: v.Plan.Route, Container: v.Plan.Container, BitrateKbps: v.Plan.BitrateKbps,
		Quality: v.Plan.Quality, AudioTrackID: v.Plan.AudioTrackID, Subtitle: v.Plan.Subtitle, Subtitles: subtitles,
		Decision: decisions(v.Plan), HDR: v.Plan.HDR, AudioRender: v.Plan.AudioRender}
	if v.End != nil {
		out.End = &SessionEnd{Reason: v.End.Reason, Message: v.End.Message}
	}
	return out
}

func versionOut(v db.PlaybackVersion) PlaybackVersion {
	out := PlaybackVersion{ID: v.PublicID, Label: v.Name, Edition: v.Edition, Container: v.Container, SizeBytes: v.Size,
		DurationMs: v.DurationMS, BitrateKbps: v.Bitrate / 1000, Video: []PlaybackVideo{}, Audio: []PlaybackAudio{},
		Subtitles: []PlaybackSubtitle{}}
	if out.Label == "" && v.Height > 0 {
		out.Label = strconv.Itoa(v.Height) + "p"
	}
	for _, t := range v.Tracks {
		id := playback.TrackID(t)
		switch t.Kind {
		case db.StreamVideo:
			out.Video = append(out.Video, PlaybackVideo{ID: id, Codec: t.Codec, Profile: t.Profile, BitDepth: t.BitDepth, Width: t.Width,
				Height: t.Height, FPS: t.FrameRate, HDR: t.HDR, BitrateKbps: t.Bitrate / 1000})
		case db.StreamAudio:
			out.Audio = append(out.Audio, PlaybackAudio{ID: id, Codec: t.Codec, Profile: t.Profile, Channels: t.Channels,
				Layout: t.ChannelLayout, SampleRate: t.SampleRate, BitDepth: t.BitDepth, Language: t.Language, Title: t.Title,
				Default: t.Default, Commentary: playback.Commentary(t)})
		case db.StreamSubtitle:
			source := "embedded"
			if t.Sidecar {
				source = "sidecar"
			}
			out.Subtitles = append(out.Subtitles, PlaybackSubtitle{ID: id, Format: t.Codec, Source: source, Language: t.Language,
				Title: t.Title, Forced: t.Forced, SDH: t.HearingImpaired, Default: t.Default})
		}
	}
	return out
}

var markerConfidence = map[db.MarkerSource]string{db.MarkerFromFile: "embedded", db.MarkerFromAnalysis: "detected",
	db.MarkerConfirmed: "detected", db.MarkerFromPerson: "manual", db.MarkerFromProvider: "provider"}

func optionsOut(o playback.Options) PlaybackOptions {
	out := PlaybackOptions{ItemID: o.Item.PublicID, Kind: o.Item.Kind.String(), Versions: []PlaybackVersion{},
		Chapters: []PlaybackChapter{}, Markers: []PlaybackMarker{}, Qualities: o.Qualities, Alternatives: o.Others}
	if o.ResumeMs > 0 {
		out.Resume = &PlaybackResume{PositionMs: o.ResumeMs}
	}
	// An episode plays within its show and a song within its album.
	switch holder := map[db.ItemKind]*db.ItemRef{db.KindEpisode: o.Item.Root, db.KindTrack: o.Item.Parent}[o.Item.Kind]; {
	case holder != nil:
		out.Container = &PlaybackContainer{Kind: holder.Kind.String(), ID: holder.PublicID}
	}
	for _, v := range o.Item.Versions {
		out.Versions = append(out.Versions, versionOut(v))
	}
	for _, m := range o.Version.Markers {
		if m.Kind == db.MarkerChapter {
			out.Chapters = append(out.Chapters, PlaybackChapter{StartMs: m.StartMS, Title: m.Title})
			continue
		}
		out.Markers = append(out.Markers, PlaybackMarker{ID: m.Kind.String() + "-" + strconv.FormatInt(m.StartMS, 10), Type: m.Kind.String(),
			StartMs: m.StartMS, EndMs: m.EndMS, Confidence: markerConfidence[m.Source], AutomaticSafe: m.Source.Trusted()})
	}
	if out.Qualities == nil {
		out.Qualities = []playback.QualityOption{}
	}
	if out.Alternatives == nil {
		out.Alternatives = []playback.Alternative{}
	}
	out.Preferred = PlaybackPreferred{VersionID: o.Version.PublicID, AudioID: o.Plan.AudioTrackID, Quality: o.Plan.Quality}
	if o.Plan.Subtitle != nil {
		out.Preferred.SubtitleID = o.Plan.Subtitle.TrackID
	}
	out.Plan = PlaybackPlan{VersionID: o.Version.PublicID, Mode: o.Plan.Mode, Engine: o.Plan.Player, Route: o.Plan.Route,
		Decision: decisions(o.Plan), EstimatedBitrateKbps: o.Plan.BitrateKbps, Quality: o.Plan.Quality}
	return out
}
