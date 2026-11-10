package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// viewer is the profile a request reads and writes as.
func viewer(r *http.Request) db.Viewer {
	c := CallerFrom(r.Context())
	return db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}
}

// refuse turns the data layer's two refusals into the API's. Something the caller may not
// see is "not found", exactly like something that does not exist.
func refuse(err error) error {
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, db.ErrInvalid):
		return ErrInvalid(strings.TrimPrefix(err.Error(), db.ErrInvalid.Error()+": "))
	}
	return err
}

func (s *Server) catalogueRoutes() {
	Handle(s, "GET /v1/libraries", WithProfile, func(r *http.Request, _ None) (Libraries, error) {
		libraries, err := s.db.Libraries(r.Context(), viewer(r))
		out := Libraries{Items: make([]Library, len(libraries))}
		for n, l := range libraries {
			out.Items[n] = Library{ID: l.PublicID, Name: l.Name, Kind: l.Kind}
		}
		return out, err
	})
	Handle(s, "GET /v1/libraries/{id}", WithProfile, func(r *http.Request, _ None) (Library, error) {
		l, err := s.db.Library(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return Library{}, refuse(err)
		}
		out := Library{ID: l.PublicID, Name: l.Name, Kind: l.Kind}
		out.Views, out.Sorts, out.Filters = browseBy(l.Kind)
		return out, nil
	})
	Handle(s, "GET /v1/items", WithProfile, func(r *http.Request, _ None) (ItemPage, error) {
		q, err := itemQuery(r.URL.Query())
		if err != nil {
			return ItemPage{}, err
		}
		page, err := s.db.Items(r.Context(), viewer(r), q)
		if err != nil {
			return ItemPage{}, refuse(err)
		}
		out := ItemPage{Items: itemShapes(page.Items), NextCursor: page.NextCursor}
		if q.WithTotal {
			out.Total = &page.Total
		}
		return out, nil
	})
	Handle(s, "GET /v1/items/{id}", WithProfile, func(r *http.Request, _ None) (Item, error) {
		detail, err := s.db.Item(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return Item{}, refuse(err)
		}
		return detailShape(detail), nil
	})
	Handle(s, "GET /v1/items/{id}/credits", WithProfile, func(r *http.Request, _ None) (CreditPage, error) {
		query := r.URL.Query()
		// The cursor is how many credits have been sent. A cast list does not change under a reader.
		offset, err := strconv.Atoi(query.Get("cursor"))
		if query.Get("cursor") != "" && (err != nil || offset < 0) {
			return CreditPage{}, ErrField("cursor", "This cursor does not belong to this list.")
		}
		limit, err := limitParam(query)
		if err != nil {
			return CreditPage{}, err
		}
		credits, total, err := s.db.Credits(r.Context(), viewer(r), r.PathValue("id"), query.Get("part"), offset, limit)
		if err != nil {
			return CreditPage{}, refuse(err)
		}
		out := CreditPage{Items: creditShapes(credits), Total: total}
		if offset+len(credits) < total && len(credits) > 0 {
			out.NextCursor = strconv.Itoa(offset + len(credits))
		}
		return out, nil
	})
	Handle(s, "GET /v1/items/{id}/related", WithProfile, func(r *http.Request, _ None) (Related, error) {
		related, err := s.db.Related(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return Related{}, refuse(err)
		}
		out := Related{Groups: []RelatedGroup{}}
		for _, kind := range []db.RelationKind{db.RelationCollectionMember, db.RelationSeriesMember, db.RelationExtra, db.RelationTrailer, db.RelationGuestArtist} {
			if items := related[kind]; len(items) > 0 {
				out.Groups = append(out.Groups, RelatedGroup{Relation: kind.String(), Items: itemShapes(items)})
			}
		}
		return out, nil
	})
	Handle(s, "GET /v1/people/{id}", WithProfile, func(r *http.Request, _ None) (Person, error) {
		p, err := s.db.Person(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return Person{}, refuse(err)
		}
		return Person{ID: p.PublicID, Name: p.Name, Biography: p.Biography, BirthDate: p.BirthDate, DeathDate: p.DeathDate,
			Birthplace: p.Birthplace, Images: imageMap(p.Images), TitleCount: p.Titles}, nil
	})
	Handle(s, "GET /v1/facets", WithProfile, func(r *http.Request, _ None) (Facets, error) {
		q, err := itemQuery(r.URL.Query())
		if err != nil {
			return Facets{}, err
		}
		facet := r.URL.Query().Get("facet")
		values, err := s.db.Facets(r.Context(), viewer(r), q, facet)
		if err != nil {
			return Facets{}, refuse(err)
		}
		out := Facets{Facet: facet, Values: make([]FacetValue, len(values))}
		for n, v := range values {
			out.Values[n] = FacetValue{Value: v.Value, Label: v.Label, Count: v.Count}
		}
		return out, nil
	})
	Handle(s, "GET /v1/home", WithProfile, func(r *http.Request, _ None) (Home, error) {
		continuing, err := s.db.ContinueWatching(r.Context(), viewer(r))
		if err != nil {
			return Home{}, err
		}
		recent, err := s.db.RecentlyAdded(r.Context(), viewer(r))
		if err != nil {
			return Home{}, err
		}
		// A row with nothing in it is left out, so an app never draws an empty shelf.
		out := Home{Rows: []HomeRow{}}
		for _, row := range []HomeRow{
			{ID: "continue", Title: "Continue Watching", Shape: "landscape", Items: itemShapes(continuing)},
			{ID: "recently_added", Title: "Recently Added", Shape: "poster", Items: itemShapes(recent)},
		} {
			if len(row.Items) > 0 {
				out.Rows = append(out.Rows, row)
			}
		}
		return out, nil
	})
	Handle(s, "PUT /v1/items/{id}/state", WithProfile, func(r *http.Request, req StateRequest) (StateReply, error) {
		if req.MadeAgoMs < 0 {
			return StateReply{}, ErrField("madeAgoMs", "This cannot be negative.")
		}
		edit := db.StateEdit{Watched: req.Watched, PositionMS: req.PositionMs, PositionText: req.Position, Favorite: req.Favorite,
			InList: req.InList, Hidden: req.Hidden, Rating: req.Rating.Value, ClearRating: req.Rating.Set && req.Rating.Value == nil,
			Ago: time.Duration(req.MadeAgoMs) * time.Millisecond}
		state, undo, err := s.db.SetState(r.Context(), viewer(r), r.PathValue("id"), edit)
		if err != nil {
			return StateReply{}, refuse(err)
		}
		out := StateReply{ItemState: stateShape(state)}
		if undo != "" {
			out.Undo = &UndoOffer{Token: undo, ExpiresAt: s.now().Add(db.UndoLifetime).UTC().Format(time.RFC3339)}
		}
		return out, nil
	})
	Handle(s, "POST /v1/items/state:undo", WithProfile, func(r *http.Request, req UndoRequest) (None, error) {
		err := s.db.UndoState(r.Context(), viewer(r), req.Token)
		if errors.Is(err, db.ErrNotFound) {
			// Used, expired, another profile's, or lost in a restart: all the same to the person.
			return None{}, ErrConflict("undo_expired", "It is too late to undo that.")
		}
		return None{}, err
	})
	// The feed is a stream, not one JSON reply, so it is registered by hand.
	feed := &Route{Pattern: "GET /v1/events", Access: WithProfile, Scope: ScopeRead}
	s.routes = append(s.routes, feed)
	s.mux.HandleFunc(feed.Pattern, func(w http.ResponseWriter, r *http.Request) { s.events(w, r, feed) })
}

