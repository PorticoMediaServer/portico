// Package preparedmedia owns owner-requested full-media derivatives. Catalog
// associations, playback occurrences and current permission remain elsewhere.
package preparedmedia

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

var (
	ErrOwner         = errors.New("server owner required")
	ErrInput         = errors.New("invalid optimization request")
	ErrConflict      = errors.New("optimization revision changed")
	ErrSourceChanged = errors.New("optimization source changed")
	ErrUnavailable   = errors.New("optimization source unavailable")
	ErrUnsupported   = errors.New("optimization source or profile unsupported")
	ErrConfiguration = errors.New("optimization encoder or sandbox not configured")
	ErrOutput        = errors.New("prepared output validation failed")
	ErrCancelled     = errors.New("optimization cancelled")
)

const TargetID = "prepared-library"
const Kind = "media-optimization"

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var validDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Authority func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error)

type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	VideoKbps   int    `json:"videoKbps"`
	AudioKbps   int    `json:"audioKbps"`
	Description string `json:"description"`
}

// Immutable recipes: a recipe change gets a new ID, never different bytes under
// an old transformation identity. Clients cannot supply arbitrary encoder flags.
var profiles = canonicalProfiles()

func canonicalProfiles() []Profile {
	out := []Profile{}
	// AudioKbps is the size estimate for a recipe, not a fixed encode rate: a
	// FLAC 7.1 programme measures larger once it has been produced.
	for _, r := range qualityladder.Fixed() {
		out = append(out, Profile{"portable-" + r.ID + "-v3", r.Label, "video", r.Width(), r.Height, r.VideoBitrateBPS / 1000, r.MaxAudioBitrateBPS / 1000, "H.264 SDR video and AAC audio in MP4."})
		out = append(out, Profile{"hevc-" + r.ID + "-v3", r.Label, "video", r.Width(), r.Height, r.VideoBitrateBPS / 1000, 384, "HEVC video that keeps HDR, with surround AAC, in MP4."})
		out = append(out, Profile{"hevc-" + r.ID + "-v4", r.Label, "video", r.Width(), r.Height, r.VideoBitrateBPS / 1000, 384, "HEVC video that keeps HDR and surround up to 7.1 (FLAC), in MP4."})
	}
	out = append(out,
		Profile{"music-original-flac-v1", "Original cue track", "audio", 0, 0, 0, 0, "FLAC at the source rate and depth, for a cue track."},
		Profile{"music-cd-quality-v1", "CD quality", "audio", 0, 0, 0, 0, "FLAC, 16-bit, in the source rate family."},
		Profile{"music-opus-320-v1", "320 kbps · Opus", "audio", 0, 0, 0, 320, "Opus with tags and artwork."},
		Profile{"music-opus-96-v1", "96 kbps · Opus", "audio", 0, 0, 0, 96, "Opus with tags and artwork."},
		Profile{"music-aac-320-v1", "320 kbps · AAC", "audio", 0, 0, 0, 320, "AAC with tags and artwork in M4A."},
		Profile{"music-aac-96-v1", "96 kbps · AAC", "audio", 0, 0, 0, 96, "AAC with tags and artwork in M4A."})
	return append(out, Profile{"audio-aac-v3", "Audio · AAC", "audio", 0, 0, 0, 192, "AAC audio in M4A. Keeps the original."})
}

// Profiles lists the recipes this server can actually make. The table above
// names every recipe that has an identity; the conversion code decides which
// of them it can build, and only those are offered.
func Profiles() []Profile {
	out := []Profile{}
	for _, p := range profiles {
		// An older rich recipe keeps its identity and its old bytes; only the
		// current version is offered as a new recipe.
		if Rich(p.ID) && strings.HasSuffix(p.ID, "-v3") {
			continue
		}
		if decoder.PreparedProducible(recipeFor(p)) {
			out = append(out, p)
		}
	}
	return out
}

// Producible reports whether the conversion code can make this recipe today
// without knowing the source's channels. A KeepSeventhOne recipe is judged by
// its 7.1 carrier here; the source-aware check is ProducibleForSource.
func Producible(id string) bool {
	p, err := profile(id)
	if err != nil {
		return false
	}
	return decoder.PreparedProducible(recipeFor(p))
}

// ProducibleForSource asks the same question with the selected source's audio
// channels, so a 5.1 source under the 7.1 recipe is admitted on the AAC path it
// is actually written through, and a 7.1 source needs the FLAC carrier.
func ProducibleForSource(id string, channels []int) bool {
	p, err := profile(id)
	if err != nil {
		return false
	}
	r := recipeFor(p)
	r.Source.AudioChannels = append([]int(nil), channels...)
	return decoder.PreparedProducible(r)
}

