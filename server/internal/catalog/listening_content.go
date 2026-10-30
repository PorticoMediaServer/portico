package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"
)

type ListeningTarget struct {
	LibraryID string `json:"libraryId"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
}
type ListeningJourney struct {
	Target            ListeningTarget `json:"target"`
	Actions           []string        `json:"actions"`
	Resume            *BookResume     `json:"resume,omitempty"`
	ResumeUnavailable bool            `json:"resumeUnavailable"`
}

type listeningSection struct {
	id, title, layout, kind, view, query string
	args                                 []any
	media                                bool
	// browseKind names the entity kind of a library-wide section. Such a
	// section pages by key over the maintained browse rows and counts from
	// maintained totals, never from the whole library; entity-scoped
	// sections (an artist's songs, a book's parts) are bounded by the entity.
	browseKind string
}

// Author navigation uses the same catalog identity as the browse pivot. Series
// retain their own group identity; group membership remains an internal key.
const listeningGroupNavigationID = `CASE g.kind WHEN 1 THEN (SELECT pid(author.public_id) FROM catalog_identities ai JOIN catalog_entities author ON author.public_id=ai.public_id WHERE ai.root=l.root AND ai.source_key=portico_author_key(g.name) AND author.library_id=g.library_id AND author.kind=13 AND author.retired=0) ELSE g.token END`

// A section's visibility predicate is applied before both count and page SQL.
// The correlated membership checks make an album, book or artist disappear when
// none of its items are visible to the viewer.
func listeningSectionVisibility(kind string, viewer Viewer) (string, []any) {
	item, args := viewer.itemVisibilitySQL("i.id")
	switch kind {
	case "song", "audiobook_file":
		return viewer.itemVisibilitySQL("projected.entity_id")
	case "chapter":
		return `EXISTS(SELECT 1 FROM catalog_book_chapters chapter JOIN catalog_entities i ON i.id=chapter.file_id WHERE chapter.file_id=projected.entity_id AND chapter.chapter_index=projected.chapter_index AND ` + item + `)`, args
	case "album":
		return `EXISTS(SELECT 1 FROM catalog_entities album JOIN catalog_songs member ON member.album_id=album.id JOIN catalog_entities i ON i.id=member.entity_id WHERE album.id=projected.entity_id AND ` + item + `)`, args
	case "artist":
		return `(EXISTS(SELECT 1 FROM catalog_entities artist CROSS JOIN catalog_albums a INDEXED BY catalog_albums_artist ON a.artist_id=artist.id CROSS JOIN catalog_songs member INDEXED BY catalog_songs_album_order ON member.album_id=a.entity_id CROSS JOIN catalog_entities i ON i.id=member.entity_id WHERE artist.id=projected.entity_id AND ` + item + `) OR EXISTS(SELECT 1 FROM catalog_entities artist CROSS JOIN catalog_song_artists sa INDEXED BY catalog_song_artists_artist ON sa.artist_id=artist.id CROSS JOIN catalog_entities i ON i.id=sa.song_id WHERE artist.id=projected.entity_id AND ` + item + `))`, append(append([]any{}, args...), args...)
	case "book":
		return `EXISTS(SELECT 1 FROM catalog_entities book JOIN catalog_book_files member ON member.book_id=book.id JOIN catalog_entities i ON i.id=member.entity_id WHERE book.id=projected.entity_id AND ` + item + `)`, args
	case "disc":
		return `EXISTS(SELECT 1 FROM catalog_entities album JOIN catalog_songs member ON member.album_id=album.id JOIN catalog_entities i ON i.id=member.entity_id WHERE album.id=projected.entity_id AND COALESCE(member.disc_number,1)=projected.disc_number AND ` + item + `)`, args
	case "author", "book_series", "context":
		return `EXISTS(SELECT 1 FROM catalog_book_groups g JOIN catalog_book_group_members m ON m.group_id=g.id JOIN catalog_book_files member ON member.book_id=m.book_id JOIN catalog_entities i ON i.id=member.entity_id WHERE g.id=projected.entity_id AND g.retired=0 AND ` + item + `)`, args
	}
	return "0", nil
}

// Resolve author/series names into stable server catalog navigation IDs. Never
// guess an author or series from a filename. Conflicting series evidence is absent.
func discTarget(id string) (string, int, error) {
	album, raw, ok := strings.Cut(id, ":disc:")
	disc, e := strconv.Atoi(raw)
	if !ok || album == "" || e != nil || disc < 0 || disc > 9999 || strconv.Itoa(disc) != raw {
		return "", 0, requestinput.New("invalid disc")
	}
	return album, disc, nil
}
func (s *Service) listeningContent(r ContentRequest, lib Library) (ContentEnvelope, error) {
	out := ContentEnvelope{Navigation: []ContentTab{}, Sorts: []ContentSort{}, Filters: []ContentFilter{}, Sections: []ContentSection{}}
	if r.View == "" {
		r.View = "browse"
	}
	r.Limit = pageLimit(r.Limit)
	if len(r.Q) > 100 {
		return out, requestinput.New("search prefix exceeds limit")
	}
	ordered := r.View == "album" || r.View == "disc" || r.View == "book" || r.View == "book_series"
	sortDefault := "title"
	if r.View == "album" || r.View == "disc" {
		sortDefault = "track"
	}
	if r.View == "book" {
		sortDefault = "part"
	}
	if r.View == "book_series" {
		sortDefault = "order"
	}
	if r.View == "discover" {
		sortDefault = "server"
	}
	if r.Sort == "" {
		r.Sort = sortDefault
	}
	if r.Direction == "" {
		r.Direction = "asc"
	}
	if r.Sort != sortDefault || r.Direction != "asc" && r.Direction != "desc" || ordered && r.Direction != "asc" || r.Category != "" || (ordered || r.View == "discover") && r.Q != "" {
		return out, requestinput.New("unsupported listening query")
	}
	if lib.Kind == "music" {
		out.Navigation = []ContentTab{{"discover", "library.discover", "discover"}, {"browse", "library.music", "browse"}, {"releases", "library.releases", "releases"}, {"songs", "library.songs", "songs"}}
	} else {
		out.Navigation = []ContentTab{{"discover", "library.discover", "discover"}, {"browse", "library.books", "browse"}, {"authors", "library.authors", "authors"}, {"series", "library.series", "series"}}
	}
	if lib.Kind == "audiobook" {
		if err := s.compactProjectionReady(30); err != nil {
			return out, err
		}
		if r.View == "book" {
			if err := s.compactProjectionReady(20); err != nil {
				return out, err
			}
		}
	}
	out.Scope = ContentScope{r.ServerID, lib.ID, lib.Kind, r.View, r.EntityID, r.ViewerFence}
	out.Heading = ContentHeading{Key: "library." + r.View, Fallback: lib.Name}
	out.Query = ContentQuery{r.Sort, r.Direction, "", r.Q, r.Limit, "none"}
	if !ordered && r.View != "discover" {
		out.Query.SearchMode = "title_prefix"
		out.Sorts = []ContentSort{{"title", "sort.title", []string{"asc", "desc"}}}
	}
	before, e := s.ContentRevision(lib.ID, r.Profile)
	if e != nil {
		return out, e
	}
	out.Revision = before
	if r.View == "discover" {
		if r.EntityID != "" || r.Cursor != "" && lib.Kind != "music" {
			return out, ErrCursor
		}
		if r.Cursor == "" {
			discovery, err := s.DiscoverWithRestrictions(lib.ID, r.Profile, r.Restrictions, r.Now)
			if err != nil {
				return out, err
			}
			for _, section := range discovery.Sections {
				if lib.Kind == "music" && section.ID == "recently_added" {
					continue
				}
				out.Sections = append(out.Sections, contentSection(section.ID, "rail", section.Title, section.Items, section.Total, ""))
			}
		}
		if lib.Kind == "music" {
			rows, err := s.musicDiscover(r, before)
			if err != nil {
				return out, err
			}
			out.Sections = append(out.Sections, rows...)
		}
		if lib.Kind == "music" {
			out.Listening = &ListeningJourney{Target: ListeningTarget{lib.ID, "library", lib.ID}, Actions: []string{"play", "shuffle", "enqueue"}}
		}
		after, err := s.ContentRevision(lib.ID, r.Profile)
		if err != nil {
			return out, err
		}
		if before != after {
			return out, ErrStaleContinuation
		}
		return out, nil
	}
	scope := cursorScope{Library: lib.ID, Profile: r.Profile, Viewer: r.ViewerFence, View: r.View, Entity: r.EntityID, Sort: r.Sort, Direction: r.Direction, Search: r.Q, Limit: r.Limit}
	sectionID, offset, keyed := "", 0, ""
	if r.Start < 0 || r.Start > 0 && r.Cursor != "" {
		return out, ErrContentStart
	}
	offset = r.Start
	if r.Cursor != "" {
		c, e := s.contentContinuation(r.Cursor, scope)
		if e != nil {
			return out, e
		}
		sectionID = c.Scope.Section
		if strings.HasPrefix(c.Value, "{") {
			// A library-wide section continues by key (see libraryListeningSection).
			keyed, offset = c.Value, 0
		} else if offset, e = strconv.Atoi(c.Value); e != nil || offset < 0 {
			return out, ErrCursor
		}
	}
	sections := []listeningSection{}
	musicItems := `SELECT pid(i.public_id) id,i.id entity_id,i.title,ar.title subtitle,printf('%08d:%08d:%020d',COALESCE(s.disc_number,1),COALESCE(s.track_number,0),i.id) ordering,0 n,0 chapter_index FROM catalog_songs s JOIN catalog_entities i ON i.id=s.entity_id JOIN catalog_entities a ON a.id=s.album_id JOIN catalog_albums al ON al.entity_id=a.id JOIN catalog_entities ar ON ar.id=al.artist_id JOIN catalog_libraries l ON l.id=i.library_id WHERE l.library_id=? AND i.retired=0 AND a.retired=0`
	books := `SELECT pid(b.public_id) id,b.id entity_id,b.title,details.author subtitle,printf('%020.6f:%s',CASE WHEN context.series_index>0 THEN context.series_index ELSE 999999 END,b.title) ordering,0 n,0 chapter_index FROM catalog_book_context context JOIN catalog_entities b ON b.id=context.book_id JOIN catalog_books details ON details.entity_id=b.id JOIN catalog_libraries l ON l.id=context.library_id WHERE l.library_id=? AND context.has_file=1 AND b.retired=0`
	albums := `SELECT pid(a.public_id) id,a.id entity_id,a.title,ar.title||CASE WHEN a.year>0 THEN ' · '||a.year ELSE '' END subtitle,a.sort_key ordering,0 n,a.year year,0 chapter_index FROM catalog_albums details JOIN catalog_entities a ON a.id=details.entity_id JOIN catalog_entities ar ON ar.id=details.artist_id JOIN catalog_libraries l ON l.id=a.library_id WHERE l.library_id=? AND a.retired=0 AND ar.retired=0 AND EXISTS(SELECT 1 FROM catalog_songs s WHERE s.album_id=a.id)`
	target := ListeningTarget{lib.ID, "library", lib.ID}
	switch r.View {
	case "browse", "discover", "releases", "songs", "authors", "series":
		if r.EntityID != "" {
			return out, errors.New("unexpected entity")
		}
		switch {
		case lib.Kind == "music" && (r.View == "browse" || r.View == "discover"):
			sections = append(sections, listeningSection{"artists", "Artists", "grid", "artist", "artist", `SELECT pid(ar.public_id) id,ar.id entity_id,ar.title,'' subtitle,ar.sort_key ordering,0 n,0 chapter_index FROM catalog_artists details JOIN catalog_entities ar ON ar.id=details.entity_id JOIN catalog_libraries l ON l.id=ar.library_id WHERE l.library_id=? AND ar.retired=0 AND (EXISTS(SELECT 1 FROM catalog_song_artists x WHERE x.artist_id=ar.id) OR EXISTS(SELECT 1 FROM catalog_albums a JOIN catalog_songs s ON s.album_id=a.entity_id WHERE a.artist_id=ar.id))`, []any{lib.ID}, false, "artist"})
		case lib.Kind == "music" && r.View == "releases":
			sections = append(sections, listeningSection{"releases", "Releases", "grid", "album", "album", albums, []any{lib.ID}, false, "album"})
		case lib.Kind == "music" && r.View == "songs":
			sections = append(sections, listeningSection{"songs", "Songs", "list", "song", "item", musicItems, []any{lib.ID}, true, "song"})
		case lib.Kind == "audiobook" && (r.View == "browse" || r.View == "discover"):
			sections = append(sections, listeningSection{"books", "Books", "grid", "book", "book", books, []any{lib.ID}, false, "book"})
		case lib.Kind == "audiobook" && (r.View == "authors" || r.View == "series"):
			kind, title := "author", "Authors"
			if r.View == "series" {
				kind, title = "book_series", "Series"
			}
			groupKind := 1
			if kind == "book_series" {
				groupKind = 2
			}
			query := `SELECT ` + listeningGroupNavigationID + ` id,g.id entity_id,g.name title,'' subtitle,g.name ordering,g.member_count n,0 chapter_index FROM catalog_book_groups g JOIN catalog_libraries l ON l.id=g.library_id WHERE l.library_id=? AND g.kind=? AND g.retired=0`
			args := []any{lib.ID, groupKind}
			if r.Viewer.EffectiveRestrictions().Active() {
				class, generation, err := s.bookGroupClassReady(lib.ID, r.Viewer.EffectiveRestrictions())
				if err != nil {
					return out, err
				}
				query = `SELECT ` + listeningGroupNavigationID + ` id,g.id entity_id,g.name title,'' subtitle,g.name ordering,COALESCE(vc.total,0) n,0 chapter_index FROM catalog_book_groups g JOIN catalog_libraries l ON l.id=g.library_id LEFT JOIN catalog_book_group_visible_counts vc ON vc.class_id=? AND vc.generation=? AND vc.group_id=g.id WHERE l.library_id=? AND g.kind=? AND g.retired=0`
				args = []any{class, generation, lib.ID, groupKind}
			}
			sections = append(sections, listeningSection{"groups", title, "grid", kind, kind, query, args, false, ""})
		default:
			return out, requestinput.New("view does not belong to this library")
		}
		if lib.Kind == "music" {
			out.Listening = &ListeningJourney{Target: target, Actions: []string{"play", "shuffle", "enqueue"}}
		}
	case "artist", "album", "disc":
		if lib.Kind != "music" {
			return out, sql.ErrNoRows
		}
		target.Kind, target.ID = r.View, r.EntityID
		entity, disc := r.EntityID, 0
		kind := r.View
		if kind == "disc" {
			entity, disc, e = discTarget(r.EntityID)
			if e != nil {
				return out, e
			}
			kind = "album"
		}
		actual, e := s.LibraryForAudioEntity(kind, entity)
		if e != nil {
			return out, e
		}
		if actual != lib.ID {
			return out, sql.ErrNoRows
		}
		title := ""
		if e = s.read().QueryRow(`SELECT e.title FROM catalog_entities e WHERE e.public_id=pid_blob(?)`, entity).Scan(&title); e != nil {
			return out, e
		}
		if r.View == "disc" {
			title += fmt.Sprintf(" · Disc %d", disc)
		}
		out.Heading = ContentHeading{Key: "entity.title", Fallback: title}
		out.Listening = &ListeningJourney{Target: target, Actions: []string{"play", "shuffle", "enqueue"}}
		if kind == "artist" {
			// The artist's songs and releases are sets found by index (their
			// albums, and the songs that credit them) and lead the query, so a
			// page reads the artist's rows, never the library's.
			artistID := `(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
			songSet := `(SELECT ss.entity_id id FROM catalog_albums aa INDEXED BY catalog_albums_artist CROSS JOIN catalog_songs ss INDEXED BY catalog_songs_album_order ON ss.album_id=aa.entity_id WHERE aa.artist_id=` + artistID + `
			 UNION SELECT sa.song_id FROM catalog_song_artists sa INDEXED BY catalog_song_artists_artist WHERE sa.artist_id=` + artistID + `) artist_set CROSS JOIN catalog_songs s ON s.entity_id=artist_set.id CROSS JOIN catalog_entities i ON i.id=s.entity_id`
			albumSet := `(SELECT aa.entity_id id FROM catalog_albums aa INDEXED BY catalog_albums_artist WHERE aa.artist_id=` + artistID + `
			 UNION SELECT ss.album_id FROM catalog_song_artists sa INDEXED BY catalog_song_artists_artist CROSS JOIN catalog_songs ss ON ss.entity_id=sa.song_id WHERE sa.artist_id=` + artistID + `) artist_set CROSS JOIN catalog_albums details ON details.entity_id=artist_set.id CROSS JOIN catalog_entities a ON a.id=details.entity_id`
			artistSongs := strings.Replace(musicItems, `FROM catalog_songs s JOIN catalog_entities i ON i.id=s.entity_id`, `FROM `+songSet, 1)
			artistAlbums := strings.Replace(albums, `FROM catalog_albums details JOIN catalog_entities a ON a.id=details.entity_id`, `FROM `+albumSet, 1)
			sections = append(sections, listeningSection{"songs", "Songs", "list", "song", "item", artistSongs, []any{entity, entity, lib.ID}, true, ""})
			sections = append(sections, listeningSection{"releases", "Releases & appearances", "grid", "album", "album", artistAlbums, []any{entity, entity, lib.ID}, false, ""})
		} else {
			if r.View == "album" {
				visible, visibleArgs := r.Viewer.itemVisibilitySQL("i.id")
				query := `SELECT pid(album.public_id)||':disc:'||COALESCE(s.disc_number,1) id,album.id entity_id,COALESCE(s.disc_number,1) disc_number,'Disc '||COALESCE(s.disc_number,1) title,'' subtitle,printf('%08d',COALESCE(s.disc_number,1)) ordering,count(*) n,0 chapter_index FROM catalog_songs s JOIN catalog_entities i ON i.id=s.entity_id JOIN catalog_entities album ON album.id=s.album_id WHERE album.public_id=pid_blob(?) AND ` + visible + ` GROUP BY album.id,COALESCE(s.disc_number,1)`
				sections = append(sections, listeningSection{"discs", "Discs", "list", "disc", "disc", query, append([]any{entity}, visibleArgs...), false, ""})
			}
			where := musicItems + ` AND a.public_id=pid_blob(?)`
			args := []any{lib.ID, entity}
			if r.View == "disc" {
				where += ` AND COALESCE(s.disc_number,1)=?`
				args = append(args, disc)
			}
			sections = append(sections, listeningSection{"songs", "Songs", "list", "song", "item", where, args, true, ""})
		}
	case "author", "book_series":
		if lib.Kind != "audiobook" {
			return out, sql.ErrNoRows
		}
		var title, key string
		if r.View == "author" {
			if e = s.read().QueryRow(`SELECT lower(trim(author.title)) FROM catalog_entities author JOIN catalog_libraries l ON l.id=author.library_id WHERE author.public_id=pid_blob(?) AND l.library_id=? AND author.kind=13 AND author.retired=0`, r.EntityID, lib.ID).Scan(&key); e != nil {
				return out, e
			}
			e = s.read().QueryRow(`SELECT g.name FROM catalog_book_groups g JOIN catalog_libraries l ON l.id=g.library_id WHERE l.library_id=? AND g.kind=1 AND g.name_key=? AND g.retired=0`, lib.ID, key).Scan(&title)
		} else {
			e = s.read().QueryRow(`SELECT g.name,g.name_key FROM catalog_book_groups g JOIN catalog_libraries l ON l.id=g.library_id WHERE g.token=? AND l.library_id=? AND g.kind=2 AND g.retired=0`, r.EntityID, lib.ID).Scan(&title, &key)
		}
		if e != nil {
			return out, e
		}
		out.Heading = ContentHeading{Key: "entity.title", Fallback: title}
		member, memberArgs := ` AND context.author_key=?`, []any{lib.ID, key}
		if r.View == "book_series" {
			member, memberArgs = ` AND context.series_key=?`, []any{lib.ID, key}
		}
		sections = append(sections, listeningSection{"books", "Books", "grid", "book", "book", books + member, memberArgs, false, ""})
		if r.View == "book_series" {
			// collections 41c/45: a series page names its author as a link to the
			// author destination. The author navigation id is the same catalog
			// identity the book page's own context section uses. A restricted
			// viewer sees only authors of this series' own books that are visible
			// in the published class: an author who owns a visible book elsewhere
			// does not qualify through this series' hidden book.
			query := `SELECT ` + listeningGroupNavigationID + ` id,g.id entity_id,g.name title,'author' subtitle,g.name ordering,0 n,0 chapter_index FROM catalog_book_groups series JOIN catalog_book_group_members member ON member.group_id=series.id JOIN catalog_book_context context ON context.book_id=member.book_id AND context.has_file=1 JOIN catalog_book_group_members authorship ON authorship.book_id=member.book_id JOIN catalog_book_groups g ON g.id=authorship.group_id AND g.kind=1 AND g.retired=0 JOIN catalog_libraries l ON l.id=g.library_id WHERE series.token=? AND l.library_id=? AND series.kind=2 AND series.retired=0`
			args := []any{r.EntityID, lib.ID}
			if r.Viewer.EffectiveRestrictions().Active() {
				class, generation, err := s.bookGroupClassReady(lib.ID, r.Viewer.EffectiveRestrictions())
				if err != nil {
					return out, err
				}
				query += ` AND EXISTS(SELECT 1 FROM catalog_book_visible_counts visible WHERE visible.class_id=? AND visible.generation=? AND visible.book_id=member.book_id)`
				args = append(args, class, generation)
			}
			sections = append(sections, listeningSection{"context", "Author", "list", "context", "", query + ` GROUP BY g.id`, args, false, ""})
		}
	case "book":
		if lib.Kind != "audiobook" {
			return out, sql.ErrNoRows
		}
		actual, e := s.LibraryForAudioEntity("book", r.EntityID)
		if e != nil {
			return out, e
		}
		if actual != lib.ID {
			return out, sql.ErrNoRows
		}
		var title string
		if e = s.read().QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=8 AND retired=0`, r.EntityID).Scan(&title); e != nil {
			return out, e
		}
		out.Heading = ContentHeading{Key: "entity.title", Fallback: title}
		out.Listening = &ListeningJourney{Target: ListeningTarget{lib.ID, "book", r.EntityID}, Actions: []string{"play", "enqueue"}}
		var resume BookResume
		resumeVisibility, resumeArgs := r.Viewer.itemVisibilitySQL("file.id")
		var resumeMillis int64
		e = s.read().QueryRow(`SELECT pid(file.public_id),r.position FROM book_resume r JOIN catalog_entities book ON book.id=r.book_id AND book.kind=8 JOIN catalog_book_files f ON f.book_id=book.id JOIN catalog_entities file ON file.id=f.entity_id AND file.id=r.item_id WHERE book.public_id=pid_blob(?) AND r.profile_id=? AND `+resumeVisibility, append([]any{r.EntityID, r.Profile}, resumeArgs...)...).Scan(&resume.ItemID, &resumeMillis)
		if e == nil {
			resume.PositionSeconds = float64(resumeMillis) / 1000
			out.Listening.Resume = &resume
			var available bool
			if e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_item_availability a JOIN catalog_entities file ON file.id=a.entity_id WHERE file.public_id=pid_blob(?) AND a.available=1 AND a.retired=0)`, resume.ItemID).Scan(&available); e != nil {
				return out, e
			}
			out.Listening.ResumeUnavailable = !available
		} else if e != sql.ErrNoRows {
			return out, e
		}
		sections = append(sections, listeningSection{"chapters", "Chapters", "list", "chapter", "item", `SELECT pid(file.public_id)||':'||c.chapter_index id,file.id entity_id,c.chapter_index,pid(file.public_id) subtitle,c.title title,printf('%08d:%08d:',COALESCE(f.disc_number,1),COALESCE(f.part_number,0))||file.sort_key||printf(':%020d:%08d',file.id,c.chapter_index) ordering,0 n FROM catalog_book_chapters c JOIN catalog_entities file ON file.id=c.file_id JOIN catalog_book_files f ON f.entity_id=file.id JOIN catalog_entities book ON book.id=f.book_id WHERE book.public_id=pid_blob(?) AND book.retired=0 AND file.retired=0`, []any{r.EntityID}, false, ""})
		sections = append(sections, listeningSection{"files", "Book parts", "list", "audiobook_file", "item", `SELECT pid(file.public_id) id,file.id entity_id,file.title,'' subtitle,printf('%08d:%08d:',COALESCE(f.disc_number,1),COALESCE(f.part_number,0))||file.sort_key||printf(':%020d',file.id) ordering,0 n,0 chapter_index FROM catalog_entities book JOIN catalog_book_files f ON f.book_id=book.id JOIN catalog_entities file ON file.id=f.entity_id WHERE book.public_id=pid_blob(?) AND book.retired=0 AND file.retired=0`, []any{r.EntityID}, true, ""})
		sections = append(sections, listeningSection{"context", "Author & series", "list", "context", "", `SELECT ` + listeningGroupNavigationID + ` id,g.id entity_id,g.name title,CASE g.kind WHEN 1 THEN 'author' ELSE 'book_series' END subtitle,g.name ordering,0 n,0 chapter_index FROM catalog_entities book JOIN catalog_book_group_members m ON m.book_id=book.id JOIN catalog_book_groups g ON g.id=m.group_id AND g.retired=0 JOIN catalog_libraries l ON l.id=g.library_id WHERE book.public_id=pid_blob(?)`, []any{r.EntityID}, false, ""})
	default:
		return out, requestinput.New("unknown listening view")
	}
	out.Entity, e = s.listeningEntity(r, out.Heading.Fallback)
	if e != nil {
		return out, e
	}
	if r.Start > 0 && len(sections) != 1 {
		// start opens one list; a view with several sections is continued by
		// each section's own cursor.
		return out, ErrContentStart
	}
	matched := sectionID == ""
	visibleTotal := 0
	for _, spec := range sections {
		if sectionID != "" && sectionID != spec.id {
			continue
		}
		matched = true
		if r.View == "discover" {
			spec.layout = "rail"
		}
		var sec ContentSection
		if spec.browseKind != "" {
			sec, e = s.libraryListeningSection(r, spec, keyed, offset)
		} else if keyed != "" {
			e = ErrCursor
		} else {
			sec, e = s.listeningSection(r, spec, offset, ordered)
		}
		if e != nil {
			return out, e
		}
		visibleTotal += sec.TotalCount
		if sec.NextCursor != "" {
			cs := scope
			cs.Section = spec.id
			sec.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: cs, Value: sec.NextCursor, Expires: time.Now().Add(30 * time.Minute).Unix()}, before)
			if e != nil {
				return out, e
			}
		}
		if len(sec.Entries) > 0 || sectionID != "" {
			out.Sections = append(out.Sections, sec)
		}
	}
	if !matched {
		return out, ErrCursor
	}
	if r.View == "artist" && r.EntityID != "" {
		for i := range out.Sections {
			if out.Sections[i].ID == "releases" {
				if e := s.markAlbumRoles(&out.Sections[i], r.EntityID); e != nil {
					return out, e
				}
			}
		}
	}
	// Popular tracks ride on the first page only: they are an unpaged top-5,
	// so a section continuation must not gain or repeat them.
	if sectionID == "" && r.View == "artist" && r.EntityID != "" {
		popular, e := s.artistPopularTracks(r, lib.ID, r.EntityID)
		if e != nil {
			return out, e
		}
		if len(popular.Entries) > 0 {
			at := len(out.Sections)
			for i, section := range out.Sections {
				if section.ID == "songs" {
					at = i + 1
				}
			}
			out.Sections = append(out.Sections, ContentSection{})
			copy(out.Sections[at+1:], out.Sections[at:])
			out.Sections[at] = popular
		}
	}
	for i := range out.Sections {
		if e := s.countEntryPlays(r.Profile, out.Sections[i].Entries); e != nil {
			return out, e
		}
		if e := s.listeningPageFavorites(r.Profile, out.Sections[i].Entries); e != nil {
			return out, e
		}
	}
	if r.EntityID != "" && visibleTotal == 0 {
		return out, sql.ErrNoRows
	}
	after, e := s.ContentRevision(lib.ID, r.Profile)
	if e != nil {
		return out, e
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	if len(out.Sections) == 0 {
		out.Empty = &ContentHeading{Key: "content.empty", Fallback: "No content matches this view."}
	}
	return out, nil
}

