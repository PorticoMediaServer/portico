package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"regexp"
	"sort"
	"strings"
)

// KeepPolicy decides what a recording group keeps once it has recorded.
type KeepPolicy struct {
	Mode             string `json:"mode"`
	KeepCount        int    `json:"keepCount"`
	KeepDays         int    `json:"keepDays"`
	KeepUntilWatched bool   `json:"keepUntilWatched"`
}

// KeepModes is the published choice list.
var KeepModes = []string{"keep-all", "keep-count", "keep-days"}

// RecordingConversion says what happens to the captured stream afterwards.
// "none" keeps the transport stream exactly as captured; "remux" repackages it
// without re-encoding; "ladder" hands it to the delivery conversion ladder.
type RecordingConversion struct {
	Mode           string `json:"mode"`
	LadderID       string `json:"ladderId"`
	DeleteOriginal bool   `json:"deleteOriginal"`
}

// ConversionModes is the published choice list.
var ConversionModes = []string{"none"}

// Sidecars are the files written next to a finished recording.
type Sidecars struct {
	NFO       bool `json:"nfo"`
	Poster    bool `json:"poster"`
	Fanart    bool `json:"fanart"`
	Thumbnail bool `json:"thumbnail"`
}

// DVRDefaults is the server-wide recording document.
type DVRDefaults struct {
	PrePaddingMs     int                 `json:"prePaddingMs"`
	PostPaddingMs    int                 `json:"postPaddingMs"`
	Keep             KeepPolicy          `json:"keepPolicy"`
	Conversion       RecordingConversion `json:"conversion"`
	FolderTemplate   string              `json:"folderTemplate"`
	MovieTemplate    string              `json:"movieFolderTemplate"`
	RecordingProfile string              `json:"recordingProfile"`
	Sidecars         Sidecars            `json:"sidecars"`
	// TunerPreference decides which tuner a new recording takes when several
	// could serve it.
	TunerPreference string `json:"tunerPreference"`
	// LiveTunerConflict is what happens when a recording needs a tuner that
	// someone is watching live: "keep-watching" (the default; the recording
	// waits for a free tuner and fails if none frees up in its window) or
	// "stop-watching" (the viewer gets a countdown and the recording takes it).
	LiveTunerConflict string `json:"liveTunerConflict"`
}

// RecordingProfiles and TunerPreferences are published choice lists.
var (
	RecordingProfiles = []string{"original"}
	TunerPreferences  = []string{"first-available"}
	// LiveTunerConflicts mirrors livechannels' policy names.
	LiveTunerConflicts = []string{livechannels.KeepWatching, livechannels.StopWatching}
)

// DefaultDVRDefaults is the answer before any owner write.
func DefaultDVRDefaults() DVRDefaults {
	return DVRDefaults{
		PrePaddingMs:      60000,
		PostPaddingMs:     180000,
		Keep:              KeepPolicy{Mode: "keep-all"},
		Conversion:        RecordingConversion{Mode: "none"},
		FolderTemplate:    "",
		MovieTemplate:     "",
		RecordingProfile:  "original",
		Sidecars:          Sidecars{},
		TunerPreference:   "first-available",
		LiveTunerConflict: livechannels.KeepWatching,
	}
}

// FolderToken is one substitution the recording folder template accepts.
type FolderToken struct {
	Token       string `json:"token"`
	Description string `json:"description"`
	Example     string `json:"example"`
	// Padded says the token accepts a two-digit form, written {season:02}.
	Padded bool `json:"supportsPadding"`
}

// FolderTokens is the template vocabulary. A template may use nothing else.
func FolderTokens() []FolderToken {
	return []FolderToken{
		{"{series}", "Series title, or the programme title when there is no series.", "Nova", false},
		{"{title}", "Programme or episode title.", "The Planets", false},
		{"{season}", "Season number.", "3", true},
		{"{episode}", "Episode number within the season.", "7", true},
		{"{year}", "Year the programme was first shown.", "2024", false},
		{"{channel}", "Channel name the recording came from.", "BBC Two", false},
		{"{channelNumber}", "Channel number as served.", "102", true},
		{"{date}", "Recording start date, YYYY-MM-DD.", "2026-09-16", false},
		{"{time}", "Recording start time, HH-MM.", "20-30", false},
		{"{source}", "Live source name.", "Rooftop aerial", false},
		{"{group}", "Recording group that scheduled it.", "Nova", false},
	}
}

