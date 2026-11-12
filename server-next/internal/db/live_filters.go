package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// LiveFilters is what the channels in use can be filtered by, before the caller decides which
// Library Channels the viewer may see: the sources that have a channel in use (for everyone,
// or every source for the owner), the groups of those sources' channels, and each Library
// Channel's group and libraries.
type LiveFilters struct {
	Sources []LiveSourceName
	Groups  []string
	Library []LiveLibraryGroup
}

// LiveSourceName is a source by its public id and name.
type LiveSourceName struct{ ID, Name string }

// LiveLibraryGroup is a Library Channel's group ("" for none) and the libraries it draws from.
type LiveLibraryGroup struct {
	Group     string
	Libraries []string
}

// ReadLiveFilters reads the sources and groups to filter by, in three small reads: the
// sources through channels_of_source, the groups as one walk of channels_of_group with one
// look for a channel the viewer may see in each, and the Library Channels, which are few.
func (d *DB) ReadLiveFilters(ctx context.Context, owner bool) (LiveFilters, error) {
	var out LiveFilters
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	inUse := `c.state = 1 AND c.missing_since_at IS NULL`
	if out.Sources, err = queryAll(ctx, tx, `SELECT s.public_id, s.name FROM live_sources s WHERE s.paused = 0 AND (? OR s.audience = 'everyone')
		AND EXISTS (SELECT 1 FROM channels c WHERE c.source_id = s.id AND `+inUse+`)`, []any{owner},
		func(n *LiveSourceName) []any { return []any{&n.ID, &n.Name} }); err != nil {
		return out, err
	}
	if out.Groups, err = queryAll(ctx, tx, `SELECT g FROM (SELECT DISTINCT group_name AS g FROM channels WHERE group_name IS NOT NULL)
		WHERE EXISTS (SELECT 1 FROM channels c JOIN live_sources s ON s.id = c.source_id WHERE c.group_name = g AND `+inUse+`
			AND s.paused = 0 AND (? OR s.audience = 'everyone'))`, []any{owner},
		func(g *string) []any { return []any{g} }); err != nil {
		return out, err
	}
	type row struct{ group, libraries string }
	rows, err := queryAll(ctx, tx, `SELECT ifnull(c.group_name, ''), ifnull(l.libraries, '[]') FROM channels c
		JOIN library_channels l ON l.channel_id = c.id WHERE c.kind = 2 AND `+inUse, nil,
		func(r *row) []any { return []any{&r.group, &r.libraries} })
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Library = append(out.Library, LiveLibraryGroup{Group: r.group, Libraries: parseLibraries(r.libraries)})
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
