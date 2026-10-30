package playback

// Staying types for v1 channel sessions (B8c). These were previously defined
// in control*.go alongside the /v2 ControlService; the service is gone but
// ChannelSessions and the linear runtime still need them. No /v2 wire
// vocabulary is preserved beyond what channel playback reads.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ControlAuthorityTx rechecks current principal and cached item/library policy
// using only the supplied transaction. The root wires identity and hosted policy.
type ControlAuthorityTx func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error)

// ControlFamilyAuthority is implemented by identity.Service.
type ControlFamilyAuthority interface {
	SessionFamilyTx(context.Context, *sql.Tx, identity.Principal) (identity.FamilyState, error)
	FamilyAuthorityTx(context.Context, *sql.Tx, string, identity.Principal) (identity.FamilyState, error)
}

// CachedPreparationPolicy is fulfilled by hosted.Service without network IO.
type CachedPreparationPolicy interface {
	AllowedTxContext(context.Context, identity.Principal, string, *sql.Tx) error
}

type ControlFault struct {
	Code       string
	HTTPStatus int
}

func (e *ControlFault) Error() string { return e.Code }
func (e *ControlFault) Is(target error) bool {
	return e.Code == "owner_account_cap" && target == ErrOwnerAccountCap || e.Code == "owner_server_cap" && target == ErrOwnerServerCap || e.Code == "transcoding_disabled" && target == ErrTranscodingDisabled
}
func controlFault(code string, status int) error {
	return &ControlFault{Code: code, HTTPStatus: status}
}

var errControlJSON = errors.New("invalid playback JSON")

var controlIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validControlID(id string) bool          { return controlIDPattern.MatchString(id) }
func validControlOptionalID(id *string) bool { return id == nil || validControlID(*id) }
func controlOneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func parseControlDecimal(value string, signed, positive bool) (int64, error) {
	if value == "" || len(value) > 20 {
		return 0, errControlJSON
	}
	digits := value
	if strings.HasPrefix(digits, "-") {
		if !signed {
			return 0, errControlJSON
		}
		digits = digits[1:]
	}
	if digits == "" || (len(digits) > 1 && digits[0] == '0') || value == "-0" {
		return 0, errControlJSON
	}
	for _, b := range []byte(digits) {
		if b < '0' || b > '9' {
			return 0, errControlJSON
		}
	}
	result, err := strconv.ParseInt(value, 10, 64)
	if err != nil || result == -9223372036854775808 || positive && result <= 0 {
		return 0, errControlJSON
	}
	return result, nil
}

type ControlRational struct {
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}
type ControlQuality struct {
	Mode           string  `json:"mode"`
	MaxBitrateBPS  *int    `json:"maxBitrateBps"`
	MaxWidth       *int    `json:"maxWidth"`
	MaxHeight      *int    `json:"maxHeight"`
	AllowLossy     bool    `json:"allowLossyConversion"`
	AllowHDRToSDR  bool    `json:"allowHDRToSDR"`
	RungID         *string `json:"qualityRungId"`
	OffersRevision *string `json:"qualityOffersRevision"`
}
type ControlCodec struct {
	Codec          string           `json:"codec"`
	Profiles       []string         `json:"profiles"`
	Levels         []string         `json:"levels"`
	BitDepths      []int            `json:"bitDepths"`
	MaxWidth       *int             `json:"maxWidth"`
	MaxHeight      *int             `json:"maxHeight"`
	MaxFrameRate   *ControlRational `json:"maxFrameRate"`
	DynamicRanges  []string         `json:"dynamicRanges"`
	MaxChannels    *int             `json:"maxChannels"`
	ChannelLayouts []string         `json:"channelLayouts"`
	SampleRates    []int            `json:"sampleRatesHz"`
}
type ControlTransport struct {
	Transport         string         `json:"transport"`
	Containers        []string       `json:"containers"`
	Video             []ControlCodec `json:"video"`
	Audio             []ControlCodec `json:"audio"`
	ByteRanges        bool           `json:"byteRanges"`
	AudioSwitch       string         `json:"audioTrackSwitching"`
	SubtitleRenderers []string       `json:"subtitleRenderers"`
}
type ControlCapabilities struct {
	ProfileVersion       string             `json:"profileVersion"`
	Engine               string             `json:"engine"`
	EngineVersion        string             `json:"engineVersion"`
	OS                   string             `json:"os"`
	OSVersion            string             `json:"osVersion"`
	RouteID              string             `json:"outputRouteId"`
	RouteRevision        string             `json:"outputRouteRevision"`
	Transports           []ControlTransport `json:"transports"`
	CandidatePreparation string             `json:"candidatePreparation"`
	BackgroundControl    bool               `json:"backgroundControl"`
	Evidence             string             `json:"evidence"`
}

