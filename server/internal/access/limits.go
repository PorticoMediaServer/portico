package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

// isCanonicalChannelID reports whether id is a canonical v1 channel id
// ("live:<source>:<channel>" or "library:<channel>", with no empty parts and
// no extra colons). The allow/deny lists store these only; anything else is
// rejected at write time.
func isCanonicalChannelID(id string) bool {
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

// ScheduleWindow is one weekday/time band in the member's own timezone. Days are
// 0 (Sunday) through 6. StartMinute and EndMinute are minutes past local
// midnight; a window whose end is not after its start wraps past midnight.
type ScheduleWindow struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
}

// AccessSchedule denies sign-in and playback outside its windows. An empty
// window list means no schedule restriction at all, which is the default.
type AccessSchedule struct {
	Timezone string           `json:"timezone"`
	Windows  []ScheduleWindow `json:"windows"`
}

// ChannelPolicy restricts live channels by their canonical v1 channel id
// only ("live:<source>:<channel>" or "library:<channel>"). Mode "all" ignores
// the list; "allow" permits only the listed ids; "deny" permits everything else.
type ChannelPolicy struct {
	Mode     string   `json:"mode"`
	Channels []string `json:"channels"`
}

// TagPolicy denies items carrying any of these catalog label or tag values.
type TagPolicy struct {
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
//	TagPolicy            — the same.
//	Schedule             — Enforcer.AdmitSession, Enforcer.AdmitPlayback and
//	                       Enforcer.AdmitChannel.
//	ChannelPolicy        — Enforcer.AdmitChannel, called by the v1 channel
//	                       start path with canonical v1 ids only.
type Limits struct {
	AccessTokens      bool              `json:"accessTokens"`
	MaxProfiles       int               `json:"maxProfiles"`
	AllowRemote       bool              `json:"allowRemote"`
	Conversion        string            `json:"conversion"`
	// AwayQuality is this person's away-from-home ceiling: the server sentinel,
	// or one published quality-ladder id that narrows it. Omitted means "leave
	// whatever is stored", so an older full write cannot clear a saved cap.
	AwayQuality       streamlimits.AwayQuality `json:"awayQuality,omitempty"`
	MaxStreams        int               `json:"maxStreams"`
	MaxStreamsMode    streamlimits.Mode `json:"maxStreamsMode"`
	RemoteBitrateKbps int               `json:"remoteBitrateKbps"`
	// RatingSystem and MaxContentRating name the highest rating the account
	// may see, exactly as a profile's restriction does: one value of one of
	// the published rating systems (GET /v1/rating-systems). With no system
	// named, the rating is read as any spelling the rating table knows.
	// MaximumAge is what that resolves to, the minimum admission age the
	// rating carries, or -1 for no ceiling; it is the number every title is
	// compared against, whatever system its own rating was issued in.
	RatingSystem     string         `json:"ratingSystem"`
	MaxContentRating string         `json:"maxContentRating"`
	MaximumAge       int            `json:"maximumAge"`
	AllowUnrated     bool           `json:"allowUnrated"`
	Schedule         AccessSchedule `json:"schedule"`
	Channels         ChannelPolicy  `json:"channelPolicy"`
	Tags             TagPolicy      `json:"tagPolicy"`
}

// DefaultLimits inherits the server stream ceiling, adds no content ceiling,
// and permits unrated content. Explicit unlimited also obeys the server ceiling.
func DefaultLimits() Limits {
	return Limits{MaxStreamsMode: streamlimits.Inherit, AccessTokens: true, MaxProfiles: 8, AllowRemote: true, Conversion: "server", AwayQuality: streamlimits.AwayQualityServer, MaximumAge: -1, AllowUnrated: true, Schedule: AccessSchedule{Windows: []ScheduleWindow{}}, Channels: ChannelPolicy{Mode: "all", Channels: []string{}}, Tags: TagPolicy{DeniedLabels: []string{}}}
}

// LimitsDocument is the wire shape every limits read and write returns.
type LimitsDocument struct {
	AccountID           string `json:"accountId"`
	Revision            int64  `json:"revision"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
	Limits              Limits `json:"limits"`
	ServerMaxStreams    *int   `json:"serverMaxStreams"`
	EffectiveMaxStreams *int   `json:"effectiveMaxStreams"`
}

func normalizeLimits(v *Limits) error {
	fields := []string{}
	if v.MaxProfiles < 1 || v.MaxProfiles > 8 {
		fields = append(fields, "limits.maxProfiles")
	}
	if v.AwayQuality != "" && !streamlimits.ValidAwayQuality(v.AwayQuality) {
		fields = append(fields, "limits.awayQuality")
	}
	if v.Conversion != "server" && v.Conversion != "audio" && v.Conversion != "none" {
		fields = append(fields, "limits.conversion")
	}
	if !streamlimits.Valid(v.MaxStreamsMode, v.MaxStreams) {
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
	if age, ok := ratingCeiling(v.RatingSystem, v.MaxContentRating); ok {
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
		if !safeText(id, 200) || strings.TrimSpace(id) == "" || !isCanonicalChannelID(id) {
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
		if !safeText(label, 120) || strings.TrimSpace(label) == "" {
			fields = append(fields, "limits.tagPolicy.deniedLabels")
			break
		}
		v.Tags.DeniedLabels[i] = strings.TrimSpace(label)
	}
	if v.Schedule.Windows == nil {
		v.Schedule.Windows = []ScheduleWindow{}
	}
	if len(fields) > 0 {
		return &ValidationError{fields}
	}
	return nil
}

// ratingCeiling resolves a limit's rating to the admission age it stands for
// on the one scale every restriction uses (identity's rating table): -1 for no
// rating, false for a rating the table does not know.
func ratingCeiling(system, code string) (int, bool) {
	if code == "" {
		return -1, true
	}
	if system != "" {
		return identity.RatingCeiling(system, code)
	}
	return identity.RatingAge(code)
}

// Ceiling is the admission age the account's rating limit resolves to, or -1
// for none. It is resolved from the stored rating each time, so a limit saved
// under an older rating table is judged by the current one; a stored rating
// the table no longer knows admits only all-ages titles rather than everything.
func (v Limits) Ceiling() int {
	age, ok := ratingCeiling(v.RatingSystem, v.MaxContentRating)
	if !ok {
		return 0
	}
	return age
}

// Narrow applies this account's content limits on top of a profile's own
// restrictions: the lower rating ceiling holds, on one age scale.
func (v Limits) Narrow(r identity.ContentRestrictions, revision int64) identity.ContentRestrictions {
	return r.Narrow(v.Ceiling(), !v.AllowUnrated, v.Tags.DeniedLabels, revision)
}

// ContentLimitsTx reads the account's limits and applies them to a profile's
// restrictions inside the caller's transaction. An account with no stored
// limits narrows nothing.
func ContentLimitsTx(ctx context.Context, tx *sql.Tx, account string, r identity.ContentRestrictions) (identity.ContentRestrictions, error) {
	if account == "" {
		return r, nil
	}
	var revision int64
	var body string
	err := tx.QueryRowContext(ctx, `SELECT revision,body FROM access_limits WHERE account_id=?`, account).Scan(&revision, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	limits := DefaultLimits()
	if err = json.Unmarshal([]byte(body), &limits); err != nil {
		return r, err
	}
	return limits.Narrow(r, revision), nil
}

func readLimits(tx *sql.Tx, account string) (LimitsDocument, error) {
	out := LimitsDocument{AccountID: account, Revision: 1, Limits: DefaultLimits()}
	streams, e := streamlimits.ReadTx(context.Background(), tx, account)
	if e != nil {
		return out, e
	}
	out.ServerMaxStreams = streams.ServerCeiling
	out.EffectiveMaxStreams = streams.EffectiveLimit
	var body string
	var updated int64
	err := tx.QueryRow(`SELECT revision,updated_ms,body FROM access_limits WHERE account_id=?`, account).Scan(&out.Revision, &updated, &body)
	if errors.Is(err, sql.ErrNoRows) {
		if e := tx.QueryRow(`SELECT access_tokens,max_profiles FROM direct_memberships WHERE account_id=?`, account).Scan(&out.Limits.AccessTokens, &out.Limits.MaxProfiles); e != nil {
			return out, e
		}
		out.Limits.MaxStreamsMode, out.Limits.AwayQuality = streams.Mode, streams.AwayQuality
		if streams.Limit != nil {
			out.Limits.MaxStreams = *streams.Limit
		}
		// A member with no stored row is unrestricted at revision 1, so a client
		// can write against that revision without a create step.
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Limits = DefaultLimits()
	if err = json.Unmarshal([]byte(body), &out.Limits); err != nil {
		return out, err
	}
	if e := tx.QueryRow(`SELECT access_tokens,max_profiles FROM direct_memberships WHERE account_id=?`, account).Scan(&out.Limits.AccessTokens, &out.Limits.MaxProfiles); e != nil {
		return out, e
	}
	out.Limits.MaxStreamsMode, out.Limits.AwayQuality = streams.Mode, streams.AwayQuality
	out.Limits.MaxStreams = 0
	if streams.Limit != nil {
		out.Limits.MaxStreams = *streams.Limit
	}
	out.Limits.MaximumAge = out.Limits.Ceiling()
	out.UpdatedAt = time.UnixMilli(updated).UTC().Format(time.RFC3339)
	return out, nil
}

// Limits returns one member's limits document.
func (s *Store) Limits(ctx context.Context, auth Authorize, account string) (out LimitsDocument, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var e error
		if e = accountExists(tx, account); e != nil {
			return e
		}
		out, e = readLimits(tx, account)
		return e
	})
	return
}

type LimitsChange struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
	Limits           Limits `json:"limits"`
}

// SetLimits replaces a member's limits. The whole envelope is written at once so
// a client never observes half a policy.
func (s *Store) SetLimits(ctx context.Context, auth Authorize, account string, c LimitsChange) (out LimitsDocument, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	if !validOperationID(c.OperationID) {
		return out, invalid("operationId")
	}
	if err = normalizeLimits(&c.Limits); err != nil {
		return out, err
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		scope := "limits:" + account
		body, want, e := receipt(tx, scope, c.OperationID, c)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		if e = accountExists(tx, account); e != nil {
			return e
		}
		current, e := readLimits(tx, account)
		if e != nil {
			return e
		}
		if current.Revision != c.ExpectedRevision {
			return &ConflictError{current.Revision}
		}
		revision, now := current.Revision+1, s.now()
		raw, e := limitsBody(c.Limits)
		if e != nil {
			return e
		}
		if c.Limits.AwayQuality != "" {
			if e = streamlimits.WriteAwayQualityTx(ctx, tx, account, c.Limits.AwayQuality); e != nil {
				return e
			}
		}
		if e = streamlimits.WriteTx(ctx, tx, account, c.Limits.MaxStreamsMode, c.Limits.MaxStreams); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO access_limits VALUES(?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms,body=excluded.body`, account, revision, now, string(raw)); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET access_tokens=?,max_profiles=? WHERE account_id=?`, c.Limits.AccessTokens, c.Limits.MaxProfiles, account); e != nil {
			return e
		}
		out, e = readLimits(tx, account)
		if e != nil {
			return e
		}
		return saveReceipt(tx, scope, c.OperationID, want, out, now)
	})
	return
}

func accountExists(tx *sql.Tx, account string) error {
	var ok bool
	if e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, account).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return sql.ErrNoRows
	}
	return nil
}

// Existing non-stream access settings use their established body store. Stream
// mode/value, token permission and profile capacity have normalized authorities
// and are deliberately absent here; reads overlay those rows, never this body.
func limitsBody(v Limits) ([]byte, error) {
	type stored struct {
		AllowRemote       bool           `json:"allowRemote"`
		Conversion        string         `json:"conversion"`
		RemoteBitrateKbps int            `json:"remoteBitrateKbps"`
		RatingSystem      string         `json:"ratingSystem"`
		MaxContentRating  string         `json:"maxContentRating"`
		AllowUnrated      bool           `json:"allowUnrated"`
		Schedule          AccessSchedule `json:"schedule"`
		Channels          ChannelPolicy  `json:"channelPolicy"`
		Tags              TagPolicy      `json:"tagPolicy"`
	}
	return json.Marshal(stored{v.AllowRemote, v.Conversion, v.RemoteBitrateKbps, v.RatingSystem, v.MaxContentRating, v.AllowUnrated, v.Schedule, v.Channels, v.Tags})
}
