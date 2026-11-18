package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Home's Recently Released walks its index without looking titles up in the table, for a
// profile that sees everything, one with a rating limit and one kept from a label: a walk that
// had to read each episode's row read thousands of scattered pages for one Home in a crowd.
func TestRecentlyReleasedWalksOnlyItsIndex(t *testing.T) {
	ctx := t.Context()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.read.SetMaxOpenConns(1) // one read connection, so its kept statements hold every walk
	if err := d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tags (public_id, kind, name, norm_name) VALUES ('tag_kept', ?, 'Kept', 'kept')`, TagLabel)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	twelve := 12
	for _, p := range []SeedProfile{{Name: "all"}, {Name: "rated", MaxRatingAge: &twelve}, {Name: "kept", BlockedTags: []string{"tag_kept"}}} {
		v, err := d.CreateSeedProfile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.HomeRowItems(ctx, v, "recently_released", ItemQuery{Limit: 40}); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := d.read.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var walks []string
	conn.Raw(func(c any) error {
		for e := c.(*keptConn).used.Front(); e != nil; e = e.Next() {
			if text := e.Value.(*keptStatement).text; strings.HasPrefix(text, "SELECT i.id, i.release_date FROM items i ") {
				walks = append(walks, text)
			}
		}
		return nil
	})
	if len(walks) < 3 { // a walk for each profile, and on an empty library the walk for titles with no date
		t.Fatalf("%d walks, want at least one for each profile", len(walks))
	}
	for _, walk := range walks {
		rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+walk, sql.Named("a1", "2026-11-18"))
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if !strings.Contains(strings.Join(plan, "; "), "COVERING INDEX items_home_release") {
			t.Fatalf("the walk reads the table:\n%s\n%s", walk, strings.Join(plan, "; "))
		}
	}
}
