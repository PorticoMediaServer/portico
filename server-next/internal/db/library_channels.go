package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// Channel kinds and states, as stored in channels.
const (
	ChannelFromSource  = 1
	ChannelFromLibrary = 2

	ChannelInUse  = 1
	ChannelHidden = 2 // hidden by the owner for everyone
	ChannelNotIn  = 3 // a source's channel the owner did not bring in
	ChannelOff    = 4
)

// The words a Library Channel's row shows besides Off.
const (
	LibraryChannelOnAir     = "on_air"
	LibraryChannelBuilding  = "building"
	LibraryChannelStale     = "needs_schedule"
	LibraryChannelAttention = "needs_attention"
)

// Limits on Library Channels. Most people never come near them; they keep a server safe.
const (
	MaxLibraryChannels = 1000
	MaxBlockPresets    = 1000
)

// ErrChannelLimit and ErrPresetLimit: the server already holds as many as it may.
var (
	ErrChannelLimit = errors.New("too many library channels")
	ErrPresetLimit  = errors.New("too many block presets")
)

// LibraryChannel is one Library Channel: its channels row and its library_channels row.
// Builder is set only by the reads that edit or schedule.
type LibraryChannel struct {
	ID          int64
	PublicID    string
	Name        string
	Number      int
	On          bool
	Description string
	TimeZone    string
	Builder     string
	// Libraries is every library the channel and its blocks draw from, by public id.
	Libraries        []string
	State, Problem   string
	ScheduledThrough time.Time
	// Titles and Unplaceable are from the last build: how many titles the channel chooses
	// from, and of those how many have no known length.
	Titles, Unplaceable int
	LogoHash            string
	Revision            int64
	UpdatedAt           time.Time
}

// LibraryChannelSave is what the owner saves. Libraries must already include every block's.
type LibraryChannelSave struct {
	Name, Description, TimeZone, Builder string
	Libraries                            []string
	On                                   bool
	IdempotencyKey, RequestHash          string
	Now                                  time.Time
}

const libraryChannelColumns = `c.id, c.public_id, c.name, c.number, c.state, ifnull(l.description, ''), l.time_zone, l.libraries,
	l.state, ifnull(l.problem, ''), ifnull(l.scheduled_through_at, 0), ifnull(c.logo_hash, ''), l.revision, l.updated_at,
	ifnull(json_extract(l.schedule_state, '$.titles'), 0), ifnull(json_extract(l.schedule_state, '$.unplaceable'), 0)`

func scanLibraryChannel(row interface{ Scan(...any) error }, ch *LibraryChannel, extra ...any) error {
	var state int
	var libraries string
	var through, updated int64
	dest := append([]any{&ch.ID, &ch.PublicID, &ch.Name, &ch.Number, &state, &ch.Description, &ch.TimeZone, &libraries,
		&ch.State, &ch.Problem, &through, &ch.LogoHash, &ch.Revision, &updated, &ch.Titles, &ch.Unplaceable}, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	ch.On = state == ChannelInUse
	if through > 0 {
		ch.ScheduledThrough = time.UnixMilli(through)
	}
	ch.UpdatedAt = time.UnixMilli(updated)
	// A list that cannot be read names no library, and a channel with none is seen by nobody but the owner.
	if json.Unmarshal([]byte(libraries), &ch.Libraries) != nil {
		ch.Libraries = []string{""}
	}
	return nil
}

func checkLibraries(tx *sql.Tx, publicIDs []string) error {
	if len(publicIDs) == 0 {
		return invalid("choose at least one library")
	}
	for _, id := range publicIDs {
		var kind string
		if err := tx.QueryRow(`SELECT kind FROM libraries WHERE public_id = ?`, id).Scan(&kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalid("the library %s does not exist", id)
			}
			return err
		}
		if kind != LibraryMovies && kind != LibraryShows && kind != LibraryAnime {
			return invalid("a Library Channel plays only movies, TV shows and anime libraries")
		}
	}
	return nil
}

