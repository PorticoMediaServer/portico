package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Recording states.
const (
	RecordingScheduled = "scheduled"
	RecordingRecording = "recording"
	RecordingRecorded  = "recorded"
	RecordingPartial   = "partial"
	RecordingFailed    = "failed"
	RecordingCancelled = "cancelled"
	RecordingSkipped   = "skipped"
)

// Recording is one airing someone asked to record.
type Recording struct {
	ID           int64
	PublicID     string
	ChannelID    int64
	ChannelName  string
	RuleID       int64
	ProfileID    int64 // the booker; 0 once nobody is left
	BookedBy     string
	AlsoWantedBy []string // profile public ids, in the order they asked
	ItemID       int64
	ProgrammeKey string
	SeriesKey    string
	Title        string
	EpisodeTitle string
	Season       *int
	Episode      *int
	Details      string
	// Rating and RatingAge are the guide's rating of the programme, copied with its facts: who
	// may see a booking or a recording follows it, and the recorded title carries it.
	Rating    string
	RatingAge *int
	StartsAt  time.Time
	EndsAt    time.Time
	PadBefore time.Duration
	PadAfter  time.Duration
	Priority  int
	State     string
	Reason    string
	Choice    string
	InGuide   bool
	Keep      bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

const recordingColumns = `r.id, r.public_id, ifnull(r.channel_id, 0), r.channel_name, ifnull(r.rule_id, 0), ifnull(r.profile_id, 0), r.booked_by,
	ifnull(r.also_wanted_by, '[]'), ifnull(r.item_id, 0), r.programme_key, ifnull(r.series_key, ''), r.title, ifnull(r.episode_title, ''),
	r.season_number, r.episode_number, ifnull(r.details, ''), r.starts_at, r.ends_at, r.pad_before_ms, r.pad_after_ms, r.priority, r.state,
	ifnull(r.reason, ''), ifnull(r.choice, ''), r.in_guide, r.keep, r.created_at, r.updated_at`

func scanRecording(row interface{ Scan(...any) error }) (Recording, error) {
	var r Recording
	var also string
	var starts, ends, before, after, created, updated int64
	err := row.Scan(&r.ID, &r.PublicID, &r.ChannelID, &r.ChannelName, &r.RuleID, &r.ProfileID, &r.BookedBy, &also, &r.ItemID, &r.ProgrammeKey,
		&r.SeriesKey, &r.Title, &r.EpisodeTitle, &r.Season, &r.Episode, &r.Details, &starts, &ends, &before, &after, &r.Priority, &r.State,
		&r.Reason, &r.Choice, &r.InGuide, &r.Keep, &created, &updated)
	json.Unmarshal([]byte(also), &r.AlsoWantedBy)
	r.Details, r.Rating, r.RatingAge = readDetails(r.Details)
	r.StartsAt, r.EndsAt, r.CreatedAt, r.UpdatedAt = time.UnixMilli(starts), time.UnixMilli(ends), time.UnixMilli(created), time.UnixMilli(updated)
	r.PadBefore, r.PadAfter = time.Duration(before)*time.Millisecond, time.Duration(after)*time.Millisecond
	return r, err
}

// programmeDetails is what recordings.details holds: the programme's summary and the guide's
// rating, copied when it is booked.
type programmeDetails struct {
	Summary   string `json:"summary,omitempty"`
	Rating    string `json:"rating,omitempty"`
	RatingAge *int   `json:"ratingAge,omitempty"`
}

func writeDetails(summary, rating string, age *int) string {
	raw, _ := json.Marshal(programmeDetails{summary, rating, age})
	return string(raw)
}

func readDetails(stored string) (summary, rating string, age *int) {
	var d programmeDetails
	if json.Unmarshal([]byte(stored), &d) != nil {
		return stored, "", nil
	}
	return d.Summary, d.Rating, d.RatingAge
}

// Booking is one airing a profile asks to record, with the programme's facts copied.
type Booking struct {
	ProfileID    int64
	ProfileName  string
	ProfilePub   string
	RuleID       int64
	ChannelID    int64
	ChannelName  string
	ProgrammeKey string
	SeriesKey    string
	Title        string
	EpisodeTitle string
	Season       *int
	Episode      *int
	Details      string
	Rating       string
	RatingAge    *int
	StartsAt     time.Time
	EndsAt       time.Time
	PadBefore    time.Duration
	PadAfter     time.Duration
	Priority     int
	Now          time.Time
}

// Book records an airing, once however many ask. An airing already booked by someone else
// answers that booking, with this profile added to the people who asked, so it is still
// recorded if the booker cancels. created is false for such an airing.
func (d *DB) Book(ctx context.Context, b Booking) (Recording, bool, error) {
	var publicID string
	created := false
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		r, err := scanRecording(tx.QueryRow(`SELECT `+recordingColumns+` FROM recordings r WHERE r.channel_id = ? AND r.programme_key = ?`,
			b.ChannelID, b.ProgrammeKey))
		switch {
		case err == nil && (r.State == RecordingScheduled || r.State == RecordingRecording):
			publicID = r.PublicID
			if r.ProfileID == b.ProfileID || slices.Contains(r.AlsoWantedBy, b.ProfilePub) {
				return nil
			}
			if r.ProfileID == 0 {
				_, err = tx.Exec(`UPDATE recordings SET profile_id = ?, booked_by = ?, updated_at = ? WHERE id = ?`,
					b.ProfileID, b.ProfileName, b.Now.UnixMilli(), r.ID)
				return err
			}
			also, _ := json.Marshal(append(r.AlsoWantedBy, b.ProfilePub))
			_, err = tx.Exec(`UPDATE recordings SET also_wanted_by = ?, updated_at = ? WHERE id = ?`, string(also), b.Now.UnixMilli(), r.ID)
			return err
		case err == nil && r.ItemID != 0:
			publicID = r.PublicID // recorded already (stopped by hand while still on): that recording answers
			return nil
		case err == nil:
			// A failed, cancelled or skipped row of the same airing is booked again.
			if _, err := tx.Exec(`DELETE FROM recordings WHERE id = ?`, r.ID); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		created, publicID = true, NewID("rec")
		now := b.Now.UnixMilli()
		_, err = tx.Exec(`INSERT INTO recordings (public_id, channel_id, rule_id, profile_id, booked_by, channel_name, programme_key, series_key,
			title, episode_title, season_number, episode_number, details, starts_at, ends_at, pad_before_ms, pad_after_ms, priority, created_at, updated_at)
			VALUES (?, ?, nullif(?, 0), ?, ?, ?, ?, nullif(?, ''), ?, nullif(?, ''), ?, ?, nullif(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
			publicID, b.ChannelID, b.RuleID, b.ProfileID, b.ProfileName, b.ChannelName, b.ProgrammeKey, b.SeriesKey, b.Title, b.EpisodeTitle,
			b.Season, b.Episode, writeDetails(b.Details, b.Rating, b.RatingAge), b.StartsAt.UnixMilli(), b.EndsAt.UnixMilli(), b.PadBefore.Milliseconds(), b.PadAfter.Milliseconds(),
			b.Priority, now, now)
		return err
	})
	if err != nil {
		return Recording{}, false, err
	}
	r, err := d.Recording(ctx, publicID)
	return r, created, err
}

// Recording reads one recording.
func (d *DB) Recording(ctx context.Context, publicID string) (Recording, error) {
	r, err := scanRecording(d.read.QueryRowContext(ctx, `SELECT `+recordingColumns+` FROM recordings r WHERE r.public_id = ?`, publicID))
	return r, notFound(err)
}

// RecordingQuery is a page of recordings: upcoming (by start) or history (newest first),
// the profile's own or everyone's.
type RecordingQuery struct {
	Upcoming   bool
	ProfileID  int64 // 0 for everyone's
	ProfilePub string
	After      int64 // the start (upcoming) or update time (history) the page goes on from
	AfterID    int64
	Limit      int
}

// Recordings reads a page. Upcoming reads recordings_upcoming in start order; history reads
// recordings_finished. A profile's own are the ones it booked or also asked for.
func (d *DB) Recordings(ctx context.Context, q RecordingQuery) ([]Recording, error) {
	where, order := `r.state IN ('scheduled', 'recording') AND (r.starts_at, r.id) > (?, ?)`, `r.starts_at, r.id`
	if !q.Upcoming {
		where, order = `r.state IN ('failed', 'cancelled', 'skipped', 'partial', 'recorded') AND (r.updated_at, r.id) < (?, ?)`, `r.updated_at DESC, r.id DESC`
		if q.After == 0 {
			q.After, q.AfterID = 1<<62, 1<<62
		}
	}
	args := []any{q.After, q.AfterID}
	if q.ProfileID != 0 {
		where += ` AND (r.profile_id = ? OR EXISTS (SELECT 1 FROM json_each(r.also_wanted_by) w WHERE w.value = ?))`
		args = append(args, q.ProfileID, q.ProfilePub)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT `+recordingColumns+` FROM recordings r WHERE `+where+` ORDER BY `+order+` LIMIT ?`,
		append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Upcoming lists the bookings that start before until, in start order, for the recorder
// and for working out clashes. It reads one range of recordings_upcoming.
func (d *DB) Upcoming(ctx context.Context, until time.Time) ([]Recording, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+recordingColumns+` FROM recordings r WHERE r.state IN ('scheduled', 'recording')
		AND r.starts_at < ? ORDER BY r.starts_at, r.id`, until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// passOn gives a booking to the next profile that asked for it, or cancels it when nobody is
// left. Every way a booker lets go of a booking (a cancel, a deleted profile or account) goes
// through it, in the transaction that lets go.
func passOn(tx *sql.Tx, r Recording, now time.Time) error {
	for len(r.AlsoWantedBy) > 0 {
		next := r.AlsoWantedBy[0]
		r.AlsoWantedBy = r.AlsoWantedBy[1:]
		var id int64
		var name string
		err := tx.QueryRow(`SELECT id, name FROM profiles WHERE public_id = ?`, next).Scan(&id, &name)
		if errors.Is(err, sql.ErrNoRows) {
			continue // a profile that has gone since it asked
		}
		if err != nil {
			return err
		}
		also, _ := json.Marshal(r.AlsoWantedBy)
		_, err = tx.Exec(`UPDATE recordings SET profile_id = ?, booked_by = ?, also_wanted_by = ?, updated_at = ? WHERE id = ?`,
			id, name, string(also), now.UnixMilli(), r.ID)
		return err
	}
	if r.State == RecordingRecording {
		// Being recorded already: what is captured is kept, for the library's sake.
		_, err := tx.Exec(`UPDATE recordings SET profile_id = NULL, also_wanted_by = NULL, updated_at = ? WHERE id = ?`, now.UnixMilli(), r.ID)
		return err
	}
	_, err := tx.Exec(`UPDATE recordings SET profile_id = NULL, also_wanted_by = NULL, state = ?, reason = 'Cancelled', updated_at = ?
		WHERE id = ?`, RecordingCancelled, now.UnixMilli(), r.ID)
	return err
}

// letGoOfBookings passes on every upcoming booking a profile made, and takes it out of the
// bookings it also asked for, before the profile is deleted.
func letGoOfBookings(tx *sql.Tx, profileID int64, now time.Time) error {
	var public string
	if err := tx.QueryRow(`SELECT public_id FROM profiles WHERE id = ?`, profileID).Scan(&public); err != nil {
		return notFound(err)
	}
	rows, err := tx.Query(`SELECT `+recordingColumns+` FROM recordings r WHERE r.state IN ('scheduled', 'recording')
		AND (r.profile_id = ? OR EXISTS (SELECT 1 FROM json_each(r.also_wanted_by) w WHERE w.value = ?))`, profileID, public)
	if err != nil {
		return err
	}
	var held []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			rows.Close()
			return err
		}
		held = append(held, r)
	}
	rows.Close()
	for _, r := range held {
		r.AlsoWantedBy = slices.DeleteFunc(r.AlsoWantedBy, func(p string) bool { return p == public })
		if r.ProfileID == profileID {
			if err := passOn(tx, r, now); err != nil {
				return err
			}
			continue
		}
		also, _ := json.Marshal(r.AlsoWantedBy)
		if _, err := tx.Exec(`UPDATE recordings SET also_wanted_by = ? WHERE id = ?`, string(also), r.ID); err != nil {
			return err
		}
	}
	return nil
}

// CancelRecording lets a profile go of an upcoming booking. The booker passes it on to the
// next who asked; someone else who asked is taken off it; everyone, for the owner or an
// administrator.
func (d *DB) CancelRecording(ctx context.Context, publicID string, profileID int64, profilePub string, everyone bool, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		r, err := scanRecording(tx.QueryRow(`SELECT `+recordingColumns+` FROM recordings r WHERE r.public_id = ?`, publicID))
		if err != nil {
			return notFound(err)
		}
		if r.State != RecordingScheduled {
			return nil
		}
		switch {
		case everyone:
			r.AlsoWantedBy = nil
			return passOn(tx, r, now)
		case r.ProfileID == profileID:
			return passOn(tx, r, now)
		}
		also, _ := json.Marshal(slices.DeleteFunc(r.AlsoWantedBy, func(p string) bool { return p == profilePub }))
		_, err = tx.Exec(`UPDATE recordings SET also_wanted_by = ?, updated_at = ? WHERE id = ?`, string(also), now.UnixMilli(), r.ID)
		return err
	})
}

// RecordingChange is a change to one booking or recording: only what is set changes.
type RecordingChange struct {
	PadBefore, PadAfter *time.Duration
	Priority            *int
	Keep                *bool
	Choice              *string // record or skip, the answer to a tuner clash
}

// ChangeRecording applies a change.
func (d *DB) ChangeRecording(ctx context.Context, publicID string, c RecordingChange, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		set := func(column string, value any) error {
			_, err := tx.Exec(`UPDATE recordings SET `+column+` = ?, updated_at = ? WHERE public_id = ?`, value, now.UnixMilli(), publicID)
			return err
		}
		for _, f := range []struct {
			ok     bool
			column string
			value  func() any
		}{
			{c.PadBefore != nil, "pad_before_ms", func() any { return c.PadBefore.Milliseconds() }},
			{c.PadAfter != nil, "pad_after_ms", func() any { return c.PadAfter.Milliseconds() }},
			{c.Priority != nil, "priority", func() any { return *c.Priority }},
			{c.Keep != nil, "keep", func() any { return *c.Keep }},
			{c.Choice != nil, "choice", func() any { return nullable(*c.Choice) }},
		} {
			if f.ok {
				if err := set(f.column, f.value()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RecordingStarted, RecordingProgress and RecordingFinished are the recorder's writes.
func (d *DB) RecordingStarted(ctx context.Context, id int64, reason string, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE recordings SET state = ?, reason = coalesce(nullif(?, ''), reason), updated_at = ? WHERE id = ?`,
			RecordingRecording, reason, now.UnixMilli(), id)
		return err
	})
}

// RecordingTitle links a recording to the title the scan made of its file.
func (d *DB) RecordingTitle(ctx context.Context, id, itemID int64) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE recordings SET item_id = ? WHERE id = ?`, itemID, id)
		return err
	})
}

// RecordingFinished ends a recording or a booking with its state and the reason.
func (d *DB) RecordingFinished(ctx context.Context, id int64, state, reason string, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE recordings SET state = ?, reason = coalesce(nullif(?, ''), reason), updated_at = ? WHERE id = ?`,
			state, reason, now.UnixMilli(), id)
		return err
	})
}

// RecordingNote keeps a reason on a recording without changing its state: started late, or
// interrupted by a restart, makes the result Partly recorded.
func (d *DB) RecordingNote(ctx context.Context, id int64, reason string, now time.Time) error {
	return d.RecordingStarted(ctx, id, reason, now)
}

// ForgetOldHistory removes failed, cancelled and skipped rows older than 30 days.
func (d *DB) ForgetOldHistory(ctx context.Context, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM recordings WHERE state IN ('failed', 'cancelled', 'skipped') AND updated_at < ?`,
			now.AddDate(0, 0, -30).UnixMilli())
		return err
	})
}

