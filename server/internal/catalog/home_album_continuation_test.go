package catalog

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// W2-A5: an album in Continue Listening resumes at the start of its last
// played song, or at the next song when that one finished; a finished album
// leaves the row's continuation. Songs keep no mid-track position.
func TestHomeAlbumContinuesAtItsCurrentSong(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture("music")
	album := homeItem(t, db, "album")
	listen := func(id, state string, at time.Time) {
		item := homeItem(t, db, id)
		c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,0,0,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET position=0`, item.ID, "play-"+id)
		c.Exec(`INSERT INTO progress_activity VALUES('p','music',?,?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at,state=excluded.state`, item.ID, at.Format(time.RFC3339), state)
	}
	target := func() (string, *float64, bool) {
		t.Helper()
		row, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range row.Entries {
			if e.ID != album.Public {
				continue
			}
			if e.Playback == nil {
				return "", nil, true
			}
			found := false
			for _, resume := range row.Resumes {
				if resume.EntryID == album.Public && resume.Target.ID == e.Playback.ItemID && resume.Target.Playback != nil && resume.Target.Playback.ItemID == resume.Target.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("no resume target for the album: %+v", row.Resumes)
			}
			return e.Playback.ItemID, e.Playback.StartSeconds, true
		}
		return "", nil, false
	}
	now := r.Now
	// Paused part-way through track 2: Play starts track 2 from its start.
	listen("t1", "ended", now.Add(-3*time.Minute))
	listen("t2", "paused", now.Add(-time.Minute))
	if id, start, ok := target(); !ok || id != homeItem(t, db, "t2").Public || start == nil || *start != 0 {
		t.Fatalf("current song: %q %v %v", id, start, ok)
	}
	// Track 2 finished: the next song.
	listen("t2", "ended", now)
	if id, _, ok := target(); !ok || id != homeItem(t, db, "t3").Public {
		t.Fatalf("next song: %q %v", id, ok)
	}
	// The last song finished: the album is done, and no continuation names it.
	listen("t3", "ended", now.Add(time.Minute))
	rows, err := s.homeAlbumResumes(r, []ContentEntry{{ID: album.Public, Kind: "album"}})
	if err != nil || len(rows) != 0 {
		t.Fatalf("finished album still continues: %+v %v", rows, err)
	}
	// Another profile's listening is never this profile's continuation.
	other := homeRequestFixture("music")
	other.Profile, other.Viewer.Profile = "q", "q"
	if rows, err = s.homeAlbumResumes(other, []ContentEntry{{ID: album.Public, Kind: "album"}}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-profile continuation: %+v %v", rows, err)
	}
}
