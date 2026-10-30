package eventfeed

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func boundedFeed(t *testing.T) (*sql.DB, *Hub, identity.Principal) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := New(db)
	h.LibraryVisible = func(_ *sql.Tx, _ identity.Principal, library string) bool { return library != "hidden" }
	return db, h, identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
}

func appendRows(t *testing.T, db *sql.DB, rows func(tx *sql.Tx) error) {
	t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, rows); err != nil {
		t.Fatal(err)
	}
}

// pageBytes is what the page costs on the wire as SSE, the larger encoding.
func pageBytes(t *testing.T, page Page) int {
	t.Helper()
	total := 0
	for _, event := range page.Events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		total += FrameBytes(event.ID, event.Type, len(encoded))
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > total+pageReserve {
		t.Fatalf("long-poll body %d is larger than the SSE budget %d", len(body), total+pageReserve)
	}
	return total
}

// Forty 4 KiB events (40 raw rows, inside the row bound) do not fit one
// 64 KiB page. Each page stays inside the budget with whole frames only, the
// cursor stops at the last delivered row, and the continuation resumes there
// with nothing lost or repeated.
func TestPagesStopAtTheByteBudgetAndContinueWithoutLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, h, p := boundedFeed(t)
	start, err := h.Read(context.Background(), p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", EventDataBytes-64)
	appendRows(t, db, func(tx *sql.Tx) error {
		for i := 0; i < 40; i++ {
			if err := apievents.Append(tx, apievents.ProfileAudience("local", "member", "one"), "settings.changed", "settings", strconv.Itoa(i), "1", map[string]string{"note": payload}); err != nil {
				return err
			}
			// Hidden rows between visible ones are consumed with them.
			if err := apievents.Append(tx, apievents.LibraryAudience("hidden"), "library.changed", "library", "hidden", "1", nil); err != nil {
				return err
			}
		}
		return nil
	})
	cursor, seen, pages := start.NextAfter, []string{}, 0
	for {
		page, err := h.Read(context.Background(), p, false, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if pages > 5 {
			t.Fatal("paging did not finish")
		}
		if bytes := pageBytes(t, page); bytes > PageBytes-pageReserve {
			t.Fatalf("page of %d events costs %d bytes", len(page.Events), bytes)
		}
		for _, event := range page.Events {
			if event.Type != "settings.changed" {
				t.Fatalf("unexpected frame %+v", event)
			}
			seen = append(seen, event.Resource.ID)
		}
		if !page.More {
			break
		}
		if len(page.Events) == 0 {
			t.Fatal("a More page must carry the frames that filled it")
		}
		cursor = page.NextAfter
	}
	if pages < 3 || len(seen) != 40 {
		t.Fatalf("want 40 events over several pages, got %d in %d", len(seen), pages)
	}
	for i, id := range seen {
		if id != strconv.Itoa(i) {
			t.Fatalf("events out of order, lost or repeated: %v", seen)
		}
	}
}

// A deliverable row outside the producer envelope resyncs the reader. The same
// row in an audience the reader cannot hear changes nothing for it: its size
// is no signal.
func TestOversizedRowsResyncOnlyTheirOwnAudience(t *testing.T) {
	db, h, p := boundedFeed(t)
	other := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "two"}}
	mine, err := h.Read(context.Background(), p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := h.Read(context.Background(), other, false, "")
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, db, func(tx *sql.Tx) error {
		if err := apievents.Append(tx, apievents.ProfileAudience("local", "member", "two"), "settings.changed", "settings", "big", "1", map[string]string{"note": strings.Repeat("y", EventDataBytes)}); err != nil {
			return err
		}
		return apievents.Append(tx, apievents.ProfileAudience("local", "member", "one"), "settings.changed", "settings", "small", "1", nil)
	})
	page, err := h.Read(context.Background(), p, false, mine.NextAfter)
	if err != nil || page.More || len(page.Events) != 1 || page.Events[0].Resource.ID != "small" {
		t.Fatalf("another audience's oversized row reached this reader: %+v %v", page, err)
	}
	page, err = h.Read(context.Background(), other, false, theirs.NextAfter)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "stream.resync" || page.Events[0].Data != nil || page.Events[0].Revision != "" {
		t.Fatalf("own oversized row must resync without metadata: %+v %v", page, err)
	}
	if strings.Contains(page.NextAfter, "big") || page.Events[0].ID != page.NextAfter {
		t.Fatalf("resync must sit at its own cursor: %+v", page)
	}
}

