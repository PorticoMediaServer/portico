package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// AdminSession is one active playback on the server (spec §14, Now Playing):
// who, on what device, what, how it's delivered and from where.
type AdminSession struct {
	App                string         `json:"app,omitempty"`
	Platform           string         `json:"platform,omitempty"`
	Address            string         `json:"address,omitempty"`
	CurrentBitrateKbps *float64       `json:"currentBitrateKbps"`
	RateWindowMs       int            `json:"rateWindowMs"`
	Live               bool           `json:"live"`
	Media              *AdminMedia    `json:"media,omitempty"`
	Delivery           *AdminDelivery `json:"delivery,omitempty"`
	ID                 string         `json:"id"`
	Protocol           string         `json:"protocol"` // "v1"
	User               AdminName      `json:"user"`
	Profile            AdminName      `json:"profile"`
	Device             AdminName      `json:"device"`
	Item               *ActivityItem  `json:"item,omitempty"`
	Kind               string         `json:"kind"` // "vod" | "audio" | "channel"
	State              string         `json:"state"`
	PositionMs         int64          `json:"positionMs"`
	// DurationMs is the length of the version being played; absent for a
	// channel or when the files do not say.
	DurationMs    int64     `json:"durationMs,omitempty"`
	Decision      Decisions `json:"decision"`
	BitrateKbps   int       `json:"bitrateKbps,omitempty"`
	BandwidthKbps int64     `json:"bandwidthKbps,omitempty"`
	// Engine is the player engine the device is playing with (native or mpv);
	// Quality is the quality in force. Both are absent for a channel.
	Engine  string   `json:"engine,omitempty"`
	Quality *Quality `json:"quality,omitempty"`
	// VideoHeight is the committed converted height, absent when unknown.
	VideoHeight    int                    `json:"videoHeight,omitempty"`
	Problem        string                 `json:"problem,omitempty"`
	BufferingSince string                 `json:"bufferingSince,omitempty"`
	Channel        *ActivityChannel       `json:"channel,omitempty"`
	Programme      *ActivityProgramme     `json:"programme,omitempty"`
	Transcode      *ConversionObservation `json:"transcode,omitempty"`
	Location       string                 `json:"location"`
	StartedAt      string                 `json:"startedAt"`
}
type AdminName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type AdminItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type AdminSessionPage struct {
	Scope      playback.ActivityScope `json:"scope"`
	ObservedAt string                 `json:"observedAt"`
	Items      []AdminSession         `json:"items"`
	Page       Page                   `json:"page"`
	Totals     SessionTotals          `json:"totals"`
}

// AdminSessionsMax is the most sessions one page lists.
const AdminSessionsMax = 200

// adminActiveV1 says what "active" means: a v1 session not ended whose lease
// holds. It is served by a partial index of only its active rows
// (migration 0066).
const adminActiveV1 = `v.ended_ms=0 AND v.lease_expires_ms>?`

// The branch yields the same columns: protocol, id, created, user id and
// name, profile id and name, device id and name, item id and title, kind,
// state, position, presentation, bandwidth, location, catalogue item and version.
// Both reads use this exact qualification, before a page receives catalogue data.
const adminV1Projection = `SELECT 'v1' AS protocol,v.id AS id,v.created_ms AS created,v.account_id,COALESCE(a.username,''),v.profile_id,COALESCE(p.name,''),v.device_id,COALESCE(d.name,''),
 CASE WHEN v.kind IN ('live','channel') THEN v.channel_id ELSE COALESCE(pid(i.public_id),'') END,CASE WHEN v.kind IN ('live','channel') THEN COALESCE(json_extract(cs.selection_json,'$.name'),'') ELSE COALESCE(i.title,'') END,
 CASE WHEN v.kind IN ('live','channel') THEN 'channel' ELSE v.kind END,v.state,CASE WHEN v.kind IN ('live','channel') THEN COALESCE(cs.confirmed_position_us/1000,0) ELSE v.position_ms END,v.presentation,v.bandwidth_kbps,v.location,CASE WHEN v.kind IN ('live','channel') THEN 0 ELSE v.item_id END,v.version_id,
 (` + activityProblemCode + `),conversion.speed,conversion.observed_ms,v.buffering_since_ms,COALESCE(cs.selection_json,'')`

const adminV1Joins = ` LEFT JOIN playback_channel_sessions cs ON cs.session_id=v.id
 LEFT JOIN account_portico_links hosted ON v.authority='hosted' AND hosted.hosted_account_id=v.account_id
 LEFT JOIN accounts a ON a.id=CASE WHEN v.authority='local' THEN v.account_id WHEN v.authority='hosted' THEN hosted.account_id END
 LEFT JOIN direct_profiles p ON p.id=v.profile_id AND p.account_id=a.id AND p.deleted=0
 LEFT JOIN identity_devices d ON d.id=v.device_id AND d.account_id=COALESCE(a.id,v.account_id) AND d.authority=v.authority
 LEFT JOIN catalog_entities i ON i.id=v.item_id`