// CreateLibraryChannel makes a Library Channel at the end of the owner's order. The same
// Idempotency-Key answers the channel made the first time.
func (d *DB) CreateLibraryChannel(ctx context.Context, actorID int64, s LibraryChannelSave) (LibraryChannel, error) {
	var publicID string
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, actorID, s.IdempotencyKey, s.RequestHash)
		if err != nil || ref != "" {
			publicID = ref
			return err
		}
		var count, last int
		if err := tx.QueryRow(`SELECT count(*), ifnull(max(number), 0) FROM channels WHERE kind = ?`, ChannelFromLibrary).Scan(&count, &last); err != nil {
			return err
		}
		if count >= MaxLibraryChannels {
			return ErrChannelLimit
		}
		if err := checkLibraries(tx, s.Libraries); err != nil {
			return err
		}
		libraries, _ := json.Marshal(s.Libraries)
		publicID = NewID("chn")
		now := s.Now.UnixMilli()
		res, err := tx.Exec(`INSERT INTO channels (public_id, kind, name, sort_name, number, state, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			publicID, ChannelFromLibrary, s.Name, sorttext.Key(s.Name, "", ""), last+1, channelState(s.On), now)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO library_channels (channel_id, description, time_zone, builder, libraries, state, created_at, updated_at)
			VALUES (?, nullif(?, ''), ?, ?, ?, ?, ?, ?)`, id, s.Description, s.TimeZone, s.Builder, string(libraries), LibraryChannelBuilding, now, now); err != nil {
			return err
		}
		if err := audit(tx, actorID, "library_channel.create", publicID, s.Name, s.Now); err != nil {
			return err
		}
		return rememberCreated(tx, actorID, s.IdempotencyKey, s.RequestHash, publicID, s.Now)
	})
	if err != nil {
		return LibraryChannel{}, err
	}
	return d.LibraryChannel(ctx, publicID, true)
}

func channelState(on bool) int {
	if on {
		return ChannelInUse
	}
	return ChannelOff
}

// LibraryChannel reads one Library Channel, with its builder when withBuilder is set.
func (d *DB) LibraryChannel(ctx context.Context, publicID string, withBuilder bool) (LibraryChannel, error) {
	var ch LibraryChannel
	builder := "''"
	if withBuilder {
		builder = "l.builder"
	}
	err := scanLibraryChannel(d.read.QueryRowContext(ctx, `SELECT `+libraryChannelColumns+`, `+builder+`
		FROM channels c JOIN library_channels l ON l.channel_id = c.id WHERE c.public_id = ?`, publicID), &ch, &ch.Builder)
	return ch, notFound(err)
}

// LibraryChannels reads a page of Library Channels in the owner's order, after the channel
// numbered after (0 for the first page), with each builder when withBuilder is set.
func (d *DB) LibraryChannels(ctx context.Context, after, limit int, withBuilder bool) ([]LibraryChannel, error) {
	builder := "''"
	if withBuilder {
		builder = "l.builder"
	}
	rows, err := d.read.QueryContext(ctx, `SELECT `+libraryChannelColumns+`, `+builder+` FROM channels c JOIN library_channels l ON l.channel_id = c.id
		WHERE c.kind = ? AND c.number > ? ORDER BY c.number, c.id LIMIT ?`, ChannelFromLibrary, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryChannel
	for rows.Next() {
		var ch LibraryChannel
		if err := scanLibraryChannel(rows, &ch, &ch.Builder); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// SaveLibraryChannel replaces a Library Channel's name, description, time zone, builder and
// on or off. match is the revision the caller read, or -1. A change to what plays marks the
// schedule to be built again from the end of the programme now on.
func (d *DB) SaveLibraryChannel(ctx context.Context, actorID int64, publicID string, s LibraryChannelSave, match int64) (LibraryChannel, error) {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var id, revision int64
		var builder, zone string
		err := tx.QueryRow(`SELECT c.id, l.revision, l.builder, l.time_zone FROM channels c JOIN library_channels l ON l.channel_id = c.id
			WHERE c.public_id = ?`, publicID).Scan(&id, &revision, &builder, &zone)
		if err != nil {
			return notFound(err)
		}
		if match >= 0 && match != revision {
			return ErrStale
		}
		if err := checkLibraries(tx, s.Libraries); err != nil {
			return err
		}
		libraries, _ := json.Marshal(s.Libraries)
		now := s.Now.UnixMilli()
		if _, err := tx.Exec(`UPDATE channels SET name = ?, sort_name = ?, state = ?, updated_at = ? WHERE id = ?`,
			s.Name, sorttext.Key(s.Name, "", ""), channelState(s.On), now, id); err != nil {
			return err
		}
		rebuild := builder != s.Builder || zone != s.TimeZone
		if _, err := tx.Exec(`UPDATE library_channels SET description = nullif(?, ''), time_zone = ?, builder = ?, libraries = ?,
			state = CASE WHEN ? THEN ? ELSE state END, revision = revision + 1, updated_at = ? WHERE channel_id = ?`,
			s.Description, s.TimeZone, s.Builder, string(libraries), rebuild, LibraryChannelBuilding, now, id); err != nil {
			return err
		}
		return audit(tx, actorID, "library_channel.update", publicID, s.Name, s.Now)
	})
	if err != nil {
		return LibraryChannel{}, err
	}
	return d.LibraryChannel(ctx, publicID, true)
}

// RegenerateLibraryChannel marks a channel's schedule to be built again from the end of the
// programme now on.
func (d *DB) RegenerateLibraryChannel(ctx context.Context, publicID string, now time.Time) (LibraryChannel, error) {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE library_channels SET state = ?, revision = revision + 1, updated_at = ?
			WHERE channel_id = (SELECT id FROM channels WHERE public_id = ? AND kind = ?)`, LibraryChannelBuilding, now.UnixMilli(), publicID, ChannelFromLibrary)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return LibraryChannel{}, err
	}
	return d.LibraryChannel(ctx, publicID, false)
}

// DeleteLibraryChannel removes a Library Channel and its schedule. It touches no title.
// Deleting one that is already gone succeeds.
func (d *DB) DeleteLibraryChannel(ctx context.Context, actorID int64, publicID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM channels WHERE public_id = ? AND kind = ?`, publicID, ChannelFromLibrary)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return audit(tx, actorID, "library_channel.delete", publicID, "", now)
	})
}