// Cursors carry no readable position, differ on every issue, and open only
// for the reader they were issued to while their key and lifetime last.
// Anything else resyncs; nothing is refused or explained.
func TestCursorsAreOpaqueAndBoundToTheirReader(t *testing.T) {
	db, h, p := boundedFeed(t)
	appendRows(t, db, func(tx *sql.Tx) error {
		return apievents.Append(tx, "server", "list.changed", "list", "guide", "1", nil)
	})
	first, err := h.Read(context.Background(), p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Read(context.Background(), p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.NextAfter == again.NextAfter || !strings.HasPrefix(first.NextAfter, cursorPrefix) || len(first.NextAfter) > cursorMaxLength || !CursorWellFormed(first.NextAfter) || first.NextAfter == "1" {
		t.Fatalf("cursor is not an opaque, unlinkable token: %q %q", first.NextAfter, again.NextAfter)
	}
	resyncs := func(reader identity.Principal, administrative bool, cursor string) bool {
		t.Helper()
		page, err := h.Read(context.Background(), reader, administrative, cursor)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Events) == 1 && page.Events[0].Type == "stream.resync"
	}
	if resyncs(p, false, first.NextAfter) {
		t.Fatal("own fresh cursor resynced")
	}
	otherProfile := p
	otherProfile.ProfileID = "two"
	otherAccount := p
	otherAccount.AccountID = "other"
	tampered := []byte(first.NextAfter)
	tampered[len(tampered)-3] ^= 1
	for name, c := range map[string]struct {
		reader identity.Principal
		admin  bool
		cursor string
	}{
		"another profile":   {otherProfile, false, first.NextAfter},
		"another account":   {otherAccount, false, first.NextAfter},
		"a changed role":    {p, true, first.NextAfter},
		"a tampered cursor": {p, false, string(tampered)},
		"a numeric cursor":  {p, false, "0"},
		"a foreign prefix":  {p, false, "e2." + first.NextAfter[3:]},
	} {
		if !resyncs(c.reader, c.admin, c.cursor) {
			t.Fatalf("%s did not resync", name)
		}
	}
	// A restart (a fresh key) and an expired cursor both resync.
	restarted := New(db)
	if page, err := restarted.Read(context.Background(), p, false, first.NextAfter); err != nil || len(page.Events) != 1 || page.Events[0].Type != "stream.resync" {
		t.Fatalf("a cursor survived a restart: %+v %v", page, err)
	}
	now := time.Now()
	h.keys.now = func() time.Time { return now.Add(cursorLife + time.Minute) }
	if !resyncs(p, false, first.NextAfter) {
		t.Fatal("an expired cursor was accepted")
	}
}

// A rotated key still opens the cursors it issued; one rotation further, they
// resync. The seal count rotates as surely as age does.
func TestCursorKeysRotateAndKeepOnePreviousKey(t *testing.T) {
	var k cursorKeys
	now := time.Now()
	k.now = func() time.Time { return now }
	b := cursorBinding(identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p"}}, "device", false)
	old := k.seal(7, b)
	k.seals = cursorKeySeals
	fresh := k.seal(9, b)
	if position, ok := k.open(old, b); !ok || position != 7 {
		t.Fatal("the previous key stopped opening its cursors")
	}
	if position, ok := k.open(fresh, b); !ok || position != 9 {
		t.Fatal("the new key does not open its own cursor")
	}
	k.seals = cursorKeySeals
	k.seal(11, b)
	if _, ok := k.open(old, b); ok {
		t.Fatal("a cursor outlived two rotations")
	}
	if _, ok := k.open(fresh, b); !ok {
		t.Fatal("the previous key stopped opening its cursors after one rotation")
	}
	before := k.current
	now = now.Add(cursorLife)
	k.seal(13, b)
	if k.current == before {
		t.Fatal("a day-old key was not rotated")
	}
}

func TestMalformedCursorShapes(t *testing.T) {
	for raw, ok := range map[string]bool{
		"":                                 true,
		"0":                                true,
		"e1.AbC-_":                         true,
		strings.Repeat("a", 128):           true,
		strings.Repeat("a", 129):           false,
		"e1.abc def":                       false,
		"e1.abc/def":                       false,
		"e1.abc+def":                       false,
		"e1.é":                        false,
		"e1.abc\ndata: {\"type\":\"evil\"}": false,
	} {
		if CursorWellFormed(raw) != ok {
			t.Fatalf("CursorWellFormed(%q) != %v", raw, ok)
		}
	}
}

func TestParkedPollPermits(t *testing.T) {
	h := New(nil)
	if _, ok := h.AdmitPoll(""); ok {
		t.Fatal("an empty installation key was pooled")
	}
	var releases []func()
	for i := 0; i < ParkedPolls; i++ {
		release, ok := h.AdmitPoll("account:installation")
		if !ok {
			t.Fatalf("permit %d refused", i+1)
		}
		releases = append(releases, release)
	}
	if _, ok := h.AdmitPoll("account:installation"); ok {
		t.Fatal("a fifth parked poll was admitted")
	}
	if release, ok := h.AdmitPoll("account:other-installation"); !ok {
		t.Fatal("another installation shares the limit")
	} else {
		release()
	}
	releases[0]()
	releases[0]() // a second release must not free another request's permit
	if _, ok := h.AdmitPoll("account:installation"); !ok {
		t.Fatal("a released permit was not returned")
	}
	if _, ok := h.AdmitPoll("account:installation"); ok {
		t.Fatal("a double release returned two permits")
	}
	h.mu.Lock()
	if _, kept := h.polls["account:other-installation"]; kept {
		h.mu.Unlock()
		t.Fatal("an idle installation key stayed in memory")
	}
	h.mu.Unlock()
}

// A reader that hears none of a busy server's events is still woken once
// enough rows pass, reads one page and moves past them, so it never falls a
// page behind for lack of a turn. A few unrelated rows do not wake it.
func TestQuietReadersAreWokenBeforeTheyFallAPageBehind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, h, p := boundedFeed(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake, release := h.SubscribeFor(p, false)
	defer release()
	go h.Run(ctx)
	other := apievents.ProfileAudience("local", "member", "two")
	write := func(n int) {
		appendRows(t, db, func(tx *sql.Tx) error {
			for i := 0; i < n; i++ {
				if err := apievents.Append(tx, other, "settings.changed", "settings", "x", "1", nil); err != nil {
					return err
				}
			}
			return nil
		})
	}
	// The first pass after start wakes everyone once.
	write(1)
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("the first pass did not wake readers")
	}
	write(5)
	select {
	case <-wake:
		t.Fatal("a few unrelated rows woke a quiet reader")
	case <-time.After(3 * time.Second):
	}
	write(lagWake)
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("a quiet reader was left to fall behind")
	}
}
