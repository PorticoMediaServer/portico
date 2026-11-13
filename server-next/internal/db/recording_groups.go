package db

import (
	"context"
	"database/sql"
	"time"
)

// RecordingReadScope is the same profile/Everyone choice and channel/rating access as the recordings read.
type RecordingReadScope struct {
	Viewer                        Viewer
	Live                          LiveViewer
	ProfilePub                    string
	Everyone, SeeOthers, AllRules bool
}

type RecordingGroupQuery struct {
	Sort        string
	AfterTitle  *string
	AfterLatest int64
	Limit       int
}

type RecordingGroup struct {
	Title                                   string
	Recordings, Unwatched, Kept, Exceptions int
	Latest                                  time.Time
	Recording                               Recording
}

type RecordingTotals struct{ Recorded, Unwatched, Groups, Rules int }

// recordingReadSQL mirrors maySeeRecording: own/also-requested bookings, the Recorded TV
// permission for others, copied guide ratings, and channel access. Personal channel hiding
// does not hide recordings. A channel that has gone remains visible only to its booker.
func recordingReadSQL(a access, scope RecordingReadScope) (string, string, []any) {
	owned := `(r.profile_id = @viewer_profile OR EXISTS (SELECT 1 FROM json_each(r.also_wanted_by) w WHERE w.value = @profile_public))`
	channel := `c.id IS NOT NULL AND ifnull(src.paused, 0) = 0`
	library := `json_array_length(lc.libraries) > 0 AND NOT EXISTS (SELECT 1 FROM json_each(lc.libraries) j
  WHERE NOT EXISTS (SELECT 1 FROM libraries l WHERE l.public_id = j.value AND ` + a.libraryVisible("l.id") + `))`
	age := `CASE WHEN json_valid(r.details) THEN CASE WHEN json_type(r.details, '$.ratingAge') = 'integer'
  THEN json_extract(r.details, '$.ratingAge') END END`
	where := `(@max_age IS NULL OR ifnull(` + age + `, -1) <= @max_age) AND (@owner OR ((` + owned + ` OR @see_others) AND
  ((` + channel + ` AND ((c.kind = 1 AND src.audience = 'everyone') OR (c.kind = 2 AND ` + library + `)))
   OR (NOT (` + channel + `) AND r.profile_id = @viewer_profile))))`
	if !scope.Everyone {
		where += ` AND ` + owned
	}
	from := `recordings r LEFT JOIN channels c ON c.id = r.channel_id AND c.state = 1 AND c.missing_since_at IS NULL
  LEFT JOIN live_sources src ON src.id = c.source_id LEFT JOIN library_channels lc ON lc.channel_id = c.id
  LEFT JOIN items i ON i.id = r.item_id AND i.available = 1
  LEFT JOIN user_item_state uis ON uis.profile_id = @viewer_profile AND uis.item_id = i.id`
	args := append(scope.Viewer.args(), sql.Named("profile_public", scope.ProfilePub), sql.Named("owner", scope.Live.Owner),
		sql.Named("see_others", scope.SeeOthers), sql.Named("max_age", scope.Live.MaxAge))
	return from, where, args
}

func recordingUnwatched(a access) string {
	return `ifnull(i.id IS NOT NULL AND (` + a.visible("i") + `) AND ifnull(uis.played, 0) = 0, 0)`
}

// RecordingGroups reads one page of titles, with counts and the actual newest recording,
// in one grouped statement. No recordings are fetched to build groups in application code.
func (d *DB) RecordingGroups(ctx context.Context, scope RecordingReadScope, q RecordingGroupQuery) ([]RecordingGroup, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, err := loadAccess(ctx, tx, scope.Viewer)
	if err != nil {
		return nil, err
	}
	from, where, args := recordingReadSQL(a, scope)
	order, after := `g.latest DESC, g.title`, `(g.latest < @after OR (g.latest = @after AND g.title > @title))`
	if q.Sort == "oldest" {
		order, after = `g.latest, g.title`, `(g.latest > @after OR (g.latest = @after AND g.title > @title))`
	}
	if q.Sort == "title" {
		order, after = `g.title`, `g.title > @title`
	}
	if q.AfterTitle == nil {
		after = "1"
	} else {
		args = append(args, sql.Named("title", *q.AfterTitle), sql.Named("after", q.AfterLatest))
	}
	args = append(args, sql.Named("limit", q.Limit))
	rows, err := tx.QueryContext(ctx, `WITH shown AS (
  SELECT r.id, r.title, r.starts_at, r.keep, r.state, (`+recordingUnwatched(a)+`) AS unwatched FROM `+from+`
  WHERE r.state IN ('recorded', 'partial') AND `+where+`
 ), ranked AS (SELECT *, row_number() OVER (PARTITION BY title ORDER BY starts_at DESC, id DESC) AS rank FROM shown),
 grouped AS (SELECT title, count(*) AS recordings, sum(unwatched) AS unwatched, sum(keep) AS kept,
  sum(state = 'partial') AS exceptions, max(starts_at) AS latest, max(CASE WHEN rank = 1 THEN id END) AS latest_id
  FROM ranked GROUP BY title)
 SELECT g.title, g.recordings, g.unwatched, g.kept, g.exceptions, g.latest, `+recordingColumns+`
 FROM grouped g JOIN recordings r ON r.id = g.latest_id WHERE `+after+` ORDER BY `+order+` LIMIT @limit`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecordingGroup{}
	for rows.Next() {
		var g RecordingGroup
		var latest int64
		g.Recording, err = scanRecordingWithPrefix(rows, &g.Title, &g.Recordings, &g.Unwatched, &g.Kept, &g.Exceptions, &latest)
		if err != nil {
			return nil, err
		}
		g.Latest = time.UnixMilli(latest).UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

// RecordingOverviewTotals counts the same scoped rows as the group read, and the rules
// this viewer's rules read offers, without walking either list.
func (d *DB) RecordingOverviewTotals(ctx context.Context, scope RecordingReadScope) (RecordingTotals, error) {
	var out RecordingTotals
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, err := loadAccess(ctx, tx, scope.Viewer)
	if err != nil {
		return out, err
	}
	from, where, args := recordingReadSQL(a, scope)
	args = append(args, sql.Named("can_record", scope.Live.Record), sql.Named("all_rules", scope.AllRules))
	err = tx.QueryRowContext(ctx, `SELECT count(*), ifnull(sum(`+recordingUnwatched(a)+`),0), count(DISTINCT r.title),
  CASE WHEN @can_record THEN (SELECT count(*) FROM recording_rules rr WHERE @all_rules OR rr.profile_id = @viewer_profile) ELSE 0 END
  FROM `+from+` WHERE r.state IN ('recorded','partial') AND `+where, args...).Scan(&out.Recorded, &out.Unwatched, &out.Groups, &out.Rules)
	return out, err
}
