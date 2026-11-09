package read

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRSAudioArtistUnionBeforeOrdering(t *testing.T) {
	f := audioRelationsFixture(t)
	// Independent SQL API union. The expected order is the legacy artist
	// releases ORDER BY, computed by SQLite over the fixture rather than by the
	// reducer: unknown year last, year DESC, title NOCASE, entity id.
	f.exec(t, `CREATE TEMP TABLE api_albums(id INTEGER PRIMARY KEY,artist INTEGER,year INTEGER,title TEXT)`)
	f.exec(t, `CREATE TEMP TABLE api_songs(id INTEGER PRIMARY KEY,album INTEGER,visible INTEGER)`)
	f.exec(t, `CREATE TEMP TABLE api_credits(song INTEGER,artist INTEGER)`)
	f.exec(t, `INSERT INTO api_albums VALUES(90,2,0,'Unknown'),(10,1,2024,'zebra'),(30,2,2019,'Middle'),(20,1,2024,'Zebra'),(40,1,2024,'Same'),(50,1,2024,'Same')`)
	f.exec(t, `INSERT INTO api_songs VALUES(100,10,1),(101,30,1),(102,90,0),(103,20,1),(104,40,1),(105,50,0)`)
	f.exec(t, `INSERT INTO api_credits VALUES(100,1),(101,1),(102,1),(103,1),(104,1)`)
	rows, err := f.conn.QueryContext(t.Context(), `SELECT a.id FROM api_albums a WHERE a.artist=1 OR EXISTS(SELECT 1 FROM api_songs s JOIN api_credits c ON c.song=s.id WHERE s.album=a.id AND c.artist=1 AND s.visible=1) ORDER BY CASE WHEN a.year=0 THEN 1 ELSE 0 END,a.year DESC,a.title COLLATE NOCASE,a.id`)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	s := rsArtistAlbumSet{ids: make([]int64, 0, 5), entries: make([]rsArtistAlbumEntry, 0, 5)}
	// Duplicate 10/20 evidence and insertion order must not affect the page
	// boundary: only the complete ordered set may page.
	for _, e := range []rsArtistAlbumEntry{{20, 2024, "Zebra"}, {40, 2024, "Same"}, {10, 2024, "zebra"}, {30, 2019, "Middle"}, {50, 2024, "Same"}, {20, 2024, "Zebra"}} {
		if err = s.add(e.id, e.year, e.title); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = s.page(rsArtistAlbumEntry{}, false, 2); !errors.Is(err, ErrInput) {
		t.Fatal("partial union exposed")
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	first, done, err := s.page(rsArtistAlbumEntry{}, false, 2)
	if err != nil || done || len(first) != 2 {
		t.Fatal(first, done, err)
	}
	second, done, err := s.page(first[len(first)-1], true, 3)
	if err != nil || !done || len(second) != 3 {
		t.Fatal(second, done, err)
	}
	got := []int64{first[0].id, first[1].id, second[0].id, second[1].id, second[2].id}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want API %v", got, want)
	}
}

func TestRSAudioArtistCapacity(t *testing.T) {
	s := rsArtistAlbumSet{ids: make([]int64, 0, 1), entries: make([]rsArtistAlbumEntry, 0, 1)}
	if err := s.add(2, 0, "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.add(2, 0, "b"); err != nil {
		t.Fatal("duplicate must not consume capacity", err)
	}
	if err := s.add(1, 0, "a"); !errors.Is(err, governor.ErrSnapshotBudget) {
		t.Fatal("capacity became truncation", err)
	}
	if !reflect.DeepEqual(s.ids, []int64{2}) || len(s.entries) != 1 {
		t.Fatal("failed admission mutated storage")
	}
}

// Any change to the ordering key must fail this independent SQL comparison.
func TestRSAudioArtistOrderCases(t *testing.T) {
	f := audioRelationsFixture(t)
	f.exec(t, `CREATE TEMP TABLE api_order(id INTEGER PRIMARY KEY,year INTEGER,title TEXT)`)
	f.exec(t, `INSERT INTO api_order VALUES(1,0,'Known'),(2,2024,'Known'),(3,2024,'known'),(4,2024,'Tie'),(5,2024,'Tie'),(6,2020,'é'||char(0)||'Z'),(7,2020,'é'||char(0)||'a'),(8,2020,'école'),(9,2020,'École')`)
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM api_order ORDER BY CASE WHEN year=0 THEN 1 ELSE 0 END,year DESC,title COLLATE NOCASE,id`)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	s := rsArtistAlbumSet{ids: make([]int64, 0, 9), entries: make([]rsArtistAlbumEntry, 0, 9)}
	for _, e := range []rsArtistAlbumEntry{{1, 0, "Known"}, {2, 2024, "Known"}, {3, 2024, "known"}, {4, 2024, "Tie"}, {5, 2024, "Tie"}, {6, 2020, "é\x00Z"}, {7, 2020, "é\x00a"}, {8, 2020, "école"}, {9, 2020, "École"}} {
		if err = s.add(e.id, e.year, e.title); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	page, done, err := s.page(rsArtistAlbumEntry{}, false, len(want))
	if err != nil || !done || len(page) != len(want) {
		t.Fatal(page, done, err)
	}
	var got []int64
	for _, e := range page {
		got = append(got, e.id)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want SQL %v", got, want)
	}
}
