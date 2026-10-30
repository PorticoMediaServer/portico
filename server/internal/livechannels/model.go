// Package livechannels owns source configuration and common guide projections.
// Playback delivery is composed through the shared playback runtime.
package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("The channel source or guide is invalid. Check the input and try again.")
	ErrConflict    = errors.New("The channel source changed. Reload it before saving.")
	ErrDenied      = errors.New("This channel operation is not permitted.")
	ErrUnavailable = errors.New("The channel guide is unavailable. Try again.")
	ErrCursor      = errors.New("The guide changed. Reload this time window.")
)

const MaxUploadBytes = 512 << 20
const MaxChannels = 50000
const MaxProgrammes = 5000000

type Provenance string

const LiveSource Provenance = "live-source"
const LibraryChannel Provenance = "library-channel"

type SourceInput struct {
	guideFile, guideDigest string
	parseContext           context.Context
	configuration          *LiveSourceSettings
	remote                 bool
	guideSourceID          string
	guideSourceRevision    int64
	guideDays              int
	RequestID              string           `json:"requestId"`
	ID                     string           `json:"id"`
	ExpectedRevision       int64            `json:"expectedRevision"`
	Name                   string           `json:"name"`
	Playlist               string           `json:"playlist"`
	Guide                  string           `json:"guide"`
	TunerCount             int              `json:"tunerCount"`
	Limits                 ImportLimits     `json:"limits,omitempty"`
	EnabledGroups          []string         `json:"enabledGroups,omitempty"`
	EnabledChannels        []string         `json:"enabledChannels,omitempty"`
	Mappings               []ChannelMapping `json:"mappings,omitempty"`
}
type Source struct {
	Diagnostics        ImportDiagnostics `json:"diagnostics"`
	DeliveryValidation string            `json:"deliveryValidation"`
	CapacityKnown      bool              `json:"capacityKnown"`
	PlanningEstimate   int               `json:"planningEstimate"`
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Revision           int64             `json:"revision"`
	State              string            `json:"state"`
	// Acquisition is how this source is kept current: "upload" for a saved
	// manual import with no refresh loop, "remote" for a URL/tuner source.
	Acquisition    string `json:"acquisition"`
	TunerCount     int    `json:"tunerCount"`
	TunerCountMode string `json:"tunerCountMode"`
	Generation     string `json:"generation"`
	PublishedAt    string `json:"publishedAt"`
	Channels       int    `json:"channels"`
	Programmes     int    `json:"programmes"`
}
type ImportLimits struct {
	Bytes      int `json:"bytes"`
	Channels   int `json:"channels"`
	Programmes int `json:"programmes"`
}

func (v ImportLimits) effective() ImportLimits {
	if v.Bytes <= 0 {
		v.Bytes = MaxUploadBytes
	}
	if v.Channels <= 0 {
		v.Channels = MaxChannels
	}
	if v.Programmes <= 0 {
		v.Programmes = MaxProgrammes
	}
	return v
}

type ImportDiagnostics struct {
	SkippedChannels    int      `json:"skippedChannels"`
	SkippedProgrammes  int      `json:"skippedProgrammes"`
	AdjustedProgrammes int      `json:"adjustedProgrammes"`
	Examples           []string `json:"examples"`
}

func (d *ImportDiagnostics) note(message string) {
	if len(d.Examples) < 10 {
		d.Examples = append(d.Examples, message)
	}
}

