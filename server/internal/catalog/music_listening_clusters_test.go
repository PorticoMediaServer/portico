package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestListeningClustersUseOnlyCurrentProfilePermittedCompletedPlays(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	north := c.Album(c.Artist(library, "North artist"), "North album", 2026)
	east := c.Album(c.Artist(library, "East artist"), "East album", 2026)
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	songs := []catalogtest.Item{}
	for n := 0; n < 12; n++ {
		album := north
		x, y := float32(1), float32(0)
		if n >= 6 {
			album = east
			x, y = 0, 1
		}
		item := c.Song(album, n+1, fmt.Sprintf("/music/%d.flac", n), fmt.Sprintf("Song %d", n))
		sonicStoredFixture(t, c, item, x, y)
		songs = append(songs, item)
	}
	hidden := c.Song(north, 20, "/music/private.flac", "Private")
	sonicStoredFixture(t, c, hidden, .5, .5)
	c.Attributes(hidden.ID, "label", "private")
	other := c.Song(east, 21, "/music/other.flac", "Other profile")
	sonicStoredFixture(t, c, other, .5, .5)
	future := c.Song(east, 22, "/music/future.flac", "Future play")
	sonicStoredFixture(t, c, future, .5, .5)
	incomplete := c.Song(north, 23, "/music/incomplete.flac", "Incomplete")
	sonicStoredFixture(t, c, incomplete, .5, .5)
	c.Drain()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		insert := func(key, profile string, item catalogtest.Item, when time.Time, completed bool) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES(?,'local','account',?,'Fixture',?,'music',7,?,?,?,100000,?)`, key, profile, item.ID, item.Public, when.UnixMilli(), when.UnixMilli(), completed)
			return err
		}
		for n, item := range songs {
			if err := insert(fmt.Sprint(n), "profile", item, at.Add(-time.Hour), true); err != nil {
				return err
			}
			if n < 6 {
				if err := insert(fmt.Sprintf("again%d", n), "profile", item, at.Add(-time.Minute), true); err != nil {
					return err
				}
			}
		}
		if err := insert("other", "other", other, at.Add(-time.Minute), true); err != nil {
			return err
		}
		if err := insert("future", "profile", future, at.Add(time.Hour), true); err != nil {
			return err
		}
		if err := insert("incomplete", "profile", incomplete, at.Add(-time.Minute), false); err != nil {
			return err
		}
		// More-recent hidden events exceed the sample bound; permission filtering
		// must precede that bound rather than emptying the permitted sample.
		_, err := tx.ExecContext(ctx, `WITH RECURSIVE n(v) AS(SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<=?) INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) SELECT 'hidden-'||v,'local','account','profile','Fixture',?,'music',7,'Private',?,?,100000,1 FROM n`, sonic.MaxClusterPoints, hidden.ID, at.Add(-time.Second).UnixMilli(), at.Add(-time.Second).UnixMilli())
		return err
	})
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	service := New(c.DB)
	read := func() MusicListeningClusters {
		t.Helper()
		facts, err := service.ListeningClusters(viewer, at)
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	facts := read()
	if facts.EligibleTracks != 12 || facts.AnalyzedTracks != 12 || len(facts.Groups) != 2 || facts.Model == nil || facts.Model.SHA256 != sonic.ModelSHA256 {
		t.Fatal("history/profile/source evidence changed", facts)
	}
	seen := map[string]bool{}
	for _, group := range facts.Groups {
		if len(group.Tracks) != 6 || len(group.Artists) != 1 || len(group.CoverAlbums) != 1 {
			t.Fatal("invented or missing group facts", group)
		}
		for _, track := range group.Tracks {
			seen[track] = true
		}
	}
	for _, item := range songs {
		if !seen[item.Public] {
			t.Fatal("permitted completed track omitted", item.Public)
		}
	}
	for _, item := range []catalogtest.Item{hidden, other, future, incomplete} {
		if seen[item.Public] {
			t.Fatal("non-authorized history shaped a cluster", item.Public)
		}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, item := range songs {
			for index := 0; index < sonic.TagCount; index++ {
				score := 0.0
				if index == 7 { // Exact original MTT rock index.
					score = .8
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO music_sonic_tags(result_id,entity_id,tag_index,score) SELECT result_id,entity_id,?,? FROM music_sonic_embeddings WHERE entity_id=?`, index, score, item.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	row, err := service.MusicMixRow(viewer, at)
	if err != nil || len(row) != 2 || row[0].NameSource.Style == nil || row[0].NameSource.Style.Tag != "rock" || row[0].NameSource.Style.Model != sonic.ModelID || row[0].NameSource.Artist != nil || row[1].NameSource.Style != nil || row[1].NameSource.Artist == nil {
		t.Fatal("duplicate strongest style did not fall back to actual top artist", row, err)
	}
	oldIDs := map[string]bool{row[0].ID: true, row[1].ID: true}
	insertPlay := func(key string, item catalogtest.Item) {
		c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES(?,'local','account','profile','Fixture',?,'music',7,?,?,?,100000,1)`, key, item.ID, item.Public, at.Add(-time.Second).UnixMilli(), at.Add(-time.Second).UnixMilli())
	}
	insertPlay("new-east-play", songs[7])
	row, err = service.MusicMixRow(viewer, at)
	if err != nil || len(row) != 2 || oldIDs[row[0].ID] || oldIDs[row[1].ID] {
		t.Fatal("another cluster's play retained old row identity", row, err)
	}
	stable := []string{row[0].ID, row[1].ID}
	insertPlay("hidden-new-play", hidden)
	row, err = service.MusicMixRow(viewer, at)
	if err != nil || len(row) != 2 || row[0].ID != stable[0] || row[1].ID != stable[1] {
		t.Fatal("hidden listening changed observable mix identity", row, err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, songs[0].ID)
	stale := read()
	if stale.EligibleTracks != 12 || stale.AnalyzedTracks != 11 {
		t.Fatal("stale model counted as analyzed", stale)
	}
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	off := read()
	if off.EligibleTracks != 0 || off.AnalyzedTracks != 0 || len(off.Groups) != 0 || off.Model != nil {
		t.Fatal("Off retained a mix selection", off)
	}
	if _, err := service.ListeningClusters(Viewer{Profile: "unbound", Libraries: []string{"music"}}, at); err == nil {
		t.Fatal("unbound profile borrowed history")
	}
}