// albumRoleSQL is the artist-relative release role for an album entity and its
// album artist, for a listed artist's public id bound at the placeholder. The
// artist page's releases section and the albums endpoint share it so their
// roles agree: an album whose primary artist differs is an appearance; a
// Compilation secondary wins over the MusicBrainz primary type; EP and Single
// become ep and single; an unlinked release stays album.
func albumRoleSQL(album, artist string) string {
	return `CASE WHEN ` + artist + `.public_id<>pid_blob(?) THEN 'appearance'
 WHEN EXISTS(SELECT 1 FROM mb_album_links l JOIN mb_release_group_types t ON t.release_revision=l.release_revision WHERE l.album_id=` + album + `.id AND t.value='Compilation' COLLATE NOCASE) THEN 'compilation'
 WHEN COALESCE((SELECT e.primary_type FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision WHERE l.album_id=` + album + `.id),'Album')='EP' THEN 'ep'
 WHEN COALESCE((SELECT e.primary_type FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision WHERE l.album_id=` + album + `.id),'Album')='Single' THEN 'single'
 ELSE 'album' END`
}

// markAlbumRoles tags each release in an artist's releases section with its
// release-group type. The section keeps one query, one visibility rule and one
// cursor; the role is a bounded probe over the page's own entries.
func (s *Service) markAlbumRoles(section *ContentSection, artist string) error {
	if len(section.Entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(section.Entries))
	for _, entry := range section.Entries {
		ids = append(ids, entry.ID)
	}
	// Album facts come from the compact tables; the release-group type comes
	// from MusicBrainz evidence linked to each integer album entity.
	rows, e := s.read().Query(`SELECT pid(album.public_id),`+albumRoleSQL("album", "ar")+` FROM catalog_albums a JOIN catalog_entities album ON album.id=a.entity_id JOIN catalog_entities ar ON ar.id=a.artist_id WHERE album.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, artist, idsJSON(ids))
	if e != nil {
		return e
	}
	roles := map[string]string{}
	for rows.Next() {
		var id, role string
		if e = rows.Scan(&id, &role); e != nil {
			rows.Close()
			return e
		}
		roles[id] = role
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for i := range section.Entries {
		section.Entries[i].Role = roles[section.Entries[i].ID]
		if section.Entries[i].Role == "" {
			section.Entries[i].Role = "album"
		}
	}
	return nil
}

// artistPopularTracks is the artist's five most played songs, restricted to
// songs the viewer may see: the whole server's plays ("Popular") when the owner
// shares activity across profiles, and otherwise the viewer's own ("Your most
// played"). Both read maintained counts over the artist's songs, and the section
// is omitted when nothing of the artist's has been played.
func (s *Service) artistPopularTracks(r ContentRequest, library, artist string) (ContentSection, error) {
	sec := contentSection("popularTracks", "list", "Your most played", []ContentEntry{}, 0, "")
	counts, countArgs := `personal_play_counts c ON c.item_id=i.id AND c.profile_id=?`, []any{r.Profile}
	recency := `0`
	if s.serverWidePlays() {
		sec.Heading.Fallback = "Popular"
		counts, countArgs, recency = `play_counts c ON c.item_id=i.id`, nil, `c.last_played_ms`
	}
	visible, visibleArgs := r.Viewer.itemVisibilitySQL("i.id")
	args := append(append([]any{artist, artist}, countArgs...), library)
	args = append(args, visibleArgs...)
	rows, e := s.read().Query(`SELECT pid(i.public_id),c.plays,`+recency+` FROM (
	 SELECT s.entity_id FROM catalog_entities artist CROSS JOIN catalog_albums a INDEXED BY catalog_albums_artist ON a.artist_id=artist.id CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=a.entity_id WHERE artist.public_id=pid_blob(?)
	 UNION SELECT sa.song_id FROM catalog_entities artist CROSS JOIN catalog_song_artists sa INDEXED BY catalog_song_artists_artist ON sa.artist_id=artist.id WHERE artist.public_id=pid_blob(?)
	 ) member JOIN catalog_entities i ON i.id=member.entity_id JOIN `+counts+` JOIN catalog_libraries l ON l.id=i.library_id WHERE l.library_id=? AND i.retired=0 AND `+visible+` ORDER BY 2 DESC,3 DESC,i.id LIMIT 5`, args...)
	if e != nil {
		return sec, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		var plays, last int64
		if e = rows.Scan(&id, &plays, &last); e != nil {
			rows.Close()
			return sec, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return sec, e
	}
	for _, id := range ids {
		item, e := s.Get(r.Profile, id)
		if e != nil {
			return sec, e
		}
		sec.Entries = append(sec.Entries, contentItem(item))
	}
	sec.TotalCount = len(sec.Entries)
	return sec, nil
}
func (s *Service) listeningSection(r ContentRequest, spec listeningSection, offset int, ordered bool) (ContentSection, error) {
	sec := contentSection(spec.id, spec.layout, spec.title, []ContentEntry{}, 0, "")
	sec.Start = offset
	visibility, visibleArgs := listeningSectionVisibility(spec.kind, r.Viewer)
	if spec.kind == "author" || spec.kind == "book_series" || spec.kind == "context" {
		visibility, visibleArgs = "projected.n>0", nil
	}
	if spec.kind == "context" {
		visibility = "1"
	}
	if r.Viewer.EffectiveRestrictions().Active() && (spec.kind == "book" || spec.kind == "author" || spec.kind == "book_series" || spec.kind == "context") {
		class, generation, err := s.bookGroupClassReady(r.Library, r.Viewer.EffectiveRestrictions())
		if err != nil {
			return sec, err
		}
		if spec.kind == "book" {
			visibility = `EXISTS(SELECT 1 FROM catalog_book_visible_counts vc WHERE vc.class_id=? AND vc.generation=? AND vc.book_id=projected.entity_id)`
			visibleArgs = []any{class, generation}
		} else if spec.kind == "context" {
			visibility = `EXISTS(SELECT 1 FROM catalog_book_group_visible_counts vc JOIN catalog_book_groups g ON g.id=vc.group_id WHERE vc.class_id=? AND vc.generation=? AND g.id=projected.entity_id AND g.retired=0)`
			visibleArgs = []any{class, generation}
		}
	}
	where := visibility
	args := append([]any{}, spec.args...)
	args = append(args, visibleArgs...)
	if r.Q != "" {
		where += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, searchPrefix(r.Q))
	}
	if e := s.read().QueryRow(`SELECT count(*) FROM (`+spec.query+`) projected WHERE `+where, args...).Scan(&sec.TotalCount); e != nil {
		return sec, e
	}
	order := "title COLLATE NOCASE " + strings.ToUpper(r.Direction) + ",entity_id " + strings.ToUpper(r.Direction) + ",COALESCE(chapter_index,0)"
	if r.View == "artist" && spec.id == "releases" {
		// An artist's releases read newest-first; unknown year sorts last.
		// The year rides the section query (projected.year is not selected).
		order = "CASE WHEN year=0 THEN 1 ELSE 0 END,year DESC,title COLLATE NOCASE,entity_id"
	} else if ordered || spec.kind == "disc" {
		order = "ordering COLLATE NOCASE,entity_id,COALESCE(chapter_index,0)"
	}
	args = append(args, r.Limit+1, offset)
	rows, e := s.read().Query(`SELECT id,title,subtitle,n FROM (`+spec.query+`) projected WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if e != nil {
		return sec, e
	}
	for rows.Next() {
		var v ContentEntry
		var n int
		if e = rows.Scan(&v.ID, &v.Title, &v.Subtitle, &n); e != nil {
			rows.Close()
			return sec, e
		}
		v.Kind = spec.kind
		v.LibraryID = r.Library
		v.Navigation = &ContentNavigation{View: spec.view, EntityID: v.ID}
		if n > 0 {
			v.Count = &n
		}
		sec.Entries = append(sec.Entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return sec, e
	}
	if len(sec.Entries) > r.Limit {
		sec.Entries = sec.Entries[:r.Limit]
		sec.NextCursor = strconv.Itoa(offset + r.Limit)
	}
	if e := s.enrichListeningEntries(r, spec, &sec); e != nil {
		return sec, e
	}
	return sec, nil
}

// enrichListeningEntries fills each page entry of a listening section: the
// media projection for songs and parts, chapter timing, release and book art
// and a book's resume. Bounded by the page.
func (s *Service) enrichListeningEntries(r ContentRequest, spec listeningSection, secp *ContentSection) error {
	sec := *secp
	defer func() { *secp = sec }()
	var e error
	for i := range sec.Entries {
		v := &sec.Entries[i]
		if spec.media {
			item, e := s.Get(r.Profile, v.ID)
			if e != nil {
				return e
			}
			*v = contentItem(item)
			if item.Song != nil {
				parts := []string{}
				if item.Song.DiscNumber != nil {
					parts = append(parts, fmt.Sprintf("Disc %d", *item.Song.DiscNumber))
				}
				if item.Song.TrackNumber != nil {
					parts = append(parts, fmt.Sprintf("Track %d", *item.Song.TrackNumber))
				}
				parts = append(parts, item.Song.Artist)
				v.Subtitle = strings.Join(parts, ", ")
			}
			if item.BookFile != nil && item.BookFile.PartNumber != nil {
				v.Subtitle = fmt.Sprintf("Part %d, %s", *item.BookFile.PartNumber, item.BookFile.BookTitle)
			}
		} else if spec.kind == "context" {
			v.Kind = v.Subtitle
			v.Subtitle = ""
			v.Navigation.View = v.Kind
		} else if spec.kind == "chapter" {
			itemID := v.Subtitle
			v.Subtitle = ""
			colon := strings.LastIndexByte(v.ID, ':')
			if colon <= 0 {
				return ErrCursor
			}
			chapterIndex, err := strconv.Atoi(v.ID[colon+1:])
			if err != nil || chapterIndex < 0 {
				return ErrCursor
			}
			var start, end float64
			var available, watched bool
			// A finished chapter is this profile's own stored fact, and only
			// while the chapter definition and the file's linked source still
			// match what was played. Nothing is inferred from the book's
			// finished mark or from the chapter the resume point is in.
			if e = s.read().QueryRow(`SELECT c.start_seconds,c.end_seconds,EXISTS(SELECT 1 FROM catalog_item_availability a WHERE a.entity_id=c.file_id AND a.available=1 AND a.retired=0),EXISTS(SELECT 1 FROM personal_chapter_completions f JOIN catalog_assets source ON source.id=f.asset_id JOIN asset_chapter_facts facts ON facts.asset_id=source.token WHERE f.profile_id=? AND f.file_entity_id=c.file_id AND f.chapter_index=c.chapter_index AND f.asset_revision=facts.revision AND f.start_ms=CAST(round(c.start_seconds*1000) AS INTEGER) AND f.end_ms=CAST(round(c.end_seconds*1000) AS INTEGER) AND EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=f.file_entity_id AND a.id=f.asset_id AND a.size=f.asset_size AND a.modified_ns=f.asset_modified_ns)) FROM catalog_book_chapters c WHERE c.file_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND c.chapter_index=?`, r.Profile, itemID, chapterIndex).Scan(&start, &end, &available, &watched); e != nil {
				return e
			}
			duration := end - start
			v.Duration = &duration
			v.Available = &available
			if watched {
				v.Watched = &watched
			}
			v.Navigation = &ContentNavigation{View: "item", EntityID: itemID}
			if available {
				v.Playback = &ContentPlayback{ItemID: itemID, StartSeconds: &start}
			}
		} else if v.Kind == "album" || v.Kind == "book" {
			table, col := "catalog_songs", "album_id"
			if v.Kind == "book" {
				table, col = "catalog_book_files", "book_id"
			}
			visible, args := r.Viewer.itemVisibilitySQL("i.id")
			var art string
			if e = s.read().QueryRow(`SELECT COALESCE((SELECT pid(i.public_id) FROM catalog_entities parent JOIN `+table+` f ON f.`+col+`=parent.id JOIN catalog_entities i ON i.id=f.entity_id JOIN catalog_item_details detail ON detail.entity_id=i.id WHERE parent.public_id=pid_blob(?) AND parent.retired=0 AND detail.poster_url<>'' AND `+visible+` ORDER BY i.sort_key,i.id LIMIT 1),'')`, append([]any{v.ID}, args...)...).Scan(&art); e != nil {
				return e
			}
			if art != "" {
				v.PosterURL = "/v1/items/" + art + "/art/poster"
			}
		}
	}
	return describeItemArtIn(s, sec.Entries, func(c *ContentEntry) *string { return &c.PosterURL })
}