// DeleteRecording removes a recording's row; its file is removed by the caller.
func (d *DB) DeleteRecording(ctx context.Context, id int64) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM recordings WHERE id = ?`, id)
		return err
	})
}

// RecordingCounts are the Recordings header's numbers for a profile, or everyone's.
type RecordingCounts struct {
	Upcoming, Recorded, Unwatched int
}

// RecordingCounts counts the recorded titles the viewer may see, by the one condition every
// list uses, and of those the ones it has not watched; upcoming bookings are counted by the
// caller from the list it may see. It reads the recordings table, which is as large as the
// library of recordings: an accepted cost on the Recordings page.
func (d *DB) RecordingCounts(ctx context.Context, v Viewer) (RecordingCounts, error) {
	var c RecordingCounts
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return c, err
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*), ifnull(sum(NOT EXISTS (SELECT 1 FROM user_item_state s WHERE s.profile_id = @viewer_profile
		AND s.item_id = i.id AND s.played = 1)), 0) FROM recordings r JOIN items i ON i.id = r.item_id
		WHERE r.state IN ('recorded', 'partial') AND `+acc.visible("i"), v.args()...).Scan(&c.Recorded, &c.Unwatched)
	return c, err
}

// RecordedEpisodes says whether an episode of a series was already recorded, so a series
// rule never records one it has.
func (d *DB) RecordedEpisode(ctx context.Context, seriesKey string, season, episode int) (bool, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM recordings WHERE series_key = ? AND season_number = ? AND episode_number = ?
		AND state IN ('scheduled', 'recording', 'recorded', 'partial')`, seriesKey, season, episode).Scan(&n)
	return n > 0, err
}

// RecordingRule is a series rule.
type RecordingRule struct {
	ID, SourceID, ChannelID, ProfileID int64
	PublicID                           string
	SeriesKey, Title                   string
	NewOnly, Paused                    bool
	KeepNewest, KeepDays               *int
	Priority                           int
	PadBefore, PadAfter                *time.Duration
}

const ruleColumns = `id, public_id, profile_id, source_id, ifnull(channel_id, 0), ifnull(series_key, ''), title, new_only, keep_newest, keep_days,
	priority, pad_before_ms, pad_after_ms, paused`

func scanRule(row interface{ Scan(...any) error }) (RecordingRule, error) {
	var r RecordingRule
	var before, after *int64
	err := row.Scan(&r.ID, &r.PublicID, &r.ProfileID, &r.SourceID, &r.ChannelID, &r.SeriesKey, &r.Title, &r.NewOnly, &r.KeepNewest,
		&r.KeepDays, &r.Priority, &before, &after, &r.Paused)
	if before != nil {
		d := time.Duration(*before) * time.Millisecond
		r.PadBefore = &d
	}
	if after != nil {
		d := time.Duration(*after) * time.Millisecond
		r.PadAfter = &d
	}
	return r, err
}

func durationMS(d *time.Duration) any {
	if d == nil {
		return nil
	}
	return d.Milliseconds()
}

// SaveRule makes a rule (r.PublicID "") or replaces one. A profile's rule for the same series
// (or title) on the same channel choice is answered instead of a second one being made.
func (d *DB) SaveRule(ctx context.Context, r RecordingRule, now time.Time) (RecordingRule, error) {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if r.PublicID == "" {
			err := tx.QueryRow(`SELECT public_id FROM recording_rules WHERE profile_id = ? AND source_id = ? AND ifnull(channel_id, 0) = ?
				AND ifnull(series_key, '') = ? AND title = ?`, r.ProfileID, r.SourceID, r.ChannelID, r.SeriesKey, r.Title).Scan(&r.PublicID)
			if err == nil || !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			r.PublicID = NewID("rul")
			_, err = tx.Exec(`INSERT INTO recording_rules (public_id, profile_id, source_id, channel_id, series_key, title, new_only, keep_newest,
				keep_days, priority, pad_before_ms, pad_after_ms, paused, created_at, updated_at) VALUES (?, ?, ?, nullif(?, 0), nullif(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.PublicID, r.ProfileID, r.SourceID, r.ChannelID, r.SeriesKey, r.Title, r.NewOnly, r.KeepNewest, r.KeepDays, r.Priority,
				durationMS(r.PadBefore), durationMS(r.PadAfter), r.Paused, now.UnixMilli(), now.UnixMilli())
			return err
		}
		res, err := tx.Exec(`UPDATE recording_rules SET channel_id = nullif(?, 0), series_key = nullif(?, ''), title = ?, new_only = ?, keep_newest = ?,
			keep_days = ?, priority = ?, pad_before_ms = ?, pad_after_ms = ?, paused = ?, updated_at = ? WHERE public_id = ?`,
			r.ChannelID, r.SeriesKey, r.Title, r.NewOnly, r.KeepNewest, r.KeepDays, r.Priority, durationMS(r.PadBefore), durationMS(r.PadAfter),
			r.Paused, now.UnixMilli(), r.PublicID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return RecordingRule{}, err
	}
	return d.Rule(ctx, r.PublicID)
}

