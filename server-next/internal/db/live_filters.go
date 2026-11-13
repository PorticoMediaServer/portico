package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// LiveChannelsAround reads, in one statement, up to window channels on each side of one
// channel in the query's order (by number or name, with its filters): before holds those
// before it, nearest first, and after the channel itself, then those after it. Who may see
// each is decided by the caller, as for LiveChannels.
func (d *DB) LiveChannelsAround(ctx context.Context, v Viewer, q LiveChannelQuery, at LiveChannel, window int) (before, after []LiveChannel, err error) {
	asc, desc, lower, upper := "c.number, c.id", "c.number DESC, c.id DESC", "(c.number, c.id) < (@number, @id)", "(c.number, c.id) >= (@number, @id)"
	if q.ByName {
		asc, desc, lower, upper = "c.sort_name, c.id", "c.sort_name DESC, c.id DESC", "(c.sort_name, c.id) < (@name, @id)", "(c.sort_name, c.id) >= (@name, @id)"
	}
	q.AfterNumber, q.AfterName, q.AfterID, q.Limit = at.Number, at.SortName, at.ID, window
	from, where := q.clauses()
	rows, err := d.read.QueryContext(ctx, `SELECT * FROM (SELECT 0, `+liveChannelColumns+` FROM `+from+liveChannelJoins+`
			WHERE `+where+` AND `+lower+` ORDER BY `+desc+` LIMIT @limit)
		UNION ALL SELECT * FROM (SELECT 1, `+liveChannelColumns+` FROM `+from+liveChannelJoins+`
			WHERE `+where+` AND `+upper+` ORDER BY `+asc+` LIMIT @limit + 1)`, q.args(v)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var side int
		c, err := scanLiveChannel(rows, &side)
		if err != nil {
			return nil, nil, err
		}
		if side == 0 {
			before = append(before, c)
		} else {
			after = append(after, c)
		}
	}
	return before, after, rows.Err()
}

// LiveSourceFor finds a source a viewer may filter the guide by: in use, not paused, and for
// everyone unless owner. Any other answers false, as a source that does not exist does.
func (d *DB) LiveSourceFor(ctx context.Context, publicID string, owner bool) (int64, bool, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT id FROM live_sources WHERE public_id = ? AND paused = 0 AND (? OR audience = 'everyone')`,
		publicID, owner).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// LiveFilters is what the channel list's filter menu is made from, before the caller decides
// which Library Channels the viewer may see and applies the profile's own marks: the channels
// in use of each source the viewer may see, counted by group; the profile's own favourite and
// hidden marks on channels in use; and each Library Channel in use.
type LiveFilters struct {
	Counts  []LiveGroupCount
	Marks   []LiveChannelMark
	Library []LiveLibraryEntry
}

// LiveGroupCount is how many channels in use one source has in one group ("" for none), and
// the lowest and highest source their listings come from (0 for none).
type LiveGroupCount struct {
	SourceID           int64
	Group              string
	Channels           int
	MinGuide, MaxGuide int64
}

// LiveChannelMark is one of the profile's own marks on a source's channel in use.
type LiveChannelMark struct {
	SourceID         int64
	Group            string
	Favorite, Hidden bool
}

// LiveLibraryEntry is one Library Channel in use: its group, the libraries it draws from, how
// far its schedule reaches, and the profile's own marks on it.
type LiveLibraryEntry struct {
	Group            string
	Libraries        []string
	ScheduledThrough time.Time
	ScheduledFrom    time.Time
	Favorite, Hidden bool
}

// ReadLiveFilters reads the filter menu's makings in three reads: one count of the visible
// sources' channels in use by source, group and listings source, which reads no row of the profile's; the
// profile's own marks, which are few; and the Library Channels, which are few.
func (d *DB) ReadLiveFilters(ctx context.Context, v Viewer, owner bool) (LiveFilters, error) {
	var out LiveFilters
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	inUse := `c.state = 1 AND c.missing_since_at IS NULL`
	visibleSources := `c.kind = 1 AND c.source_id IN (SELECT id FROM live_sources WHERE paused = 0 AND (@owner OR audience = 'everyone'))`
	args := []any{sql.Named("owner", owner), sql.Named("profile", v.ProfileID)}
	// Read from channels_in_use_by_group alone, in its order: no row of the table is read.
	if out.Counts, err = queryAll(ctx, tx, `SELECT c.source_id, ifnull(c.group_name, ''), count(*), min(ifnull(c.guide_source_id, 0)),
		max(ifnull(c.guide_source_id, 0)) FROM channels c INDEXED BY channels_in_use_by_group
		WHERE c.kind = 1 AND c.state = 1 AND c.missing_since_at IS NULL
		AND c.source_id IN (SELECT id FROM live_sources WHERE paused = 0 AND (@owner OR audience = 'everyone'))
		GROUP BY c.source_id, c.group_name, c.guide_source_id`, args,
		func(g *LiveGroupCount) []any {
			return []any{&g.SourceID, &g.Group, &g.Channels, &g.MinGuide, &g.MaxGuide}
		}); err != nil {
		return out, err
	}
	if out.Marks, err = queryAll(ctx, tx, `SELECT c.source_id, ifnull(c.group_name, ''), pc.favorite, pc.hidden FROM profile_channels pc
		JOIN channels c ON c.id = pc.channel_id WHERE pc.profile_id = @profile AND (pc.favorite = 1 OR pc.hidden = 1) AND `+visibleSources+
		` AND `+inUse, args, func(m *LiveChannelMark) []any { return []any{&m.SourceID, &m.Group, &m.Favorite, &m.Hidden} }); err != nil {
		return out, err
	}
	type row struct {
		group, libraries string
		from, through    int64
		favorite, hidden bool
	}
	rows, err := queryAll(ctx, tx, `SELECT ifnull(c.group_name, ''), ifnull(l.libraries, '[]'), ifnull(l.scheduled_through_at, 0),
		ifnull((SELECT min(starts_at) FROM channel_schedule WHERE channel_id = c.id), 0),
		ifnull(pc.favorite, 0), ifnull(pc.hidden, 0) FROM channels c JOIN library_channels l ON l.channel_id = c.id
		LEFT JOIN profile_channels pc ON pc.profile_id = @profile AND pc.channel_id = c.id WHERE c.kind = 2 AND `+inUse, args,
		func(r *row) []any { return []any{&r.group, &r.libraries, &r.through, &r.from, &r.favorite, &r.hidden} })
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		e := LiveLibraryEntry{Group: r.group, Libraries: parseLibraries(r.libraries), Favorite: r.favorite, Hidden: r.hidden}
		if r.from != 0 {
			e.ScheduledFrom = time.UnixMilli(r.from).UTC()
		}
		if r.through != 0 {
			e.ScheduledThrough = time.UnixMilli(r.through)
		}
		out.Library = append(out.Library, e)
	}
	return out, nil
}

func parseLibraries(raw string) []string {
	var libraries []string
	if json.Unmarshal([]byte(raw), &libraries) != nil {
		return []string{""} // unreadable: names a library nobody may see
	}
	return libraries
}