var templateToken = regexp.MustCompile(`\{([a-zA-Z]+)(?::0?2)?\}`)

// validTemplate refuses anything but the published tokens and refuses any path
// escape, so a template can never write outside the recordings root.
func validTemplate(v string) bool {
	if v == "" || !safeText(v, 400) {
		return false
	}
	if strings.ContainsAny(v, `\:*?"<>|`) && !strings.Contains(v, ":0") {
		return false
	}
	if strings.HasPrefix(v, "/") || strings.Contains(v, "..") || strings.Contains(v, "//") {
		return false
	}
	known := map[string]bool{}
	for _, token := range FolderTokens() {
		known[strings.Trim(token.Token, "{}")] = token.Padded
	}
	remainder := templateToken.ReplaceAllStringFunc(v, func(match string) string {
		name := templateToken.FindStringSubmatch(match)[1]
		padded, ok := known[name]
		if !ok || strings.Contains(match, ":0") && !padded {
			return "\x00"
		}
		return ""
	})
	if strings.Contains(remainder, "\x00") || strings.ContainsAny(remainder, "{}") {
		return false
	}
	// At least one token must vary per recording or every recording collides.
	return strings.Contains(v, "{title}") || strings.Contains(v, "{episode}") || strings.Contains(v, "{date}")
}

func validateDVRDefaults(v *DVRDefaults) error {
	fields := []string{}
	if v.Keep.KeepUntilWatched || v.Sidecars != (Sidecars{}) || v.Conversion.DeleteOriginal {
		fields = append(fields, "settings.unsupported")
	}
	if (v.Keep.Mode == "keep-count" && v.Keep.KeepCount < 1) || (v.Keep.Mode == "keep-days" && v.Keep.KeepDays < 1) {
		fields = append(fields, "settings.keepPolicy")
	}

	if !oneOf(v.Keep.Mode, KeepModes...) {
		fields = append(fields, "settings.keepPolicy.mode")
	}
	if !oneOf(v.Conversion.Mode, ConversionModes...) {
		fields = append(fields, "settings.conversion.mode")
	}
	if !safeText(v.Conversion.LadderID, 128) || v.Conversion.Mode != "ladder" && v.Conversion.LadderID != "" {
		fields = append(fields, "settings.conversion.ladderId")
	}
	if v.Conversion.Mode == "none" && v.Conversion.DeleteOriginal {
		// There is no converted copy to keep instead, so this would delete the
		// only recording that exists.
		fields = append(fields, "settings.conversion.deleteOriginal")
	}
	if v.FolderTemplate != "" {
		fields = append(fields, "settings.folderTemplate")
	}
	if v.MovieTemplate != "" {
		fields = append(fields, "settings.movieFolderTemplate")
	}
	if !oneOf(v.RecordingProfile, RecordingProfiles...) {
		fields = append(fields, "settings.recordingProfile")
	}
	if !oneOf(v.TunerPreference, TunerPreferences...) {
		fields = append(fields, "settings.tunerPreference")
	}
	if v.LiveTunerConflict == "" {
		v.LiveTunerConflict = livechannels.KeepWatching
	}
	if !oneOf(v.LiveTunerConflict, LiveTunerConflicts...) {
		fields = append(fields, "settings.liveTunerConflict")
	}
	if len(fields) > 0 {
		return invalid(fields...)
	}
	clampInt(&v.PrePaddingMs, 0, 1800000)
	clampInt(&v.PostPaddingMs, 0, 7200000)
	clampInt(&v.Keep.KeepCount, 0, 1000)
	clampInt(&v.Keep.KeepDays, 0, 3650)
	return nil
}

const dvrScope = "dvr"

// DVRDocument is the DVR settings page: the document plus every vocabulary the
// page renders from.
type DVRDocument struct {
	Revision     int64               `json:"revision"`
	Digest       string              `json:"digest"`
	Settings     DVRDefaults         `json:"settings"`
	FolderTokens []FolderToken       `json:"folderTokens"`
	Enumerations map[string][]string `json:"enumerations"`
}