// browseBy says what a library of each kind can be browsed by. Sorts list the direction each
// starts in. Every list also takes the same filters under a parent, a person or a tag.
func browseBy(kind string) ([]View, []Sort, []Filter) {
	sorts := []Sort{{db.SortTitle, "asc"}, {db.SortAdded, "desc"}, {db.SortYear, "desc"}, {db.SortRelease, "desc"}}
	filters := []Filter{{ID: "genre", Type: "tag"}, {ID: "year", Type: "year"}, {ID: "watched", Type: "boolean"},
		{ID: "favorite", Type: "boolean"}, {ID: "inList", Type: "boolean"}}
	resolution := Filter{ID: "resolution", Type: "choice", Values: []string{"sd", "720p", "1080p", "4k"}}
	switch kind {
	case db.LibraryMovies:
		return []View{{"movie"}}, sorts, append(filters, resolution)
	case db.LibraryShows:
		return []View{{"show"}, {"episode"}}, sorts, append(filters, resolution)
	case db.LibraryMusic:
		return []View{{"artist"}, {"album"}, {"track"}}, sorts[:3], filters
	case db.LibraryAudiobooks:
		return []View{{"audiobook"}}, sorts[:3], filters
	}
	return []View{{"book"}}, sorts[:3], filters
}

// defaultOrder is the direction a sort starts in when the request does not say.
var defaultOrder = map[string]bool{db.SortAdded: true, db.SortYear: true, db.SortRelease: true}

func boolParam(query url.Values, name string) (*bool, error) {
	switch query.Get(name) {
	case "":
		return nil, nil
	case "true":
		yes := true
		return &yes, nil
	case "false":
		no := false
		return &no, nil
	}
	return nil, ErrField(name, "Use true or false.")
}

func limitParam(query url.Values) (int, error) {
	if query.Get("limit") == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit < 1 {
		return 0, ErrField("limit", "Use a whole number of at least 1.")
	}
	return limit, nil // a limit above the largest page is served as the largest page
}

