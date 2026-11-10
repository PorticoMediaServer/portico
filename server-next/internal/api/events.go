package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Event is one change on GET /v1/events: what to read again. It never carries the thing
// itself. ID is absent for something deleted; LibraryID then says which library's lists changed.
type Event struct {
	Topic     string `json:"topic"`
	Op        string `json:"op"`
	ID        string `json:"id,omitempty"`
	LibraryID string `json:"libraryId,omitempty"`
}

// EventPage is the answer of GET /v1/events?poll=true, for an app or a network that cannot
// hold a stream open. It waits up to `wait` seconds (25 unless said, 30 at most) for
// something to change and answers at once when something has. Cursor is what to send as
// `cursor` next time; with no cursor sent, the answer is immediate and only says where to
// start. Resync means: read again everything on screen.
//
// Without poll=true the route is a server-sent event stream of the same things, each event
// with an id that is the cursor to reconnect with (as Last-Event-ID or ?cursor=):
//
//   - "ready": the first event of a stream opened with no cursor. Data {}.
//   - "change": data is an Event.
//   - "resync": read again everything on screen. Data {}.
//   - "end": the server is closing the stream, which it does every ten minutes; connect
//     again from this id. Data {}. A stream that stops without it was cut off.
type EventPage struct {
	Events []Event `json:"events"`
	Cursor string  `json:"cursor"`
	Resync bool    `json:"resync,omitempty"`
}

// How the change feed behaves.
const (
	eventsPoll      = time.Second      // how often a waiting reader looks for new changes
	eventsHeartbeat = 20 * time.Second // a comment line, so proxies keep the stream open
	eventsBurst     = 500              // more changes than this at once are sent as one "read everything again"
	eventsPerPoll   = 20               // more item changes than this in one look are sent as one event per library
	eventsWait      = 25 * time.Second // how long a poll waits unless it says
	eventsWaitMost  = 30 * time.Second
)

func (s *Server) eventRoutes() {
	// The feed is a stream or a held request, not one plain JSON reply, so it is registered by hand.
	feed := &Route{Pattern: "GET /v1/events", Access: WithProfile, Scope: ScopeRead, Response: reflect.TypeFor[EventPage]()}
	s.routes = append(s.routes, feed)
	s.mux.HandleFunc(feed.Pattern, func(w http.ResponseWriter, r *http.Request) { s.events(w, r, feed) })
}

// sent is one event as the feed words it: its id, its name and, for a change, what changed.
type sent struct {
	id     int64
	name   string
	change Event
}

// changesAfter reads what this viewer should hear after a cursor, and the cursor to ask
// from next. A reader whose cursor is older than the oldest change kept, or a burst too
// large to be worth listing (a library import), is one "resync".
func (s *Server) changesAfter(ctx context.Context, v db.Viewer, after int64) ([]sent, int64, error) {
	events, next, reread, err := s.db.Events(ctx, v, after, eventsBurst)
	if err != nil {
		return nil, after, err
	}
	if reread || next-after >= eventsBurst {
		if next, err = s.db.ChangesHead(ctx); err != nil {
			return nil, after, err
		}
		return []sent{{id: next, name: "resync"}}, next, nil
	}
	// During an import items change faster than a screen should redraw. Then the feed says
	// once per library that its lists changed, instead of naming every item.
	items, libraries := 0, map[string]int64{}
	for _, e := range events {
		if e.Topic == db.TopicItem {
			items++
			libraries[e.LibraryID] = e.ID
		}
	}
	var out []sent
	for _, e := range events {
		switch {
		case e.Topic != db.TopicItem || items <= eventsPerPoll:
			out = append(out, sent{e.ID, "change", Event{Topic: e.Topic.String(), Op: e.Op.String(), ID: e.PublicID, LibraryID: e.LibraryID}})
		case libraries[e.LibraryID] == e.ID:
			out = append(out, sent{e.ID, "change", Event{Topic: db.TopicLibrary.String(), Op: db.OpUpdated.String(), LibraryID: e.LibraryID}})
		}
	}
	return out, next, nil
}

// events is GET /v1/events. See EventPage for what a reader is told.
func (s *Server) events(w http.ResponseWriter, r *http.Request, route *Route) {
	r, err := s.admit(r, route)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ctx, leave := s.feeds.join(r.Context(), CallerFrom(r.Context()))
	defer leave()
	r = r.WithContext(ctx)
	v, query := viewer(r), r.URL.Query()
	cursor, given := r.Header.Get("Last-Event-ID"), true
	if cursor == "" {
		cursor = query.Get("cursor")
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
	if poll, err := boolParam(query, "poll"); err != nil {
		s.writeError(w, r, err)
		return
	} else if poll != nil && *poll {
		s.pollEvents(w, r, v, after, given)
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
	poll, heartbeat, end := time.NewTicker(eventsPoll), time.NewTicker(eventsHeartbeat), time.NewTimer(s.eventsLifetime)
	defer poll.Stop()
	defer heartbeat.Stop()
	defer end.Stop()
	for {
		events, next, err := s.changesAfter(ctx, v, after)
		if err != nil {
			return
		}
		for _, e := range events {
			if e.name == "change" {
				send(e.id, e.name, e.change)
			} else {
				send(e.id, e.name, struct{}{})
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
			// Said out loud, so an app can tell the planned end from a dropped connection.
			send(after, "end", struct{}{})
			if flusher != nil {
				flusher.Flush()
			}
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": still here\n\n")
		case <-poll.C:
		}
	}
}

// pollEvents answers one held request: what changed after the cursor, as soon as anything
// has, or nothing once the wait is over.
func (s *Server) pollEvents(w http.ResponseWriter, r *http.Request, v db.Viewer, after int64, given bool) {
	wait := eventsWait
	if text := r.URL.Query().Get("wait"); text != "" {
		seconds, err := strconv.Atoi(text)
		if err != nil || seconds < 0 {
			s.writeError(w, r, ErrField("wait", "Use a whole number of seconds."))
			return
		}
		wait = min(time.Duration(seconds)*time.Second, eventsWaitMost)
	}
	out := EventPage{Events: []Event{}}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	for given {
		events, next, err := s.changesAfter(ctx, v, after)
		if err != nil && ctx.Err() == nil {
			s.writeError(w, r, err)
			return
		}
		after = next
		for _, e := range events {
			if e.name == "change" {
				out.Events = append(out.Events, e.change)
			} else {
				out.Resync = true
			}
		}
		if len(events) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			if r.Context().Err() != nil {
				return // the reader went away
			}
			given = false // the wait is over: answer with nothing new
		case <-time.After(eventsPoll):
		}
	}
	out.Cursor = strconv.FormatInt(after, 10)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}
