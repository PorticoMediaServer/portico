// Package playback runs viewing sessions: what a title can be played as, starting, changing,
// following progress, stopping, stream limits and remembered playback failures. A session is
// a struct in memory. Nothing about a running session is stored: after a restart the apps
// start again by themselves where they were, from the progress that was saved.
//
// The media engine (the decision how a file reaches a device, and the streams themselves)
// is behind the two small interfaces in this file.
package playback

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Planner decides how one file reaches one device. It reads nothing and keeps nothing.
type Planner interface {
	// CheckCapabilities refuses a capability document this server cannot plan from.
	CheckCapabilities(document json.RawMessage) error
	// Qualities is the quality menu for one version on one kind of connection, widest first:
	// the original, then what the owner's limits leave of the lower choices.
	Qualities(version db.PlaybackVersion, kind db.ItemKind, network string) []QualityOption
	// Plan decides. Tracks the choice leaves open are chosen from the viewer's settings.
	Plan(r PlanRequest) (Plan, error)
}

// Streams produces and serves what a plan describes. One session has one stream at a time,
// named by the session's id; a song got ready ahead of time has the id its session will have.
type Streams interface {
	Start(ctx context.Context, r StartRequest) (Stream, error)
	// Restart moves a running stream to a position its producer has not reached.
	Restart(ctx context.Context, sessionID string, positionMs int64) error
	// Stop ends a stream and makes its addresses stop working. Stopping twice is fine.
	Stop(sessionID string)
	// Ready returns once the stream has something to play, or when ctx ends.
	Ready(ctx context.Context, sessionID string) error
}

// What planning or starting can refuse. Anything else is a fault.
var (
	// ErrUnsupported: no way of delivering this version plays on this device.
	ErrUnsupported = errors.New("this title can't be played on this device")
	// ErrConversionOff: it needs converting and the owner has turned converting off.
	ErrConversionOff = errors.New("this title needs converting, and converting is off on this server")
	// ErrTooSlow: the connection cannot carry the fixed quality asked for.
	ErrTooSlow = errors.New("the connection is too slow for this quality")
	// ErrUpdateRequired: the app is too old to play from this server.
	ErrUpdateRequired = errors.New("the app needs updating")
	// ErrSourceUnavailable: the file cannot be reached right now.
	ErrSourceUnavailable = errors.New("the file can't be reached")
	// ErrBusy: the server is converting as many streams as the owner allows. The start waits
	// and is tried again by itself.
	ErrBusy = errors.New("the server is converting as many streams as it is allowed")
)

// Networks a session can come over. The server tells home from away; only the app knows it
// is on mobile data.
const (
	NetworkLocal    = "local"
	NetworkRemote   = "remote"
	NetworkCellular = "cellular"
)

// Quality modes.
const (
	QualityOriginal  = "original"
	QualityCD        = "cd-quality"
	QualityFixed     = "fixed"
	QualityAutomatic = "automatic"
	QualityLimit     = "limit"
)

// Quality is the quality a viewer asks for, and the quality in force on a stream.
//
// original is the file as it is. fixed is one entry of the menu, by id: a ceiling, so a file
// already within it still plays as it is. automatic starts at the original and moves down
// and back up as the app measures its connection: the app names the entry to be on, or asks
// for one step. cd-quality is for music. limit is ceilings in numbers.
type Quality struct {
	Mode                string `json:"mode"`
	ID                  string `json:"id,omitempty"`
	MaxVideoBitrateKbps int    `json:"maxVideoBitrateKbps,omitempty"`
	MaxHeight           int    `json:"maxHeight,omitempty"`
	MaxAudioBitrateKbps int    `json:"maxAudioBitrateKbps,omitempty"`
	// Step is only ever asked for, with automatic: "down" or "up" moves one entry from where
	// the session is.
	Step string `json:"step,omitempty"`
}

// QualityOption is one entry of the quality menu.
type QualityOption struct {
	Mode                string  `json:"mode"`
	ID                  string  `json:"id,omitempty"`
	MaxVideoBitrateKbps int     `json:"maxVideoBitrateKbps,omitempty"`
	MaxHeight           int     `json:"maxHeight,omitempty"`
	MaxAudioBitrateKbps int     `json:"maxAudioBitrateKbps,omitempty"`
	SameAsOriginal      bool    `json:"sameAsOriginal,omitempty"`
	DataGBPerHour       float64 `json:"dataGBPerHour,omitempty"`
}

// Choice is what a viewer chose for a session. What is left empty is decided by planning.
type Choice struct {
	// AudioTrackID is the audio track; empty lets the viewer's settings decide.
	AudioTrackID string
	// SubtitleTrackID is the subtitle track. SubtitlesOff says the viewer wants none; with
	// neither, the viewer's settings decide.
	SubtitleTrackID string
	SubtitlesOff    bool
	// SubtitleDelivery is auto, sidecar, embeddedClient or burn.
	SubtitleDelivery string
	SubtitleOffsetMs int64
	Quality          Quality
}

