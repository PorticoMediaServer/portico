package playbackv1

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func settleContainerProjection(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogtest.New(t, db).Drain()
}

// A container that can be as large as a library (a collection, a playlist)
// lists its keys in order from an index: the first page of a million-member
// collection costs its page, not a sort of all of it. Albums and books are
// index-ordered too. An artist, a show, a season and a disc are sorted, which
// is bounded by the one artist or show.
func TestLargeContainersListInIndexOrder(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"album", "book", "collection", "playlist"} {
		args := []any{"x"}
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+containers[kind].keys, args...)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		rows.Close()
		// "FOR LAST n TERMS" sorts within one group of the outer loop (an
		// artist's album's songs): bounded by the album. A full sort is not.
		if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") || strings.Contains(plan, "SCAN ") && !strings.Contains(plan, "USING") {
			t.Fatalf("%s sorts or scans:\n%s", kind, plan)
		}
	}
}

// The collection keys query lists members in member order: year, then sort
// title (or title), then id, as the collection writer records it in order_key.
// Recomputing order_key after an edit is the collections lane's job; here the
// query is checked against the same order, and removals drop out.
func TestCollectionOrderKeyFollowsTheItem(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	handle := c.Library("l", "L", "movie", "/m")
	type movie struct {
		key, title, sort string
		year             int
	}
	fixtures := []movie{
		{"a", "Zed", "", 1999},
		{"b", "The Apple", "Apple", 2001},
		{"c", "Apple", "", 2001},
		{"d", "Beta", "", 0},
		{"e", "Big 2", "", 2001},
		{"f", "Big", "", 2001},
	}
	names := catalogtest.Names{}
	for _, f := range fixtures {
		names[f.key] = c.Entity(compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: f.key, Title: f.title, SortTitle: f.sort, Year: f.year}, nil)
	}
	collection := c.Collection(handle, "C", names["d"], names["f"], names["a"], names["c"], names["b"], names["e"])
	// Member order as the writer records it: year, sort title, id.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n, key := range []string{"d", "f", "a", "c", "b", "e"} {
			if err := compactcatalog.SetCollectionMemberTx(ctx, tx, collection.ID, names[key].ID, strings.Repeat("0", 6-len(itoa(n)))+itoa(n), true); err != nil {
				return err
			}
		}
		return nil
	})
	settleContainerProjection(t, db)
	order := func(q string) string {
		rows, err := db.Query(q, collection.Public)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			out = append(out, id)
		}
		return strings.Join(out, ",")
	}
	old := `SELECT pid(i.public_id) FROM catalog_collection_members m JOIN catalog_entities i ON i.id=m.item_id JOIN catalog_entities c ON c.id=m.collection_id WHERE c.public_id=pid_blob(?) ORDER BY m.order_key,m.item_id`
	want := names.Publics("d", "f", "a", "c", "b", "e")
	if a, b := order(old), order(containers["collection"].keys); a != b || b != strings.Join(want, ",") {
		t.Fatalf("order changed: %s, now %s", a, b)
	}
	// Removing a member, or the item itself, removes its place.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCollectionMemberTx(ctx, tx, collection.ID, names["a"].ID, "", false)
	})
	c.Delete(names["b"].ID)
	settleContainerProjection(t, db)
	if a, b := order(old), order(containers["collection"].keys); a != b || strings.Contains(b, names["a"].Public) || strings.Contains(b, names["b"].Public) {
		t.Fatalf("after removals: %s, now %s", a, b)
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}
