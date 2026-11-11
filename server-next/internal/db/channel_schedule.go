package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChannelCondition is one test of a Library Channel's selection, or a group of tests when
// Conditions is set. The fields and operators are those of a library's Filters, without
// anything personal, because a channel is shared:
//
//	year            is, between, before, after   whole years
//	decade          in                           1980, 1990, ...
//	genre, tag, studio, network, country, mood   any, all     names
//	contentRating   in                           "PG-13", ...
//	communityRating atLeast, atMost, between     0 to 10
//	length          atLeast, atMost, between     minutes; a show has no length, so it is not tested
//	actor, director, writer, collection          any          public ids
//	resolution      in                           sd, 720p, 1080p, 4k
//
// A condition this server does not know is kept in the document and not tested.
type ChannelCondition struct {
	Field      string             `json:"field,omitempty"`
	Op         string             `json:"op,omitempty"`
	Values     []json.RawMessage  `json:"values,omitempty"`
	Exclude    bool               `json:"exclude,omitempty"`
	Match      string             `json:"match,omitempty"`
	Conditions []ChannelCondition `json:"conditions,omitempty"`
}

// Known says whether this server can test the condition.
func (c ChannelCondition) Known() bool {
	if c.Conditions != nil || c.Match != "" {
		for _, inner := range c.Conditions {
			if !inner.Known() {
				return false
			}
		}
		return true
	}
	ops, ok := conditionOps[c.Field]
	return ok && strings.Contains(" "+ops+" ", " "+c.Op+" ")
}

var conditionOps = map[string]string{
	"year": "is between before after", "decade": "in", "contentRating": "in", "communityRating": "atLeast atMost between",
	"length": "atLeast atMost between", "resolution": "in",
	"genre": "any all", "tag": "any all", "studio": "any all", "network": "any all", "country": "any all", "mood": "any all",
	"actor": "any", "director": "any", "writer": "any", "collection": "any",
}

var conditionTagKinds = map[string]string{"genre": TagGenre, "tag": TagLabel, "studio": TagStudio, "network": TagNetwork,
	"country": TagCountry, "mood": TagMood}

var conditionRoles = map[string]CreditRole{"actor": RoleActor, "director": RoleDirector, "writer": RoleWriter}

// ChannelSelection is what one selection of a Library Channel draws from, ready to read.
type ChannelSelection struct {
	Libraries  []string // public ids
	Shows      bool     // shows rather than films
	Films      bool
	Match      string // all or any
	Conditions []ChannelCondition
	// AddedSince, when not zero, keeps only titles added since then.
	AddedSince time.Time
	Always     []string // public ids, played whatever the conditions say
	OnlyAlways bool     // the Always titles and nothing else
	Never      []string // public ids, never played; Never wins
	// Text asks for each title's summary, to test words against.
	Text bool
	// Plays asks for how often each title has been played on this server.
	Plays bool
}

// ChannelCandidate is one title a selection could play.
type ChannelCandidate struct {
	ID         int64
	PublicID   string
	Show       bool
	DurationMS int64
	Title      string
	Summary    string
	SortTitle  string
	Year       int
	Release    string
	AddedAt    int64
	Rating     float64
	Plays      int64
}