// PlanRequest is everything one decision depends on.
type PlanRequest struct {
	Item    db.PlaybackItem
	Version db.PlaybackVersion
	// Capabilities is the document the device last published, as it was sent. Nil plans for
	// a device that can play only the most widely supported formats.
	Capabilities json.RawMessage
	Choice       Choice
	Network      string
	// ThroughputKbps is the app's own measure of its connection, 0 when it has none.
	ThroughputKbps int
	// Failures are the ways of delivering this file that failed on this device and are still
	// remembered.
	Failures []db.RouteFailure
	// Preferences are the profile's and the device's settings, key to JSON value.
	Preferences map[string]string
}

// Routes a plan can take, cheapest first.
const (
	RouteOriginal   = "original"
	RouteRepackaged = "repackaged"
	RouteConverted  = "converted"
)

// Decision says what happens to one kind of track and why.
type Decision struct {
	// Action is direct, copy, transcode, burn, sidecar, device or drop.
	Action   string   `json:"action"`
	Reasons  []string `json:"reasons,omitempty"`
	To       string   `json:"to,omitempty"`
	Channels int      `json:"channels,omitempty"`
}

// SubtitleInForce is the subtitle a stream shows and how it reaches the screen.
type SubtitleInForce struct {
	TrackID string `json:"trackId"`
	// Kind is text, ass, pgs, vobsub or dvb.
	Kind string `json:"kind"`
	// Delivery is sidecar (a text file the app draws), device (the player draws the track
	// itself) or burn (in the picture).
	Delivery string `json:"delivery"`
}

// HDR says what became of the picture's dynamic range.
type HDR struct {
	Source     string `json:"source,omitempty"`
	Delivered  string `json:"delivered,omitempty"`
	ToneMapped bool   `json:"toneMapped"`
}

// Plan is the decision for one version, device and set of choices.
type Plan struct {
	// Key names what the stream has to produce. Two plans with the same key are served by
	// the same stream, so a change that leaves it alone (a subtitle the app draws itself)
	// does not interrupt playback.
	Key string
	// Mode is direct (the file itself, by byte range) or stream (HLS).
	Mode string
	// Player is which of the device's players plays it: native or mpv.
	Player string
	// Route is original, repackaged or converted.
	Route        string
	Container    string
	BitrateKbps  int
	Video        *Decision
	Audio        *Decision
	Subtitles    *Decision
	Quality      Quality
	AudioTrackID string
	Subtitle     *SubtitleInForce
	HDR          *HDR
	// PlaybackInfo is what the file holds against what is playing here, and AudioRender how
	// an app that decodes music itself should play it. Both pass through to the app as they are.
	PlaybackInfo json.RawMessage
	AudioRender  json.RawMessage
	// Detail is the engine's own plan, handed back to it when the stream is started.
	Detail any
}

// StartRequest asks for a stream.
type StartRequest struct {
	SessionID  string
	Item       db.PlaybackItem
	Version    db.PlaybackVersion
	Plan       Plan
	PositionMs int64
}

// SubtitleFile is a subtitle the app fetches beside the stream.
type SubtitleFile struct {
	TrackID  string `json:"trackId"`
	Format   string `json:"format"`
	URL      string `json:"url"`
	OffsetMs int64  `json:"offsetMs,omitempty"`
}

// Stream is a running stream. Its address carries its own permission, the grant, which dies
// with the stream.
type Stream struct {
	Grant     string
	URL       string
	Mode      string
	Subtitles []SubtitleFile
}

// NoEngine is the media engine of a server that has none yet: nothing can be played, and it
// says so. It lets everything around playback run until the real engine is wired in.
type NoEngine struct{}

func (NoEngine) CheckCapabilities(json.RawMessage) error { return nil }
func (NoEngine) Qualities(db.PlaybackVersion, db.ItemKind, string) []QualityOption {
	return []QualityOption{{Mode: QualityOriginal}}
}
func (NoEngine) Plan(PlanRequest) (Plan, error) { return Plan{}, ErrSourceUnavailable }
func (NoEngine) Start(context.Context, StartRequest) (Stream, error) {
	return Stream{}, ErrSourceUnavailable
}
func (NoEngine) Restart(context.Context, string, int64) error { return ErrSourceUnavailable }
func (NoEngine) Stop(string)                                  {}
func (NoEngine) Ready(context.Context, string) error          { return ErrSourceUnavailable }

// SetEngine replaces the media engine. Call it before the server starts serving.
func (s *Service) SetEngine(planner Planner, streams Streams) {
	s.planner, s.streams = planner, streams
}
