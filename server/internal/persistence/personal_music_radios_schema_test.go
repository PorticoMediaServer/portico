package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// FX-183: the canonical pre-release schema keeps the profile-recency index and
// adds a seed-leading index beside it. Both are part of a fresh install, and
// SQLite must use the seed index for the foreign key's child-key equality.
func TestPersonalMusicRadiosSeedIndexServesForeignKeyEquality(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seed, err := indexColumns(db, "personal_music_radios_seed")
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != 1 || seed[0] != "seed_id" {
		t.Fatalf("seed index columns = %v, want [seed_id] leading", seed)
	}
	recent, err := indexColumns(db, "personal_music_radios_recent")
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 || recent[0] != "profile_id" || recent[1] != "last_started_ms" || recent[2] != "seed_id" {
		t.Fatalf("recency index columns = %v, want profile_id, last_started_ms, seed_id", recent)
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM personal_music_radios WHERE seed_id=?`, int64(7))
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err = rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, " ")
	if !strings.Contains(plan, "SEARCH") || strings.Contains(plan, "SCAN personal_music_radios") {
		t.Fatalf("seed equality plan is not a search: %s", plan)
	}
	if !strings.Contains(plan, "personal_music_radios_seed") {
		t.Fatalf("seed equality plan does not use the seed index: %s", plan)
	}

	if err = VerifySchemaObjects(context.Background(), db); err != nil {
		t.Fatalf("a fresh install fails its own schema check: %v", err)
	}
	if err = VerifySeededRows(context.Background(), db); err != nil {
		t.Fatalf("a fresh install fails its own seeded-row check: %v", err)
	}
}

func indexColumns(db *sql.DB, index string) ([]string, error) {
	rows, err := db.Query(`PRAGMA index_info(` + index + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[int]string{}
	width := 0
	for rows.Next() {
		var seqno, cid int
		var name string
		if err = rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		columns[seqno] = name
		if seqno+1 > width {
			width = seqno + 1
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if width == 0 {
		return nil, sql.ErrNoRows
	}
	ordered := make([]string, width)
	for index, name := range columns {
		ordered[index] = name
	}
	return ordered, nil
}
