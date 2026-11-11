package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Play history: every finished play on the server, for the owner and the administrators. It
// includes plays made while a person had "Pause history" on, and a play stays, reading as
// removed, after its title, its library, its profile or its account is gone.

// PlayEntry is one play.
type PlayEntry struct {
	PlayedAt time.Time `json:"playedAt"`
	// Username, then Profile when its name differs, then DeviceName name the person.
	Username   string `json:"username"`
	Profile    string `json:"profile,omitempty"`
	DeviceName string `json:"deviceName,omitempty"`
	// AccountID is absent once the account is removed; LibraryID once the library is.
	AccountID   string `json:"accountId,omitempty"`
	LibraryID   string `json:"libraryId,omitempty"`
	LibraryName string `json:"libraryName,omitempty"`
	// ItemID is absent once the title is removed; Title and Subtitle stay.
	ItemID    string `json:"itemId,omitempty"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	WatchedMs int64  `json:"watchedMs"`
	Away      bool   `json:"away"`
	// Delivery is original, repackaged or converted; absent when not known.
	Delivery string `json:"delivery,omitempty"`
}

// PlaySummary is the top people, busiest times and top played over a range.
type PlaySummary struct {
	People []PlayTotal `json:"people"`
	Titles []PlayTotal `json:"titles"`
	// Busiest counts plays by weekday (0 is Sunday) and hour, in the server's time zone.
	Busiest [7][24]int `json:"busiest"`
	// Partial says the range held more than a million plays and the newest million were read.
	Partial bool `json:"partial,omitempty"`
}

// PlayTotal is how much one person or one title was played.
type PlayTotal struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	Plays     int    `json:"plays"`
	WatchedMs int64  `json:"watchedMs"`
}

const playsPage = 100

func (s *Server) playHistoryRoutes() {
	Handle(s, "GET /v1/admin/play-history", Admin, func(r *http.Request, _ None) (Page[PlayEntry], error) {
		q, err := s.playQuery(r, false)
		if err != nil {
			return Page[PlayEntry]{}, err
		}
		q.Limit = playsPage + 1
		plays, err := s.db.Plays(r.Context(), q)
		out := Page[PlayEntry]{Items: []PlayEntry{}}
		if len(plays) > playsPage {
			plays = plays[:playsPage]
			out.NextCursor = strconv.FormatInt(plays[len(plays)-1].ID, 10)
		}
		for _, p := range plays {
			entry := PlayEntry{PlayedAt: p.PlayedAt.UTC(), Username: p.AccountName, DeviceName: p.DeviceName, AccountID: p.AccountID,
				LibraryID: p.LibraryID, LibraryName: p.LibraryName, ItemID: p.ItemID, Kind: p.Kind.String(), Title: p.Title,
				Subtitle: p.Subtitle, WatchedMs: p.WatchedMS, Away: p.Away, Delivery: p.Delivery.String()}
			if !strings.EqualFold(p.ProfileName, p.AccountName) {
				entry.Profile = p.ProfileName
			}
			out.Items = append(out.Items, entry)
		}
		return out, err
	})
	Handle(s, "GET /v1/admin/play-history/summary", Admin, func(r *http.Request, _ None) (PlaySummary, error) {
		q, err := s.playQuery(r, true)
		if err != nil {
			return PlaySummary{}, err
		}
		sum, err := s.db.SummarizePlays(r.Context(), q)
		out := PlaySummary{People: []PlayTotal{}, Titles: []PlayTotal{}, Busiest: sum.Busiest, Partial: sum.Stopped}
		for _, c := range sum.People {
			out.People = append(out.People, PlayTotal{ID: c.ID, Name: c.Name, Plays: c.Plays, WatchedMs: c.WatchedMS})
		}
		for _, c := range sum.Titles {
			out.Titles = append(out.Titles, PlayTotal{ID: c.ID, Name: c.Name, Detail: c.Detail, Plays: c.Plays, WatchedMs: c.WatchedMS})
		}
		return out, err
	})
}

// playQuery reads the filters the list and the summary share: person, library, kind, text and
// the period. A summary with no period is the last 30 days.
func (s *Server) playQuery(r *http.Request, summary bool) (db.PlayQuery, error) {
	query := r.URL.Query()
	q := db.PlayQuery{Asker: CallerFrom(r.Context()).UserID, AccountID: query.Get("person"), LibraryID: query.Get("library"),
		Text: strings.TrimSpace(query.Get("text"))}
	for _, name := range strings.FieldsFunc(query.Get("kind"), func(r rune) bool { return r == ',' }) {
		kind, ok := db.ParseItemKind(name)
		if !ok {
			return q, ErrField("kind", fmt.Sprintf("%q is not a kind of item.", name))
		}
		q.Kinds = append(q.Kinds, kind)
	}
	for name, at := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if raw := query.Get(name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return q, ErrField(name, "Give a time such as 2026-11-10T18:00:00Z.")
			}
			*at = t
		}
	}
	if summary && q.From.IsZero() {
		q.From = s.now().AddDate(0, 0, -30)
	}
	if !summary {
		if raw := query.Get("cursor"); raw != "" {
			before, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || before <= 0 {
				return q, ErrField("cursor", "This is not a cursor this list gave.")
			}
			q.Before = before
		}
	}
	return q, nil
}
