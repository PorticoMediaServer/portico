package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func (s *Service) ContentRevision(library, profile string) (ContentRevision, error) {
	var revision ContentRevision
	err := s.read().QueryRow(`SELECT r.revision,COALESCE(v.revision,0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id=?`, profile, library).Scan(&revision.Catalog, &revision.Viewer)
	return revision, err
}
func searchPrefix(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	value = strings.ReplaceAll(value, "_", `\_`)
	return value + "%"
}
func contentItem(item Item) ContentEntry {
	// PERF-12: grid/row/list entries omit the synopsis by default. Clients
	// read overview only in heroes/detail and in episode rows, so episodes
	// keep it (Apple episode rows render a 2-line synopsis) while every other
	// grid entry drops it. BackdropURL stays: both clients fall back to it
	// when a poster is missing and landscape rows prefer it.
	overview := ""
	if item.Kind == "episode" {
		runes := []rune(item.Overview)
		if len(runes) > 1000 {
			runes = runes[:1000]
		}
		overview = string(runes)
	}
	entry := ContentEntry{Missing: item.Missing, Watched: item.Watched, LibraryID: item.LibraryID, Overview: overview, BackdropURL: item.BackdropURL, StillURL: item.StillURL, ID: item.ID, Kind: item.Kind, Title: item.Title, PosterURL: item.PosterURL, Available: &item.Available, Duration: &item.Duration, ProgressSeconds: &item.ProgressSeconds, AddedAt: item.AddedAt, Navigation: &ContentNavigation{View: "item", EntityID: item.ID}, Playback: &ContentPlayback{ItemID: item.ID}}
	if item.Kind == "song" {
		entry.AudioFacts = item.AudioFacts
	}
	if !item.Available {
		entry.Playback = nil
	}
	if item.Year > 0 {
		entry.Subtitle = strconv.Itoa(item.Year)
	}
	if item.Episode != nil {
		entry.Subtitle = item.Episode.ShowTitle
		entry.EpisodeNumber = &item.Episode.Number
		entry.DateBased = item.Episode.Numbering == "date"
		entry.Special = item.Episode.SeasonNumber != nil && *item.Episode.SeasonNumber == 0
		if item.Episode.Numbering == "absolute" {
			entry.AbsoluteNumber = &item.Episode.Number
		}
		entry.SeasonNumber = item.Episode.SeasonNumber
	}
	if item.Song != nil {
		entry.Subtitle = item.Song.Artist
		entry.TrackNumber = item.Song.TrackNumber
		entry.DiscNumber = item.Song.DiscNumber
		if item.Song.AlbumID != "" && item.Song.AlbumTitle != "" {
			entry.Album = &ContentEntityRef{ID: item.Song.AlbumID, Name: item.Song.AlbumTitle}
		}
	}
	if item.BookFile != nil {
		entry.Subtitle = item.BookFile.BookTitle
	}
	return entry
}
func contentItems(items []Item) []ContentEntry {
	out := []ContentEntry{}
	for _, item := range items {
		out = append(out, contentItem(item))
	}
	return out
}
func contentSection(id, kind, title string, entries []ContentEntry, count int, next string) ContentSection {
	return ContentSection{ID: id, Type: kind, Heading: ContentHeading{Key: "section." + id, Fallback: title}, Entries: entries, TotalCount: count, NextCursor: next}
}