type Preview struct {
	Diagnostics    ImportDiagnostics `json:"diagnostics"`
	Channels       int               `json:"channels"`
	Programmes     int               `json:"programmes"`
	AvailableStart string            `json:"availableStart"`
	AvailableEnd   string            `json:"availableEnd"`
	Names          []string          `json:"names"`
}
type Channel struct {
	Mark                    *ChannelMark `json:"mark,omitempty"`
	LogoPath                string       `json:"logoPath,omitempty"`
	ID                      string       `json:"id"`
	SourceID                string       `json:"sourceId"`
	Provenance              Provenance   `json:"provenance"`
	Name                    string       `json:"name"`
	Number                  string       `json:"number"`
	Group                   string       `json:"group"`
	Generation              string       `json:"generation"`
	Programmes              []Programme  `json:"programmes"`
	TuneAvailable           bool         `json:"tuneAvailable"`
	TuneUnavailableReason   string       `json:"tuneUnavailableReason"`
	RecordUnavailableReason string       `json:"recordUnavailableReason"`
	RecordAvailable         bool         `json:"recordAvailable"`
	Favorite                bool         `json:"favorite"`
	Hidden                  bool         `json:"hidden"`
	ServerHidden            bool         `json:"serverHidden"`
	PreferenceRevision      int64        `json:"preferenceRevision"`
}
type Programme struct {
	RecordingID    string `json:"recordingId,omitempty"`
	RecordingState string `json:"recordingState,omitempty"`
	ID             string `json:"id"`
	ChannelID      string `json:"channelId"`
	Title          string `json:"title"`
	Start          string `json:"start"`
	End            string `json:"end"`
	// Lineage retains provider correction identity when one is supplied.
	Lineage     string `json:"lineage"`
	SeriesID    string `json:"seriesId,omitempty"`
	EpisodeID   string `json:"episodeId,omitempty"`
	NewEvidence string `json:"newEvidence,omitempty"`
	Description string `json:"description,omitempty"`
	// Guide facts (programme_facts.go). Categories and Flags are always
	// published; the rest only when known.
	Subtitle   string            `json:"subtitle,omitempty"`
	Episode    *ProgrammeEpisode `json:"episode,omitempty"`
	Categories []string          `json:"categories"`
	Rating     *ProgrammeRating  `json:"rating,omitempty"`
	Year       int               `json:"year,omitempty"`
	StarRating string            `json:"starRating,omitempty"`
	Flags      ProgrammeFlags    `json:"flags"`
	// Image is a path this server serves (a Library Channel item's poster);
	// a provider's icon URL is never published.
	Image string `json:"image,omitempty"`
}
type GuideQuery struct {
	// Compact returns only the first three current/future slots per channel.
	Compact bool

	Viewer              Owner
	FavoritesOnly       bool
	IncludeHidden       bool
	IncludeServerHidden bool
	Group               string
	Start               time.Time
	End                 time.Time
	Timezone            string
	Search              string
	SourceID            string
	Kind                Provenance
	Limit               int
	Cursor              string
	// ChannelIDs, when set, limits the page to these channels (at most 50):
	// the guide asks for the programmes of the channels on screen, not every
	// channel's for each time window.
	ChannelIDs []string
	// NoProgrammes answers the channel rows only (the channel list is the same
	// for every time window).
	NoProgrammes bool
	// MarkChannels joins this guide's read snapshot to the complete current
	// source domain. It is supplied by the viewer facade, never by a request.
	MarkChannels func(context.Context, *sql.Tx, []Channel) error `json:"-"`
	// ProgrammeAllowed, when set, is the viewer's content-restriction predicate
	// for a programme's rating (Channels spec §8.1). A programme it refuses
	// keeps its time slot but shows RestrictedProgrammeTitle and nothing else,
	// as a restricted Library Channel slot does.
	ProgrammeAllowed func(rating string) bool `json:"-"`
}
type GuideSource struct {
	RefreshState   string     `json:"refreshState"`
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Generation     string     `json:"generation"`
	PublishedAt    string     `json:"publishedAt"`
	AvailableStart string     `json:"availableStart"`
	AvailableEnd   string     `json:"availableEnd"`
	Provenance     Provenance `json:"provenance"`
}
type Guide struct {
	Sources     []GuideSource `json:"sources"`
	State       string        `json:"state"`
	ViewerFence string        `json:"viewerFence"`
	Start       string        `json:"start"`
	End         string        `json:"end"`
	Timezone    string        `json:"timezone"`
	Channels    []Channel     `json:"channels"`
	NextCursor  string        `json:"nextCursor"`
	ObservedAt  string        `json:"observedAt"`
	// Days is how many calendar days from now (today counts as 1) the
	// listed sources have guide data for, so a day picker offers exactly those
	// days (FEAT-04). 0 when no source has any.
	Days int `json:"days"`
}

// Authority is supplied by HTTP integration. It must reauthorize the session and
// current source/channel policy in this transaction; returning an empty fence fails closed.
// Owner operations must require an interactive local server owner. A nil allowed
// predicate denies every channel. Generated media needs a stronger per-item adapter.
type Authority func(context.Context, *sql.Tx, bool) (fence string, allowed func(sourceID, channelID string) bool, err error)

type parsedChannel struct{ key, guideKey, name, number, sourceNumber, group, country, locator, logo, catchup, catchupSource, catchupDays string }
type parsedProgramme struct {
	key, channel, title, lineage              string
	series, episode, newEvidence, description string
	icon                                      string // first valid XMLTV <icon src>, or "" (kept a string: programmes compare with !=)
	facts                                     string // programmeFacts JSON
	start, end                                time.Time
}
type parsedSource struct {
	choices     []parsedChannel
	spool       *programmeSpool
	diagnostics ImportDiagnostics
	channels    []parsedChannel
	programmes  []parsedProgramme
}
