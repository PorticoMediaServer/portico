package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"strconv"
	"strings"
	"time"
)

// PlaybackHistoryEntry is one finished or running Playback v1 session as an
// owner reads it. EndedAt and DurationMs are null while the session runs,
// because it has no end time yet and history must not invent one.
//
// DeliveryMode is the presentation's mode ("direct" or "stream"); the strategy
// says what the server did to the media: "original", "copy_remux",
// "audio_conversion" or "video_conversion" ("" for a channel). QualityMode is
// the quality the viewer asked for. State is the live session's state, or why
// it ended ("stopped", "transferred", "lease_expired", "terminated", ...).
type PlaybackHistoryEntry struct {
	ID              string           `json:"id"`
	Viewer          string           `json:"viewer"`
	Authority       string           `json:"authority"`
	AccountID       string           `json:"accountId"`
	ProfileID       string           `json:"profileId"`
	Title           string           `json:"title"`
	MediaKind       string           `json:"mediaKind"`
	LibraryName     string           `json:"libraryName"`
	ItemID          string           `json:"itemId"`
	StartedAt       apiwire.Instant  `json:"startedAt"`
	EndedAt         *apiwire.Instant `json:"endedAt"`
	DurationMs      *int64           `json:"durationMs"`
	DurationSeconds *int64           `json:"-"`
	DeliveryMode    string           `json:"deliveryMode"`
	DeliveryReason  string           `json:"deliveryStrategy"`
	QualityMode     string           `json:"qualityMode"`
	State           string           `json:"state"`
}

// PlaybackHistoryPeriods are the only accepted windows. Each is a bounded range
// over one index, so the query cost does not grow with library size.
var PlaybackHistoryPeriods = map[string]int64{"24h": 1, "7d": 7, "30d": 30}

// Channel sessions read their channel's name from the selection they tuned;
// everything else reads its item and library.
const historyColumns = `v.id,v.authority,v.account_id,v.profile_id,v.created_ms,v.ended_ms,v.state,v.end_reason,v.kind,
 CASE WHEN v.kind IN ('live','channel') THEN COALESCE(json_extract(cs.selection_json,'$.name'),'') ELSE COALESCE(i.title,'') END,
 CASE WHEN v.kind IN ('live','channel') THEN 'channel' ELSE COALESCE(k.name,'') END,
 COALESCE(lib.name,''),CASE WHEN v.kind IN ('live','channel') THEN v.channel_id ELSE COALESCE(pid(i.public_id),'') END,
 COALESCE(NULLIF(p.name,''),NULLIF(a.username,''),''),
 v.presentation,COALESCE(json_extract(v.request,'$.quality.mode'),'')`
const historyFrom = ` FROM playback_v1_sessions v INDEXED BY playback_v1_sessions_created
 LEFT JOIN playback_channel_sessions cs ON cs.session_id=v.id
 LEFT JOIN catalog_entities i ON i.id=v.item_id
 LEFT JOIN catalog_kinds k ON k.id=i.kind
 LEFT JOIN catalog_libraries cl ON cl.id=i.library_id
 LEFT JOIN libraries lib ON lib.id=cl.library_id
 LEFT JOIN accounts a ON v.authority='local' AND a.id=v.account_id
 LEFT JOIN direct_profiles p ON v.authority='local' AND p.id=v.profile_id AND p.account_id=v.account_id AND p.deleted=0`

// historyPresentation is what history reads of a stored presentation: never
// its URL, which is a capability.
type historyPresentation struct {
	Mode     string `json:"mode"`
	Decision struct {
		Video *struct {
			Action string `json:"action"`
		} `json:"video"`
		Audio *struct {
			Action string `json:"action"`
		} `json:"audio"`
	} `json:"decision"`
	AudioRender *struct {
		Mode string `json:"mode"`
	} `json:"audioRender"`
}

func deliveryOf(kind, raw string) (mode, strategy string) {
	var p historyPresentation
	if raw == "" || json.Unmarshal([]byte(raw), &p) != nil {
		return "", ""
	}
	mode = p.Mode
	switch {
	case kind == "live" || kind == "channel":
		return mode, ""
	case p.AudioRender != nil:
		if p.AudioRender.Mode == "converted" {
			return "stream", "audio_conversion"
		}
		return "direct", "original"
	case mode == "direct":
		return mode, "original"
	case p.Decision.Video != nil && p.Decision.Video.Action == "transcode":
		return mode, "video_conversion"
	case p.Decision.Audio != nil && p.Decision.Audio.Action == "transcode":
		return mode, "audio_conversion"
	}
	return mode, "copy_remux"
}