// OrderLibraryChannels numbers the Library Channels in the order given, which must name each
// of them once.
func (d *DB) OrderLibraryChannels(ctx context.Context, publicIDs []string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM channels WHERE kind = ?`, ChannelFromLibrary).Scan(&count); err != nil {
			return err
		}
		if count != len(publicIDs) {
			return invalid("the order must name every Library Channel once")
		}
		for n, id := range publicIDs {
			res, err := tx.Exec(`UPDATE channels SET number = ?, updated_at = ? WHERE public_id = ? AND kind = ?`, n+1, now.UnixMilli(), id, ChannelFromLibrary)
			if err != nil {
				return err
			}
			if changed, _ := res.RowsAffected(); changed == 0 {
				return invalid("%s is not a Library Channel", id)
			}
		}
		var distinct int
		if err := tx.QueryRow(`SELECT count(DISTINCT number) FROM channels WHERE kind = ?`, ChannelFromLibrary).Scan(&distinct); err != nil {
			return err
		}
		if distinct != count {
			return invalid("the order must name every Library Channel once")
		}
		return nil
	})
}

// SetLibraryChannelLogo stores the hash of a channel's uploaded logo, or "" for the monogram.
func (d *DB) SetLibraryChannelLogo(ctx context.Context, publicID, hash string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE channels SET logo_hash = nullif(?, ''), updated_at = ? WHERE public_id = ? AND kind = ?`,
			hash, now.UnixMilli(), publicID, ChannelFromLibrary)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// LibraryKinds maps library public ids to their kinds, for the libraries that exist.
func (d *DB) LibraryKinds(ctx context.Context) (map[string]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT public_id, kind FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return nil, err
		}
		out[id] = kind
	}
	return out, rows.Err()
}

