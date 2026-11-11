package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// VisibleItems answers which of the titles named the viewer may see, by the same one
// condition every list uses. It reads as many rows as it is given ids.
func (d *DB) VisibleItems(ctx context.Context, v Viewer, itemIDs []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(itemIDs); start += 200 {
		part := itemIDs[start:min(start+200, len(itemIDs))]
		got, err := ids(ctx, tx, `SELECT i.id FROM items i WHERE i.id IN `+intList(part)+` AND i.available = 1 AND `+acc.visible("i"))
		if err != nil {
			return nil, err
		}
		for _, id := range got {
			out[id] = true
		}
	}
	return out, nil
}

// LiveViewer is what deciding who may see a channel needs to know about a viewer.
type LiveViewer struct {
	Owner bool
	// LiveTV says the account and the profile are both allowed Live TV.
	LiveTV bool
	// Libraries are the public ids of the libraries that exist and the viewer may see.
	Libraries map[string]bool
}

// ViewerLive reads what deciding who may see a channel needs, in one read transaction.
func (d *DB) ViewerLive(ctx context.Context, v Viewer) (LiveViewer, error) {
	var out LiveViewer
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil || !acc.known {
		return out, err
	}
	var role string
	var primary, userLive, profileLive bool
	if err := tx.QueryRowContext(ctx, `SELECT u.role, p.is_primary, u.allow_live_tv, p.allow_live_tv FROM profiles p JOIN users u ON u.id = p.user_id
		WHERE p.id = ?`, v.ProfileID).Scan(&role, &primary, &userLive, &profileLive); err != nil {
		return out, err
	}
	out.Owner = role == "owner" && primary
	out.LiveTV = out.Owner || userLive && profileLive
	rows, err := tx.QueryContext(ctx, `SELECT l.public_id FROM libraries l WHERE `+acc.libraryVisible("l.id"))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Libraries = map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out.Libraries[id] = true
	}
	return out, rows.Err()
}

// LiveChannel is one channel as the guide and On now list it.
type LiveChannel struct {
	ID       int64
	PublicID string
	Kind     int
	Name     string
	SortName string
	Number   int
	Group    string
	// Libraries is a Library Channel's copy of the libraries it draws from, by public id.
	Libraries []string
	LogoHash  string
	Favorite  bool
	Hidden    bool
	// GuideSource and GuideKey say which listings in guide.db are this channel's.
	GuideSource int64
	GuideKey    string
}

// LiveChannelQuery is a page of channels in use: by number (the default) or name, after the
// channel the cursor names, only the profile's favourites or all of them, and with the
// profile's own hidden ones only when asked.
type LiveChannelQuery struct {
	ByName     bool
	Favorites  bool
	WithHidden bool
	// NotFavorites leaves the profile's favourites out, for On now's second part.
	NotFavorites bool
	AfterNumber  int
	AfterName    string
	AfterID      int64
	Limit        int
}

// LiveChannels reads a page of channels in use, with the profile's own marks. It reads a run
// of one ordered index and stops when the page is full, or, for favourites, the profile's own
// marked rows. Who may see each channel is decided by the caller, through one function.
func (d *DB) LiveChannels(ctx context.Context, v Viewer, q LiveChannelQuery) ([]LiveChannel, error) {
	order, after := "c.number, c.id", "(c.number, c.id) > (@number, @id)"
	if q.ByName {
		order, after = "c.sort_name, c.id", "(c.sort_name, c.id) > (@name, @id)"
	}
	from := `channels c LEFT JOIN profile_channels pc ON pc.profile_id = @profile AND pc.channel_id = c.id`
	where := `c.state = 1 AND ` + after
	if q.Favorites {
		from = `profile_channels pc JOIN channels c ON c.id = pc.channel_id`
		where += ` AND pc.profile_id = @profile AND pc.favorite = 1`
	}
	if q.NotFavorites {
		where += ` AND ifnull(pc.favorite, 0) = 0`
	}
	if !q.WithHidden {
		where += ` AND ifnull(pc.hidden, 0) = 0`
	}
	rows, err := d.read.QueryContext(ctx, `SELECT c.id, c.public_id, c.kind, c.name, c.sort_name, c.number, ifnull(c.group_name, ''),
		ifnull(l.libraries, '[]'), ifnull(c.logo_hash, ''), ifnull(pc.favorite, 0), ifnull(pc.hidden, 0), ifnull(c.guide_source_id, 0),
		ifnull(c.guide_key, '') FROM `+from+` LEFT JOIN library_channels l ON l.channel_id = c.id
		WHERE `+where+` ORDER BY `+order+` LIMIT @limit`,
		sql.Named("profile", v.ProfileID), sql.Named("number", q.AfterNumber), sql.Named("name", q.AfterName), sql.Named("id", q.AfterID),
		sql.Named("limit", q.Limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveChannel
	for rows.Next() {
		var c LiveChannel
		var libraries string
		if err := rows.Scan(&c.ID, &c.PublicID, &c.Kind, &c.Name, &c.SortName, &c.Number, &c.Group, &libraries, &c.LogoHash, &c.Favorite,
			&c.Hidden, &c.GuideSource, &c.GuideKey); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(libraries), &c.Libraries) != nil {
			c.Libraries = []string{""} // unreadable: names a library nobody may see
		}
		if c.Kind == ChannelFromLibrary {
			c.GuideSource, c.GuideKey = LibraryGuideSource, c.PublicID
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LiveChannelByID reads one channel in use with the profile's own marks.
func (d *DB) LiveChannelByID(ctx context.Context, v Viewer, publicID string) (LiveChannel, error) {
	var id int64
	var number int
	if err := d.read.QueryRowContext(ctx, `SELECT id, number FROM channels WHERE public_id = ? AND state = 1`, publicID).Scan(&id, &number); err != nil {
		return LiveChannel{}, notFound(err)
	}
	page, err := d.LiveChannels(ctx, v, LiveChannelQuery{WithHidden: true, AfterNumber: number, AfterID: id - 1, Limit: 1})
	if err != nil {
		return LiveChannel{}, err
	}
	if len(page) == 0 || page[0].ID != id {
		return LiveChannel{}, ErrNotFound
	}
	return page[0], nil
}

// SetChannelMarks sets a profile's favourite and hidden marks on a channel. A channel with
// neither keeps no row.
func (d *DB) SetChannelMarks(ctx context.Context, v Viewer, channelID int64, favorite, hidden bool, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		if !favorite && !hidden {
			_, err := tx.Exec(`DELETE FROM profile_channels WHERE profile_id = ? AND channel_id = ?`, v.ProfileID, channelID)
			return err
		}
		_, err := tx.Exec(`INSERT INTO profile_channels (profile_id, channel_id, favorite, hidden, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (profile_id, channel_id) DO UPDATE SET favorite = excluded.favorite, hidden = excluded.hidden,
			updated_at = excluded.updated_at`, v.ProfileID, channelID, favorite, hidden, now.UnixMilli())
		return err
	})
}
