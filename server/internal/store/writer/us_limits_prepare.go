package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
	"slices"
	"sort"
	"strings"
	"time"
)

type usLimitsWindow struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
}

// usLimitsSchedule denies sign-in and playback outside its windows. An empty
// window list means no schedule restriction at all, which is the default.
type usLimitsSchedule struct {
	Timezone string           `json:"timezone"`
	Windows  []usLimitsWindow `json:"windows"`
}

// usLimitsChannels restricts live channels by their canonical v1 channel id
// only ("live:<source>:<channel>" or "library:<channel>"). Mode "all" ignores
// the list; "allow" permits only the listed ids; "deny" permits everything else.
type usLimitsChannels struct {
	Mode     string   `json:"mode"`
	Channels []string `json:"channels"`
}

// usLimitsTags denies items carrying any of these catalog label or tag values.
type usLimitsTags struct {
	DeniedLabels []string `json:"deniedLabels"`
}

// Complete prepared access envelope; source semantics are in access/limits.go.
type usLimits struct {
	AccessTokens bool   `json:"accessTokens"`
	MaxProfiles  int    `json:"maxProfiles"`
	AllowRemote  bool   `json:"allowRemote"`
	Conversion   string `json:"conversion"`
	// AwayQuality is this person's away-from-home ceiling: the server sentinel,
	// or one published quality-ladder id that narrows it. Omitted means "leave
	// whatever is stored", so an older full write cannot clear a saved cap.
	AwayQuality       string `json:"awayQuality,omitempty"`
	MaxStreams        int    `json:"maxStreams"`
	MaxStreamsMode    string `json:"maxStreamsMode"`
	RemoteBitrateKbps int    `json:"remoteBitrateKbps"`
	// RatingSystem and MaxContentRating name the highest rating the account
	// may see, exactly as a profile's restriction does: one value of one of
	// the published rating systems (GET /v1/rating-systems). With no system
	// named, the rating is read as any spelling the rating table knows.
	// MaximumAge is what that resolves to, the minimum admission age the
	// rating carries, or -1 for no ceiling; it is the number every title is
	// compared against, whatever system its own rating was issued in.
	RatingSystem     string           `json:"ratingSystem"`
	MaxContentRating string           `json:"maxContentRating"`
	MaximumAge       int              `json:"maximumAge"`
	AllowUnrated     bool             `json:"allowUnrated"`
	Schedule         usLimitsSchedule `json:"schedule"`
	Channels         usLimitsChannels `json:"channelPolicy"`
	Tags             usLimitsTags     `json:"tagPolicy"`
}

func canonicalUSLimitsChannel(id string) bool {
	if strings.HasPrefix(id, "live:") {
		rest := strings.TrimPrefix(id, "live:")
		source, channel, ok := strings.Cut(rest, ":")
		return ok && source != "" && channel != "" && !strings.Contains(channel, ":")
	}
	if strings.HasPrefix(id, "library:") {
		channel := strings.TrimPrefix(id, "library:")
		return channel != "" && !strings.Contains(channel, ":")
	}
	return false
}

