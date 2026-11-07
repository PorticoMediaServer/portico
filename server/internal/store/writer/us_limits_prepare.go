package writer

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
	"reflect"
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

// Limits is one member's access envelope.
//
// Where each limit is enforced:
//
//	MaxStreams           — Enforcer.AdmitPlayback, called before a playback lease
//	                       is created; counts live playback_sessions rows plus
//	                       live playback_v1_sessions rows (a v1 session's legacy
//	                       presentation row is excluded, so one stream counts once).
//	RemoteBitrateKbps    — Enforcer.AdmitPlayback returns it as a clamp, and the
//	                       delivery policy applies it as MaxVideoBitrateBPS for a
//	                       remote transport class.
//	MaxContentRating     — folded into the viewer's content restrictions
//	                       (Limits.Narrow) and applied by the one catalogue
//	                       predicate, for lists, detail and playback alike.
//	usLimitsTags            — the same.
//	Schedule             — Enforcer.AdmitSession, Enforcer.AdmitPlayback and
//	                       Enforcer.AdmitChannel.
//	usLimitsChannels        — Enforcer.AdmitChannel, called by the v1 channel
//	                       start path with canonical v1 ids only.
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
	if !validUSSecurityIdentity(identity, owner, issuer) || !validUSMemberTarget(target.member) || target.member.expectedRevision >= usMaxRevision || target.limitsRevision < 1 || target.limitsRevision >= usMaxRevision {
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

// JSON is only a cached projection, never schema authority. Exact native text
// bytes are separately bound: encoding/json replaces malformed UTF-8. Nil/empty
// slices remain distinguished in JSON and all nested strings are also captured.
type usLimitsDigestPayload struct {
	body []byte
	text [][]byte
}

func (c *usLimitsCommand) canonicalDigest() (RequestDigest, error) {
	if c == nil {
		return RequestDigest{}, invalid("us_limits_nil")
	}
	var p usLimitsDigestPayload
	// Reflection includes private scope and issuer witnesses, unlike json.Marshal.
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.String:
			p.text = append(p.text, []byte(v.String()))
		case reflect.Pointer:
			p.body = append(p.body, byte(0))
			if !v.IsNil() {
				p.body[len(p.body)-1] = 1
				walk(v.Elem())
			}
		case reflect.Array, reflect.Slice:
			if v.Kind() == reflect.Slice {
				if v.IsNil() {
					p.body = append(p.body, 0)
				} else {
					p.body = append(p.body, 1)
				}
			}
			b, _ := json.Marshal(v.Len())
			p.body = append(p.body, b...)
			p.body = append(p.body, ':')
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Bool:
			if v.Bool() {
				p.body = append(p.body, 't')
			} else {
				p.body = append(p.body, 'f')
			}
		case reflect.Int, reflect.Int64:
			b, _ := json.Marshal(v.Int())
			p.body = append(p.body, b...)
			p.body = append(p.body, ':')
		case reflect.Uint8:
			p.body = append(p.body, byte(v.Uint()))
		}
	}
	walk(reflect.ValueOf(c.request))
	return canonicalRequestDigest(usMemberLimitsEdit, 1, c.request.identity, p)
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
