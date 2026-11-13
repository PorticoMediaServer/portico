package db

import (
	"context"
	"database/sql"
	"errors"
)

// ItemDetail is everything the detail page of one item shows.
type ItemDetail struct {
	Item
	OriginalTitle string
	Summary       string
	Tagline       string
	Edition       string
	CriticRating  float64
	Available     bool
	Parent        *ItemRef
	Root          *ItemRef
	Series        *ItemRef // a book's or audiobook's series, when the viewer may see it
	Tags          []Tag
	ExternalIDs   []ExternalID
	Credits       []Credit // the first few, in billing order
	CreditTotal   int
	Versions      []Version
}

// ItemRef names another item: the show an episode belongs to, for example.
type ItemRef struct {
	PublicID string
	Kind     ItemKind
	Title    string
	Index    *int
}

// Tag is a genre, studio, label or similar.
type Tag struct {
	PublicID string
	Kind     string
	Name     string
}

// Credit is one person's part in an item.
type Credit struct {
	PersonID  string
	Name      string
	Role      CreditRole
	Character string
	Image     *Image
}

// Version is one playable version of an item, with its parts and their tracks.
type Version struct {
	PublicID      string
	Name          string
	Edition       string
	Container     string
	DurationMS    int64
	Bitrate       int64
	Size          int64
	Width         int
	Height        int
	VideoCodec    string
	AudioCodec    string
	AudioChannels int
	HDR           string
	Parts         []Part
	Markers       []Marker

	id int64
}

// MediaID is the version's own row id, for the server's own records of it (a prepared copy).
// It never goes on the wire.
func (v Version) MediaID() int64 { return v.id }

// Part is one file, or one range of a file, of a version.
type Part struct {
	FileID  int64
	Name    string
	Size    int64
	StartMS *int64
	EndMS   *int64
	Streams []Stream
	// DurationMS is the part's own length, from its file's probe (or its range within the
	// file); nil while the file has not been probed.
	DurationMS *int64
	// OffsetMS is where the part begins in the whole title, for a version in several parts:
	// the lengths of the parts before it, in order. Nil for a version in one part, and from
	// the first part whose length is not known yet. For parts encoded alike, which are played
	// by copying their packets one after another, a part's length here is the time its packets
	// take (packetsUS), the same place the stream starts it; for parts that differ, which are
	// converted into one stream, it is the gapless length, as that stream's is.
	OffsetMS *int64

	// packetsUS is how long the file's sound packets take, when they have been measured.
	packetsUS *int64
}

// placeParts gives each part of a version in several parts its place in the whole title.
func placeParts(parts []Part) {
	if len(parts) < 2 {
		return
	}
	if packetParts(parts) {
		var at int64
		for n := range parts {
			offset := (at + 500) / 1000
			parts[n].OffsetMS = &offset
			at += *parts[n].packetsUS
		}
		return
	}
	var at int64
	for n := range parts {
		if parts[n].DurationMS == nil {
			return
		}
		offset := at
		parts[n].OffsetMS = &offset
		at += *parts[n].DurationMS
	}
}

// packetParts says whole audio files have packet lengths for the book's common clock.
// Copied parts retain encoder padding; converted parts fill it before joining.
func packetParts(parts []Part) bool {
	for _, p := range parts {
		if p.packetsUS == nil || *p.packetsUS <= 0 || p.StartMS != nil || p.EndMS != nil {
			return false
		}
		audio := false
		for _, s := range p.Streams {
			audio = audio || s.Kind == StreamAudio
		}
		if !audio {
			return false
		}
	}
	return true
}

// detailCredits is how many credits come with the detail itself; the rest are paged.
const detailCredits = 20

