package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// LiveSource is one source of channels or listings. Secret is the sealed sign-in; it never
// leaves the server.
type LiveSource struct {
	ID          int64
	PublicID    string
	Kind        string
	Name        string
	Config      string
	Secret      []byte
	Audience    string
	TunerCount  int
	Paused      bool
	State       string
	LastError   string
	Report      string
	RefreshedAt time.Time
	Revision    int64
	CreatedAt   time.Time
}

// LiveSourceSave is what the owner saves for a source. PublicID, on a create, is the id the
// caller made, so a sign-in can be sealed to it before it is stored.
type LiveSourceSave struct {
	PublicID                     string
	Kind, Name, Config, Audience string
	Secret                       []byte
	KeepSecret                   bool // a change that does not send the sign-in keeps the one stored
	TunerCount                   int
	Paused                       bool
	IdempotencyKey, RequestHash  string
	Now                          time.Time
}

const liveSourceColumns = `id, public_id, kind, name, config, secret, audience, tuner_count, paused, state, ifnull(last_error, ''),
	ifnull(report, ''), ifnull(refreshed_at, 0), revision, created_at`

func scanLiveSource(row interface{ Scan(...any) error }) (LiveSource, error) {
	var s LiveSource
	var refreshed, created int64
	err := row.Scan(&s.ID, &s.PublicID, &s.Kind, &s.Name, &s.Config, &s.Secret, &s.Audience, &s.TunerCount, &s.Paused, &s.State,
		&s.LastError, &s.Report, &refreshed, &s.Revision, &created)
	if refreshed > 0 {
		s.RefreshedAt = time.UnixMilli(refreshed)
	}
	s.CreatedAt = time.UnixMilli(created)
	return s, err
}

// LiveSources lists every source, oldest first. A server has a handful.
func (d *DB) LiveSources(ctx context.Context) ([]LiveSource, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+liveSourceColumns+` FROM live_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveSource
	for rows.Next() {
		s, err := scanLiveSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LiveSourceByID reads one source by its row id, to tune one of its channels.
func (d *DB) LiveSourceByID(ctx context.Context, id int64) (LiveSource, error) {
	s, err := scanLiveSource(d.read.QueryRowContext(ctx, `SELECT `+liveSourceColumns+` FROM live_sources WHERE id = ?`, id))
	return s, notFound(err)
}

// LiveSource reads one source.
func (d *DB) LiveSource(ctx context.Context, publicID string) (LiveSource, error) {
	s, err := scanLiveSource(d.read.QueryRowContext(ctx, `SELECT `+liveSourceColumns+` FROM live_sources WHERE public_id = ?`, publicID))
	return s, notFound(err)
}

// CreateLiveSource saves a new source, waiting for its first refresh. The same
// Idempotency-Key answers the source made the first time.
func (d *DB) CreateLiveSource(ctx context.Context, actorID int64, s LiveSourceSave) (LiveSource, error) {
	var publicID string
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, actorID, s.IdempotencyKey, s.RequestHash)
		if err != nil || ref != "" {
			publicID = ref
			return err
		}
		if publicID = s.PublicID; publicID == "" {
			publicID = NewID("lsr")
		}
		now := s.Now.UnixMilli()
		if _, err := tx.Exec(`INSERT INTO live_sources (public_id, kind, name, config, secret, audience, tuner_count, paused, state,
			created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'new', ?, ?)`, publicID, s.Kind, s.Name, s.Config, s.Secret, s.Audience,
			s.TunerCount, s.Paused, now, now); err != nil {
			return err
		}
		if err := audit(tx, actorID, "live_source.create", publicID, s.Name, s.Now); err != nil {
			return err
		}
		return rememberCreated(tx, actorID, s.IdempotencyKey, s.RequestHash, publicID, s.Now)
	})
	if err != nil {
		return LiveSource{}, err
	}
	return d.LiveSource(ctx, publicID)
}

// SaveLiveSource replaces a source's settings; match is the revision read, or -1.
func (d *DB) SaveLiveSource(ctx context.Context, actorID int64, publicID string, s LiveSourceSave, match int64) (LiveSource, error) {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var revision int64
		if err := tx.QueryRow(`SELECT revision FROM live_sources WHERE public_id = ?`, publicID).Scan(&revision); err != nil {
			return notFound(err)
		}
		if match >= 0 && match != revision {
			return ErrStale
		}
		_, err := tx.Exec(`UPDATE live_sources SET name = ?, config = ?, secret = CASE WHEN ? THEN secret ELSE ? END, audience = ?,
			tuner_count = ?, paused = ?, revision = revision + 1, updated_at = ? WHERE public_id = ?`,
			s.Name, s.Config, s.KeepSecret, s.Secret, s.Audience, s.TunerCount, s.Paused, s.Now.UnixMilli(), publicID)
		if err != nil {
			return err
		}
		return audit(tx, actorID, "live_source.update", publicID, s.Name, s.Now)
	})
	if err != nil {
		return LiveSource{}, err
	}
	return d.LiveSource(ctx, publicID)
}

// DeleteLiveSource removes a source with its channels, the series rules and upcoming
// bookings on them and their reminders; finished recordings stay. Deleting one that is gone
// succeeds.
func (d *DB) DeleteLiveSource(ctx context.Context, actorID int64, publicID string, now time.Time) (int64, error) {
	var id int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM live_sources WHERE public_id = ?`, publicID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if _, err := tx.Exec(`DELETE FROM live_sources WHERE id = ?`, id); err != nil {
			return err
		}
		return audit(tx, actorID, "live_source.delete", publicID, "", now)
	})
	return id, err
}