// DVRSettings reads the DVR defaults page.
func (s *Service) DVRSettings(ctx context.Context, auth Authorize) (DVRDocument, error) {
	return s.runtimeDVRSettings(ctx, auth)
}
func (s *Service) SaveDVRSettings(ctx context.Context, auth Authorize, change Change[DVRDefaults]) (DVRDocument, error) {
	return s.saveRuntimeDVRSettings(ctx, auth, change)
}

func dvrEnumerations() map[string][]string {
	return map[string][]string{"keepMode": KeepModes, "conversionMode": ConversionModes, "recordingProfile": RecordingProfiles, "tunerPreference": TunerPreferences, "liveTunerConflict": LiveTunerConflicts}
}

// TunerAllocation is one live source's tuners: how many there are, what is
// using them now, and what is queued next.
type TunerAllocation struct {
	SourceID   string            `json:"sourceId"`
	SourceName string            `json:"sourceName"`
	TunerCount int               `json:"tunerCount"`
	InUse      int               `json:"inUse"`
	Active     []TunerAssignment `json:"active"`
	Upcoming   []TunerAssignment `json:"upcoming"`
	// Conflicts counts upcoming recordings that cannot all fit at once.
	Conflicts int `json:"conflicts"`
}

// TunerAssignment is one recording's claim on a tuner.
type TunerAssignment struct {
	Kind      string `json:"kind"`
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
	// AccountName and ProfileName name whose it is, for the owner's list;
	// empty when the account is not this server's own.
	AccountName  string `json:"accountName"`
	ProfileName  string `json:"profileName"`
	RecordingID  string `json:"recordingId"`
	ChannelID    string `json:"channelId"`
	Title        string `json:"title"`
	State        string `json:"state"`
	StartsAt     string `json:"startsAt"`
	EndsAt       string `json:"endsAt"`
	AllocationID string `json:"allocationId,omitempty"`
}

// TunerView is the whole allocation page.
type TunerView struct {
	Sources    []TunerAllocation `json:"sources"`
	ObservedAt string            `json:"observedAt"`
	// UpcomingWindowHours is how far ahead the upcoming lists look.
	UpcomingWindowHours int `json:"upcomingWindowHours"`
}

// upcomingWindow is how far ahead the tuner page looks. It is the one source
// for both the query horizon and UpcomingWindowHours, which the client help
// renders as "over the next N hours".
const upcomingWindow = 12

// Tuners reports which tuner each source is using and what is queued. It reads
// the DVR store's own tables; nothing here schedules or claims anything.
func (s *Service) Tuners(ctx context.Context, auth Authorize) (TunerView, error) {
	out := TunerView{Sources: []TunerAllocation{}, UpcomingWindowHours: upcomingWindow}
	now := s.milliseconds()
	out.ObservedAt = timeFromMilliseconds(now)
	horizon := now + int64(upcomingWindow)*3600000
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		// Without a live channel store there are no tuners to allocate, which is
		// an empty view rather than a failure.
		if !tableExists(ctx, tx, "live_sources") {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,name,tuner_count FROM live_sources WHERE NOT EXISTS(SELECT 1 FROM live_source_removals r WHERE r.source_id=live_sources.id) ORDER BY name,id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a TunerAllocation
			if err = rows.Scan(&a.SourceID, &a.SourceName, &a.TunerCount); err != nil {
				return err
			}
			a.Active, a.Upcoming = []TunerAssignment{}, []TunerAssignment{}
			out.Sources = append(out.Sources, a)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if !tableExists(ctx, tx, "dvr_recordings") {
			return nil
		}
		for i := range out.Sources {
			source := &out.Sources[i]
			if source.Active, err = activeAssignments(ctx, tx, source.SourceID, now); err != nil {
				return err
			}
			channels := map[string]bool{}
			for _, a := range source.Active {
				channels[a.ChannelID] = true
			}
			source.InUse = len(channels)
			if source.Upcoming, err = assignments(ctx, tx, source.SourceID, `r.state IN('scheduled','conflicted','waiting-source','waiting-guide') AND r.start_ms>=? AND r.start_ms<`+itoa(horizon), now); err != nil {
				return err
			}
			source.Conflicts = overlapExcess(source.Upcoming, source.TunerCount)
		}
		return nil
	})
	return out, err
}