// Rule reads one rule.
func (d *DB) Rule(ctx context.Context, publicID string) (RecordingRule, error) {
	r, err := scanRule(d.read.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM recording_rules WHERE public_id = ?`, publicID))
	return r, notFound(err)
}

// Rules lists a profile's rules, or everyone's with profileID 0. Tens of rows.
func (d *DB) Rules(ctx context.Context, profileID int64) ([]RecordingRule, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+ruleColumns+` FROM recording_rules WHERE ? = 0 OR profile_id = ? ORDER BY title, id`,
		profileID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingRule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRule removes a rule; its upcoming bookings are kept, or cancelled with cancel.
func (d *DB) DeleteRule(ctx context.Context, publicID string, cancel bool, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`SELECT id FROM recording_rules WHERE public_id = ?`, publicID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if cancel {
			// Each booking the rule made passes to the next person who also asked for it, as a
			// cancel by its booker does, and is cancelled only when nobody is left.
			rows, err := tx.Query(`SELECT `+recordingColumns+` FROM recordings r WHERE r.rule_id = ? AND r.state = ?`, id, RecordingScheduled)
			if err != nil {
				return err
			}
			var held []Recording
			for rows.Next() {
				r, err := scanRecording(rows)
				if err != nil {
					rows.Close()
					return err
				}
				held = append(held, r)
			}
			rows.Close()
			for _, r := range held {
				if err := passOn(tx, r, now); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(`DELETE FROM recording_rules WHERE id = ?`, id)
		return err
	})
}

// GuideAirings reads up to limit of a series' or a title's airings in the guide from from to
// until: by the guide's series id through its index, or by title through the title index.
func (d *DB) GuideAirings(ctx context.Context, seriesKey, title string, from, until time.Time, limit int) ([]GuideProgramme, error) {
	column, value := "series_key", seriesKey
	if seriesKey == "" {
		column, value = "title", title
	}
	rows, err := d.guide.read.QueryContext(ctx, `SELECT source_id, guide_key, starts_at, ends_at, programme_key, ifnull(series_key, ''), title,
		ifnull(episode_title, ''), season_number, episode_number, ifnull(summary, ''), ifnull(rating, ''), rating_age, ifnull(flags, 0)
		FROM programmes WHERE `+column+` = ? AND starts_at >= ? AND starts_at < ? AND source_id <> 0 ORDER BY starts_at LIMIT ?`,
		value, from.UnixMilli(), until.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuideProgramme
	for rows.Next() {
		var p GuideProgramme
		var starts, ends int64
		if err := rows.Scan(&p.SourceID, &p.GuideKey, &starts, &ends, &p.ProgrammeKey, &p.SeriesKey, &p.Title, &p.EpisodeTitle, &p.Season,
			&p.Episode, &p.Summary, &p.Rating, &p.RatingAge, &p.Flags); err != nil {
			return nil, err
		}
		p.StartsAt, p.EndsAt = time.UnixMilli(starts), time.UnixMilli(ends)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ChannelsForListings lists the channels in use whose listings are a guide key of a source,
// for booking a programme found in the guide.
func (d *DB) ChannelsForListings(ctx context.Context, guideSource int64, guideKey string) ([]LiveChannel, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT c.id, c.public_id, c.name, ifnull(c.source_id, 0) FROM channels c
		WHERE c.guide_source_id = ? AND c.guide_key = ? AND c.state = 1 AND c.missing_since_at IS NULL`, guideSource, guideKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveChannel
	for rows.Next() {
		var c LiveChannel
		if err := rows.Scan(&c.ID, &c.PublicID, &c.Name, &c.SourceID); err != nil {
			return nil, err
		}
		c.Kind = ChannelFromSource
		out = append(out, c)
	}
	return out, rows.Err()
}

// ItemForFile finds the title a library file plays, by the file's folder and name: how a
// recording finds the title the scan made of it.
func (d *DB) ItemForFile(ctx context.Context, libraryID int64, dir, name string) (int64, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT m.item_id FROM files f JOIN directories dr ON dr.id = f.directory_id
		JOIN library_paths p ON p.id = dr.library_path_id JOIN media_parts mp ON mp.file_id = f.id JOIN media m ON m.id = mp.media_id
		WHERE p.library_id = ? AND dr.rel_path = ? AND f.name = ? LIMIT 1`, libraryID, dir, name).Scan(&id)
	return id, notFound(err)
}

// ProfileName is a profile's public id and name, for saying who booked a recording.
type ProfileName struct{ PublicID, Name string }

// ProfileNames reads a profile's public id and name.
func (d *DB) ProfileNames(ctx context.Context, profileID int64) (ProfileName, error) {
	var p ProfileName
	err := d.read.QueryRowContext(ctx, `SELECT public_id, name FROM profiles WHERE id = ?`, profileID).Scan(&p.PublicID, &p.Name)
	return p, notFound(err)
}

// ChannelName reads a channel's name by its row id.
func (d *DB) ChannelName(ctx context.Context, id int64) (string, error) {
	var name string
	err := d.read.QueryRowContext(ctx, `SELECT name FROM channels WHERE id = ?`, id).Scan(&name)
	return name, notFound(err)
}

// Reminder is "Remind me" on one upcoming airing.
type Reminder struct {
	ChannelID    int64
	ProgrammeKey string
	Title        string
	StartsAt     time.Time
	EndsAt       time.Time
}

// SetReminder keeps a reminder for a profile; setting one again changes nothing.
func (d *DB) SetReminder(ctx context.Context, profileID int64, r Reminder, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO reminders (profile_id, channel_id, programme_key, title, starts_at, ends_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (profile_id, channel_id, programme_key) DO UPDATE SET starts_at = excluded.starts_at,
			ends_at = excluded.ends_at, title = excluded.title`, profileID, r.ChannelID, r.ProgrammeKey, r.Title, r.StartsAt.UnixMilli(),
			r.EndsAt.UnixMilli(), now.UnixMilli())
		return err
	})
}