func normalizeUSLimits(v *usLimits) error {
	fields := []string{}
	if v.MaxProfiles < 1 || v.MaxProfiles > 8 {
		fields = append(fields, "limits.maxProfiles")
	}
	if v.AwayQuality != "" && !validUSLimitsAwayQuality(v.AwayQuality) {
		fields = append(fields, "limits.awayQuality")
	}
	if v.Conversion != "server" && v.Conversion != "audio" && v.Conversion != "none" {
		fields = append(fields, "limits.conversion")
	}
	if !securitypolicy.ValidStreamMode(v.MaxStreamsMode, v.MaxStreams) {
		fields = append(fields, "limits.maxStreams")
	}
	if v.RemoteBitrateKbps < 0 || v.RemoteBitrateKbps > 200000 {
		fields = append(fields, "limits.remoteBitrateKbps")
	}
	v.RatingSystem, v.MaxContentRating = strings.TrimSpace(v.RatingSystem), strings.TrimSpace(v.MaxContentRating)
	if v.MaxContentRating == "" {
		v.RatingSystem = ""
	}
	if v.RatingSystem == "" {
		// A rating given without its system is stored in one spelling.
		v.MaxContentRating = strings.ToUpper(v.MaxContentRating)
	}
	if age, ok := usLimitsRatingCeiling(v.RatingSystem, v.MaxContentRating); ok {
		v.MaximumAge = age
	} else {
		fields = append(fields, "limits.maxContentRating")
	}
	if v.Schedule.Timezone != "" {
		if _, e := time.LoadLocation(v.Schedule.Timezone); e != nil {
			fields = append(fields, "limits.schedule.timezone")
		}
	}
	if len(v.Schedule.Windows) > 64 {
		fields = append(fields, "limits.schedule.windows")
	}
	for i := range v.Schedule.Windows {
		w := &v.Schedule.Windows[i]
		if len(w.Days) == 0 || len(w.Days) > 7 || w.StartMinute < 0 || w.StartMinute > 1439 || w.EndMinute < 0 || w.EndMinute > 1440 || w.StartMinute == w.EndMinute {
			fields = append(fields, "limits.schedule.windows")
			break
		}
		seen := map[int]bool{}
		for _, d := range w.Days {
			if d < 0 || d > 6 || seen[d] {
				fields = append(fields, "limits.schedule.windows")
				break
			}
			seen[d] = true
		}
		sort.Ints(w.Days)
	}
	switch v.Channels.Mode {
	case "", "all":
		v.Channels.Mode = "all"
	case "allow", "deny":
	default:
		fields = append(fields, "limits.channelPolicy.mode")
	}
	if v.Channels.Channels == nil {
		v.Channels.Channels = []string{}
	}
	if len(v.Channels.Channels) > 512 {
		fields = append(fields, "limits.channelPolicy.channels")
	}
	for _, id := range v.Channels.Channels {
		if !usCredentialSafeText(id, 200) || strings.TrimSpace(id) == "" || !canonicalUSLimitsChannel(id) {
			fields = append(fields, "limits.channelPolicy.channels")
			break
		}
	}
	if v.Tags.DeniedLabels == nil {
		v.Tags.DeniedLabels = []string{}
	}
	if len(v.Tags.DeniedLabels) > 256 {
		fields = append(fields, "limits.tagPolicy.deniedLabels")
	}
	for i, label := range v.Tags.DeniedLabels {
		if !usCredentialSafeText(label, 120) || strings.TrimSpace(label) == "" {
			fields = append(fields, "limits.tagPolicy.deniedLabels")
			break
		}
		v.Tags.DeniedLabels[i] = strings.TrimSpace(label)
	}
	if v.Schedule.Windows == nil {
		v.Schedule.Windows = []usLimitsWindow{}
	}
	if len(fields) > 0 {
		return invalid(strings.Join(fields, ","))
	}
	return nil
}

// Comparison witnesses only. Root must recheck current authority and target tier
// before receipt disclosure. No captured permission grants authority.
type usLimitsTarget struct {
	member           usMemberTarget
	limitsRevision   int64
	limitsPresent    bool
	role             string
	current          usLimits
	serverMaxStreams *int
}
type usLimitsRequest struct{ *usLimitsPayload }
type usLimitsPayload struct {
	identity            CommandIdentity
	ownerID             string
	issuer              usPolicyIssuer
	target              usLimitsTarget
	input               usLimits
	normalized          usLimits
	effectiveMaxStreams *int
	ownerOnlyTransition bool
}
type usLimitsCommand struct {
	request usLimitsRequest
	digest  RequestDigest
}

