package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
)

func TestEventsRecheckRetainedPersonalTitles(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	c := catalogtest.New(t, d.DB)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	books := c.Library("books", "Books", "audiobook", "/books")
	hidden := c.Movie(movies, "/movies/hidden.mp4", "Hidden film", 2020)
	visible := c.Movie(movies, "/movies/visible.mp4", "Visible film", 2020)
	book := c.Book(books, "Hidden book", "Author")
	part := c.BookFile(book, 1, "/books/hidden.m4b")
	c.Drain()
	p, err := d.Identity.Authenticate(session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	audience := apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID)
	h := New(d)
	start := eventStart(t, editorRequest(t, h, "GET", "/v1/events?waitMs=0", session.AccessToken, "", nil))
	if err := dbwork.WithWriteTx(context.Background(), d.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		for _, event := range []struct {
			kind, id string
			data     any
		}{
			{"item", hidden.Public, nil},
			{"book", book.Public, nil},
			{"library", "books", map[string]any{"titles": []string{part.Public, book.Public}, "personal": "1"}},
			{"item", visible.Public, nil},
			{"library", "movies", map[string]any{"titles": []string{visible.Public}, "personal": "2"}},
		} {
			if err := apievents.Append(tx, audience, "personal.changed", event.kind, event.id, "1", event.data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func(cursor string) eventfeed.Page {
		t.Helper()
		w := editorRequest(t, h, "GET", "/v1/events?after="+cursor+"&waitMs=0", session.AccessToken, "", nil)
		if w.Code != 200 {
			t.Fatalf("events: %d %s", w.Code, w.Body.String())
		}
		var page eventfeed.Page
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	open := read(start)
	if len(open.Events) != 5 {
		t.Fatalf("unrestricted retained events: %+v", open)
	}
	for _, item := range []catalogtest.Item{hidden, part} {
		c.Attributes(item.ID, "label", "Blocked")
	}
	if _, err := dbwork.ExecWrite(context.Background(), d.DB, dbwork.ClassInteractive, `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('profile','["blocked"]')`); err != nil {
		t.Fatal(err)
	}
	page := read(start)
	if len(page.Events) != 3 {
		t.Fatalf("current visibility: %+v", page)
	}
	// Both cursors stand at the newest event: neither repeats anything.
	for _, cursor := range []string{open.NextAfter, page.NextAfter} {
		if again := read(cursor); len(again.Events) != 0 {
			t.Fatalf("cursor repeated events: %+v", again)
		}
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hidden.Public, book.Public, part.Public} {
		if strings.Contains(string(raw), id) {
			t.Fatalf("retained hidden identifier %s: %s", id, raw)
		}
	}
	if strings.Contains(string(page.Events[0].Data), "titles") || !strings.Contains(string(page.Events[2].Data), visible.Public) {
		t.Fatalf("generic and visible title hints: %+v", page)
	}
}