// ChannelCandidates calls fn with every title the selection matches, in no order. It reads
// the chosen libraries' films or shows once, so its cost follows the size of those libraries:
// it runs for the owner's preview and for the background job that builds a schedule, never
// for a viewer's read.
func (d *DB) ChannelCandidates(ctx context.Context, sel ChannelSelection, fn func(ChannelCandidate) error) error {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := newQuery(Viewer{})
	if err := sel.where(ctx, tx, q); err != nil {
		if errors.Is(err, errMatchesNothing) {
			return nil
		}
		return err
	}
	summary, plays := "''", "0"
	if sel.Text {
		summary = "ifnull(i.summary, '')"
	}
	if sel.Plays {
		plays = "(SELECT count(*) FROM play_history h WHERE h.item_id = i.id)"
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id, i.public_id, i.kind, ifnull(i.duration_ms, 0), i.title, `+summary+`, i.sort_title,
		ifnull(i.year, 0), ifnull(i.release_date, ''), i.added_at, ifnull(i.community_rating, 0), `+plays+`
		FROM items i WHERE `+q.sql.String(), q.args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c ChannelCandidate
		var kind ItemKind
		if err := rows.Scan(&c.ID, &c.PublicID, &kind, &c.DurationMS, &c.Title, &c.Summary, &c.SortTitle, &c.Year, &c.Release,
			&c.AddedAt, &c.Rating, &c.Plays); err != nil {
			return err
		}
		c.Show = kind == KindShow
		if err := fn(c); err != nil {
			return err
		}
	}
	return rows.Err()
}

var errMatchesNothing = errors.New("matches nothing")

func (sel ChannelSelection) where(ctx context.Context, tx *sql.Tx, q *query) error {
	var libraries []string
	for _, id := range sel.Libraries {
		libraries = append(libraries, q.arg(id))
	}
	var kinds []string
	if sel.Films {
		kinds = append(kinds, fmt.Sprint(int(KindMovie)))
	}
	if sel.Shows {
		kinds = append(kinds, fmt.Sprint(int(KindShow)))
	}
	if len(libraries) == 0 || len(kinds) == 0 {
		return errMatchesNothing
	}
	q.add(`i.library_id IN (SELECT id FROM libraries WHERE public_id IN (`, strings.Join(libraries, ", "), `)) AND i.kind IN (`,
		strings.Join(kinds, ", "), `) AND i.available = 1`)
	if len(sel.Never) > 0 {
		q.add(` AND i.public_id NOT IN (`, q.list(sel.Never), `)`)
	}
	match := "1"
	if sel.OnlyAlways {
		match = "0"
	} else if len(sel.Conditions) > 0 {
		var err error
		if match, err = conditionGroup(ctx, tx, q, sel.Match, sel.Conditions); err != nil {
			return err
		}
	}
	if !sel.AddedSince.IsZero() && !sel.OnlyAlways {
		match = "(" + match + ") AND i.added_at >= " + q.arg(sel.AddedSince.UnixMilli())
	}
	if len(sel.Always) > 0 {
		match = "(" + match + ") OR i.public_id IN (" + q.list(sel.Always) + ")"
	}
	q.add(` AND (`, match, `)`)
	return nil
}

func (q *query) list(values []string) string {
	names := make([]string, len(values))
	for n, v := range values {
		names[n] = q.arg(v)
	}
	return strings.Join(names, ", ")
}

func conditionGroup(ctx context.Context, tx *sql.Tx, q *query, match string, conditions []ChannelCondition) (string, error) {
	joiner, neutral := " AND ", "1"
	if match == "any" {
		joiner, neutral = " OR ", "0"
	}
	var parts []string
	for _, c := range conditions {
		var part string
		var err error
		if c.Conditions != nil || c.Match != "" {
			part, err = conditionGroup(ctx, tx, q, c.Match, c.Conditions)
		} else {
			part, err = condition(ctx, tx, q, c, neutral)
		}
		if err != nil {
			return "", err
		}
		if part == "" {
			continue // a condition this server cannot test
		}
		if c.Exclude {
			part = "NOT (" + part + ")"
		}
		parts = append(parts, "("+part+")")
	}
	if len(parts) == 0 {
		return neutral, nil
	}
	return strings.Join(parts, joiner), nil
}

func numbers(values []json.RawMessage) ([]float64, error) {
	out := make([]float64, len(values))
	for n, raw := range values {
		if err := json.Unmarshal(raw, &out[n]); err != nil {
			return nil, invalid("a value of this condition must be a number")
		}
	}
	return out, nil
}

func texts(values []json.RawMessage) ([]string, error) {
	out := make([]string, len(values))
	for n, raw := range values {
		if err := json.Unmarshal(raw, &out[n]); err != nil {
			return nil, invalid("a value of this condition must be text")
		}
	}
	return out, nil
}

// range2 is the SQL for a number column against atLeast, atMost or between.
func range2(q *query, column, op string, values []float64, scale float64) (string, error) {
	need := map[string]int{"atLeast": 1, "atMost": 1, "between": 2, "is": 1, "before": 1, "after": 1}[op]
	if len(values) != need {
		return "", invalid("this condition takes %d value(s)", need)
	}
	switch op {
	case "atLeast":
		return column + " >= " + q.arg(values[0]*scale), nil
	case "atMost":
		return column + " <= " + q.arg(values[0]*scale), nil
	case "between":
		return column + " BETWEEN " + q.arg(min(values[0], values[1])*scale) + " AND " + q.arg(max(values[0], values[1])*scale), nil
	case "is":
		return column + " = " + q.arg(values[0]*scale), nil
	case "before":
		return column + " < " + q.arg(values[0]*scale), nil
	}
	return column + " > " + q.arg(values[0]*scale), nil
}

func condition(ctx context.Context, tx *sql.Tx, q *query, c ChannelCondition, neutral string) (string, error) {
	if !c.Known() {
		return "", nil
	}
	if len(c.Values) == 0 {
		return "", invalid("the condition on %s has no values", c.Field)
	}
	switch c.Field {
	case "year", "communityRating":
		values, err := numbers(c.Values)
		if err != nil {
			return "", err
		}
		column := "i.year"
		if c.Field == "communityRating" {
			column = "i.community_rating"
		}
		return range2(q, column, c.Op, values, 1)
	case "length":
		values, err := numbers(c.Values)
		if err != nil {
			return "", err
		}
		test, err := range2(q, "i.duration_ms", c.Op, values, 60000)
		if c.Exclude {
			// Exclude flips the test, and a show is never tested: keep it neutral either way.
			neutral = map[string]string{"1": "0", "0": "1"}[neutral]
		}
		return "CASE WHEN i.kind = " + fmt.Sprint(int(KindShow)) + " THEN " + neutral + " ELSE " + test + " END", err
	case "decade":
		values, err := numbers(c.Values)
		if err != nil {
			return "", err
		}
		var parts []string
		for _, v := range values {
			parts = append(parts, "i.year BETWEEN "+q.arg(int(v)/10*10)+" AND "+q.arg(int(v)/10*10+9))
		}
		return strings.Join(parts, " OR "), nil
	case "contentRating":
		values, err := texts(c.Values)
		if err != nil {
			return "", err
		}
		return "i.content_rating IN (" + q.list(values) + ")", nil
	case "resolution":
		values, err := texts(c.Values)
		if err != nil {
			return "", err
		}
		var classes []string
		for _, v := range values {
			class, ok := resolutionClasses[v]
			if !ok {
				return "", invalid("%q is not a resolution", v)
			}
			classes = append(classes, fmt.Sprint(class))
		}
		return "EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND m.width IS NOT NULL AND " + resolutionSQL + " IN (" +
			strings.Join(classes, ", ") + "))", nil
	}
	values, err := texts(c.Values)
	if err != nil {
		return "", err
	}
	var ids []int64
	var each func(id int64) string
	if kind, ok := conditionTagKinds[c.Field]; ok {
		for _, name := range values {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE kind = ? AND norm_name = ?`, kind, normTag(name)).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			ids = append(ids, id) // 0 is a name no title carries: it matches nothing
		}
		each = func(id int64) string {
			return fmt.Sprintf("EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = i.id AND t.tag_id = %d)", id)
		}
	} else if role, ok := conditionRoles[c.Field]; ok {
		for _, publicID := range values {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM people WHERE public_id = ?`, publicID).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			ids = append(ids, id)
		}
		each = func(id int64) string {
			return fmt.Sprintf("EXISTS (SELECT 1 FROM credits k WHERE k.person_id = %d AND k.role = %d AND k.item_id = i.id)", id, role)
		}
	} else { // collection
		for _, publicID := range values {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE public_id = ? AND kind = ?`, publicID, KindCollection).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			ids = append(ids, id)
		}
		each = func(id int64) string {
			return fmt.Sprintf("EXISTS (SELECT 1 FROM item_relations r WHERE r.item_id = %d AND r.kind = %d AND r.related_item_id = i.id)",
				id, RelationCollectionMember)
		}
	}
	parts := make([]string, len(ids))
	for n, id := range ids {
		parts[n] = each(id)
	}
	joiner := " OR "
	if c.Op == "all" {
		joiner = " AND "
	}
	return strings.Join(parts, joiner), nil
}