// LinearReference is a channel selection, never an item, path or provider URL.
type LinearReference struct {
	Kind       string `json:"kind"`
	SourceID   string `json:"sourceId"`
	ChannelID  string `json:"channelId"`
	Generation string `json:"generation"`
}
type LinearOverlay struct {
	Enabled      bool   `json:"enabled"`
	Corner       string `json:"corner"`
	SizePercent  int    `json:"sizePercent"`
	InsetPercent int    `json:"insetPercent"`
	Treatment    string `json:"treatment"`
}
type LinearProgramme struct {
	ID      string `json:"id"`
	ItemID  string `json:"itemId"`
	Title   string `json:"title"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
}

// LinearSelection is private source-selection evidence.
type LinearSelection struct {
	Number         string           `json:"number,omitempty"`
	Reference      LinearReference  `json:"reference"`
	Name           string           `json:"name"`
	ItemID         string           `json:"itemId"`
	AssetID        string           `json:"assetId"`
	LibraryID      string           `json:"libraryId"`
	SourceFence    string           `json:"sourceFence"`
	EntryID        string           `json:"entryId"`
	EntryStartMS   int64            `json:"entryStartMs"`
	EntryEndMS     int64            `json:"entryEndMs"`
	SourceOffsetMS int64            `json:"sourceOffsetMs"`
	DurationMS     int64            `json:"durationMs"`
	Title          string           `json:"title"`
	Next           *LinearProgramme `json:"next"`
	Quality        ControlQuality   `json:"quality"`
	LogoItemID     string           `json:"logoItemId"`
	Overlay        LinearOverlay    `json:"overlay"`
}

// ResolveTx chooses current programme/offset on the server. CheckTx validates a
// retained selection without selecting a new item.
type LinearResolver interface {
	ResolveTx(context.Context, *sql.Tx, identity.Principal, LinearReference, string, time.Time) (LinearSelection, error)
	CheckTx(context.Context, *sql.Tx, identity.Principal, LinearSelection) error
	BindTx(context.Context, *sql.Tx, string, LinearSelection, time.Time) error
}
type LinearSeek struct {
	ID         string  `json:"seekCommandId"`
	Generation string  `json:"bufferGeneration"`
	Kind       string  `json:"kind"`
	PositionUS *string `json:"positionUs"`
}
type LinearDesired struct {
	State   string      `json:"state"`
	Seek    *LinearSeek `json:"seek"`
	RetryID *string     `json:"retryRequestId"`
}
type LinearInterval struct {
	StartUS string `json:"startUs"`
	EndUS   string `json:"endUs"`
}

// LinearMedia is the channel timeline projection.
type LinearMedia struct {
	ReservationRequiredAtMS *int64           `json:"reservationRequiredAtMs,omitempty"`
	StreamURL               string           `json:"streamUrl"`
	BufferGeneration        string           `json:"bufferGeneration"`
	PresentationGeneration  string           `json:"presentationGeneration"`
	OriginMS                int64            `json:"originMs"`
	WindowRevision          string           `json:"windowRevision"`
	WindowStartUS           string           `json:"windowStartUs"`
	WindowEndUS             string           `json:"windowEndUs"`
	LiveEdgeUS              string           `json:"liveEdgeUs"`
	Gaps                    []LinearInterval `json:"gaps"`
	State                   string           `json:"state"`
	ErrorCode               string           `json:"errorCode"`
	LogoURL                 string           `json:"logoUrl"`
	// Engine is the player engine the stream was made for: "native" or "mpv".
	Engine string `json:"engine,omitempty"`
	// Clocks map media time to the channel's timeline, for a player that
	// reports media time rather than program dates (mpv).
	Clocks []LinearClock `json:"clocks,omitempty"`
}

// LinearClock is one clock epoch: over [startUs, endUs) of the channel
// timeline, channel position = media time + offsetUs.
type LinearClock struct {
	StartUS  string `json:"startUs"`
	EndUS    string `json:"endUs"`
	OffsetUS string `json:"offsetUs"`
}

// LinearDelivery is an owned source/presentation implementation.
type LinearDelivery interface {
	Snapshot(string) LinearMedia
	ResolveSeek(string, string, string, *string) (string, error)
	SelectionAt(string, string, string) (*LinearSelection, error)
}

type LinearWork struct {
	// MemberCeilings are private account policy, read with this producer's authority.
	// A changed ceiling retires the old producer and timeshift generation.
	MemberCeilings ReplanCeilings
	ClientProfile  ClientProfile
	// MPVProfile is the device's mpv engine when it plays channels and the
	// viewer did not choose Native; EngineSetting is the viewer's setting.
	MPVProfile                                                         *ClientProfile
	EngineSetting                                                      string
	PlaybackID                                                         string
	Principal                                                          identity.Principal
	FamilyID                                                           string
	Selection                                                          LinearSelection
	Desired                                                            LinearDesired
	Capabilities                                                       ControlCapabilities
	SourceRevision, DesiredRevision, OwnershipRevision, PolicyRevision int64
	LeaseGeneration, LeaseAuthorityRevision                            int64
	RetryRevision, BufferOrdinal, ProducerOrdinal                      int64
	LeaseUntil                                                         time.Time
	PreparationAttempt                                                 string
	PreparationStatus, PreparationError                                string
	TranscodingEnabled                                                 bool
	DirectInput                                                        bool
}

func validLinearReference(r LinearReference) bool {
	return controlOneOf(r.Kind, "live-source", "library-channel") && validControlID(r.ChannelID) && validControlID(r.Generation) && (r.Kind == "live-source" && validControlID(r.SourceID) || r.Kind == "library-channel" && (r.SourceID == "" || r.SourceID == r.ChannelID))
}
func validLinearDesired(d LinearDesired) bool {
	if !controlOneOf(d.State, "playing", "paused") || !validControlOptionalID(d.RetryID) {
		return false
	}
	if d.Seek == nil {
		return true
	}
	q := d.Seek
	if !validControlID(q.ID) || !validControlID(q.Generation) || !controlOneOf(q.Kind, "position", "live") {
		return false
	}
	if q.Kind == "live" {
		return q.PositionUS == nil
	}
	if q.PositionUS == nil {
		return false
	}
	_, err := parseControlDecimal(*q.PositionUS, true, false)
	return err == nil
}

func optionalLinearPosition(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