func cloneUSLimits(v usLimits) usLimits {
	v.Schedule.Windows = slices.Clone(v.Schedule.Windows)
	for i := range v.Schedule.Windows {
		v.Schedule.Windows[i].Days = slices.Clone(v.Schedule.Windows[i].Days)
	}
	v.Channels.Channels = slices.Clone(v.Channels.Channels)
	v.Tags.DeniedLabels = slices.Clone(v.Tags.DeniedLabels)
	return v
}
func cloneUSLimitsInt(v *int) *int {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

// All normalization, including timezone database access, precedes writer hold.
func prepareUSLimits(identity CommandIdentity, owner string, issuer usPolicyIssuer, target usLimitsTarget, input usLimits) (*usLimitsCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) || !validUSMemberTarget(target.member) || target.member.expectedRevision >= usMaxRevision || target.limitsRevision < 1 || target.limitsRevision >= usMaxRevision || !target.limitsPresent && target.limitsRevision != 1 {
		return nil, invalid("us_limits_scope")
	}
	if target.role != "owner" && target.role != "admin" && target.role != "member" {
		return nil, invalid("us_limits_role")
	}
	if !validUSLimitsAwayQuality(target.current.AwayQuality) || target.serverMaxStreams != nil && *target.serverMaxStreams < 1 {
		return nil, invalid("us_limits_snapshot")
	}
	input = cloneUSLimits(input)
	normalized := cloneUSLimits(input)
	if err := normalizeUSLimits(&normalized); err != nil {
		return nil, err
	}
	if normalized.AwayQuality == "" {
		normalized.AwayQuality = target.current.AwayQuality
	}
	target.current = cloneUSLimits(target.current)
	target.serverMaxStreams = cloneUSLimitsInt(target.serverMaxStreams)
	var effective *int
	if normalized.MaxStreamsMode == "limit" {
		x := normalized.MaxStreams
		effective = &x
	}
	if target.serverMaxStreams != nil && (effective == nil || *target.serverMaxStreams < *effective) {
		effective = cloneUSLimitsInt(target.serverMaxStreams)
	}
	// Target-tier and owner-only permission comparisons remain explicit witnesses.
	ownerOnly := target.role == "owner" || target.role == "admin"
	c := &usLimitsCommand{request: usLimitsRequest{&usLimitsPayload{identity, owner, issuer, target, input, normalized, effective, ownerOnly}}}
	d, err := c.canonicalDigest()
	if err != nil {
		return nil, err
	}
	c.digest = d
	return c, nil
}

// Native string bytes use byte fields in the digest projection. In particular,
// the source ignores an arbitrary rating-system input when the rating is blank;
// even an accepted malformed byte spelling must remain bound, not JSON-repaired.
// The shared codec owns all type/container framing and length prefixes.
type usLimitsDigestValue struct {
	accessTokens                   bool
	maxProfiles                    int
	allowRemote                    bool
	conversion, awayQuality        []byte
	maxStreams                     int
	maxStreamsMode                 []byte
	remoteBitrateKbps              int
	ratingSystem, maxContentRating []byte
	maximumAge                     int
	allowUnrated                   bool
	timezone                       []byte
	windows                        []usLimitsWindow
	channelMode                    []byte
	channels, labels               [][]byte
}

func usLimitsDigestStrings(values []string) [][]byte {
	if values == nil {
		return nil
	}
	out := make([][]byte, len(values))
	for n, value := range values {
		out[n] = []byte(value)
	}
	return out
}
func usLimitsDigestValues(v usLimits) usLimitsDigestValue {
	return usLimitsDigestValue{v.AccessTokens, v.MaxProfiles, v.AllowRemote, []byte(v.Conversion), []byte(v.AwayQuality), v.MaxStreams, []byte(v.MaxStreamsMode), v.RemoteBitrateKbps, []byte(v.RatingSystem), []byte(v.MaxContentRating), v.MaximumAge, v.AllowUnrated, []byte(v.Schedule.Timezone), v.Schedule.Windows, []byte(v.Channels.Mode), usLimitsDigestStrings(v.Channels.Channels), usLimitsDigestStrings(v.Tags.DeniedLabels)}
}

type usLimitsCanonicalRequest struct {
	identity            CommandIdentity
	ownerID             string
	issuer              usPolicyIssuer
	member              usMemberTarget
	limitsRevision      int64
	limitsPresent       bool
	role                string
	current             usLimitsDigestValue
	serverMaxStreams    *int
	input, normalized   usLimitsDigestValue
	effectiveMaxStreams *int
	ownerOnlyTransition bool
}