// DeleteReminder removes one; removing one that is gone succeeds.
func (d *DB) DeleteReminder(ctx context.Context, profileID, channelID int64, programmeKey string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM reminders WHERE profile_id = ? AND channel_id = ? AND programme_key = ?`, profileID, channelID, programmeKey)
		return err
	})
}

// Reminders lists a profile's reminders whose programme has not ended, soonest first. A
// profile keeps a few.
func (d *DB) Reminders(ctx context.Context, profileID int64, now time.Time) ([]Reminder, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT channel_id, programme_key, title, starts_at, ends_at FROM reminders
		WHERE profile_id = ? AND ends_at > ? ORDER BY starts_at LIMIT 500`, profileID, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var starts, ends int64
		if err := rows.Scan(&r.ChannelID, &r.ProgrammeKey, &r.Title, &starts, &ends); err != nil {
			return nil, err
		}
		r.StartsAt, r.EndsAt = time.UnixMilli(starts), time.UnixMilli(ends)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForgetEndedReminders removes reminders whose programme has ended.
func (d *DB) ForgetEndedReminders(ctx context.Context, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM reminders WHERE ends_at < ?`, now.UnixMilli())
		return err
	})
}

// HasReminder and AiringRecording answer a programme page's two questions.
func (d *DB) HasReminder(ctx context.Context, profileID, channelID int64, programmeKey string) (bool, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM reminders WHERE profile_id = ? AND channel_id = ? AND programme_key = ?`,
		profileID, channelID, programmeKey).Scan(&n)
	return n > 0, err
}

