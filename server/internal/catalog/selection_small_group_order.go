package catalog

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type smallOrderValue struct {
	i               int64
	f               float64
	s               string
	typ             uint8 // 0 NULL, 1 integer, 2 real, 3 text
	nocase, reverse bool
}
type smallOrderKey struct {
	values  [12]smallOrderValue
	n       int
	present bool
}

func (k *smallOrderKey) integer(v int64) { k.values[k.n] = smallOrderValue{i: v, typ: 1}; k.n++ }
func (k *smallOrderKey) text(v string, nocase bool) {
	k.values[k.n] = smallOrderValue{s: v, typ: 3, nocase: nocase}
	k.n++
}
func (k *smallOrderKey) real(v sql.NullFloat64) {
	k.values[k.n] = smallOrderValue{f: v.Float64}
	if v.Valid {
		k.values[k.n].typ = 2
	}
	k.n++
}
func smallOrderCompare(a, b smallOrderKey) int {
	for i := 0; i < min(a.n, b.n); i++ {
		x, y := a.values[i], b.values[i]
		c := 0
		if x.typ == 0 || y.typ == 0 {
			c = int(x.typ) - int(y.typ)
		} else {
			switch x.typ {
			case 1:
				if x.i < y.i {
					c = -1
				} else if x.i > y.i {
					c = 1
				}
			case 2:
				if x.f < y.f {
					c = -1
				} else if x.f > y.f {
					c = 1
				}
			case 3:
				left, right := x.s, y.s
				if x.nocase {
					c = smallOrderNoCase(left, right)
				} else {
					c = strings.Compare(left, right)
				}
			}
		}
		if c != 0 {
			if x.reverse {
				return -c
			}
			return c
		}
	}
	return a.n - b.n
}