// gridSection is contentSection for a grid opened at start.
func gridSection(id, title string, entries []ContentEntry, count int, next string, start int) ContentSection {
	section := contentSection(id, "grid", title, entries, count, next)
	section.Start = start
	return section
}
func (s *Service) contentOnce(request ContentRequest) (ContentEnvelope, error) {
	out := ContentEnvelope{Navigation: []ContentTab{}, Sorts: []ContentSort{}, Filters: []ContentFilter{}, Sections: []ContentSection{}}
	lib, err := s.library(request.Library)
	if err != nil {
		return out, err
	}
	if lib.Kind == "music" || lib.Kind == "audiobook" {
		return s.listeningContent(request, lib)
	}
	if request.View == "" {
		request.View = DefaultLibraryView(lib.Kind)
	}
	request.Limit = pageLimit(request.Limit)
	if len(request.Q) > 100 {
		return out, requestinput.New("search prefix exceeds limit")
	}
	if request.View != "discover" && request.View != "browse" && request.View != "collections" && request.View != "categories" && request.View != "collection" && request.View != "show" && request.View != "season" && request.View != "artist" && request.View != "album" && request.View != "book" {
		return out, requestinput.New("unknown library content view")
	}
	out.Heading = ContentHeading{Key: "library." + request.View, Fallback: lib.Name}
	out.Scope = ContentScope{request.ServerID, lib.ID, lib.Kind, request.View, request.EntityID, request.ViewerFence}
	out.Navigation = []ContentTab{{"discover", "library.discover", "discover"}, {"browse", "library.browse", "browse"}}
	switch lib.Kind {
	case "movie":
		out.Navigation[1].LabelKey = "library.movies"
	case "tv", "anime":
		out.Navigation[1].LabelKey = "library.shows"
	case "music":
		out.Navigation[1].LabelKey = "library.music"
	case "audiobook":
		out.Navigation[1].LabelKey = "library.books"
	}
	if lib.Kind == "movie" {
		out.Navigation = append(out.Navigation, ContentTab{"collections", "library.collections", "collections"}, ContentTab{"categories", "library.categories", "categories"})
	}
	out.Sorts = []ContentSort{{"title", "sort.title", []string{"asc", "desc"}}}
	sortDefault := "title"
	if lib.Kind == "movie" && (request.View == "browse" || request.View == "collection") {
		out.Sorts = append(out.Sorts, ContentSort{"added", "sort.added", []string{"asc", "desc"}}, ContentSort{"year", "sort.year", []string{"asc", "desc"}})
	}
	switch request.View {
	case "discover":
		sortDefault = "server"
	case "categories":
		sortDefault = "decade"
	case "show":
		sortDefault = "season"
	case "season":
		sortDefault = "episode"
	case "album":
		sortDefault = "track"
	case "book":
		sortDefault = "part"
	}
	if sortDefault != "title" {
		out.Sorts = []ContentSort{{sortDefault, "sort." + sortDefault, []string{"asc"}}}
	}
	if request.Sort == "" {
		request.Sort = sortDefault
	}
	if request.Direction == "" {
		request.Direction = "asc"
		if request.Sort == "added" || request.Sort == "year" || request.Sort == "decade" {
			request.Direction = "desc"
		}
	}
	validSort := false
	for _, sort := range out.Sorts {
		if sort.ID == request.Sort {
			for _, direction := range sort.Directions {
				if direction == request.Direction {
					validSort = true
				}
			}
		}
	}
	if request.View == "discover" || request.View == "categories" {
		expectedDirection := "asc"
		if request.View == "categories" {
			expectedDirection = "desc"
		}
		validSort = request.Sort == sortDefault && request.Direction == expectedDirection
		out.Sorts = []ContentSort{}
	}
	if !validSort {
		return out, requestinput.New("sort is not applicable to this view")
	}
	if (request.View == "discover" || request.View == "categories" || request.View == "show" || request.View == "season" || request.View == "album" || request.View == "book") && (request.Q != "" || request.Category != "") {
		return out, requestinput.New("search and category are not applicable to this view")
	}
	if request.Category != "" && lib.Kind != "movie" {
		return out, requestinput.New("category is not applicable to this library")
	}
	if request.Category != "" && request.View != "browse" && request.View != "collection" {
		return out, requestinput.New("category is not applicable to this view")
	}
	out.Query = ContentQuery{request.Sort, request.Direction, request.Category, request.Q, request.Limit, "title_prefix"}
	if request.View != "browse" && request.View != "collection" && request.View != "collections" && request.View != "artist" {
		out.Query.SearchMode = "none"
	}
	before, err := s.ContentRevision(lib.ID, request.Profile)
	if err != nil {
		return out, err
	}
	out.Revision = before
	scope := cursorScope{Library: lib.ID, Viewer: request.ViewerFence, Profile: request.Profile, View: request.View, Entity: request.EntityID, Sort: request.Sort, Direction: request.Direction, Category: request.Category, Search: request.Q, Limit: request.Limit}
	inner, sectionID := "", ""
	if request.Cursor != "" {
		c, err := s.contentContinuation(request.Cursor, scope)
		if err != nil {
			return out, err
		}
		inner = c.Value
		sectionID = c.Scope.Section
	}
	if lib.Kind == "movie" && (request.View == "browse" || request.View == "collection") {
		categories, err := s.contentCategories(request)
		if err != nil {
			return out, err
		}
		options := []ContentFilterOption{}
		for _, category := range categories {
			options = append(options, ContentFilterOption{ID: category.ID, Label: category.Name, Count: category.Count})
		}
		out.Filters = []ContentFilter{{ID: "category", LabelKey: "filter.category", Options: options}}
	}
	// Every entity must belong to the already-authorized library before child rows
	// are loaded; caller identity alone never authorizes a cross-library entity.
	if request.EntityID != "" {
		var actual string
		var e error
		switch request.View {
		case "collection":
			// Readiness before existence (SEC-02: hidden answers as absent).
			if e = s.CollectionClassesReady(Viewer{Libraries: []string{lib.ID}, Restrictions: request.Viewer.Restrictions}); e != nil {
				return out, e
			}
			var c Collection
			c, e = s.Collection(request.EntityID)
			actual = c.LibraryID
		case "show":
			actual, e = s.LibraryForShow(request.EntityID)
		case "season":
			actual, e = s.LibraryForSeason(request.EntityID)
		case "artist", "album", "book":
			actual, e = s.LibraryForAudioEntity(request.View, request.EntityID)
		default:
			return out, requestinput.New("entityId is not applicable to this view")
		}
		if e != nil {
			return out, e
		}
		if actual != lib.ID {
			return out, sql.ErrNoRows
		}
		// SEC-02: a container the viewer may not see (every member hidden by
		// the profile's restrictions) does not exist for it, exactly as the
		// detail and browse routes answer; its name is not echoed back.
		if request.View == "collection" {
			_, e = s.VisibleCollection(s.Context(), request.Viewer, request.EntityID)
		} else {
			e = s.VisibleEntity(s.Context(), request.Viewer, request.View, request.EntityID)
		}
		if e != nil {
			return out, e
		}
	} else if request.View == "collection" || request.View == "show" || request.View == "season" || request.View == "artist" || request.View == "album" || request.View == "book" {
		return out, requestinput.New("entityId is required")
	}
	if request.EntityID != "" {
		var title string
		if request.View == "season" {
			var number int
			err = s.read().QueryRow(`SELECT parent.title,se.number FROM catalog_entities e JOIN catalog_seasons se ON se.entity_id=e.id JOIN catalog_entities parent ON parent.id=se.show_id WHERE e.public_id=pid_blob(?)`, request.EntityID).Scan(&title, &number)
			title += " · Season " + strconv.Itoa(number)
		} else {
			err = s.read().QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, request.EntityID).Scan(&title)
		}
		if err != nil {
			return out, err
		}
		out.Heading = ContentHeading{Key: "entity.title", Fallback: title}
	}
	if request.Start < 0 || request.Start > 0 && (inner != "" || request.View != "browse" && request.View != "collection" && request.View != "collections" && request.View != "artist") {
		return out, ErrContentStart
	}
	switch request.View {
	case "discover":
		if inner != "" {
			return out, ErrCursor
		}
		discovery, e := s.DiscoverWithRestrictions(lib.ID, request.Profile, request.Restrictions, request.Now)
		if e != nil {
			return out, e
		}
		for _, section := range discovery.Sections {
			if len(section.Items) == 0 {
				continue
			}
			cs := contentSection(section.ID, "rail", section.Title, section.Items, section.Total, "")
			if strings.HasPrefix(section.ID, recFamily+":") {
				cs.Heading = ContentHeading{Key: section.TitleText.Code, Fallback: section.Title, Params: section.TitleText.Params}
				cs.SeeAll = section.SeeAll
			}
			out.Sections = append(out.Sections, cs)
		}
	case "browse", "collection":
		if lib.Kind == "movie" {
			rows, next, e := s.Browse(BrowseQuery{Restrictions: request.Restrictions, Library: lib.ID, Viewer: request.ViewerFence, Profile: request.Profile, Sort: request.Sort, Direction: request.Direction, Category: request.Category, Collection: request.EntityID, Search: request.Q, Cursor: inner, Start: request.Start, Limit: request.Limit})
			if e != nil {
				return out, e
			}
			count, e := s.contentMovieCount(request)
			if e != nil {
				return out, e
			}
			section := gridSection("items", "Movies", contentItems(rows), count, next, request.Start)
			out.Sections = append(out.Sections, section)
		} else if request.View == "collection" {
			return out, requestinput.New("collections require a movie library")
		} else {
			entries, next, count, e := s.contentEntities(request, inner, request.Limit)
			if e != nil {
				return out, e
			}
			section := gridSection("items", lib.Name, entries, count, next, request.Start)
			out.Sections = append(out.Sections, section)
		}
	case "collections", "artist":
		if request.View == "collections" && lib.Kind != "movie" {
			return out, requestinput.New("collections require a movie library")
		}
		if request.View == "artist" && (sectionID == "" || sectionID == "top_songs") {
			rows, next, count, e := s.ArtistTopSongs(request.Profile, request.Library, request.EntityID, inner, request.Limit, request.Restrictions)
			if e != nil {
				return out, e
			}
			out.Sections = append(out.Sections, contentSection("top_songs", "list", "Top songs", rows, count, next))
		}
		if request.View == "artist" && sectionID != "" && sectionID != "items" {
			if sectionID != "top_songs" {
				return out, requestinput.New("invalid artist section")
			}
			break
		}
		entries, next, count, e := s.contentEntities(request, inner, request.Limit)
		if e != nil {
			return out, e
		}
		title := "Collections"
		if request.View == "artist" {
			title = "Albums"
		}
		section := gridSection("items", title, entries, count, next, request.Start)
		out.Sections = append(out.Sections, section)
	case "categories":
		if lib.Kind != "movie" {
			return out, requestinput.New("categories require a movie library")
		}
		categories, e := s.Categories(request.Viewer, lib.ID)
		if e != nil {
			return out, e
		}
		entries := []ContentEntry{}
		for _, category := range categories {
			count := category.Count
			entries = append(entries, ContentEntry{ID: category.ID, Kind: "category", Title: category.Name, Count: &count, ArtworkPaths: category.ArtworkPaths, Navigation: &ContentNavigation{View: "browse", Category: category.ID}})
		}
		out.Sections = append(out.Sections, contentSection("categories", "grid", "Categories", entries, len(entries), ""))
	case "show":
		if sectionID == "" || sectionID == "seasons" {
			seasons, e := s.Seasons(Viewer{Profile: request.Profile, Fence: request.ViewerFence, Libraries: []string{request.Library}, Restrictions: request.Restrictions}, request.EntityID)
			if e != nil {
				return out, e
			}
			entries := []ContentEntry{}
			for _, season := range seasons {
				entries = append(entries, ContentEntry{ID: season.ID, Kind: "season", Title: season.Title, Navigation: &ContentNavigation{View: "season", EntityID: season.ID}})
			}
			out.Sections = append(out.Sections, contentSection("seasons", "grid", "Seasons", entries, len(entries), ""))
		}
		if sectionID == "" || sectionID == "unassigned_absolute" {
			rows, next, e := s.Episodes(Viewer{Profile: request.Profile, Fence: request.ViewerFence, Libraries: []string{request.Library}, Restrictions: request.Restrictions}, request.EntityID, "", inner, request.Limit)
			if e != nil {
				return out, e
			}
			var count int
			restriction, bound := ItemRestrictionSQL("item.id", request.Restrictions)
			args := append([]any{request.EntityID}, bound...)
			e = s.read().QueryRow(`SELECT count(*) FROM catalog_episodes e JOIN catalog_entities item ON item.id=e.entity_id WHERE e.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND e.season_id IS NULL AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=e.entity_id) AND `+restriction, args...).Scan(&count)
			if e != nil {
				return out, e
			}
			out.Sections = append(out.Sections, contentSection("unassigned_absolute", "list", "Unassigned absolute episodes", contentItems(rows), count, next))
		}
	case "season":
		rows, next, e := s.Episodes(Viewer{Profile: request.Profile, Fence: request.ViewerFence, Libraries: []string{request.Library}, Restrictions: request.Restrictions}, "", request.EntityID, inner, request.Limit)
		if e != nil {
			return out, e
		}
		var count int
		restriction, bound := ItemRestrictionSQL("item.id", request.Restrictions)
		args := append([]any{request.EntityID}, bound...)
		e = s.read().QueryRow(`SELECT count(*) FROM catalog_episodes e JOIN catalog_entities item ON item.id=e.entity_id WHERE e.season_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=e.entity_id) AND `+restriction, args...).Scan(&count)
		if e != nil {
			return out, e
		}
		out.Sections = append(out.Sections, contentSection("episodes", "list", "Episodes", contentItems(rows), count, next))
	case "album", "book":
		id := "songs"
		kind := "album"
		title := "Songs"
		if request.View == "book" {
			id, kind, title = "files", "book", "Files"
		}
		if sectionID == "" || sectionID == id {
			rows, next, e := s.AudioItems(request.Viewer, request.EntityID, kind, inner, request.Limit)
			if e != nil {
				return out, e
			}
			table, column := "catalog_songs", "album_id"
			if kind == "book" {
				table, column = "catalog_book_files", "book_id"
			}
			var count int
			visible, args := request.Viewer.itemVisibilitySQL("d.entity_id")
			if e = s.read().QueryRow(`SELECT count(*) FROM `+table+` d WHERE d.`+column+`=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+visible, append([]any{request.EntityID}, args...)...).Scan(&count); e != nil {
				return out, e
			}
			out.Sections = append(out.Sections, contentSection(id, "list", title, contentItems(rows), count, next))
		}
		if request.View == "book" && (sectionID == "" || sectionID == "chapters") {
			rows, next, e := s.BookChapters(request.Viewer, request.EntityID, inner, request.Limit)
			if e != nil {
				return out, e
			}
			entries := []ContentEntry{}
			for _, chapter := range rows {
				start := chapter.StartSeconds
				duration := chapter.EndSeconds - chapter.StartSeconds
				var available bool
				if e = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_item_availability a JOIN catalog_entities e ON e.id=a.entity_id WHERE e.public_id=pid_blob(?) AND a.available=1)`, chapter.ItemID).Scan(&available); e != nil {
					return out, e
				}
				entry := ContentEntry{ID: chapter.ID, Kind: "chapter", Title: chapter.Title, Duration: &duration, Available: &available, Navigation: &ContentNavigation{View: "item", EntityID: chapter.ItemID}}
				if available {
					entry.Playback = &ContentPlayback{ItemID: chapter.ItemID, StartSeconds: &start}
				}
				entries = append(entries, entry)
			}
			var count int
			visible, args := request.Viewer.itemVisibilitySQL("f.entity_id")
			if e = s.read().QueryRow(`SELECT count(*) FROM catalog_book_chapters c JOIN catalog_book_files f ON f.entity_id=c.file_id WHERE f.book_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+visible, append([]any{request.EntityID}, args...)...).Scan(&count); e != nil {
				return out, e
			}
			out.Sections = append(out.Sections, contentSection("chapters", "list", "Chapters", entries, count, next))
		}
	}
	if request.Cursor == "" {
		nonempty := make([]ContentSection, 0, len(out.Sections))
		for _, section := range out.Sections {
			if len(section.Entries) > 0 {
				nonempty = append(nonempty, section)
			}
		}
		out.Sections = nonempty
	}
	for index := range out.Sections {
		section := &out.Sections[index]
		if section.NextCursor != "" {
			scoped := scope
			scoped.Section = section.ID
			section.NextCursor, err = s.encodeCursor(cursorValue{Scope: scoped, Value: section.NextCursor, Expires: time.Now().Add(30 * time.Minute).Unix()})
			if err != nil {
				return out, err
			}
		}
	}
	after, err := s.ContentRevision(lib.ID, request.Profile)
	if err != nil {
		return out, err
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	hasEntries := false
	for _, section := range out.Sections {
		if len(section.Entries) > 0 {
			hasEntries = true
		}
	}
	if !hasEntries {
		out.Empty = &ContentHeading{Key: "content.empty", Fallback: "No content matches this view."}
	}
	return out, nil
}
func (s *Service) contentContinuation(raw string, scope cursorScope) (cursorValue, error) {
	// The section is signed inside the cursor; decode it only to select the
	// expected scope, then authenticate the entire payload before using anything.
	if len(raw) > 4096 {
		return cursorValue{}, ErrCursor
	}
	payload, _, ok := strings.Cut(raw, ".")
	if !ok {
		return cursorValue{}, ErrCursor
	}
	data, err := decodeCursorPayload(payload)
	if err != nil {
		return cursorValue{}, ErrCursor
	}
	scope.Section = data.Scope.Section
	return s.decodeCursor(raw, scope)
}

// Content retries a first page that raced a publication; see consistentRead.
func (s *Service) Content(request ContentRequest) (ContentEnvelope, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out ContentEnvelope
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).Content(request)
			return readErr
		})
		return out, err
	}
	if request.Profile != "" && request.Profile != request.Viewer.Profile || request.ViewerFence != "" && request.ViewerFence != request.Viewer.Fence {
		if request.Cursor != "" {
			return ContentEnvelope{}, ErrCursor
		}
		return ContentEnvelope{}, sql.ErrNoRows
	}
	if !request.Viewer.AllowsLibrary(request.Library) {
		return ContentEnvelope{}, sql.ErrNoRows
	}
	request = request.scoped()
	if err := s.prepareViewer(request.Viewer); err != nil {
		return ContentEnvelope{}, err
	}
	out, err := consistentRead(request.Cursor == "", func() (ContentEnvelope, error) { return s.contentOnce(request) })
	if errors.Is(err, ErrVisibilityBuilding) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	pages := make([][]ContentEntry, 0, len(out.Sections))
	for _, section := range out.Sections {
		pages = append(pages, section.Entries)
	}
	if err = s.EnrichListeningEntries(request.Viewer, pages...); err != nil {
		return out, err
	}
	applyAuthorSeriesEntityFacts(&out, request)
	return out, s.attachViewerRatings(request.Viewer.Profile, pages...)
}

// consistentRead runs a multi-statement catalog read that fences itself with a
// before/after revision comparison. Background metadata and artwork work bumps
// library revisions constantly on a busy server, so a first page is retried a
// few times rather than failed; only a continuation, whose cursor belongs to the
// earlier revision, is reported stale on the first mismatch.
func consistentRead[T any](firstPage bool, read func() (T, error)) (T, error) {
	const attempts = 4
	var out T
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		out, err = read()
		if err == nil || !errors.Is(err, ErrStaleContinuation) || !firstPage {
			return out, err
		}
	}
	return out, err
}

// browseContentRevision fences ordering independently from decoration changes.
func (s *Service) browseContentRevision(library, profile string) (ContentRevision, error) {
	var out ContentRevision
	err := s.read().QueryRow(`SELECT r.order_revision,COALESCE(v.revision,0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id=?`, profile, library).Scan(&out.Catalog, &out.Viewer)
	return out, err
}