// recipeFor is the one translation from a recipe's identity to what the
// encoder is asked for, shared by the encode and by the question above.
func recipeFor(p Profile) decoder.PreparedRecipe {
	if r, ok := musicRecipe(p); ok {
		return r
	}
	if Rich(p.ID) {
		return decoder.PreparedRecipe{DropChapters: true, BoundedFragments: true, VideoCodec: "hevc", KeepSeventhOne: strings.HasSuffix(p.ID, "-v4"), Width: p.Width, Height: p.Height, VideoKbps: p.VideoKbps, AudioKbps: p.AudioKbps}
	}
	return decoder.PreparedRecipe{DropChapters: !strings.HasSuffix(p.ID, "-v1"), BoundedFragments: strings.HasSuffix(p.ID, "-v3") || strings.HasSuffix(p.ID, "-v4"), AudioOnly: p.Kind == "audio", Width: p.Width, Height: p.Height, VideoKbps: p.VideoKbps, AudioKbps: p.AudioKbps}
}

// Rich reports whether a recipe keeps HEVC, HDR and surround sound.
func Rich(id string) bool { return strings.HasPrefix(id, "hevc-") }

// RichProfileFor is the HEVC recipe at a picture height, or "" when there is
// none at that height.
func RichProfileFor(quality string) string {
	rung, ok := qualityladder.Find(quality)
	if !ok {
		return ""
	}
	for _, p := range profiles {
		if Rich(p.ID) && strings.HasSuffix(p.ID, "-v4") && p.Height == rung.Height && p.VideoKbps == rung.VideoBitrateBPS/1000 {
			return p.ID
		}
	}
	return ""
}

// ProfileByID exposes the immutable recipe to size estimators.
func ProfileByID(id string) (Profile, error) { return profile(id) }

func profile(id string) (Profile, error) {
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, ErrInput
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type Request struct {
	IdempotencyKey         string `json:"idempotencyKey"`
	ItemID                 string `json:"itemId"`
	SourceID               string `json:"sourceId"`
	ExpectedSourceRevision string `json:"expectedSourceRevision"`
	ProfileID              string `json:"profileId"`
	TargetID               string `json:"targetId"`
}
type Command struct {
	IdempotencyKey   string `json:"idempotencyKey"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type SourceChoice struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	PartIndex int    `json:"partIndex"`
	Container string `json:"container"`
	Height    int    `json:"height"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}
type Job struct {
	ID         string `json:"id"`
	ItemID     string `json:"itemId"`
	SourceID   string `json:"sourceId"`
	ProfileID  string `json:"profileId"`
	TargetID   string `json:"targetId"`
	State      string `json:"state"`
	Phase      string `json:"phase"`
	Generation int64  `json:"generation"`
	Bytes      int64  `json:"bytes"`
	ErrorCode  string `json:"errorCode"`
	Revision   int64  `json:"revision"`
	CreatedMS  int64  `json:"createdMs"`
	UpdatedMS  int64  `json:"updatedMs"`
	VersionID  string `json:"versionId"`
}
type Facts struct {
	AudioSampleRate   int     `json:"audioSampleRate,omitempty"`
	AudioBitDepth     int     `json:"audioBitDepth,omitempty"`
	AudioChannelCount int     `json:"audioChannels,omitempty"`
	Container         string  `json:"container"`
	VideoCodec        string  `json:"videoCodec"`
	AudioCodec        string  `json:"audioCodec"`
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	Duration          float64 `json:"duration"`
	AudioTracks       int     `json:"audioTracks"`
}
type Version struct {
	ID             string  `json:"id"`
	ItemID         string  `json:"itemId"`
	SourceID       string  `json:"sourceId"`
	ProfileID      string  `json:"profileId"`
	TargetID       string  `json:"targetId"`
	SourceRevision string  `json:"sourceRevision"`
	PartIndex      int     `json:"partIndex"`
	EditionID      *string `json:"editionId"`
	Digest         string  `json:"digest"`
	Size           int64   `json:"size"`
	Facts          Facts   `json:"facts"`
	State          string  `json:"state"`
	Revision       int64   `json:"revision"`
	CreatedMS      int64   `json:"createdMs"`
	Selectable     bool    `json:"selectable"`
	Reason         string  `json:"reason"`
}
type View struct {
	ItemID                 string         `json:"itemId"`
	CanManage              bool           `json:"canManage"`
	PreparedOffersRevision string         `json:"preparedOffersRevision"`
	Configured             bool           `json:"configured"`
	ConfigurationReason    string         `json:"configurationReason"`
	Profiles               []Profile      `json:"profiles"`
	Targets                []Target       `json:"targets"`
	Sources                []SourceChoice `json:"sources"`
	Jobs                   []Job          `json:"jobs"`
	Versions               []Version      `json:"versions"`
	PollAfterMS            int            `json:"pollAfterMs"`
}
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Choice is an explicit server offer, not a library edition or an original
// source-version claim. Queue ownership and occurrence transitions stay in VOD.
type Choice struct {
	VersionID        string `json:"versionId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	OffersRevision   string `json:"offersRevision"`
}

func ValidChoice(c *Choice) bool {
	return c == nil || validID.MatchString(c.VersionID) && c.ExpectedRevision > 0 && validDigest.MatchString(c.OffersRevision)
}
func OffersRevision(item string, versions []Version) string {
	return hash([]any{"prepared-offers-v1", item, versions})
}
