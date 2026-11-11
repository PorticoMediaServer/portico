package db_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A matched title falls due a refresh by its kind and age, counted from when it was last
// refreshed: two days for a title added in the last fourteen or a show still running, fourteen
// for a show, thirty for a film. Finding what is due reads the items_due index, so it costs
// what is due and not the library.
func TestARefreshFallsDueByKindAndAge(t *testing.T) {
	l := newLibrary(t, 300)
	day := 24 * time.Hour
	var film, show db.Item
	for _, lib := range l.libs {
		for _, it := range everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindMovie, db.KindShow}, Limit: 200}) {
			if it.Kind == db.KindMovie && film.ID == 0 {
				film = it
			}
			if it.Kind == db.KindShow && show.ID == 0 {
				show = it
			}
		}
	}
	match := func(it db.Item, continuing bool) {
		t.Helper()
		if err := l.d.ApplyDetails(ctx, db.Background, it.ID, db.ItemDetails{Match: db.MatchFound, Continuing: &continuing}); err != nil {
			t.Fatal(err)
		}
	}
	due := func(it db.Item) time.Duration {
		t.Helper()
		library, err := l.d.LibraryID(ctx, it.LibraryID)
		if err != nil {
			t.Fatal(err)
		}
		page, err := l.d.DueForRefresh(ctx, library, time.Now().Add(400*day).UnixMilli(), nil, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page {
			if r.ID == it.ID {
				return time.Until(time.UnixMilli(r.Due)).Round(day)
			}
		}
		t.Fatalf("%s is not listed as due", it.Title)
		return 0
	}
	// Just added: soon.
	if err := l.d.ExecForTest(ctx, `UPDATE items SET added_at = ? WHERE id IN (?, ?)`, time.Now().UnixMilli(), film.ID, show.ID); err != nil {
		t.Fatal(err)
	}
	match(film, false)
	match(show, true)
	if d := due(film); d != 2*day {
		t.Errorf("a film just added falls due in %v; want 2 days", d)
	}
	// Added long ago: by its kind, and a show still running soon.
	if err := l.d.ExecForTest(ctx, `UPDATE items SET added_at = added_at - ? WHERE id IN (?, ?)`, (15 * day).Milliseconds(), film.ID, show.ID); err != nil {
		t.Fatal(err)
	}
	match(film, false)
	match(show, true)
	if d := due(film); d != 30*day {
		t.Errorf("an older film falls due in %v; want 30 days", d)
	}
	if d := due(show); d != 2*day {
		t.Errorf("an older show still running falls due in %v; want 2 days", d)
	}
	match(show, false)
	if d := due(show); d != 14*day {
		t.Errorf("an older show that has ended falls due in %v; want 14 days", d)
	}
	// Nothing is due now, and the read says so from the index.
	library, err := l.d.LibraryID(ctx, film.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := l.d.DueForRefresh(ctx, library, time.Now().UnixMilli(), nil, 100); err != nil || len(page) != 0 {
		t.Errorf("due now: %v, %v", page, err)
	}
	plan, err := db.PlanForTest(l.d, `SELECT id FROM items WHERE library_id = ? AND match_state IN (1, 2) AND parent_id IS NULL AND refresh_due <= ?
		ORDER BY refresh_due, id LIMIT 200`, library, time.Now().UnixMilli())
	if err != nil || !strings.Contains(plan, "items_due") || strings.Contains(plan, "TEMP B-TREE") {
		t.Errorf("finding what is due does not read the items_due index in order: %q, %v", plan, err)
	}
}