// Item returns one item with its versions, tracks, first credits, tags, pictures and the
// viewer's state. An item the viewer may not see is not found.
func (d *DB) Item(ctx context.Context, v Viewer, publicID string) (ItemDetail, error) {
	var out ItemDetail
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	args := append(v.args(), sql.Named("id", publicID))
	err = scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns(acc)+`,
		ifnull(i.original_title, ''), ifnull(i.summary, ''), ifnull(i.tagline, ''), ifnull(i.edition, ''),
		ifnull(i.critic_rating, 0), i.available`+itemFrom+`i.public_id = @id AND `+acc.visible("i"), args...),
		&out.Item, &out.OriginalTitle, &out.Summary, &out.Tagline, &out.Edition, &out.CriticRating, &out.Available)
	if errors.Is(err, sql.ErrNoRows) {
		// A title that was merged into another opens the one that remains.
		var kept string
		if tx.QueryRowContext(ctx, mergedIntoSQL, KeyMerged, publicID).Scan(&kept) == nil {
			tx.Rollback()
			return d.Item(ctx, v, kept)
		}
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	items := []Item{out.Item}
	if err := loadImages(ctx, tx, items); err != nil {
		return out, err
	}
	if err := loadLeafCounts(ctx, tx, v, acc, items); err != nil {
		return out, err
	}
	out.Item = items[0]
	for _, ref := range []struct {
		id   int64
		into **ItemRef
	}{{out.ParentID, &out.Parent}, {out.RootID, &out.Root}} {
		if ref.id == 0 {
			continue
		}
		r := &ItemRef{}
		err := tx.QueryRowContext(ctx, `SELECT r.public_id, r.kind, r.title, r.index_number FROM items r WHERE r.id = ? AND `+acc.visible("r"), ref.id).
			Scan(&r.PublicID, &r.Kind, &r.Title, &r.Index)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		*ref.into = r
	}
	// A book or audiobook is tied to its series by a relation, from the series' side.
	if out.Kind == KindBook || out.Kind == KindAudiobook {
		r := &ItemRef{}
		err := tx.QueryRowContext(ctx, `SELECT s.public_id, s.kind, s.title, s.index_number FROM item_relations r CROSS JOIN items s ON s.id = r.item_id
			WHERE r.related_item_id = ? AND r.kind = ? AND `+acc.visible("s")+` ORDER BY r.item_id LIMIT 1`, out.ID, RelationSeriesMember).
			Scan(&r.PublicID, &r.Kind, &r.Title, &r.Index)
		switch {
		case err == nil:
			out.Series = r
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	if out.Tags, err = queryAll(ctx, tx, `SELECT t.public_id, t.kind, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id = ? AND t.kind <> 'mood' ORDER BY t.kind, t.name`, []any{out.ID}, func(t *Tag) []any { return []any{&t.PublicID, &t.Kind, &t.Name} }); err != nil {
		return out, err
	}
	if out.ExternalIDs, err = queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`, []any{out.ID},
		func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} }); err != nil {
		return out, err
	}
	if out.Credits, err = credits(ctx, tx, out.ID, CreditsAll, 0, detailCredits); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM credits WHERE item_id = ?`, out.ID).Scan(&out.CreditTotal); err != nil {
		return out, err
	}
	out.Versions, err = versions(ctx, tx, out.ID)
	if out.Kind == KindAudiobook && len(out.Versions) > 0 {
		out.DurationMS = out.Versions[0].DurationMS
	}
	return out, err
}

// queryAll runs a query and scans every row into a T.
func queryAll[T any](ctx context.Context, tx *sql.Tx, query string, args []any, dest func(*T) []any) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var row T
		if err := rows.Scan(dest(&row)...); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Which of an item's credits to list.
const (
	CreditsAll  = ""
	CreditsCast = "cast" // the people seen or heard: actors, guests, narrators and artists
	CreditsCrew = "crew" // everyone else
)

const castRoles = `(1, 7, 8, 9)` // RoleActor, RoleNarrator, RoleArtist, RoleGuest

// creditsPart is the condition for one part of the credits, as " AND ..." or nothing.
func creditsPart(part string) (string, error) {
	switch part {
	case CreditsAll:
		return "", nil
	case CreditsCast:
		return ` AND c.role IN ` + castRoles, nil
	case CreditsCrew:
		return ` AND c.role NOT IN ` + castRoles, nil
	}
	return "", invalid("Ask for the cast or the crew.")
}

func credits(ctx context.Context, tx *sql.Tx, itemID int64, part string, offset, limit int) ([]Credit, error) {
	type creditRow struct {
		Credit
		imageID sql.NullInt64
		hash    sql.NullString
	}
	only, err := creditsPart(part)
	if err != nil {
		return nil, err
	}
	rows, err := queryAll(ctx, tx, `SELECT p.public_id, p.name, c.role, ifnull(c.character, ''), img.id, img.file_hash
		FROM credits c JOIN people p ON p.id = c.person_id
		LEFT JOIN images img ON img.person_id = p.id AND img.role = 7 AND img.selected = 1
		WHERE c.item_id = ?`+only+` ORDER BY c.position LIMIT ? OFFSET ?`, []any{itemID, limit, offset},
		func(c *creditRow) []any {
			return []any{&c.PersonID, &c.Name, &c.Role, &c.Character, &c.imageID, &c.hash}
		})
	out := make([]Credit, len(rows))
	for n, row := range rows {
		out[n] = row.Credit
		if row.imageID.Valid {
			out[n].Image = &Image{ID: row.imageID.Int64, Role: ImagePortrait, FileHash: row.hash.String}
		}
	}
	return out, err
}

// Credits pages through an item's cast and crew in billing order, all of them or one part.
// offset is how many to skip; a cast list is short and never reorders under a reader, so a
// plain position is enough. The total is of the part asked for.
func (d *DB) Credits(ctx context.Context, v Viewer, itemID, part string, offset, limit int) ([]Credit, int, error) {
	only, err := creditsPart(part)
	if err != nil {
		return nil, 0, err
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, 0, err
	}
	var id int64
	var total int
	err = tx.QueryRowContext(ctx, `SELECT i.id, (SELECT count(*) FROM credits c WHERE c.item_id = i.id`+only+`) FROM items i
		WHERE i.public_id = ? AND `+acc.visible("i"), itemID).Scan(&id, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}
	out, err := credits(ctx, tx, id, part, max(offset, 0), limit)
	return out, total, err
}

// versions are the versions of an item that can be played: one with a missing file is not offered.
func versions(ctx context.Context, tx *sql.Tx, itemID int64) ([]Version, error) {
	out, err := queryAll(ctx, tx, `SELECT id, public_id, ifnull(name, ''), ifnull(edition, ''), ifnull(container, ''), ifnull(duration_ms, 0),
		ifnull(bitrate, 0), ifnull(size, 0), ifnull(width, 0), ifnull(height, 0), ifnull(video_codec, ''), ifnull(audio_codec, ''),
		ifnull(audio_channels, 0), ifnull(hdr, '') FROM media WHERE item_id = ? AND NOT EXISTS (SELECT 1 FROM media_parts p
		JOIN files f ON f.id = p.file_id WHERE p.media_id = media.id AND f.missing_since_at IS NOT NULL) ORDER BY position, id`, []any{itemID},
		func(m *Version) []any {
			return []any{&m.id, &m.PublicID, &m.Name, &m.Edition, &m.Container, &m.DurationMS, &m.Bitrate, &m.Size, &m.Width,
				&m.Height, &m.VideoCodec, &m.AudioCodec, &m.AudioChannels, &m.HDR}
		})
	if err != nil {
		return nil, err
	}
	for n := range out {
		m := &out[n]
		if m.Parts, err = queryAll(ctx, tx, `SELECT f.id, f.name, f.size, p.start_ms, p.end_ms, CASE WHEN p.end_ms IS NOT NULL THEN p.end_ms - ifnull(p.start_ms, 0)
			WHEN f.duration_ms > 0 THEN max(f.duration_ms - ifnull(p.start_ms, 0), 0) END, json_extract(f.extra, '$.packets.lengthUs') FROM media_parts p
			JOIN files f ON f.id = p.file_id WHERE p.media_id = ? ORDER BY p.position`, []any{m.id},
			func(p *Part) []any {
				return []any{&p.FileID, &p.Name, &p.Size, &p.StartMS, &p.EndMS, &p.DurationMS, &p.packetsUS}
			}); err != nil {
			return nil, err
		}
		for p := range m.Parts {
			if m.Parts[p].Streams, err = streams(ctx, tx, m.Parts[p].FileID); err != nil {
				return nil, err
			}
		}
		placeParts(m.Parts)
		if len(m.Parts) > 1 && packetParts(m.Parts) {
			var total int64
			for _, part := range m.Parts {
				total += *part.packetsUS
			}
			m.DurationMS = (total + 500) / 1000
		}
		if m.Markers, err = queryAll(ctx, tx, `SELECT kind, source, start_ms, end_ms, ifnull(title, ''),confidence,provenance FROM media_markers
			WHERE media_id = ? AND dismissed = 0 ORDER BY start_ms`, []any{m.id},
			func(k *Marker) []any {
				return []any{&k.Kind, &k.Source, &k.StartMS, &k.EndMS, &k.Title, &k.Confidence, &k.Provenance}
			}); err != nil {
			return nil, err
		}
	}
	if err := projectSegmentMarkers(ctx, tx, itemID, out); err != nil {
		return nil, err
	}
	return out, nil
}

func streams(ctx context.Context, tx *sql.Tx, fileID int64) ([]Stream, error) {
	return queryAll(ctx, tx, `SELECT kind, ifnull(codec, ''), ifnull(profile, ''), ifnull(language, ''), ifnull(title, ''), is_default,
		is_forced, is_hearing_impaired, ifnull(bitrate, 0), ifnull(width, 0), ifnull(height, 0), ifnull(frame_rate, 0),
		ifnull(bit_depth, 0), ifnull(hdr, ''), ifnull(channels, 0), ifnull(channel_layout, ''), ifnull(sample_rate, 0)
		FROM media_streams WHERE file_id = ? ORDER BY stream_index`, []any{fileID},
		func(s *Stream) []any {
			return []any{&s.Kind, &s.Codec, &s.Profile, &s.Language, &s.Title, &s.Default, &s.Forced, &s.HearingImpaired, &s.Bitrate,
				&s.Width, &s.Height, &s.FrameRate, &s.BitDepth, &s.HDR, &s.Channels, &s.ChannelLayout, &s.SampleRate}
		})
}

// Person is someone with credits.
type Person struct {
	PublicID   string
	Name       string
	Biography  string
	BirthDate  string
	DeathDate  string
	Birthplace string
	Images     []Image
	// Titles is how many items the viewer may see that credit this person.
	Titles int
	// CrewTitles counts those items with any credit other than acting, each title once.
	CrewTitles int
	// Roles are the person's roles on those items, most titles first: a person page's groups.
	Roles      []PersonRole
	KindCounts map[string]int
	KnownFor   []Item
}

// PersonRole is one role a person has, and on how many titles the viewer may see.
type PersonRole struct {
	Role   CreditRole
	Titles int
}

// Person returns a person the viewer may know of: one credited on at least one item the viewer
// may see. Their titles are listed with Items and its Person filter.
func (d *DB) Person(ctx context.Context, v Viewer, publicID string) (Person, error) {
	var p Person
	var id int64
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return p, err
	}
	err = tx.QueryRowContext(ctx, `SELECT p.id, p.public_id, p.name, ifnull(p.biography, ''), ifnull(p.birth_date, ''),
		ifnull(p.death_date, ''), ifnull(p.birthplace, ''),
		(SELECT count(DISTINCT i.id) FROM credits c JOIN items i ON i.id = c.item_id WHERE c.person_id = p.id AND `+acc.visible("i")+`),
		(SELECT count(DISTINCT i.id) FROM credits c JOIN items i ON i.id = c.item_id
		 WHERE c.person_id = p.id AND c.role NOT IN (`+itoa(int64(RoleActor))+`, `+itoa(int64(RoleGuest))+`) AND `+acc.visible("i")+`)
		FROM people p WHERE p.public_id = ?`, publicID).
		Scan(&id, &p.PublicID, &p.Name, &p.Biography, &p.BirthDate, &p.DeathDate, &p.Birthplace, &p.Titles, &p.CrewTitles)
	if errors.Is(err, sql.ErrNoRows) || err == nil && p.Titles == 0 {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Images, err = queryAll(ctx, tx, `SELECT id, role, ifnull(file_hash, ''), ifnull(width, 0), ifnull(height, 0), ifnull(placeholder, '')
		FROM images WHERE person_id = ? AND selected = 1`, []any{id},
		func(i *Image) []any { return []any{&i.ID, &i.Role, &i.FileHash, &i.Width, &i.Height, &i.Placeholder} })
	if err != nil {
		return p, err
	}
	p.Roles, err = queryAll(ctx, tx, `SELECT c.role, count(DISTINCT i.id) FROM credits c JOIN items i ON i.id = c.item_id
		WHERE c.person_id = ? AND `+acc.visible("i")+` GROUP BY c.role ORDER BY 2 DESC, c.role`, []any{id},
		func(r *PersonRole) []any { return []any{&r.Role, &r.Titles} })
	if err != nil {
		return p, err
	}
	type kindCount struct {
		Kind  ItemKind
		Count int
	}
	counts, err := queryAll(ctx, tx, `SELECT i.kind, count(DISTINCT i.id) FROM credits c JOIN items i ON i.id = c.item_id
		WHERE c.person_id = ? AND `+acc.visible("i")+` GROUP BY i.kind`, []any{id},
		func(k *kindCount) []any { return []any{&k.Kind, &k.Count} })
	if err != nil {
		return p, err
	}
	p.KindCounts = map[string]int{}
	for _, k := range counts {
		p.KindCounts[k.Kind.String()] = k.Count
	}
	// Crew is stored before cast: acting's billing position counts only acting credits.
	// Directors and writers (including creators) rank with the lead; each title appears once.
	p.KnownFor, err = queryAll(ctx, tx, `SELECT `+itemColumns(acc)+` FROM items i JOIN credits c ON c.item_id = i.id`+stateJoin+`
		c.person_id = @person AND i.kind IN (1, 2, 6, 8, 9, 10, 11) AND `+acc.visible("i")+` GROUP BY i.id
		ORDER BY min(CASE WHEN c.role IN (@director, @writer) THEN 0 WHEN c.role IN (@actor, @guest) THEN
			(SELECT count(*) FROM credits prior WHERE prior.item_id = c.item_id AND prior.position < c.position AND prior.role IN (@actor, @guest))
			ELSE c.position END),
		i.community_rating DESC, i.year DESC, i.id LIMIT 12`,
		[]any{sql.Named("viewer_profile", v.ProfileID), sql.Named("person", id),
			sql.Named("director", RoleDirector), sql.Named("writer", RoleWriter), sql.Named("actor", RoleActor), sql.Named("guest", RoleGuest)},
		func(it *Item) []any { return it.dest() })
	if err != nil {
		return p, err
	}
	if err := loadImages(ctx, tx, p.KnownFor); err != nil {
		return p, err
	}
	return p, loadLeafCounts(ctx, tx, v, acc, p.KnownFor)
}