func scanHistory(rows *sql.Rows) (PlaybackHistoryEntry, error) {
	var v PlaybackHistoryEntry
	var ended int64
	var state, reason, kind, presentation string
	e := rows.Scan(&v.ID, &v.Authority, &v.AccountID, &v.ProfileID, &v.StartedAt, &ended, &state, &reason, &kind, &v.Title, &v.MediaKind, &v.LibraryName, &v.ItemID, &v.Viewer, &presentation, &v.QualityMode)
	if e != nil {
		return v, e
	}
	if v.Viewer == "" {
		v.Viewer = "Unavailable profile"

	}
	v.DeliveryMode, v.DeliveryReason = deliveryOf(kind, presentation)
	v.State = state
	if ended > 0 {
		v.State = reason
		if v.State == "" {
			v.State = "stopped"
		}
		if ended >= int64(v.StartedAt) {
			durationMs := ended - int64(v.StartedAt)
			seconds := durationMs / 1000
			instant := apiwire.Instant(ended)
			v.EndedAt, v.DurationSeconds, v.DurationMs = &instant, &seconds, &durationMs
		}
	}
	return v, nil
}

// PlaybackHistory pages one period, newest first, in one keyset order over
// (start time, id): paging cannot repeat or skip a row while playback starts,
// and a page costs its page. The cursor is "t:<start ms>:<id>".
func (s *Store) PlaybackHistory(ctx context.Context, auth Authorize, period, cursor string, limit int) (out Page[PlaybackHistoryEntry], e error) {
	out.Items = []PlaybackHistoryEntry{}
	window, ok := PlaybackHistoryPeriods[period]
	if !ok || limit < 1 || limit > 200 {
		return out, ErrInvalid
	}
	beforeMS, beforeID := int64(1<<62), ""
	if cursor != "" {
		at, id, ok := strings.Cut(strings.TrimPrefix(cursor, "t:"), ":")
		n, err := strconv.ParseInt(at, 10, 64)
		if !strings.HasPrefix(cursor, "t:") || !ok || err != nil || id == "" {
			return out, ErrInvalid
		}
		beforeMS, beforeID = n, id
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+historyColumns+historyFrom+` WHERE v.created_ms>=? AND (v.created_ms<? OR v.created_ms=? AND v.id<?) ORDER BY v.created_ms DESC,v.id DESC LIMIT ?`,
			s.now()-days(int(window)), beforeMS, beforeMS, beforeID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanHistory(rows)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, v)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = "t:" + strconv.FormatInt(int64(last.StartedAt), 10) + ":" + last.ID
		}
		return nil
	})
	return
}

// PlaybackHistoryExportLimit bounds a CSV export. An owner exporting a month of
// history gets a file, not an unbounded database scan.
const PlaybackHistoryExportLimit = 5000

// PlaybackHistoryCSV renders the same rows as a spreadsheet-ready export. Cells
// are quoted and any leading formula character is neutralised, so opening the
// file in a spreadsheet cannot execute a title.
func (s *Store) PlaybackHistoryCSV(ctx context.Context, auth Authorize, period string) (string, error) {
	var out strings.Builder
	out.WriteString("id,viewer,title,mediaKind,library,startedAt,endedAt,durationMs,deliveryMode,deliveryStrategy,qualityMode,state\n")
	cursor := ""
	for written := 0; written < PlaybackHistoryExportLimit; {
		page, e := s.PlaybackHistory(ctx, auth, period, cursor, 200)
		if e != nil {
			return "", e
		}
		for _, v := range page.Items {
			out.WriteString(strings.Join([]string{
				csvCell(v.ID), csvCell(v.Viewer), csvCell(v.Title), csvCell(v.MediaKind), csvCell(v.LibraryName),
				csvCell(time.UnixMilli(int64(v.StartedAt)).UTC().Format(time.RFC3339Nano)), csvCell(optionalInstant(v.EndedAt)), csvCell(optionalNumber(v.DurationMs)),
				csvCell(v.DeliveryMode), csvCell(v.DeliveryReason), csvCell(v.QualityMode), csvCell(v.State),
			}, ","))
			out.WriteString("\n")
			written++
			if written >= PlaybackHistoryExportLimit {
				break
			}
		}
		if page.NextCursor == "" || len(page.Items) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	return out.String(), nil
}

func optionalNumber(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
func csvCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		v = "'" + v
	}
	return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
}

func optionalInstant(value *apiwire.Instant) string {
	if value == nil {
		return ""
	}
	return time.UnixMilli(int64(*value)).UTC().Format(time.RFC3339Nano)
}