// ShowsRuntime adds up the length of the scheduled episodes of the shows named. It reads each
// show's own index range.
func (d *DB) ShowsRuntime(ctx context.Context, showIDs []int64) (int64, error) {
	var total int64
	for start := 0; start < len(showIDs); start += 200 {
		part := showIDs[start:min(start+200, len(showIDs))]
		var ms int64
		if err := d.read.QueryRowContext(ctx, `SELECT ifnull(sum(duration_ms), 0) FROM items WHERE root_id IN `+intList(part)+`
			AND kind = ? AND parent_index > 0 AND available = 1`, KindEpisode).Scan(&ms); err != nil {
			return 0, err
		}
		total += ms
	}
	return total, nil
}

// ScheduledProgrammes counts a channel's programmes from at on, through the schedule's key.
func (d *DB) ScheduledProgrammes(ctx context.Context, channelID int64, at time.Time) (int, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM channel_schedule WHERE channel_id = ? AND ends_at > ? AND starts_at > ?`,
		channelID, at.UnixMilli(), at.Add(-48*time.Hour).UnixMilli()).Scan(&n)
	return n, err
}

// Episode is one episode of a show, for a Library Channel.
type Episode struct {
	ID         int64
	Season     int
	Number     int
	DurationMS int64
}

// ShowEpisodes reads up to limit episodes of a show in order, after the one given (season and
// number 0 for the first). Specials (season 0) and episodes whose file is gone are left out.
// It reads the show's own index range, whatever the size of the library.
func (d *DB) ShowEpisodes(ctx context.Context, showID int64, afterSeason, afterNumber int, afterID int64, limit int) ([]Episode, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT id, ifnull(parent_index, 0), ifnull(index_number, 0), ifnull(duration_ms, 0) FROM items
		WHERE root_id = ? AND kind = ? AND parent_index > 0 AND available = 1
		AND (parent_index, ifnull(index_number, 0), id) > (?, ?, ?) ORDER BY parent_index, index_number, id LIMIT ?`,
		showID, KindEpisode, afterSeason, afterNumber, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := rows.Scan(&e.ID, &e.Season, &e.Number, &e.DurationMS); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ShowEpisodeCount counts a show's scheduled episodes, as ShowEpisodes reads them.
func (d *DB) ShowEpisodeCount(ctx context.Context, showID int64) (int, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE root_id = ? AND kind = ? AND parent_index > 0 AND available = 1`,
		showID, KindEpisode).Scan(&n)
	return n, err
}

// ShowEpisodeAt reads the show's episode at a place in its order, counting from 0.
func (d *DB) ShowEpisodeAt(ctx context.Context, showID int64, place int) (Episode, error) {
	var e Episode
	err := d.read.QueryRowContext(ctx, `SELECT id, ifnull(parent_index, 0), ifnull(index_number, 0), ifnull(duration_ms, 0) FROM items
		WHERE root_id = ? AND kind = ? AND parent_index > 0 AND available = 1 ORDER BY parent_index, index_number, id LIMIT 1 OFFSET ?`,
		showID, KindEpisode, place).Scan(&e.ID, &e.Season, &e.Number, &e.DurationMS)
	return e, notFound(err)
}

// ScheduleRow is one programme of a Library Channel. ItemID 0 is the slate.
type ScheduleRow struct {
	StartsAt, EndsAt time.Time
	ItemID           int64
}

// LibraryChannelSchedule is what the schedule job reads to top a channel up or build it again.
type LibraryChannelSchedule struct {
	ChannelID        int64
	PublicID         string
	On               bool
	TimeZone         string
	Builder          string
	State            string // the stored schedule_state document, or ""
	ScheduledThrough time.Time
	Revision         int64
	Rebuild          bool // the builder changed or Regenerate was asked for
}

// LibraryChannelsToSchedule lists the channels that are on and whose schedule ends before
// until or must be built again. It reads at most 1,000 small rows.
func (d *DB) LibraryChannelsToSchedule(ctx context.Context, until time.Time) ([]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT c.public_id FROM channels c JOIN library_channels l ON l.channel_id = c.id
		WHERE c.kind = ? AND c.state = ? AND (l.state = ? OR ifnull(l.scheduled_through_at, 0) < ?) ORDER BY c.number`,
		ChannelFromLibrary, ChannelInUse, LibraryChannelBuilding, until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LibraryChannelForSchedule reads what the schedule job needs for one channel.
func (d *DB) LibraryChannelForSchedule(ctx context.Context, publicID string) (LibraryChannelSchedule, error) {
	var s LibraryChannelSchedule
	var state, through int64
	var stored, word string
	err := d.read.QueryRowContext(ctx, `SELECT c.id, c.public_id, c.state, l.time_zone, l.builder, ifnull(l.schedule_state, ''),
		ifnull(l.scheduled_through_at, 0), l.revision, l.state FROM channels c JOIN library_channels l ON l.channel_id = c.id
		WHERE c.public_id = ?`, publicID).Scan(&s.ChannelID, &s.PublicID, &state, &s.TimeZone, &s.Builder, &stored, &through, &s.Revision, &word)
	if err != nil {
		return s, notFound(err)
	}
	s.On, s.State, s.Rebuild = state == ChannelInUse, stored, word == LibraryChannelBuilding
	if through > 0 {
		s.ScheduledThrough = time.UnixMilli(through)
	}
	return s, nil
}

// ChannelProgrammes reads a channel's programmes that end after from and start before to.
func (d *DB) ChannelProgrammes(ctx context.Context, channelID int64, from, to time.Time) ([]ScheduleRow, error) {
	// A programme is at most a day long, so the one that started before from is found in that day.
	rows, err := d.read.QueryContext(ctx, `SELECT starts_at, ends_at, ifnull(item_id, 0) FROM channel_schedule
		WHERE channel_id = ? AND starts_at > ? AND starts_at < ? AND ends_at > ? ORDER BY starts_at`,
		channelID, from.Add(-48*time.Hour).UnixMilli(), to.UnixMilli(), from.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduleRow
	for rows.Next() {
		var starts, ends int64
		var r ScheduleRow
		if err := rows.Scan(&starts, &ends, &r.ItemID); err != nil {
			return nil, err
		}
		r.StartsAt, r.EndsAt = time.UnixMilli(starts), time.UnixMilli(ends)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScheduleWrite is one channel's schedule saved by the job: the rows from From on are
// replaced by Rows, and State and Through describe where the schedule now ends.
type ScheduleWrite struct {
	ChannelID int64
	// Revision and ReadThrough are the channel's revision and scheduled end the job read; a
	// channel saved or scheduled since is left alone.
	Revision    int64
	ReadThrough time.Time
	From        time.Time
	Rows        []ScheduleRow
	State       string
	Through     time.Time
	Word        string // on_air or needs_attention
	Problem     string
	ForgetEnd   time.Time // rows that ended before this are removed
}

// ErrChannelChanged: the channel was saved or removed while its schedule was being built.
var ErrChannelChanged = errors.New("the channel changed while its schedule was built")

// SaveChannelSchedule writes one channel's schedule and where it got to in one transaction,
// as background work.
func (d *DB) SaveChannelSchedule(ctx context.Context, w ScheduleWrite) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		var revision, through int64
		err := tx.QueryRow(`SELECT revision, ifnull(scheduled_through_at, 0) FROM library_channels WHERE channel_id = ?`, w.ChannelID).
			Scan(&revision, &through)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChannelChanged
		}
		if err != nil {
			return err
		}
		read := int64(0)
		if !w.ReadThrough.IsZero() {
			read = w.ReadThrough.UnixMilli()
		}
		if revision != w.Revision || through != read {
			return ErrChannelChanged
		}
		if _, err := tx.Exec(`DELETE FROM channel_schedule WHERE channel_id = ? AND (starts_at >= ? OR ends_at < ?)`,
			w.ChannelID, w.From.UnixMilli(), w.ForgetEnd.UnixMilli()); err != nil {
			return err
		}
		insert, err := tx.Prepare(`INSERT INTO channel_schedule (channel_id, starts_at, ends_at, item_id) VALUES (?, ?, ?, nullif(?, 0))`)
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, r := range w.Rows {
			if _, err := insert.Exec(w.ChannelID, r.StartsAt.UnixMilli(), r.EndsAt.UnixMilli(), r.ItemID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE library_channels SET schedule_state = ?, scheduled_through_at = ?, state = ?, problem = nullif(?, '')
			WHERE channel_id = ?`, w.State, w.Through.UnixMilli(), w.Word, w.Problem, w.ChannelID)
		return err
	})
}

// ProgrammeTitle is what the guide copy and the preview show for a scheduled title.
type ProgrammeTitle struct {
	ID            int64
	PublicID      string
	Title         string
	EpisodeTitle  string
	Season        *int
	Episode       *int
	Summary       string
	ContentRating string
	RatingAge     *int
	Year          int
	ShowID        int64
}

// ProgrammeTitles reads the titles named, for a schedule's copy into the guide or a preview.
// It reads as many rows as it is given ids.
func (d *DB) ProgrammeTitles(ctx context.Context, ids []int64) (map[int64]ProgrammeTitle, error) {
	out := map[int64]ProgrammeTitle{}
	for start := 0; start < len(ids); start += 200 {
		part := ids[start:min(start+200, len(ids))]
		rows, err := d.read.QueryContext(ctx, `SELECT i.id, i.public_id, ifnull(s.title, i.title), CASE WHEN s.id IS NULL THEN '' ELSE i.title END,
			CASE WHEN s.id IS NULL THEN NULL ELSE i.parent_index END, CASE WHEN s.id IS NULL THEN NULL ELSE i.index_number END,
			ifnull(i.summary, ''), ifnull(i.content_rating, ''), i.rating_age, ifnull(i.year, 0), ifnull(s.id, 0)
			FROM items i LEFT JOIN items s ON s.id = i.root_id AND i.kind = `+fmt.Sprint(int(KindEpisode))+`
			WHERE i.id IN `+intList(part))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var t ProgrammeTitle
			if err := rows.Scan(&t.ID, &t.PublicID, &t.Title, &t.EpisodeTitle, &t.Season, &t.Episode, &t.Summary, &t.ContentRating,
				&t.RatingAge, &t.Year, &t.ShowID); err != nil {
				rows.Close()
				return nil, err
			}
			out[t.ID] = t
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