func itoa(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func assignments(ctx context.Context, tx *sql.Tx, source, condition string, bound int64) ([]TunerAssignment, error) {
	out := []TunerAssignment{}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.channel_id,r.programme_json,r.state,r.start_ms,r.end_ms,r.allocation_id,r.account_id,r.profile_id,COALESCE(u.username,''),COALESCE(CASE WHEN p.deleted=1 THEN p.name||' (removed)' ELSE p.name END,'') FROM dvr_recordings r LEFT JOIN accounts u ON u.id=r.account_id LEFT JOIN direct_profiles p ON p.id=r.profile_id AND p.account_id=r.account_id WHERE r.source_id=? AND `+condition+` ORDER BY r.start_ms,r.id`, source, bound)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a TunerAssignment
		var programme string
		var start, end int64
		if err = rows.Scan(&a.RecordingID, &a.ChannelID, &programme, &a.State, &start, &end, &a.AllocationID, &a.AccountID, &a.ProfileID, &a.AccountName, &a.ProfileName); err != nil {
			return out, err
		}
		var decoded struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(programme), &decoded)
		a.Kind = "recording"
		a.Title, a.StartsAt, a.EndsAt = decoded.Title, timeFromMilliseconds(start), timeFromMilliseconds(end)
		out = append(out, a)
	}
	return out, rows.Err()
}

// overlapExcess counts how many upcoming recordings cannot fit given the tuner
// count, by sweeping the start and end points.
func overlapExcess(list []TunerAssignment, tuners int) int {
	if tuners <= 0 || len(list) == 0 {
		return 0
	}
	type point struct {
		at      string
		delta   int
		channel string
	}
	points := make([]point, 0, len(list)*2)
	for _, a := range list {
		key := a.ChannelID
		if key == "" {
			key = a.RecordingID
		}
		points = append(points, point{a.StartsAt, 1, key}, point{a.EndsAt, -1, key})
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].at == points[j].at {
			return points[i].delta < points[j].delta
		}
		return points[i].at < points[j].at
	})
	current, worst := 0, 0
	counts := map[string]int{}
	for _, p := range points {
		old := counts[p.channel]
		counts[p.channel] += p.delta
		if old == 0 && counts[p.channel] > 0 {
			current++
		}
		if old > 0 && counts[p.channel] == 0 {
			current--
		}
		if current > worst {
			worst = current
		}
	}
	if worst > tuners {
		return worst - tuners
	}
	return 0
}

// RecordingGroup is a standing instruction to record a series or anything
// matching a keyword.
type RecordingGroup struct {
	Owner     livechannels.Owner `json:"owner"`
	Status    string             `json:"status"`
	ID        string             `json:"id"`
	Kind      string             `json:"kind"`
	Name      string             `json:"name"`
	Match     string             `json:"match"`
	SourceID  string             `json:"sourceId"`
	Enabled   bool               `json:"enabled"`
	Revision  int64              `json:"revision"`
	Options   GroupOptions       `json:"options"`
	CreatedAt string             `json:"createdAt"`
	UpdatedAt string             `json:"updatedAt"`
}

// GroupOptions overrides the server defaults for one group. A nil field
// inherits.
type GroupOptions struct {
	PrePaddingMs    *int        `json:"prePaddingMs"`
	PostPaddingMs   *int        `json:"postPaddingMs"`
	Keep            *KeepPolicy `json:"keepPolicy"`
	NewEpisodesOnly bool        `json:"newEpisodesOnly"`
	Priority        int         `json:"priority"`
	FolderTemplate  string      `json:"folderTemplate"`
}

// RecordingGroupPage is one page of the groups listing.
type RecordingGroupPage struct {
	Items      []RecordingGroup `json:"items"`
	NextCursor string           `json:"nextCursor"`
}

// RecordingGroupChange creates or updates one group.
type RecordingGroupChange struct {
	Owner            livechannels.Owner `json:"owner"`
	Anchor           dvr.Occurrence     `json:"anchor"`
	ExpectedRevision int64              `json:"expectedRevision"`
	OperationID      string             `json:"operationId"`
	Kind             string             `json:"kind"`
	Name             string             `json:"name"`
	Match            string             `json:"match"`
	SourceID         string             `json:"sourceId"`
	Enabled          bool               `json:"enabled"`
	Options          GroupOptions       `json:"options"`
}

func validateGroup(c *RecordingGroupChange) error {
	fields := []string{}
	if !oneOf(c.Kind, "series", "title", "keyword") {
		fields = append(fields, "kind")
	}
	if c.Name == "" || !safeText(c.Name, 160) {
		fields = append(fields, "name")
	}
	if c.Match == "" || !safeText(c.Match, 512) {
		fields = append(fields, "match")
	}
	if !safeText(c.SourceID, 128) {
		fields = append(fields, "sourceId")
	}
	if c.Options.Keep != nil && !oneOf(c.Options.Keep.Mode, KeepModes...) {
		fields = append(fields, "options.keepPolicy.mode")
	}
	if c.Options.FolderTemplate != "" && !validTemplate(c.Options.FolderTemplate) {
		fields = append(fields, "options.folderTemplate")
	}
	if c.Options.PrePaddingMs != nil && (*c.Options.PrePaddingMs < 0 || *c.Options.PrePaddingMs > 21600000) {
		fields = append(fields, "options.prePaddingMs")
	}
	if c.Options.PostPaddingMs != nil && (*c.Options.PostPaddingMs < 0 || *c.Options.PostPaddingMs > 21600000) {
		fields = append(fields, "options.postPaddingMs")
	}
	if len(fields) > 0 {
		return invalid(fields...)
	}
	if c.Options.Priority < -1000 || c.Options.Priority > 1000 {
		return invalid("options.priority")
	}
	if c.Options.Keep != nil && (c.Options.Keep.KeepCount < 0 || c.Options.Keep.KeepCount > 100000 || c.Options.Keep.KeepDays < 0 || c.Options.Keep.KeepDays > 3650) {
		return invalid("options.keepPolicy")
	}
	return nil
}

// RecordingGroups pages the groups newest first.
func (s *Service) RecordingGroups(ctx context.Context, auth Authorize, token string, limit int) (RecordingGroupPage, error) {
	return s.runtimeRecordingGroups(ctx, auth, token, limit)
}
func (s *Service) SaveRecordingGroup(ctx context.Context, auth Authorize, id string, change RecordingGroupChange) (RecordingGroup, error) {
	return s.saveRuntimeRecordingGroup(ctx, auth, id, change)
}
func (s *Service) DeleteRecordingGroup(ctx context.Context, auth Authorize, id string, revision int64, operation string) error {
	return s.deleteRuntimeRecordingGroup(ctx, auth, id, revision, operation)
}

func activeAssignments(ctx context.Context, tx *sql.Tx, source string, now int64) ([]TunerAssignment, error) {
	out := []TunerAssignment{}
	rows, e := tx.QueryContext(ctx, `SELECT a.resource_id,a.channel_id,COALESCE(json_extract(r.programme_json,'$.title'),c.name,''),a.kind,a.account_id,a.profile_id,COALESCE(u.username,''),COALESCE(CASE WHEN p.deleted=1 THEN p.name||' (removed)' ELSE p.name END,''),COALESCE(NULLIF(r.state,''),a.state),a.created_ms,a.lease_until_ms,a.id FROM live_allocations a LEFT JOIN accounts u ON u.id=a.account_id LEFT JOIN direct_profiles p ON p.id=a.profile_id AND p.account_id=a.account_id LEFT JOIN dvr_recordings r ON a.kind='recording' AND r.id=a.resource_id LEFT JOIN live_sources s ON s.id=a.source_id LEFT JOIN live_channel_versions c ON c.generation_id=s.active_generation AND c.channel_id=a.channel_id WHERE a.source_id=? AND a.state!='released' ORDER BY a.channel_id,a.kind,a.id`, source)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var a TunerAssignment
		var start, end int64
		if e = rows.Scan(&a.RecordingID, &a.ChannelID, &a.Title, &a.Kind, &a.AccountID, &a.ProfileID, &a.AccountName, &a.ProfileName, &a.State, &start, &end, &a.AllocationID); e != nil {
			return out, e
		}
		a.StartsAt = timeFromMilliseconds(start)
		a.EndsAt = timeFromMilliseconds(end)
		if end <= now {
			a.State = "retiring"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
