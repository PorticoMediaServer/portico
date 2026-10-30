package dlna

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// Object ids are opaque to players. The first character says what the rest
// names: "0" is the root, "l" a library, "e" a catalogue entity's public id,
// "a" an audiobook author group's token.
const rootID = "0"

// maxBrowse bounds one Browse answer. A player asking for everything (a
// RequestedCount of 0) pages on with the TotalMatches it is given.
const maxBrowse = 500

// object is one DIDL-Lite entry.
type object struct {
	id, parent, title, class string
	container                bool
	children                 int
	year, track              int
	artist, album            string
	art                      string // public id whose artwork /dlna/art/ serves
	res                      *resource
}

// resource is the one original file an item plays.
type resource struct {
	token, container, video, audio string
	size                           int64
	duration                       float64
	width, height                  int
	sampleRate, bitDepth, channels int
}

// Catalogue kinds (catalog_kinds) the tree shows.
const (
	kindMovie    = 1
	kindShow     = 2
	kindSeason   = 3
	kindEpisode  = 4
	kindArtist   = 5
	kindAlbum    = 6
	kindSong     = 7
	kindBook     = 8
	kindBookFile = 9
)

// libraryKinds is the top level of each kind of library. Audiobook libraries
// list their author groups first (see libraryScope).
var libraryKinds = map[string]int{"movie": kindMovie, "tv": kindShow, "anime": kindShow, "music": kindArtist, "audiobook": kindBook}

// The DIDL class of each kind; anything else is a plain folder.
var classes = map[int]string{
	kindMovie: "object.item.videoItem.movie", kindEpisode: "object.item.videoItem",
	kindSong: "object.item.audioItem.musicTrack", kindBookFile: "object.item.audioItem",
	kindArtist: "object.container.person.musicArtist", kindAlbum: "object.container.album.musicAlbum", kindBook: "object.container.album.musicAlbum",
}

const folderClass = "object.container.storageFolder"

// artwork names the metadata namespace and role an entity's thumbnail comes
// from. A song or book file asks for its own cover, which falls back to its
// album's or book's.
var artwork = map[int][2]string{
	kindMovie: {"item", "poster"}, kindEpisode: {"item", "poster"}, kindShow: {"show", "poster"}, kindSeason: {"season", "poster"},
	kindAlbum: {"album", "cover"}, kindSong: {"item", "cover"}, kindBook: {"book", "cover"}, kindBookFile: {"item", "cover"},
}

// entitySelect reads entities the way the tree shows them, ignoring every
// viewer filter by design. A playable item is listed only with an available,
// local original (a remote .strm source has no file to serve here). The column
// list is kept apart from SELECT so it is never prepared as a statement alone.
const entitySelect = `SELECT ` + entityColumns

const entityColumns = `e.id,pid(e.public_id),e.kind,e.title,e.year,COALESCE(pid(p.public_id),''),COALESCE(p.title,''),COALESCE(gp.title,''),
 COALESCE(song.track_number,part.part_number,episode.number,0),
 COALESCE(a.token,''),COALESCE(a.container,''),COALESCE(a.video_codec,''),COALESCE(a.audio_codec,''),COALESCE(a.width,0),COALESCE(a.height,0),COALESCE(a.duration,0),COALESCE(a.size,0),COALESCE(mf.sample_rate,0),COALESCE(mf.bit_depth,0),COALESCE(mf.channels,0),
 CASE WHEN k.container=1 THEN (SELECT COUNT(*) FROM catalog_entities c WHERE c.parent_id=e.id AND c.retired=0) ELSE 0 END`

const entityFrom = ` FROM catalog_entities e
 JOIN catalog_kinds k ON k.id=e.kind
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 LEFT JOIN catalog_entities p ON p.id=e.parent_id
 LEFT JOIN catalog_entities gp ON gp.id=p.parent_id
 LEFT JOIN catalog_songs song ON song.entity_id=e.id
 LEFT JOIN catalog_book_files part ON part.entity_id=e.id
 LEFT JOIN catalog_episodes episode ON episode.entity_id=e.id
 LEFT JOIN catalog_seasons season ON season.entity_id=e.id
 LEFT JOIN catalog_assets a ON a.id=(SELECT x.id FROM catalog_asset_links l JOIN catalog_assets x ON x.id=l.asset_id
  WHERE l.entity_id=e.id AND l.part_index=0 AND l.available=1 AND x.available=1 AND x.container!='strm' ORDER BY l.asset_id LIMIT 1)
 LEFT JOIN music_technical_facts mf ON mf.asset_id=a.token AND mf.size=a.size AND mf.modified_ns=a.modified_ns
 WHERE e.retired=0 AND e.kind BETWEEN 1 AND 9 AND (k.playable=0 OR a.id IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)`