// AiringRecording is the booking or recording of an airing, if any.
func (d *DB) AiringRecording(ctx context.Context, channelID int64, programmeKey string) (Recording, error) {
	r, err := scanRecording(d.read.QueryRowContext(ctx, `SELECT `+recordingColumns+` FROM recordings r WHERE r.channel_id = ? AND r.programme_key = ?`,
		channelID, programmeKey))
	return r, notFound(err)
}

// ChannelPublicID reads a channel's public id by its row id.
func (d *DB) ChannelPublicID(ctx context.Context, id int64) (string, error) {
	var public string
	err := d.read.QueryRowContext(ctx, `SELECT public_id FROM channels WHERE id = ?`, id).Scan(&public)
	return public, notFound(err)
}

// RuleRecordings lists a rule's recorded titles that Keep does not protect, newest first: what
// its keep choice may delete. A rule has as many as it recorded.
func (d *DB) RuleRecordings(ctx context.Context, ruleID int64) ([]Recording, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+recordingColumns+` FROM recordings r WHERE r.rule_id = ? AND r.state IN ('recorded', 'partial')
		AND r.keep = 0 ORDER BY r.starts_at DESC`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChannelListings says which listings in the guide are a channel's.
func (d *DB) ChannelListings(ctx context.Context, channelID int64) (source int64, key string, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT ifnull(guide_source_id, 0), ifnull(guide_key, '') FROM channels WHERE id = ?`, channelID).
		Scan(&source, &key)
	return source, key, notFound(err)
}

// GuideProgrammeByKey finds where a booked programme of a channel's listings airs now. By the
// guide's own id when it gives one; in an ordinary guide, whose programmes carry none (their
// key is the channel and the start, so a moved programme has a new key), by the same title and
// episode details within three hours either side of the booked time: a schedule change moves
// a programme within its evening, and the window never reaches the same show's airing of the
// next day. An airing already over is never taken.
func (d *DB) GuideProgrammeByKey(ctx context.Context, source int64, guideKey, programmeKey string, booking Recording, now time.Time) (GuideProgramme, error) {
	title, booked := booking.Title, booking.StartsAt
	var p GuideProgramme
	var starts, ends int64
	// A guide may give one id to every airing of an episode: the airing nearest the booked
	// time, and not yet over, is the booking's.
	err := d.guide.read.QueryRowContext(ctx, `SELECT starts_at, ends_at, title, programme_key FROM programmes WHERE source_id = ? AND guide_key = ?
		AND programme_key = ? AND ends_at > ? ORDER BY abs(starts_at - ?) LIMIT 1`, source, guideKey, programmeKey, now.UnixMilli(),
		booked.UnixMilli()).Scan(&starts, &ends, &p.Title, &p.ProgrammeKey)
	if errors.Is(err, sql.ErrNoRows) && !strings.HasPrefix(programmeKey, "id:") {
		// The guide gives no ids: its key is the channel and the start, so a moved programme
		// has a new key. The one programme of the same title that overlaps the booked time is
		// it; none or several, and the booking stays unresolved.
		var n int
		err = d.guide.read.QueryRowContext(ctx, `SELECT count(*), ifnull(max(starts_at), 0), ifnull(max(ends_at), 0), ifnull(max(title), ''), ifnull(max(programme_key), '') FROM programmes
			WHERE source_id = ? AND guide_key = ? AND title = ? AND starts_at < ? AND ends_at > ? AND ends_at > ?
			AND (? = '' OR ifnull(episode_title, '') = ?) AND (? IS NULL OR season_number = ?) AND (? IS NULL OR episode_number = ?)`,
			source, guideKey, title, booked.Add(3*time.Hour).UnixMilli(), booked.Add(-3*time.Hour).UnixMilli(), now.UnixMilli(),
			booking.EpisodeTitle, booking.EpisodeTitle, booking.Season, booking.Season, booking.Episode, booking.Episode).
			Scan(&n, &starts, &ends, &p.Title, &p.ProgrammeKey)
		if err == nil && n != 1 {
			err = sql.ErrNoRows
		}
	}
	p.StartsAt, p.EndsAt = time.UnixMilli(starts), time.UnixMilli(ends)
	return p, notFound(err)
}

// MoveBooking follows a booking's programme where the guide has moved it, or says it is no
// longer in the guide (inGuide false), when nothing is recording it yet.
func (d *DB) MoveBooking(ctx context.Context, id int64, key string, starts, ends time.Time, inGuide bool, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE recordings SET programme_key = CASE WHEN ? THEN ? ELSE programme_key END,
			starts_at = CASE WHEN ? THEN ? ELSE starts_at END, ends_at = CASE WHEN ? THEN ? ELSE ends_at END,
			in_guide = ?, updated_at = ? WHERE id = ? AND state = ?`, inGuide, key, inGuide, starts.UnixMilli(), inGuide, ends.UnixMilli(), inGuide,
			now.UnixMilli(), id, RecordingScheduled)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			// Someone booked the moved airing already: that booking records it.
			_, err = tx.Exec(`UPDATE recordings SET in_guide = 0, updated_at = ? WHERE id = ? AND state = ?`, now.UnixMilli(), id, RecordingScheduled)
		}
		return err
	})
}

// RecordedBytes is how much the recordings in a library take, for the owner's space cap. It
// adds up the library's files, as many as there are recordings.
func (d *DB) RecordedBytes(ctx context.Context, libraryID int64) (int64, error) {
	var n int64
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(sum(f.size), 0) FROM files f JOIN directories dr ON dr.id = f.directory_id
		JOIN library_paths p ON p.id = dr.library_path_id WHERE p.library_id = ?`, libraryID).Scan(&n)
	return n, err
}

// KeepCandidates lists the recorded titles the owner's default keep rule decides on: not
// protected by Keep, and not from a series rule with its own keep choice, by show (the series
// or the title) and newest first.
func (d *DB) KeepCandidates(ctx context.Context) ([]Recording, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+recordingColumns+` FROM recordings r LEFT JOIN recording_rules u ON u.id = r.rule_id
		WHERE r.state IN ('recorded', 'partial') AND r.keep = 0 AND u.id IS NULL
		ORDER BY coalesce(r.series_key, r.title), r.starts_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