const adminRanked = `WITH clock AS (SELECT ? now), ranked AS MATERIALIZED (
 SELECT v.id,v.created_ms,` + activityPriority + ` priority FROM playback_v1_sessions v INDEXED BY playback_v1_sessions_active CROSS JOIN clock` + conversionJoins + `
 WHERE v.ended_ms=0 AND v.lease_expires_ms>clock.now
 ) `

type SessionTotals struct {
	Streams       int64 `json:"streams"`
	Paused        int64 `json:"paused"`
	Converting    int64 `json:"converting"`
	BandwidthKbps int64 `json:"bandwidthKbps"`
	People        int64 `json:"people"`
	AtHome        int64 `json:"atHome"`
	Away          int64 `json:"away"`
}

// AdminSessions lists the first ten by the same qualified order as Activity.
// Show all reads bounded pages; totals always describe the whole current set.
func (s *Service) AdminSessions(ctx context.Context, limit int, cursor string) (AdminSessionPage, error) {
	return s.activitySessionPage(ctx, limit, cursor, false)
}

// A request composing authorized channel marks with these session rows borrows
// its one read transaction. Standalone activity reads retain their normal bound.
func activityRead(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return tx, func() {}, nil
	}
	return dbwork.BeginRead(ctx, db)
}

func (s *Service) activitySessionPage(ctx context.Context, limit int, cursor string, popover bool) (AdminSessionPage, error) {
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > AdminSessionsMax {
		return AdminSessionPage{}, &FieldError{Path: "limit"}
	}
	afterPriority, afterCreated, afterID := -1, int64(0), ""
	if cursor != "" {
		parts := strings.SplitN(cursor, ":", 4)
		if len(parts) != 4 || parts[0] != "a" || parts[3] == "" || len(cursor) > 1024 {
			return AdminSessionPage{}, &FieldError{Path: "cursor"}
		}
		p, e := strconv.Atoi(parts[1])
		n, ne := strconv.ParseInt(parts[2], 10, 64)
		if e != nil || ne != nil || p < 0 || p > 2 || n < 0 {
			return AdminSessionPage{}, &FieldError{Path: "cursor"}
		}
		afterPriority, afterCreated, afterID = p, n, parts[3]
	}
	tx, done, err := activityRead(ctx, s.DB)
	if err != nil {
		return AdminSessionPage{}, err
	}
	defer done()
	now := s.now().UnixMilli()
	out := AdminSessionPage{ObservedAt: time.UnixMilli(now).UTC().Format(time.RFC3339Nano), Items: []AdminSession{}, Page: Page{Limit: limit}}
	err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(SUM(v.state='paused'),0),COALESCE(SUM(`+activityConverting+`),0),
 COALESCE(SUM(CASE WHEN v.bandwidth_kbps>0 THEN v.bandwidth_kbps WHEN json_valid(v.presentation) THEN max(0,COALESCE(json_extract(v.presentation,'$.bitrateKbps'),0)) ELSE 0 END),0),
 count(DISTINCT CASE WHEN v.authority='hosted' THEN COALESCE((SELECT 'local:'||account_id FROM account_portico_links WHERE hosted_account_id=v.account_id),'hosted:'||v.account_id) ELSE v.authority||':'||v.account_id END),COALESCE(SUM(v.location='local'),0),COALESCE(SUM(v.location='remote'),0)
 FROM playback_v1_sessions v INDEXED BY playback_v1_sessions_active WHERE `+adminActiveV1, now).Scan(&out.Totals.Streams, &out.Totals.Paused, &out.Totals.Converting, &out.Totals.BandwidthKbps, &out.Totals.People, &out.Totals.AtHome, &out.Totals.Away)
	if err != nil {
		return out, err
	}
	out.Page.Total = out.Totals.Streams
	rows, err := tx.QueryContext(ctx, adminRanked+`, chosen AS MATERIALIZED (
 SELECT * FROM ranked WHERE priority>? OR priority=? AND (created_ms<? OR created_ms=? AND id>?)
 ORDER BY priority,created_ms DESC,id LIMIT ?
 ) `+adminV1Projection+` FROM chosen CROSS JOIN playback_v1_sessions v ON v.id=chosen.id CROSS JOIN clock`+adminV1Joins+conversionJoins+`
 ORDER BY chosen.priority,chosen.created_ms DESC,chosen.id`, now, afterPriority, afterPriority, afterCreated, afterCreated, afterID, limit+1)
	if err != nil {
		return out, err
	}
	var lengths []durationKey
	var lastCreated int64
	var lastPriority int
	for rows.Next() {
		a, length, created, e := readAdminSession(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if len(out.Items) == limit {
			out.Page.NextCursor = "a:" + strconv.Itoa(lastPriority) + ":" + strconv.FormatInt(lastCreated, 10) + ":" + out.Items[len(out.Items)-1].ID
			break
		}
		lastCreated = created
		lastPriority = 2
		if a.Problem != "" {
			lastPriority = 0
		} else if convertingDecision(a.Decision) {
			lastPriority = 1
		}
		out.Items = append(out.Items, a)
		lengths = append(lengths, length)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	durations, err := durationsOf(ctx, tx, lengths)
	if err != nil {
		return out, err
	}
	for n := range out.Items {
		out.Items[n].DurationMs = durations[lengths[n]]
	}
	// Large lists are compact: neither artwork joins nor catalogue walks beyond
	// this page. Small-card and popover reads enrich only their bounded choices.
	if err = enrichActivityItems(ctx, tx, out.Items, true); err != nil {
		return out, err
	}
	if !popover {
		if err = enrichAdminFacts(ctx, tx, s, out.Items); err != nil {
			return out, err
		}
	}
	return out, nil
}
func convertingDecision(d Decisions) bool {
	return d.Video != nil && (d.Video.Action == "transcode" || d.Video.Action == "burn") || d.Audio != nil && d.Audio.Action == "transcode" || d.Subtitles != nil && d.Subtitles.Action == "burn"
}

// readAdminSession shares the committed display projection, not capability URLs.
// Missing canonical names stay absent in both Activity and Dashboard.
func readAdminSession(rows interface{ Scan(...any) error }) (AdminSession, durationKey, int64, error) {
	var a AdminSession
	var item, title, presentation, version string
	var created, entity int64
	var speed *float64
	var observed *int64
	var buffering int64
	var selection string
	if err := rows.Scan(&a.Protocol, &a.ID, &created, &a.User.ID, &a.User.Name, &a.Profile.ID, &a.Profile.Name, &a.Device.ID, &a.Device.Name, &item, &title, &a.Kind, &a.State, &a.PositionMs, &presentation, &a.BandwidthKbps, &a.Location, &entity, &version, &a.Problem, &speed, &observed, &buffering, &selection); err != nil {
		return a, durationKey{}, 0, err
	}
	if item != "" {
		a.Item = &ActivityItem{ID: item, Title: title, Kind: a.Kind, Art: []ActivityArt{}}
	}
	var p struct {
		Decision    Decisions `json:"decision"`
		BitrateKbps int       `json:"bitrateKbps"`
		Engine      string    `json:"engine"`
		Quality     *Quality  `json:"quality"`
		VideoHeight int       `json:"videoHeight"`
	}
	if presentation != "" && json.Unmarshal([]byte(presentation), &p) == nil {
		a.Decision, a.BitrateKbps = p.Decision, p.BitrateKbps
		a.Engine, a.Quality, a.VideoHeight = p.Engine, p.Quality, p.VideoHeight
	}
	if speed != nil && observed != nil && convertingDecision(a.Decision) {
		a.Transcode = &ConversionObservation{Speed: *speed, ObservedAt: time.UnixMilli(*observed).UTC().Format(time.RFC3339Nano), CadenceMs: 1000}
	}
	if buffering > 0 && a.State == "buffering" {
		a.BufferingSince = time.UnixMilli(buffering).UTC().Format(time.RFC3339Nano)
	}
	if a.Kind == "channel" && selection != "" {
		var selected struct {
			Name   string `json:"name"`
			Number string `json:"number"`
			Title  string `json:"title"`
			End    int64  `json:"entryEndMs"`
		}
		if json.Unmarshal([]byte(selection), &selected) == nil {
			if selected.Name != "" {
				a.Channel = &ActivityChannel{Name: selected.Name, Number: selected.Number}
			}
			if selected.Title != "" && selected.End > 0 {
				a.Programme = &ActivityProgramme{Title: selected.Title, EndsAt: time.UnixMilli(selected.End).UTC().Format(time.RFC3339Nano)}
			}
		}
	}
	a.Live = a.Kind == "channel" || a.Kind == "live"
	if a.Live {
		a.PositionMs = 0
	}
	a.RateWindowMs = 5000
	a.StartedAt = time.UnixMilli(created).UTC().Format(time.RFC3339Nano)
	return a, durationKey{entity, version}, created, nil
}

// AdminTerminateMessageMax bounds what an administrator can show the viewer.
const AdminTerminateMessageMax = 500

// Terminate ends a v1 session for an administrator (spec §14). The session's
// device gets session.updated with reason "terminated" and the message. Ending
// one that's already over is not an error; an unknown id is not found.
func (s *Service) Terminate(ctx context.Context, id, message string) error {
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > AdminTerminateMessageMax || strings.ContainsAny(message, "\x00") {
		return &FieldError{Path: "message"}
	}
	if _, err := loadRow(ctx, s.DB, id); err == nil {
		return s.End(ctx, id, "terminated", message)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return ErrNotFound
}