// itemQuery reads the parameters GET /v1/items and GET /v1/facets share. A parameter the
// server does not know is ignored.
func itemQuery(query url.Values) (db.ItemQuery, error) {
	q := db.ItemQuery{Library: query.Get("library"), Parent: query.Get("parent"), Person: query.Get("person"), Tag: query.Get("tag"),
		Collection: query.Get("collection"),
		Genre:      query.Get("genre"), Resolution: query.Get("resolution"), Sort: query.Get("sort"), Cursor: query.Get("cursor")}
	for _, name := range strings.FieldsFunc(query.Get("kind"), func(r rune) bool { return r == ',' }) {
		kind, ok := db.ParseItemKind(name)
		if !ok {
			return q, ErrField("kind", fmt.Sprintf("%q is not a kind of item.", name))
		}
		q.Kinds = append(q.Kinds, kind)
	}
	if year := query.Get("year"); year != "" {
		from, to, ranged := strings.Cut(year, "..")
		if !ranged {
			to = from
		}
		var errFrom, errTo error
		q.YearFrom, errFrom = strconv.Atoi(from)
		q.YearTo, errTo = strconv.Atoi(to)
		if errFrom != nil || errTo != nil || q.YearFrom > q.YearTo || q.YearFrom < 1 {
			return q, ErrField("year", `Use a year such as "1999" or a range such as "1990..1999".`)
		}
	}
	var err error
	if q.Watched, err = boolParam(query, "watched"); err != nil {
		return q, err
	}
	if q.Favorite, err = boolParam(query, "favorite"); err != nil {
		return q, err
	}
	if q.InList, err = boolParam(query, "inList"); err != nil {
		return q, err
	}
	if q.Limit, err = limitParam(query); err != nil {
		return q, err
	}
	if at := query.Get("at"); at != "" {
		if q.At, err = strconv.Atoi(at); err != nil || q.At < 0 {
			return q, ErrField("at", "Use a position in the list, counting from 0.")
		}
	}
	switch query.Get("order") {
	case "":
		q.Desc = defaultOrder[q.Sort]
	case "desc":
		q.Desc = true
	case "asc":
	default:
		return q, ErrField("order", "Use asc or desc.")
	}
	total, err := boolParam(query, "total")
	if err != nil {
		return q, err
	}
	q.WithTotal = total != nil && *total
	return q, nil
}

// How the change feed behaves. An app reconnects with the id of the last event it saw.
const (
	eventsPoll      = time.Second      // how often a stream looks for new changes
	eventsHeartbeat = 20 * time.Second // a comment line, so proxies keep the stream open
	eventsLifetime  = 10 * time.Minute // a stream ends before its access token does; the app reconnects
	eventsBurst     = 500              // more changes than this at once are sent as one "read everything again"
	eventsPerPoll   = 20               // more item changes than this in one look are sent as one event per library
)

// events is GET /v1/events: a server-sent event stream. A "change" event names one thing to
// read again and never carries it. A "resync" event means read again everything on screen;
// it is sent to an app whose cursor is older than the oldest change kept, and in place of a
// burst too large to be worth listing, such as a library import. Every event has an id, and
// that id is the cursor to reconnect with (as Last-Event-ID or ?cursor=).
func (s *Server) events(w http.ResponseWriter, r *http.Request, route *Route) {
	r, err := s.admit(r, route)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ctx, v := r.Context(), viewer(r)
	cursor, given := r.Header.Get("Last-Event-ID"), true
	if cursor == "" {
		cursor = r.URL.Query().Get("cursor")
	}
	after, err := strconv.ParseInt(cursor, 10, 64)
	if cursor == "" {
		// A new reader starts from now: it has just read what it shows.
		given = false
		if after, err = s.db.ChangesHead(ctx); err != nil {
			s.writeError(w, r, err)
			return
		}
	} else if err != nil || after < 0 {
		s.writeError(w, r, ErrField("cursor", "This cursor does not belong to this feed."))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	send := func(id int64, event string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, raw)
	}
	if !given {
		// Tells the app where it starts, so its first reconnect has a cursor.
		send(after, "ready", struct{}{})
	}
	poll, heartbeat, end := time.NewTicker(eventsPoll), time.NewTicker(eventsHeartbeat), time.NewTimer(eventsLifetime)
	defer poll.Stop()
	defer heartbeat.Stop()
	defer end.Stop()
	for {
		events, next, reread, err := s.db.Events(ctx, v, after, eventsBurst)
		switch {
		case err != nil:
			return
		case reread || next-after >= eventsBurst:
			if next, err = s.db.ChangesHead(ctx); err != nil {
				return
			}
			send(next, "resync", struct{}{})
		default:
			// During an import items change faster than a screen should redraw. Then the
			// feed says once per library that its lists changed, instead of naming every item.
			items, libraries := 0, map[string]int64{}
			for _, e := range events {
				if e.Topic == db.TopicItem {
					items++
					libraries[e.LibraryID] = e.ID
				}
			}
			for _, e := range events {
				switch {
				case e.Topic != db.TopicItem || items <= eventsPerPoll:
					send(e.ID, "change", Event{Topic: e.Topic.String(), Op: e.Op.String(), ID: e.PublicID, LibraryID: e.LibraryID})
				case libraries[e.LibraryID] == e.ID:
					send(e.ID, "change", Event{Topic: db.TopicLibrary.String(), Op: db.OpUpdated.String(), LibraryID: e.LibraryID})
				}
			}
		}
		after = next
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-ctx.Done():
			return
		case <-end.C:
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": still here\n\n")
		case <-poll.C:
		}
	}
}