// scope is one container's children: a condition on e and their order.
type scope struct {
	where string
	args  []any
	order string
}

func scanEntity(rows interface{ Scan(...any) error }) (object, int64, int, error) {
	var o object
	var id int64
	var kind int
	var parentTitle, grandparentTitle string
	var r resource
	err := rows.Scan(&id, &o.id, &kind, &o.title, &o.year, &o.parent, &parentTitle, &grandparentTitle, &o.track,
		&r.token, &r.container, &r.video, &r.audio, &r.width, &r.height, &r.duration, &r.size, &r.sampleRate, &r.bitDepth, &r.channels, &o.children)
	if err != nil {
		return o, 0, 0, err
	}
	o.art = o.id
	o.id, o.parent = "e"+o.id, "e"+o.parent
	o.class = classes[kind]
	if o.class == "" {
		o.class = folderClass
	}
	o.container = r.token == ""
	if kind == kindSong || kind == kindBookFile {
		// An audio file's embedded cover probes as a video stream.
		r.video, r.width, r.height = "", 0, 0
	}
	if !o.container {
		o.res = &r
	}
	if _, ok := artwork[kind]; !ok {
		o.art = ""
	}
	switch kind {
	case kindSong:
		o.album, o.artist = parentTitle, grandparentTitle
	case kindAlbum:
		o.artist = parentTitle
	case kindBookFile:
		o.album = parentTitle
	}
	return o, id, kind, nil
}

