package read

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRSAudioArtistUnionBeforePaging(t *testing.T) {
	f := audioRelationsFixture(t)
	// Independent SQL API union, with a small explicit current-visibility oracle.
	// Duplicate primary+featured evidence, hidden featured songs and artist IDs
	// unrelated to album ordering must not change the entity-ID page boundary.
	f.exec(t, `CREATE TEMP TABLE api_albums(id INTEGER PRIMARY KEY,artist INTEGER)`)
	f.exec(t, `CREATE TEMP TABLE api_songs(id INTEGER PRIMARY KEY,album INTEGER,visible INTEGER)`)
	f.exec(t, `CREATE TEMP TABLE api_credits(song INTEGER,artist INTEGER)`)
	f.exec(t, `INSERT INTO api_albums VALUES(90,2),(10,1),(30,2),(20,1)`)
	f.exec(t, `INSERT INTO api_songs VALUES(100,10,1),(101,30,1),(102,90,0)`)
	f.exec(t, `INSERT INTO api_credits VALUES(100,1),(101,1),(102,1)`)
	rows, err := f.conn.QueryContext(t.Context(), `SELECT a.id FROM api_albums a WHERE a.artist=1 OR EXISTS(SELECT 1 FROM api_songs s JOIN api_credits c ON c.song=s.id WHERE s.album=a.id AND c.artist=1 AND s.visible=1) ORDER BY a.id`)
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
	s := rsArtistAlbumSet{ids: make([]int64, 0, 3)}
	for _, id := range []int64{20, 10, 10, 30} {
		if err = s.add(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = s.page(0, 2); !errors.Is(err, ErrInput) {
		t.Fatal("partial union exposed")
	}
	s.complete = true
	first, done, err := s.page(0, 2)
	if err != nil || done {
		t.Fatal(first, done, err)
	}
	second, done, err := s.page(first[len(first)-1], 2)
	if err != nil || !done {
		t.Fatal(second, done, err)
	}
	got := append(append([]int64(nil), first...), second...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want API %v", got, want)
	}
}
func TestRSAudioArtistCapacity(t *testing.T) {
	s := rsArtistAlbumSet{ids: make([]int64, 0, 1)}
	if err := s.add(2); err != nil {
		t.Fatal(err)
	}
	if err := s.add(2); err != nil {
		t.Fatal("duplicate must not consume capacity", err)
	}
	if err := s.add(1); !errors.Is(err, governor.ErrSnapshotBudget) {
		t.Fatal("capacity became truncation", err)
	}
	if !reflect.DeepEqual(s.ids, []int64{2}) {
		t.Fatal("failed admission mutated storage")
	}
}
