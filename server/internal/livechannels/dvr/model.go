// Package dvr owns recording intent and scheduling, not playback or accounts.
package dvr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const Version = "1.0"

var (
	ErrInvalid                  = errors.New("Check the recording options and try again.")
	ErrConflict                 = errors.New("The recording changed. Reload before applying this action.")
	ErrDenied                   = errors.New("This recording is not available to this profile.")
	ErrUnavailable              = errors.New("Recordings are temporarily unavailable.")
	ErrUnsupportedPredicate     = errors.New("This programme has no reliable new or repeat metadata. Choose all episodes.")
	ErrCaptureUnavailable       = errors.New("Recording capture is not available on this server.")
	ErrDeletionUnavailable      = errors.New("Physical recording retirement is unavailable on this server.")
	ErrStoragePolicyUnavailable = errors.New("Measured recording storage enforcement is unavailable on this server.")
	ErrLease                    = errors.New("This recording worker no longer owns the capture.")
)
var hexID = regexp.MustCompile(`^[0-9a-f]{48}$`)
var canonicalID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validText(s string, max int) bool {
	return s != "" && utf8.ValidString(s) && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func recordID(o livechannels.Owner, source, programme string) string {
	return digest([]string{"dvr-occurrence-v1", o.Authority, o.AccountID, o.ProfileID, source, programme})
}

// DurableAuthority consumes current local policy, not the bearer/session that
// happened to create a rule. Account/profile ownership never follows a revision.
type DurableAuthority func(context.Context, *sql.Tx, livechannels.Owner, string, string) error

type Options struct {
	BeforeMs      int `json:"beforeMs"`
	AfterMs       int `json:"afterMs"`
	Priority      int `json:"priority"`
	RetentionDays int `json:"retentionDays"` // zero = keep until manually deleted
	EpisodeLimit  int `json:"episodeLimit"`  // zero = unlimited; Keep is always exempt
}

func (o Options) Valid() bool {
	return o.BeforeMs >= 0 && o.BeforeMs <= 21600000 && o.AfterMs >= 0 && o.AfterMs <= 21600000 && o.Priority >= -1000 && o.Priority <= 1000 && o.RetentionDays >= 0 && o.RetentionDays <= 3650 && o.EpisodeLimit >= 0 && o.EpisodeLimit <= 100000
}

type Occurrence struct {
	SourceID    string `json:"sourceId"`
	ChannelID   string `json:"channelId"`
	Generation  string `json:"generation"`
	ProgrammeID string `json:"programmeId"`
}

func (p Occurrence) Valid() bool {
	return hexID.MatchString(p.SourceID) && canonicalID.MatchString(p.ChannelID) && hexID.MatchString(p.Generation) && canonicalID.MatchString(p.ProgrammeID)
}

type ScheduleInput struct {
	UseDefaults bool       `json:"useDefaults"`
	RequestID   string     `json:"requestId"`
	Occurrence  Occurrence `json:"occurrence"`
	Options     Options    `json:"options"`
}
type Mutation struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type UpdateInput struct {
	Mutation
	Options Options `json:"options"`
}
type KeepInput struct {
	Mutation
	Keep bool `json:"keep"`
}

type RuleConfig struct {
	MatchKind       string   `json:"matchKind"`
	Title           string   `json:"title"`
	ChannelID       string   `json:"channelId"`
	Name            string   `json:"name"`
	SourceID        string   `json:"sourceId"`
	SeriesID        string   `json:"seriesId"`
	Enabled         bool     `json:"enabled"`
	Episodes        string   `json:"episodes"` // all | new; unknown evidence never matches new
	AllowedChannels []string `json:"allowedChannels"`
	BlockedChannels []string `json:"blockedChannels"`
	BlockedKeywords []string `json:"blockedKeywords"`
	Keywords        []string `json:"keywords"` // every nonempty keyword, case-insensitive
	Options         Options  `json:"options"`
}

func (c RuleConfig) Valid() bool {
	if !validText(c.Name, 160) || !hexID.MatchString(c.SourceID) || (!c.titleMatch() && (!validText(c.SeriesID, 512) || c.Title != "" || c.ChannelID != "") || c.titleMatch() && (c.SeriesID != "" || !validText(c.Title, 512) || !canonicalID.MatchString(c.ChannelID))) || (c.MatchKind != "" && c.MatchKind != "series" && c.MatchKind != "title") || !c.Options.Valid() || (c.Episodes != "all" && c.Episodes != "new") || c.AllowedChannels == nil || c.BlockedChannels == nil || c.Keywords == nil || len(c.AllowedChannels) > 2000 || len(c.BlockedChannels) > 2000 || len(c.Keywords) > 20 || len(c.BlockedKeywords) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, xs := range [][]string{c.AllowedChannels, c.BlockedChannels} {
		for _, id := range xs {
			if !canonicalID.MatchString(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	for _, s := range append(append([]string{}, c.Keywords...), c.BlockedKeywords...) {
		if !validText(strings.TrimSpace(s), 128) {
			return false
		}
	}
	return true
}
func (c RuleConfig) Matches(p livechannels.Programme) bool {
	if !c.Enabled || !c.identityMatches(p) {
		return false
	}
	if c.Episodes == "new" && p.NewEvidence != "new" {
		return false
	}
	allowed := len(c.AllowedChannels) == 0
	for _, id := range c.AllowedChannels {
		if id == p.ChannelID {
			allowed = true
		}
	}
	for _, id := range c.BlockedChannels {
		if id == p.ChannelID {
			return false
		}
	}
	if !allowed {
		return false
	}
	text := strings.ToLower(p.Title + "\n" + p.Description)
	for _, k := range c.BlockedKeywords {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(k))) {
			return false
		}
	}
	for _, k := range c.Keywords {
		if !strings.Contains(text, strings.ToLower(strings.TrimSpace(k))) {
			return false
		}
	}
	return true
}

type RuleInput struct {
	UseDefaults bool `json:"useDefaults"`
	Mutation
	ID     string     `json:"id"`
	Anchor Occurrence `json:"anchor"` // only required on create, unchanged thereafter
	Config RuleConfig `json:"config"`
}
type Rule struct {
	Summary        RuleSummary `json:"summary"`
	ID             string      `json:"id"`
	Revision       int64       `json:"revision"`
	Config         RuleConfig  `json:"config"`
	ReconcileState string      `json:"reconcileState"`
	Diagnostic     string      `json:"diagnostic"`
	UpdatedAt      string      `json:"updatedAt"`
}
type Recording struct {
	PositionMs int64      `json:"positionMs"`
	DurationMs int64      `json:"durationMs"`
	Overlaps   []Overlap  `json:"overlaps"`
	ID         string     `json:"id"`
	Revision   int64      `json:"revision"`
	Occurrence Occurrence `json:"occurrence"`
	// Channel is the recorded channel as the owner knows it now (FEAT-08):
	// its current name and number, else the ones from the guide the recording
	// was scheduled from; empty once the channel is gone from both.
	Channel RecordingChannel `json:"channel"`
	// Watched is the owner's watched state of the published recording
	// (catalogue personal state); null until the recording is published.
	Watched   *bool                  `json:"watched"`
	Programme livechannels.Programme `json:"programme"`
	// SeriesID is the stable grouping key for DVR rows by show (FEAT-08): the
	// programme's series id. A recording without series metadata carries none
	// and the client shows it ungrouped. SeriesTitle names the show when the
	// programme has a title.
	SeriesID      string                        `json:"seriesId,omitempty"`
	SeriesTitle   string                        `json:"seriesTitle,omitempty"`
	RuleID        string                        `json:"ruleId"`
	Options       Options                       `json:"options"`
	Start         string                        `json:"start"`
	End           string                        `json:"end"`
	State         string                        `json:"state"`
	Reason        string                        `json:"reason"`
	Keep          bool                          `json:"keep"`
	LibraryID     string                        `json:"libraryId"`
	ItemID        string                        `json:"itemId"`
	CoverageStart string                        `json:"coverageStart"`
	CoverageEnd   string                        `json:"coverageEnd"`
	Bytes         int64                         `json:"bytes"`
	Conflicts     []livechannels.LosingInterval `json:"conflicts"`
}

// RecordingChannel names a recording's channel for a list row.
type RecordingChannel struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Number string                    `json:"number"`
	Mark   *livechannels.ChannelMark `json:"mark,omitempty"`
}

// setSeriesGrouping derives the show-grouping key from the programme the
// recording was scheduled from. The series id is the stable key; the series
// title is the programme's title when a series is present.
func setSeriesGrouping(r *Recording) {
	if r.Programme.SeriesID == "" {
		r.SeriesID, r.SeriesTitle = "", ""
		return
	}
	r.SeriesID = r.Programme.SeriesID
	r.SeriesTitle = r.Programme.Title
}

// decorateTx adds what a recording row shows beyond its own table: the
// channel's name and the owner's watched state.
func decorateTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, r *Recording) error {
	if e := channelTx(ctx, tx, r); e != nil {
		return e
	}
	r.Overlaps = []Overlap{}
	var image artworkref.Descriptor
	if e := tx.QueryRowContext(ctx, `SELECT img.small_digest,img.small_width,img.small_height,img.small_bytes,img.large_digest,img.large_width,img.large_height,img.large_bytes FROM dvr_programme_images d JOIN live_programme_images img ON img.url=d.url WHERE d.recording_id=?`, r.ID).Scan(image.ScanTargets()...); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	} else if e == nil {
		r.Programme.Image = "/v1/dvr/recordings/" + r.ID + "/image?" + image.Slots()
	}
	r.Watched = nil
	if r.ItemID == "" {
		return nil
	}
	var watched bool
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(pi.watched,0),COALESCE(pg.position,0),COALESCE((SELECT CAST(max(a.duration)*1000 AS INTEGER) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=ce.id),0) FROM catalog_entities ce LEFT JOIN personal_items pi ON pi.item_id=ce.id AND pi.profile_id=? LEFT JOIN progress pg ON pg.item_id=ce.id AND pg.profile_id=? AND pg.unit=0 WHERE ce.public_id=pid_blob(?)`, persistence.PersonalOwnerKey(o.Authority, o.AccountID, o.ProfileID), persistence.PersonalOwnerKey(o.Authority, o.AccountID, o.ProfileID), r.ItemID).Scan(&watched, &r.PositionMs, &r.DurationMs)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	r.Watched = &watched
	return nil
}

// channelTx fills r.Channel: the source's current guide first, then the guide
// generation the recording was scheduled from.
func channelTx(ctx context.Context, tx *sql.Tx, r *Recording) error {
	r.Channel = RecordingChannel{ID: r.Occurrence.ChannelID}
	e := tx.QueryRowContext(ctx, `SELECT c.name,c.number FROM live_channel_versions c
 WHERE c.channel_id=? AND c.generation_id IN(COALESCE((SELECT active_generation FROM live_sources WHERE id=?),''),?)
 ORDER BY c.generation_id=COALESCE((SELECT active_generation FROM live_sources WHERE id=?),'') DESC LIMIT 1`,
		r.Occurrence.ChannelID, r.Occurrence.SourceID, r.Occurrence.Generation, r.Occurrence.SourceID).Scan(&r.Channel.Name, &r.Channel.Number)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	return e
}

func mutable(state string) bool {
	return state == "scheduled" || state == "conflicted" || state == "waiting-source" || state == "waiting-guide"
}
func capturing(state string) bool {
	return state == "preparing" || state == "recording" || state == "finalizing"
}
func terminal(state string) bool {
	return state == "completed" || state == "incomplete-playable" || state == "failed" || state == "cancelled" || state == "pending-delete" || state == "deleted"
}
func padded(p livechannels.Programme, o Options) (time.Time, time.Time, error) {
	a, e := time.Parse(time.RFC3339, p.Start)
	if e != nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	b, e := time.Parse(time.RFC3339, p.End)
	if e != nil || !b.After(a) || b.Sub(a) > 7*24*time.Hour {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	return a.Add(-time.Duration(o.BeforeMs) * time.Millisecond), b.Add(time.Duration(o.AfterMs) * time.Millisecond), nil
}

type Page struct {
	DeletionAvailable        bool        `json:"deletionAvailable"`
	Usage                    Usage       `json:"usage"`
	Recordings               []Recording `json:"recordings"`
	Rules                    []Rule      `json:"rules"`
	NextCursor               string      `json:"nextCursor"`
	Revision                 int64       `json:"revision"`
	CaptureAvailable         bool        `json:"captureAvailable"`
	CaptureUnavailableReason string      `json:"captureUnavailableReason"`
}

func (c RuleConfig) titleMatch() bool { return c.MatchKind == "title" }
func (c RuleConfig) identityMatches(p livechannels.Programme) bool {
	if c.titleMatch() {
		return p.ChannelID == c.ChannelID && strings.EqualFold(strings.TrimSpace(c.Title), strings.TrimSpace(p.Title))
	}
	return p.SeriesID != "" && p.SeriesID == c.SeriesID
}
func (c RuleConfig) identitySQL() (string, []any) {
	if c.titleMatch() {
		return `p.channel_id=? AND lower(trim(p.title))=lower(trim(?))`, []any{c.ChannelID, c.Title}
	}
	return `m.series_id=?`, []any{c.SeriesID}
}