// SQLite NOCASE folds ASCII and stops its byte comparison at a shared NUL,
// then breaks that equality by the original byte lengths. Go's lowercased
// string comparison alone is observably different for legal stored NULs.
func smallOrderNoCase(a, b string) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		if x == 0 {
			break
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

type smallGroupOrder struct {
	field, value, direction                                  string
	root                                                     int64
	kind                                                     compactcatalog.Kind
	stage, index                                             int
	member                                                   selectionBatchMemberCursor
	item, afterEntry, entry, album, book, afterSong, ordinal int64
	artistMembers                                            selectionBatchMemberCursor
	key                                                      smallOrderKey
	keys                                                     []smallOrderKey
	release                                                  string
	role                                                     int64
}

// NewSmallGroupOrder consumes only complete prepared identities. It compares
// native source tuples instead of materialising ROW_NUMBER/GROUP BY. Taking
// each entity's minimum tuple is order-equivalent to minimum source ordinal.
func NewSmallGroupOrder(ids []int64, target *BrowseNode, direction, basis string) (*SmallSelectionOrder, error) {
	p, err := newSmallSelectionOrder(ids, basis)
	if err != nil {
		return nil, err
	}
	if target == nil || target.Operator != "equals" || direction != "asc" && direction != "desc" {
		return nil, ErrSelectionBatchUnsupported
	}
	value, ok := target.Value.(string)
	if !ok {
		return nil, ErrSelectionBatchUnsupported
	}
	switch target.Field {
	case "collection", "list", "memberOf", "episodeGroup":
	default:
		return nil, ErrSelectionBatchUnsupported
	}
	p.group = &smallGroupOrder{field: target.Field, value: value, direction: direction, keys: make([]smallOrderKey, len(ids))}
	if p.Bytes() > SelectionBatchMaximumBytes {
		return nil, ErrSelectionBatchCapacity
	}
	return p, nil
}
func (g *smallGroupOrder) bytes() int {
	n := int(unsafe.Sizeof(*g)) + len(g.field) + len(g.value) + len(g.direction) + len(g.release) + cap(g.keys)*int(unsafe.Sizeof(smallOrderKey{}))
	for _, k := range g.keys {
		for _, v := range k.values {
			n += len(v.s)
		}
	}
	for _, v := range g.key.values {
		n += len(v.s)
	}
	return n
}
func (g *smallGroupOrder) clone() *smallGroupOrder { n := *g; n.keys = slices.Clone(g.keys); return &n }
func (g *smallGroupOrder) accept() {
	g.key.present = true
	if !g.keys[g.index].present || smallOrderCompare(g.key, g.keys[g.index]) < 0 {
		g.keys[g.index] = g.key
	}
	g.key = smallOrderKey{}
	g.stage = 1
}
func (g *smallGroupOrder) skip() { g.key = smallOrderKey{}; g.stage = 1 }

// GroupCommandKeys preserves the legacy Playlist command semantics: source
// order follows direction, and displayed identity ties are ALWAYS ascending.
// Native tuples are order-equivalent to ord.pos; no source-wide ordinal census
// is needed. The caller's lease must cover the encoded keys through capture.
func (p *SmallSelectionOrder) GroupCommandKeys(reserve func(int64) error) (map[int64]string, error) {
	if !p.Complete() || p.group == nil {
		return nil, ErrSelectionBatchIncomplete
	}
	bound := int64(1024*len(p.ids) + 65536)
	for _, key := range p.group.keys {
		for _, value := range key.values {
			bound += int64(8 * len(value.s))
		}
	}
	if reserve != nil {
		if err := reserve(bound); err != nil {
			return nil, err
		}
	}
	out := make(map[int64]string, len(p.ids))
	for i, id := range p.ids {
		key := ""
		for _, value := range p.group.keys[i].values[:p.group.keys[i].n] {
			desc := (p.group.direction == "desc") != value.reverse
			var raw any
			switch value.typ {
			case 1:
				raw = value.i
			case 2:
				raw = value.f
			case 3:
				raw = value.s
			}
			if value.typ == 3 && value.nocase {
				text := value.s
				if at := strings.IndexByte(text, 0); at >= 0 {
					text = text[:at]
				}
				key += bulkOrderValue(asciiFold(text), desc) + bulkOrderValue(int64(len(value.s)), desc)
			} else {
				key += bulkOrderValue(raw, desc)
			}
		}
		out[id] = key + bulkOrderValue(id, false) + "V"
	}
	return out, nil
}

func (s *Service) smallGroupUnit(p *SmallSelectionOrder, reserve func(int64) error) error {
	g := p.group
	switch g.stage {
	case 0:
		if len(p.ids) == 0 {
			p.done = true
			return nil
		}
		value := g.value
		if g.field == "episodeGroup" {
			var ok bool
			value, _, ok = splitEpisodeGroup(value)
			if !ok {
				return ErrBrowseGroupOrder
			}
		}
		var kind int
		var err error
		if g.field == "list" {
			err = s.read().QueryRow(`SELECT id FROM catalog_playlists WHERE token=? AND deleted=0`, value).Scan(&g.root)
		} else {
			err = s.read().QueryRow(`SELECT id,kind FROM catalog_entities WHERE public_id=pid_blob(?)`, value).Scan(&g.root, &kind)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSelectionBatchStale
		}
		if err != nil {
			return err
		}
		g.kind = compactcatalog.Kind(kind)
		g.stage = 1
		return nil
	case 1:
		if g.index == len(p.ids) {
			p.ordered = slices.Clone(p.ids)
			positions := make(map[int64]int, len(p.ids))
			for i, id := range p.ids {
				positions[id] = i
				if !g.keys[i].present {
					return ErrSelectionBatchStale
				}
			}
			slices.SortFunc(p.ordered, func(a, b int64) int {
				c := smallOrderCompare(g.keys[positions[a]], g.keys[positions[b]])
				if c == 0 {
					if a < b {
						c = -1
					} else if a > b {
						c = 1
					}
				}
				if g.direction == "desc" {
					c = -c
				}
				return c
			})
			p.done = true
			return nil
		}
		done, ready, item, err := s.selectionBatchNextMember(p.ids[g.index], &g.member)
		if err != nil {
			return err
		}
		if done {
			g.index++
			g.member = selectionBatchMemberCursor{}
			return nil
		}
		if ready {
			g.item = item
			g.key = smallOrderKey{}
			g.afterEntry = 0
			g.stage = 2
		}
		return nil
	case 2:
		return s.smallGroupMember(g, reserve)
	case 3: // Playlist's item index is the raw source, including other lists.
		var owner int64
		err := s.read().QueryRow(`SELECT id,playlist_id FROM catalog_playlist_entries INDEXED BY catalog_playlist_entries_item WHERE item_id=? AND id>? ORDER BY id LIMIT 1`, g.item, g.afterEntry).Scan(&g.entry, &owner)
		if errors.Is(err, sql.ErrNoRows) {
			g.skip()
			return nil
		}
		if err != nil {
			return err
		}
		g.afterEntry = g.entry
		if owner == g.root {
			g.stage = 4
		}
		return nil
	case 4:
		key, ok, err := s.smallOrderText(`SELECT order_key AS v FROM catalog_playlist_entries WHERE id=?`, []any{g.entry}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSelectionBatchStale
		}
		g.key = smallOrderKey{}
		g.key.text(key, false)
		g.key.integer(g.entry)
		g.key.present = true
		if !g.keys[g.index].present || smallOrderCompare(g.key, g.keys[g.index]) < 0 {
			g.keys[g.index] = g.key
		}
		g.key = smallOrderKey{}
		g.stage = 3
		return nil
	case 5: // Artist appearance membership, raw credit before album predicate.
		var song, album int64
		err := s.read().QueryRow(`SELECT song_id FROM catalog_song_artists INDEXED BY catalog_song_artists_artist WHERE artist_id=? AND song_id>? ORDER BY song_id LIMIT 1`, g.root, g.afterSong).Scan(&song)
		if errors.Is(err, sql.ErrNoRows) {
			g.skip()
			return nil
		}
		if err != nil {
			return err
		}
		g.afterSong = song
		err = s.read().QueryRow(`SELECT album_id FROM catalog_songs WHERE entity_id=?`, song).Scan(&album)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if album == g.album {
			g.stage = 6
		}
		return nil
	case 6:
		return s.smallGroupArtistKey(g, reserve)
	case 7: // Compilation type inventory has at most 64 rows by baseline.
		var ordinal int64
		err := s.read().QueryRow(`SELECT ordinal FROM mb_release_group_types WHERE release_revision=? AND ordinal>? ORDER BY ordinal LIMIT 1`, g.release, g.ordinal).Scan(&ordinal)
		if errors.Is(err, sql.ErrNoRows) {
			g.key.values[0].i = g.role
			g.accept()
			return nil
		}
		if err != nil {
			return err
		}
		g.ordinal = ordinal
		g.stage = 8
		return nil
	case 8:
		value, ok, err := s.smallOrderText(`SELECT value AS v FROM mb_release_group_types WHERE release_revision=? AND ordinal=?`, []any{g.release, g.ordinal}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSelectionBatchStale
		}
		if asciiLower(value) == "compilation" {
			g.role = 3
		}
		g.stage = 7
		return nil
	case 9: // Author group expands all parts of every represented book.
		done, ready, item, err := s.selectionBatchNextMember(g.root, &g.artistMembers)
		if err != nil {
			return err
		}
		if done {
			g.skip()
			return nil
		}
		if !ready {
			return nil
		}
		var book int64
		err = s.read().QueryRow(`SELECT book_id FROM catalog_book_files WHERE entity_id=?`, item).Scan(&book)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if book == g.book {
			g.stage = 10
		}
		return nil
	case 10:
		return s.smallGroupAuthorKey(g, reserve)
	}
	return ErrSelectionBatchIncomplete
}

func (s *Service) smallGroupMember(g *smallGroupOrder, reserve func(int64) error) error {
	if g.field == "list" {
		g.stage = 3
		return nil
	}
	if g.field == "collection" {
		key, ok, err := s.smallOrderText(`SELECT order_key AS v FROM catalog_collection_members WHERE collection_id=? AND item_id=?`, []any{g.root, g.item}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			g.skip()
			return nil
		}
		g.key.text(key, false)
		g.key.integer(g.item)
		g.accept()
		return nil
	}
	var parent, season, number, disc, track, owner int64
	var err error
	switch g.kind {
	case compactcatalog.Show, compactcatalog.Season:
		err = s.read().QueryRow(`SELECT ep.show_id,COALESCE(ep.season_id,0),ep.number,COALESCE(sd.number,1) FROM catalog_episodes ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.entity_id=?`, g.item).Scan(&parent, &season, &number, &disc)
		if errors.Is(err, sql.ErrNoRows) {
			g.skip()
			return nil
		}
		if err != nil {
			return err
		}
		if g.kind == compactcatalog.Show {
			if parent != g.root {
				g.skip()
				return nil
			}
			if disc == 0 {
				g.key.integer(1)
			} else {
				g.key.integer(0)
			}
			g.key.integer(disc)
		} else if season != g.root {
			g.skip()
			return nil
		}
		g.key.integer(number)
		g.key.integer(g.item)
		g.accept()
		return nil
	case compactcatalog.Album, compactcatalog.Artist:
		err = s.read().QueryRow(`SELECT so.album_id,COALESCE(so.disc_number,1),COALESCE(so.track_number,0),a.artist_id FROM catalog_songs so JOIN catalog_albums a ON a.entity_id=so.album_id WHERE so.entity_id=?`, g.item).Scan(&g.album, &disc, &track, &owner)
		if errors.Is(err, sql.ErrNoRows) {
			g.skip()
			return nil
		}
		if err != nil {
			return err
		}
		if g.kind == compactcatalog.Album {
			if g.album != g.root {
				g.skip()
				return nil
			}
			g.key.integer(disc)
			g.key.integer(track)
			g.key.integer(g.item)
			g.accept()
			return nil
		}
		g.role = 0
		g.afterSong = 0
		g.stage = 6
		if owner != g.root {
			g.role = 4
			g.stage = 5
		}
		return nil
	case compactcatalog.Book, compactcatalog.Author:
		err = s.read().QueryRow(`SELECT book_id,COALESCE(disc_number,1),COALESCE(part_number,0) FROM catalog_book_files WHERE entity_id=?`, g.item).Scan(&g.book, &disc, &track)
		if errors.Is(err, sql.ErrNoRows) {
			g.skip()
			return nil
		}
		if err != nil {
			return err
		}
		if g.kind == compactcatalog.Book {
			if g.book != g.root {
				g.skip()
				return nil
			}
			g.key.integer(disc)
			g.key.integer(track)
			g.key.integer(g.item)
			g.accept()
			return nil
		}
		g.artistMembers = selectionBatchMemberCursor{}
		g.stage = 9
		return nil
	}
	return ErrBrowseGroupOrder
}

func (s *Service) smallGroupArtistKey(g *smallGroupOrder, reserve func(int64) error) error {
	var year, disc, track int64
	err := s.read().QueryRow(`SELECT a.year,COALESCE(so.disc_number,1),COALESCE(so.track_number,0) FROM catalog_entities a JOIN catalog_songs so ON so.album_id=a.id WHERE so.entity_id=?`, g.item).Scan(&year, &disc, &track)
	if err != nil {
		return err
	}
	title, ok, err := s.smallOrderText(`SELECT sort_key AS v FROM catalog_entities WHERE id=?`, []any{g.album}, reserve)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSelectionBatchStale
	}
	g.key = smallOrderKey{}
	g.key.integer(g.role)
	if year == 0 {
		g.key.integer(1)
	} else {
		g.key.integer(0)
	}
	g.key.integer(year)
	g.key.values[2].reverse = true
	g.key.text(title, true)
	g.key.integer(g.album)
	g.key.integer(disc)
	g.key.integer(track)
	g.key.integer(g.item)
	if g.role == 4 {
		g.accept()
		return nil
	}
	release, found, err := s.smallOrderText(`SELECT release_revision AS v FROM mb_album_links WHERE album_id=?`, []any{g.album}, reserve)
	if err != nil {
		return err
	}
	if !found {
		g.accept()
		return nil
	}
	g.release = release
	primary, ok, err := s.smallOrderText(`SELECT primary_type AS v FROM mb_release_evidence WHERE revision_id=?`, []any{release}, reserve)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSelectionBatchStale
	}
	g.role = 0
	if primary == "EP" {
		g.role = 1
	}
	if primary == "Single" {
		g.role = 2
	}
	g.ordinal = -1
	g.stage = 7
	return nil
}
func (s *Service) smallGroupAuthorKey(g *smallGroupOrder, reserve func(int64) error) error {
	series, ok, err := s.smallOrderText(`SELECT series AS v FROM catalog_books WHERE entity_id=?`, []any{g.book}, reserve)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSelectionBatchStale
	}
	title, ok, err := s.smallOrderText(`SELECT sort_key AS v FROM catalog_entities WHERE id=?`, []any{g.book}, reserve)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSelectionBatchStale
	}
	var place sql.NullFloat64
	var disc, part int64
	err = s.read().QueryRow(`SELECT CAST(NULLIF(bk.series_position,'') AS REAL),COALESCE(f.disc_number,1),COALESCE(f.part_number,0) FROM catalog_books bk JOIN catalog_book_files f ON f.book_id=bk.entity_id WHERE f.entity_id=?`, g.item).Scan(&place, &disc, &part)
	if err != nil {
		return err
	}
	g.key = smallOrderKey{}
	if series == "" {
		g.key.integer(1)
	} else {
		g.key.integer(0)
	}
	g.key.text(series, true)
	g.key.real(place)
	g.key.text(title, true)
	g.key.integer(g.book)
	g.key.integer(disc)
	g.key.integer(part)
	g.key.integer(g.item)
	g.accept()
	return nil
}