// browse answers ContentDirectory Browse inside one read snapshot, so the
// count and the page agree.
func (s *Server) browse(r *http.Request, args map[string]string) ([]string, error) {
	id := args["ObjectID"]
	start, err1 := strconv.ParseUint(args["StartingIndex"], 10, 32)
	count, err2 := strconv.ParseUint(args["RequestedCount"], 10, 32)
	if args["StartingIndex"] == "" {
		start, err1 = 0, nil
	}
	if id == "" || len(id) > 128 || err1 != nil || err2 != nil {
		return nil, soapFault{errInvalidArgs, "Invalid Args"}
	}
	if count == 0 || count > maxBrowse {
		count = maxBrowse
	}
	var objects []object
	var total int
	var update uint32
	err := dbwork.WithReadSnapshot(r.Context(), s.DB, func(ctx context.Context) error {
		var err error
		switch args["BrowseFlag"] {
		case "BrowseMetadata":
			var o object
			if o, err = s.metadata(ctx, id); err == nil {
				objects, total = []object{o}, 1
			}
		case "BrowseDirectChildren":
			objects, total, err = s.children(ctx, id, int(start), int(count))
		default:
			err = soapFault{errInvalidArgs, "Invalid Args"}
		}
		if err == nil {
			update, err = s.systemUpdateID(ctx)
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = errNoObject
	}
	if err != nil {
		return nil, err
	}
	return []string{"Result", didl(baseURL(r), objects), "NumberReturned", strconv.Itoa(len(objects)), "TotalMatches", strconv.Itoa(total), "UpdateID", strconv.FormatUint(uint64(update), 10)}, nil
}

// systemUpdateID changes whenever any library's catalogue does, which tells a
// player its cached listing is stale.
func (s *Server) systemUpdateID(ctx context.Context) (uint32, error) {
	var sum int64
	err := dbwork.QueryRow(ctx, s.DB, `SELECT COALESCE(SUM(revision+order_revision),0) FROM library_revisions`).Scan(&sum)
	return uint32(sum), err
}

type library struct {
	id, name, kind string
	handle         int64
}

func (s *Server) libraries(ctx context.Context) ([]library, error) {
	rows, err := dbwork.Query(ctx, s.DB, `SELECT l.id,l.name,l.kind,cl.id FROM libraries l JOIN catalog_libraries cl ON cl.library_id=l.id AND cl.retired=0 ORDER BY l.position,l.name,l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []library
	for rows.Next() {
		var l library
		if err = rows.Scan(&l.id, &l.name, &l.kind, &l.handle); err != nil {
			return nil, err
		}
		if _, ok := libraryKinds[l.kind]; ok {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

func (s *Server) library(ctx context.Context, id string) (library, error) {
	all, err := s.libraries(ctx)
	if err != nil {
		return library{}, err
	}
	for _, l := range all {
		if l.id == id {
			return l, nil
		}
	}
	return library{}, errNoObject
}

// ungrouped are an audiobook library's books with no author group.
const ungrouped = ` AND NOT EXISTS(SELECT 1 FROM catalog_book_group_members m JOIN catalog_book_groups g ON g.id=m.group_id AND g.kind=1 AND g.retired=0 WHERE m.book_id=e.id)`

func libraryScope(l library) scope {
	where := `e.library_id=? AND e.kind=?`
	if l.kind == "audiobook" {
		where += ungrouped
	}
	return scope{where, []any{l.handle, libraryKinds[l.kind]}, `e.sort_key,e.id`}
}

func (s *Server) count(ctx context.Context, sc scope) (int, error) {
	var n int
	err := dbwork.QueryRow(ctx, s.DB, `SELECT COUNT(*)`+entityFrom+` AND `+sc.where, sc.args...).Scan(&n)
	return n, err
}

func (s *Server) list(ctx context.Context, sc scope, parent string, offset, limit int) ([]object, error) {
	if limit <= 0 {
		return nil, nil
	}
	args := append(append([]any{}, sc.args...), limit, offset)
	rows, err := dbwork.Query(ctx, s.DB, entitySelect+entityFrom+` AND `+sc.where+` ORDER BY `+sc.order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []object
	for rows.Next() {
		o, _, _, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		o.parent = parent
		out = append(out, o)
	}
	return out, rows.Err()
}

// groups are an audiobook library's authors, which the catalogue keeps as
// groups of books rather than entities with children.
func (s *Server) groups(ctx context.Context, l library, offset, limit int) ([]object, int, error) {
	var total int
	if err := dbwork.QueryRow(ctx, s.DB, `SELECT COUNT(*) FROM catalog_book_groups WHERE library_id=? AND kind=1 AND retired=0`, l.handle).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || offset >= total {
		return nil, total, nil
	}
	rows, err := dbwork.Query(ctx, s.DB, `SELECT token,name,member_count FROM catalog_book_groups WHERE library_id=? AND kind=1 AND retired=0 ORDER BY name COLLATE NOCASE,id LIMIT ? OFFSET ?`, l.handle, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []object
	for rows.Next() {
		o := object{parent: "l" + l.id, class: "object.container.person.musicArtist", container: true}
		if err = rows.Scan(&o.id, &o.title, &o.children); err != nil {
			return nil, 0, err
		}
		o.id = "a" + o.id
		out = append(out, o)
	}
	return out, total, rows.Err()
}

func (s *Server) libraryObject(ctx context.Context, l library) (object, error) {
	n, err := s.count(ctx, libraryScope(l))
	if err != nil {
		return object{}, err
	}
	if l.kind == "audiobook" {
		_, groups, err := s.groups(ctx, l, 0, 0)
		if err != nil {
			return object{}, err
		}
		n += groups
	}
	return object{id: "l" + l.id, parent: rootID, title: l.name, class: folderClass, container: true, children: n}, nil
}

// entityScope is the children of a container entity.
func entityScope(id int64, kind int) (scope, bool) {
	switch kind {
	case kindShow:
		// Seasons first, then any episode the show holds outside a season.
		return scope{`e.parent_id=? AND e.kind IN(3,4)`, []any{id}, `e.kind,COALESCE(season.number,episode.number,0),e.sort_key,e.id`}, true
	case kindSeason:
		return scope{`e.parent_id=? AND e.kind=4`, []any{id}, `COALESCE(episode.number,0),e.sort_key,e.id`}, true
	case kindArtist:
		return scope{`e.parent_id=? AND e.kind=6`, []any{id}, `e.year,e.sort_key,e.id`}, true
	case kindAlbum:
		return scope{`e.parent_id=? AND e.kind=7`, []any{id}, `COALESCE(song.disc_number,1),COALESCE(song.track_number,0),e.sort_key,e.id`}, true
	case kindBook:
		return scope{`e.parent_id=? AND e.kind=9`, []any{id}, `COALESCE(part.disc_number,1),COALESCE(part.part_number,0),e.sort_key,e.id`}, true
	}
	return scope{}, false
}

func (s *Server) entity(ctx context.Context, public string) (object, int64, int, error) {
	if _, ok := entityid.Decode(public); !ok {
		return object{}, 0, 0, errNoObject
	}
	return scanEntity(dbwork.QueryRow(ctx, s.DB, entitySelect+entityFrom+` AND e.public_id=pid_blob(?)`, public))
}

func (s *Server) children(ctx context.Context, id string, offset, limit int) ([]object, int, error) {
	switch {
	case id == rootID:
		all, err := s.libraries(ctx)
		if err != nil {
			return nil, 0, err
		}
		var out []object
		for i := offset; i < len(all) && len(out) < limit; i++ {
			o, err := s.libraryObject(ctx, all[i])
			if err != nil {
				return nil, 0, err
			}
			out = append(out, o)
		}
		return out, len(all), nil
	case id[0] == 'l':
		l, err := s.library(ctx, id[1:])
		if err != nil {
			return nil, 0, err
		}
		sc := libraryScope(l)
		total, err := s.count(ctx, sc)
		if err != nil {
			return nil, 0, err
		}
		if l.kind != "audiobook" {
			out, err := s.list(ctx, sc, id, offset, limit)
			return out, total, err
		}
		// Authors, then the books no author group holds.
		out, groups, err := s.groups(ctx, l, offset, limit)
		if err != nil {
			return nil, 0, err
		}
		books, err := s.list(ctx, sc, id, max(0, offset-groups), limit-len(out))
		return append(out, books...), groups + total, err
	case id[0] == 'a':
		var group int64
		var library string
		err := dbwork.QueryRow(ctx, s.DB, `SELECT g.id,cl.library_id FROM catalog_book_groups g JOIN catalog_libraries cl ON cl.id=g.library_id AND cl.retired=0 WHERE g.token=? AND g.kind=1 AND g.retired=0`, id[1:]).Scan(&group, &library)
		if err != nil {
			return nil, 0, err
		}
		sc := scope{`e.kind=8 AND e.id IN(SELECT book_id FROM catalog_book_group_members WHERE group_id=?)`, []any{group}, `e.sort_key,e.id`}
		total, err := s.count(ctx, sc)
		if err != nil {
			return nil, 0, err
		}
		out, err := s.list(ctx, sc, id, offset, limit)
		return out, total, err
	case id[0] == 'e':
		_, entity, kind, err := s.entity(ctx, id[1:])
		if err != nil {
			return nil, 0, err
		}
		sc, ok := entityScope(entity, kind)
		if !ok {
			return nil, 0, nil
		}
		total, err := s.count(ctx, sc)
		if err != nil {
			return nil, 0, err
		}
		out, err := s.list(ctx, sc, id, offset, limit)
		return out, total, err
	}
	return nil, 0, errNoObject
}

// metadata is BrowseMetadata: the object itself, with its real parent.
func (s *Server) metadata(ctx context.Context, id string) (object, error) {
	switch {
	case id == rootID:
		all, err := s.libraries(ctx)
		return object{id: rootID, parent: "-1", title: s.friendlyName(), class: folderClass, container: true, children: len(all)}, err
	case id[0] == 'l':
		l, err := s.library(ctx, id[1:])
		if err != nil {
			return object{}, err
		}
		return s.libraryObject(ctx, l)
	case id[0] == 'a':
		o := object{id: id, class: "object.container.person.musicArtist", container: true}
		err := dbwork.QueryRow(ctx, s.DB, `SELECT 'l'||cl.library_id,g.name,g.member_count FROM catalog_book_groups g JOIN catalog_libraries cl ON cl.id=g.library_id AND cl.retired=0 WHERE g.token=? AND g.kind=1 AND g.retired=0`, id[1:]).Scan(&o.parent, &o.title, &o.children)
		return o, err
	case id[0] == 'e':
		o, entity, kind, err := s.entity(ctx, id[1:])
		if err != nil {
			return o, err
		}
		if sc, ok := entityScope(entity, kind); ok {
			if o.children, err = s.count(ctx, sc); err != nil {
				return o, err
			}
		}
		if o.parent == "e" {
			// A top-level entity sits in its library, or a book in its author.
			var library, group string
			if err = dbwork.QueryRow(ctx, s.DB, `SELECT cl.library_id,COALESCE((SELECT g.token FROM catalog_book_group_members m JOIN catalog_book_groups g ON g.id=m.group_id AND g.kind=1 AND g.retired=0 WHERE m.book_id=e.id ORDER BY g.id LIMIT 1),'') FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, entity).Scan(&library, &group); err != nil {
				return o, err
			}
			o.parent = "l" + library
			if group != "" && kind == kindBook {
				o.parent = "a" + group
			}
		}
		return o, nil
	}
	return object{}, errNoObject
}