func (c *usLimitsCommand) canonicalPayload() usLimitsCanonicalRequest {
	r := c.request
	return usLimitsCanonicalRequest{r.identity, r.ownerID, r.issuer, r.target.member, r.target.limitsRevision, r.target.limitsPresent, r.target.role, usLimitsDigestValues(r.target.current), r.target.serverMaxStreams, usLimitsDigestValues(r.input), usLimitsDigestValues(r.normalized), r.effectiveMaxStreams, r.ownerOnlyTransition}
}
func (c *usLimitsCommand) canonicalDigest() (RequestDigest, error) {
	if c == nil || c.request.usLimitsPayload == nil {
		return RequestDigest{}, invalid("us_limits_nil")
	}
	return canonicalRequestDigest(usMemberLimitsEdit, 1, c.request.identity, c.canonicalPayload())
}
func validUSLimitsAwayQuality(s string) bool {
	switch s {
	case "server", "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p":
		return true
	}
	return false
}
func usLimitsRatingCeiling(system, code string) (int, bool) {
	if code == "" {
		return -1, true
	}
	if system != "" {
		return securitypolicy.RatingCeiling(system, code)
	}
	return securitypolicy.RatingAge(code)
}
func (usLimitsRequest) String() string             { return "usLimitsRequest{redacted}" }
func (usLimitsRequest) GoString() string           { return "usLimitsRequest{redacted}" }
func (usLimitsRequest) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsRequest") }
func (usLimitsCommand) String() string             { return "usLimitsCommand{redacted}" }
func (usLimitsCommand) GoString() string           { return "usLimitsCommand{redacted}" }
func (usLimitsCommand) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsCommand") }

// checkedDigest rejects independent mutation before root composes any effect.
func (c *usLimitsCommand) checkedDigest() (RequestDigest, error) {
	d, err := c.canonicalDigest()
	if err != nil {
		return RequestDigest{}, err
	}
	if d != c.digest {
		return RequestDigest{}, invalid("us_limits_mutated")
	}
	return d, nil
}

func (usLimitsWindow) String() string             { return "usLimitsWindow{redacted}" }
func (usLimitsWindow) GoString() string           { return "usLimitsWindow{redacted}" }
func (usLimitsWindow) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsWindow") }

func (usLimitsSchedule) String() string             { return "usLimitsSchedule{redacted}" }
func (usLimitsSchedule) GoString() string           { return "usLimitsSchedule{redacted}" }
func (usLimitsSchedule) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsSchedule") }

func (usLimitsChannels) String() string             { return "usLimitsChannels{redacted}" }
func (usLimitsChannels) GoString() string           { return "usLimitsChannels{redacted}" }
func (usLimitsChannels) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsChannels") }

func (usLimitsTags) String() string             { return "usLimitsTags{redacted}" }
func (usLimitsTags) GoString() string           { return "usLimitsTags{redacted}" }
func (usLimitsTags) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsTags") }

func (usLimits) String() string             { return "usLimits{redacted}" }
func (usLimits) GoString() string           { return "usLimits{redacted}" }
func (usLimits) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimits") }

func (usLimitsTarget) String() string             { return "usLimitsTarget{redacted}" }
func (usLimitsTarget) GoString() string           { return "usLimitsTarget{redacted}" }
func (usLimitsTarget) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsTarget") }

func (usLimitsPayload) String() string             { return "usLimitsPayload{redacted}" }
func (usLimitsPayload) GoString() string           { return "usLimitsPayload{redacted}" }
func (usLimitsPayload) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsPayload") }

func (usLimitsDigestValue) String() string             { return "usLimitsDigestValue{redacted}" }
func (usLimitsDigestValue) GoString() string           { return "usLimitsDigestValue{redacted}" }
func (usLimitsDigestValue) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usLimitsDigestValue") }

func (usLimitsCanonicalRequest) String() string   { return "usLimitsCanonicalRequest{redacted}" }
func (usLimitsCanonicalRequest) GoString() string { return "usLimitsCanonicalRequest{redacted}" }
func (usLimitsCanonicalRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usLimitsCanonicalRequest")
}
