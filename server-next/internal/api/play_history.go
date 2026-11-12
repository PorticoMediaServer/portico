package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/console"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Play history: every play on the server, for the owner and the administrators. It
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
	// Withheld says the title is not the reader's to see: an administrator held to some
	// libraries reads a play outside them without its title, subtitle, item or library, as Now
	// playing lists such a stream.
	Withheld bool `json:"withheld,omitempty"`
}

// PlaySummary is the top people, busiest times and top played over a range.
type PlaySummary struct {
	People []PlayTotal `json:"people"`
	Titles []PlayTotal `json:"titles"`
	// Busiest counts plays by weekday (0 is Sunday) and hour, in the server's time zone.
	Busiest [7][24]int `json:"busiest"`
	// People, Titles and Busiest are counted from the newest plays of the range, at most
	// Window of them. CountedFrom is when the oldest play counted was, present when the range
	// held more: the page says "from your latest 20,000 plays, since …".
	Window      int        `json:"window"`
	CountedFrom *time.Time `json:"countedFrom,omitempty"`
	// Totals are the whole range's plays and time played, from the server's daily totals,
	// for the owner and administrators with every library and with no other filter set.
	Totals *PlayRangeTotals `json:"totals,omitempty"`
}

// PlayRangeTotals are a range's plays and time played, from the daily totals.
type PlayRangeTotals struct {
	Plays     int64 `json:"plays"`
	WatchedMs int64 `json:"watchedMs"`
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
		q.Limit = playsPage
		plays, next, err := s.db.Plays(r.Context(), q)
		out := Page[PlayEntry]{Items: []PlayEntry{}}
		if next > 0 {
			// A page can come back short with a cursor: a filter matched few of the plays one
			// page reads (see db.playsWindow). Reading on gives the older ones.
			out.NextCursor = strconv.FormatInt(next, 10)
		}
		for _, p := range plays {
			entry := PlayEntry{PlayedAt: p.PlayedAt.UTC(), Username: p.AccountName, DeviceName: p.DeviceName, AccountID: p.AccountID,
				LibraryID: p.LibraryID, LibraryName: p.LibraryName, ItemID: p.ItemID, Kind: p.Kind.String(), Title: p.Title,
				Subtitle: p.Subtitle, WatchedMs: p.WatchedMS, Away: p.Away, Delivery: p.Delivery.String(), Withheld: p.Withheld}
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
		if err != nil {
			return PlaySummary{}, err
		}
		out := PlaySummary{People: []PlayTotal{}, Titles: []PlayTotal{}, Busiest: sum.Busiest, Window: db.SummaryWindow}
		if sum.Stopped {
			from := sum.CountedFrom.UTC()
			out.CountedFrom = &from
		}
		if out.Totals, err = s.rangeTotals(r, q); err != nil {
			return PlaySummary{}, err
		}
		for _, c := range sum.People {
			out.People = append(out.People, PlayTotal{ID: c.ID, Name: c.Name, Plays: c.Plays, WatchedMs: c.WatchedMS})
		}
		for _, c := range sum.Titles {
			out.Titles = append(out.Titles, PlayTotal{ID: c.ID, Name: c.Name, Detail: c.Detail, Plays: c.Plays, WatchedMs: c.WatchedMS})
		}
		return out, err
	})
}

// rangeTotals are the range's plays and time played from the daily totals, which are kept for
// the whole server: only for an asker with every library, and only with no filter that the
// totals cannot follow (a person, a library, a kind, text).
func (s *Server) rangeTotals(r *http.Request, q db.PlayQuery) (*PlayRangeTotals, error) {
	if q.AccountID != "" || q.LibraryID != "" || len(q.Kinds) > 0 || q.Text != "" {
		return nil, nil
	}
	if all, err := s.db.HasAllLibraries(r.Context(), q.Asker); err != nil || !all {
		return nil, err
	}
	to := q.To
	if to.IsZero() {
		to = s.now()
	}
	first, last := console.Day(q.From.In(time.Local)), console.Day(to.In(time.Local))
	out := &PlayRangeTotals{}
	add := func(metric string, n int64) {
		switch {
		case strings.HasPrefix(metric, console.MetricPlays+"."):
			out.Plays += n
		case strings.HasPrefix(metric, console.MetricPlayedMs+"."):
			out.WatchedMs += n
		}
	}
	rows, err := s.db.DailyTotals(r.Context(), first, last, "")
	for _, row := range rows {
		add(row.Metric, row.Value)
	}
	if day, today := s.totals.Today(); day >= first && day <= last {
		for metric, n := range today {
			add(metric, n)
		}
	}
	return out, err
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