// SourceRefreshed records the outcome of a refresh. A refresh that failed keeps the source's
// channels and guide and says why.
func (d *DB) SourceRefreshed(ctx context.Context, sourceID int64, state, lastError, report string, tuners int, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE live_sources SET state = ?, last_error = nullif(?, ''), report = coalesce(nullif(?, ''), report),
			refreshed_at = CASE WHEN ? = '' THEN ? ELSE refreshed_at END, tuner_count = CASE WHEN ? > 0 THEN ? ELSE tuner_count END
			WHERE id = ?`, state, lastError, report, lastError, now.UnixMilli(), tuners, tuners, sourceID)
		return err
	})
}

// SourceChannel is one channel as a refresh brings it in.
type SourceChannel struct {
	Key          string
	Name         string
	Number       int // 0 when the source sent none
	SourceNumber int
	Group        string
	GuideSource  int64
	GuideKey     string
	Locator      string
	Logo         string
	BroughtIn    bool
}

// channelEdits are the fields the owner set by hand, which a refresh leaves alone.
type channelEdits []string

// ImportSourceChannels brings in a refresh's channels as background work, a few hundred to a
// transaction: a channel already known keeps its row, so a profile's favourite survives,
// and what the owner set by hand stays. Channels the source no longer sends are marked missing
// and leave the guide.
func (d *DB) ImportSourceChannels(ctx context.Context, sourceID int64, channels []SourceChannel, now time.Time) error {
	const batch = 500
	for start := 0; start < len(channels); start += batch {
		part := channels[start:min(start+batch, len(channels))]
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			for _, c := range part {
				var id int64
				var editedText sql.NullString
				err := tx.QueryRow(`SELECT id, edited FROM channels WHERE source_id = ? AND source_key = ?`, sourceID, c.Key).Scan(&id, &editedText)
				state := ChannelInUse
				if !c.BroughtIn {
					state = ChannelNotIn
				}
				if errors.Is(err, sql.ErrNoRows) {
					_, err = tx.Exec(`INSERT INTO channels (public_id, kind, source_id, source_key, name, sort_name, number, source_number,
						group_name, state, guide_source_id, guide_key, locator, logo_url, updated_at)
						VALUES (?, ?, ?, ?, ?, ?, ?, nullif(?, 0), nullif(?, ''), ?, nullif(?, 0), nullif(?, ''), ?, nullif(?, ''), ?)`,
						NewID("chn"), ChannelFromSource, sourceID, c.Key, c.Name, sorttext.Key(c.Name, "", ""), c.Number, c.SourceNumber,
						c.Group, state, c.GuideSource, c.GuideKey, c.Locator, c.Logo, now.UnixMilli())
					if err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return err
				}
				var edited channelEdits
				json.Unmarshal([]byte(editedText.String), &edited)
				name, number, listings := !slices.Contains(edited, "name"), !slices.Contains(edited, "number"), !slices.Contains(edited, "listings")
				// The owner's own hide, or choice to bring the channel in or leave it out, stays.
				keepState := slices.Contains(edited, "hidden") || slices.Contains(edited, "broughtIn")
				if _, err := tx.Exec(`UPDATE channels SET name = CASE WHEN ? THEN ? ELSE name END, sort_name = CASE WHEN ? THEN ? ELSE sort_name END,
					number = CASE WHEN ? THEN ? ELSE number END, source_number = nullif(?, 0), group_name = nullif(?, ''),
					state = CASE WHEN ? THEN state WHEN ? THEN ? ELSE ? END,
					guide_source_id = CASE WHEN ? THEN nullif(?, 0) ELSE guide_source_id END, guide_key = CASE WHEN ? THEN nullif(?, '') ELSE guide_key END,
					locator = ?, logo_url = nullif(?, ''), missing_since_at = NULL, updated_at = ? WHERE id = ?`,
					name, c.Name, name, sorttext.Key(c.Name, "", ""), number, c.Number, c.SourceNumber, c.Group,
					keepState, c.BroughtIn, ChannelInUse, ChannelNotIn, listings, c.GuideSource, listings, c.GuideKey,
					c.Locator, c.Logo, now.UnixMilli(), id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE channels SET missing_since_at = ? WHERE source_id = ? AND updated_at < ? AND missing_since_at IS NULL`,
			now.UnixMilli(), sourceID, now.UnixMilli())
		return err
	})
}