// TitleLibraries answers the library of each title named, by public id; a title that does not
// exist, or is not a film or a show, is left out.
func (d *DB) TitleLibraries(ctx context.Context, publicIDs []string) (map[string]string, error) {
	out := map[string]string{}
	for start := 0; start < len(publicIDs); start += 200 {
		part := publicIDs[start:min(start+200, len(publicIDs))]
		args := make([]any, len(part))
		for n, id := range part {
			args[n] = id
		}
		rows, err := d.read.QueryContext(ctx, `SELECT i.public_id, l.public_id FROM items i JOIN libraries l ON l.id = i.library_id
			WHERE i.kind IN (1, 2) AND i.public_id IN (?`+strings.Repeat(", ?", len(part)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item, library string
			if err := rows.Scan(&item, &library); err != nil {
				rows.Close()
				return nil, err
			}
			out[item] = library
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BlockPreset is a block the owner saved to reuse on any Library Channel.
type BlockPreset struct {
	ID          int64
	PublicID    string
	Name        string
	Description string
	Library     string // public id
	Recipe      string
	Revision    int64
	UpdatedAt   time.Time
}

// BlockPresetSave is what the owner saves for a preset.
type BlockPresetSave struct {
	Name, Description, Library, Recipe string
	IdempotencyKey, RequestHash        string
	Now                                time.Time
}

const blockPresetColumns = `p.id, p.public_id, p.name, ifnull(p.description, ''), l.public_id, p.recipe, p.revision, p.updated_at`

func scanBlockPreset(row interface{ Scan(...any) error }) (BlockPreset, error) {
	var p BlockPreset
	var updated int64
	err := row.Scan(&p.ID, &p.PublicID, &p.Name, &p.Description, &p.Library, &p.Recipe, &p.Revision, &updated)
	p.UpdatedAt = time.UnixMilli(updated)
	return p, err
}

func presetLibrary(tx *sql.Tx, publicID string) (int64, error) {
	if err := checkLibraries(tx, []string{publicID}); err != nil {
		return 0, err
	}
	var id int64
	return id, tx.QueryRow(`SELECT id FROM libraries WHERE public_id = ?`, publicID).Scan(&id)
}

// BlockPresets reads a page of presets by name, after the preset named after (or the first page).
func (d *DB) BlockPresets(ctx context.Context, afterName string, afterID int64, limit int) ([]BlockPreset, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+blockPresetColumns+` FROM block_presets p JOIN libraries l ON l.id = p.library_id
		WHERE (p.name, p.id) > (?, ?) ORDER BY p.name, p.id LIMIT ?`, afterName, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockPreset
	for rows.Next() {
		p, err := scanBlockPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// BlockPreset reads one preset.
func (d *DB) BlockPreset(ctx context.Context, publicID string) (BlockPreset, error) {
	p, err := scanBlockPreset(d.read.QueryRowContext(ctx, `SELECT `+blockPresetColumns+` FROM block_presets p
		JOIN libraries l ON l.id = p.library_id WHERE p.public_id = ?`, publicID))
	return p, notFound(err)
}

// CreateBlockPreset saves a new preset. The same Idempotency-Key answers the one made first.
func (d *DB) CreateBlockPreset(ctx context.Context, actorID int64, s BlockPresetSave) (BlockPreset, error) {
	var publicID string
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, actorID, s.IdempotencyKey, s.RequestHash)
		if err != nil || ref != "" {
			publicID = ref
			return err
		}
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM block_presets`).Scan(&count); err != nil {
			return err
		}
		if count >= MaxBlockPresets {
			return ErrPresetLimit
		}
		library, err := presetLibrary(tx, s.Library)
		if err != nil {
			return err
		}
		publicID = NewID("bpr")
		now := s.Now.UnixMilli()
		if _, err := tx.Exec(`INSERT INTO block_presets (public_id, name, description, library_id, recipe, created_at, updated_at)
			VALUES (?, ?, nullif(?, ''), ?, ?, ?, ?)`, publicID, s.Name, s.Description, library, s.Recipe, now, now); err != nil {
			return err
		}
		return rememberCreated(tx, actorID, s.IdempotencyKey, s.RequestHash, publicID, s.Now)
	})
	if err != nil {
		return BlockPreset{}, err
	}
	return d.BlockPreset(ctx, publicID)
}

// SaveBlockPreset replaces a preset; match is the revision read, or -1. No channel changes.
func (d *DB) SaveBlockPreset(ctx context.Context, publicID string, s BlockPresetSave, match int64) (BlockPreset, error) {
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var revision int64
		if err := tx.QueryRow(`SELECT revision FROM block_presets WHERE public_id = ?`, publicID).Scan(&revision); err != nil {
			return notFound(err)
		}
		if match >= 0 && match != revision {
			return ErrStale
		}
		library, err := presetLibrary(tx, s.Library)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE block_presets SET name = ?, description = nullif(?, ''), library_id = ?, recipe = ?, revision = revision + 1,
			updated_at = ? WHERE public_id = ?`, s.Name, s.Description, library, s.Recipe, s.Now.UnixMilli(), publicID)
		return err
	})
	if err != nil {
		return BlockPreset{}, err
	}
	return d.BlockPreset(ctx, publicID)
}

// DeleteBlockPreset removes a preset; channels built from it keep their copies.
func (d *DB) DeleteBlockPreset(ctx context.Context, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM block_presets WHERE public_id = ?`, publicID)
		return err
	})
}
