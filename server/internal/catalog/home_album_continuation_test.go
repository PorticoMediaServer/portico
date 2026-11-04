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
	rows, err := s.homeAudioResume(r, []ContentEntry{{ID: album.Public, Kind: "album"}})
	if err != nil || len(rows) != 0 {
		t.Fatalf("finished album still continues: %+v %v", rows, err)
	}
	// Another profile's listening is never this profile's continuation.
	other := homeRequestFixture("music")
	other.Profile, other.Viewer.Profile = "q", "q"
	if rows, err = s.homeAudioResume(other, []ContentEntry{{ID: album.Public, Kind: "album"}}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-profile continuation: %+v %v", rows, err)
	}
}

// W2-A5 (Home decision, 2 Nov): an album continues as an album, with its current
// song and how far through the album that song is, on Home and in the music
// library's Discover. The resume is the current song from its start, never
// mid-song.
func TestAlbumContinuationCarriesAlbumProgressOnHomeAndDiscover(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture("music")
	album := homeItem(t, db, "album")
	// Each fixture song is 5,400 s long: paused in song 2, one song of three is behind.
	listen := func(id, state string, at time.Time) {
		item := homeItem(t, db, id)
		c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,0,0,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET position=0`, item.ID, "play-"+id)
		c.Exec(`INSERT INTO progress_activity VALUES('p','music',?,?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at,state=excluded.state`, item.ID, at.Format(time.RFC3339), state)
	}
	listen("t1", "ended", r.Now.Add(-3*time.Minute))
	listen("t2", "paused", r.Now.Add(-time.Minute))
	c.Drain()
	check := func(where string, entries []ContentEntry) {
		t.Helper()
		for _, e := range entries {
			if e.ID != album.Public {
				continue
			}
			if e.Playback == nil || e.Playback.ItemID != homeItem(t, db, "t2").Public || e.Playback.StartSeconds == nil || *e.Playback.StartSeconds != 0 {
				t.Fatalf("%s: the album resumes at its current song's start: %+v", where, e.Playback)
			}
			if e.Duration == nil || *e.Duration != 16200 || e.ProgressSeconds == nil || *e.ProgressSeconds != 5400 {
				t.Fatalf("%s: album progress %v of %v, want 5400 of 16200", where, deref(e.ProgressSeconds), deref(e.Duration))
			}
			return
		}
		t.Fatalf("%s: the album is not continued: %+v", where, entries)
	}
	row, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{})
	if err != nil {
		t.Fatal(err)
	}
	check("Home", row.Entries)
	discovery, err := s.DiscoverWithRestrictions("music", "p", r.Viewer.Restrictions, r.Now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range discovery.Sections {
		if section.ID == "continue_listening" {
			found = true
			check("Discover", section.Items)
		}
	}
	if !found {
		t.Fatalf("music Discover has no Continue Listening: %+v", discovery.Sections)
	}
	// Another profile's listening continues nothing in this profile's Discover.
	if other, err := s.DiscoverWithRestrictions("music", "q", r.Viewer.Restrictions, r.Now); err != nil {
		t.Fatal(err)
	} else {
		for _, section := range other.Sections {
			if section.ID == "continue_listening" {
				t.Fatalf("cross-profile Discover continuation: %+v", section.Items)
			}
		}
	}
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