// SourceChannelRow is one of a source's channels on the owner's channel map.
type SourceChannelRow struct {
	ID           int64
	PublicID     string
	Name         string
	Number       int
	SourceNumber int
	Group        string
	State        int
	GuideSource  string
	GuideKey     string
	Edited       []string
	Missing      bool
}

// SourceChannels reads a page of a source's channels by number, with those not brought in.
func (d *DB) SourceChannels(ctx context.Context, sourceID int64, afterNumber int, afterID int64, limit int) ([]SourceChannelRow, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT c.id, c.public_id, c.name, c.number, ifnull(c.source_number, 0), ifnull(c.group_name, ''), c.state,
		ifnull(g.public_id, ''), ifnull(c.guide_key, ''), ifnull(c.edited, '[]'), c.missing_since_at IS NOT NULL
		FROM channels c LEFT JOIN live_sources g ON g.id = c.guide_source_id
		WHERE c.source_id = ? AND (c.number, c.id) > (?, ?) ORDER BY c.number, c.id LIMIT ?`, sourceID, afterNumber, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceChannelRow
	for rows.Next() {
		var r SourceChannelRow
		var edited string
		if err := rows.Scan(&r.ID, &r.PublicID, &r.Name, &r.Number, &r.SourceNumber, &r.Group, &r.State, &r.GuideSource, &r.GuideKey,
			&edited, &r.Missing); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(edited), &r.Edited)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChannelEdit is the owner's change to one channel: only what is set changes, and each change
// is remembered so a refresh leaves it alone.
type ChannelEdit struct {
	PublicID    string
	Name        *string
	Number      *int
	GuideSource *string // a source's public id, "" for none
	GuideKey    *string
	Hidden      *bool
	BroughtIn   *bool
}

// EditSourceChannels applies the owner's changes to many of a source's channels at once.
func (d *DB) EditSourceChannels(ctx context.Context, sourceID int64, edits []ChannelEdit, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		for _, e := range edits {
			var id int64
			var editedText string
			if err := tx.QueryRow(`SELECT id, ifnull(edited, '[]') FROM channels WHERE public_id = ? AND source_id = ?`, e.PublicID, sourceID).
				Scan(&id, &editedText); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return invalid("%s is not one of this source's channels", e.PublicID)
				}
				return err
			}
			var edited channelEdits
			json.Unmarshal([]byte(editedText), &edited)
			mark := func(field string) {
				if !slices.Contains(edited, field) {
					edited = append(edited, field)
				}
			}
			if e.Name != nil {
				mark("name")
				if _, err := tx.Exec(`UPDATE channels SET name = ?, sort_name = ? WHERE id = ?`, *e.Name, sorttext.Key(*e.Name, "", ""), id); err != nil {
					return err
				}
			}
			if e.Number != nil {
				mark("number")
				if _, err := tx.Exec(`UPDATE channels SET number = ? WHERE id = ?`, *e.Number, id); err != nil {
					return err
				}
			}
			if e.GuideSource != nil || e.GuideKey != nil {
				mark("listings")
				var guide sql.NullInt64
				if e.GuideSource != nil && *e.GuideSource != "" {
					var g int64
					if err := tx.QueryRow(`SELECT id FROM live_sources WHERE public_id = ?`, *e.GuideSource).Scan(&g); err != nil {
						return invalid("the listings source %s does not exist", *e.GuideSource)
					}
					guide = sql.NullInt64{Int64: g, Valid: true}
				}
				key := ""
				if e.GuideKey != nil {
					key = *e.GuideKey
				}
				if _, err := tx.Exec(`UPDATE channels SET guide_source_id = ?, guide_key = nullif(?, '') WHERE id = ?`, guide, key, id); err != nil {
					return err
				}
			}
			state := 0
			switch {
			case e.Hidden != nil && *e.Hidden:
				mark("hidden")
				state = ChannelHidden
			case e.Hidden != nil:
				edited = slices.DeleteFunc(edited, func(f string) bool { return f == "hidden" })
				state = ChannelInUse
			}
			if e.BroughtIn != nil {
				mark("broughtIn")
				state = map[bool]int{true: ChannelInUse, false: ChannelNotIn}[*e.BroughtIn]
			}
			raw, _ := json.Marshal(edited)
			if _, err := tx.Exec(`UPDATE channels SET edited = ?, state = CASE WHEN ? > 0 THEN ? ELSE state END, updated_at = ? WHERE id = ?`,
				string(raw), state, state, now.UnixMilli(), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ChannelLocator is where a source's channel streams from, with its source's tuner count.
// It is read only to tune in or record, never sent to an app.
func (d *DB) ChannelLocator(ctx context.Context, channelID int64) (locator string, sourceID int64, tuners int, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT ifnull(c.locator, ''), s.id, s.tuner_count FROM channels c JOIN live_sources s ON s.id = c.source_id
		WHERE c.id = ?`, channelID).Scan(&locator, &sourceID, &tuners)
	return locator, sourceID, tuners, notFound(err)
}
