package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestListeningClustersAggregateOnlyCompleteCurrentOriginalTagHeads(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Actual artist"), "Album", 2026)
	a := c.Song(album, 1, "/music/a.flac", "A")
	b := c.Song(album, 2, "/music/b.flac", "B")
	for _, item := range []catalogtest.Item{a, b} {
		sonicStoredFixture(t, c, item, 1, 0)
	}
	c.Drain()
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n, item := range []catalogtest.Item{a, a, b} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed) VALUES(?,'local','account','profile','Fixture',?,'music',7,?,?,?,100000,1)`, fmt.Sprint(n), item.ID, item.Public, at.Add(-time.Hour).UnixMilli(), at.Add(-time.Hour).UnixMilli()); err != nil {
				return err
			}
		}
		for n, item := range []catalogtest.Item{a, b} {
			for index := 0; index < sonic.TagCount; index++ {
				score := 0.0
				if index == 3 {
					score = []float64{.9, .3}[n]
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO music_sonic_tags(result_id,entity_id,tag_index,score) SELECT result_id,entity_id,?,? FROM music_sonic_embeddings WHERE entity_id=?`, index, score, item.ID); err != nil {
					return err
				}
			}
		}
		// A different source result can legally have another cue entity in its
		// composite key. It must never replace this entity's current tag head.
		if _, err := tx.ExecContext(ctx, `INSERT INTO music_sonic_embeddings(result_id,entity_id,model_id,model_version,model_sha256,source_start_us,source_end_us,dimensions,vector) SELECT other.result_id,own.entity_id,own.model_id,own.model_version,own.model_sha256,own.source_start_us,own.source_end_us,own.dimensions,own.vector FROM music_sonic_embeddings own CROSS JOIN music_sonic_embeddings other WHERE own.entity_id=? AND other.entity_id=?`, a.ID, b.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO music_sonic_tags(result_id,entity_id,tag_index,score) SELECT tag.result_id,?,tag.tag_index,1 FROM music_sonic_tags tag WHERE tag.entity_id=?`, a.ID, b.ID)
		return err
	})
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}}
	s := New(c.DB)
	got, err := s.ListeningClusters(viewer, at)
	if err != nil || len(got.Groups) != 1 {
		t.Fatal(got, err)
	}
	group := got.Groups[0]
	if group.TaggedTracks != 2 || len(group.Tags) != sonic.TagCount || group.Tags[3].Index != 3 || math.Abs(group.Tags[3].Score-.7) > 1e-9 || group.Tags[0].Score != 0 {
		t.Fatal("wrong weighted head or zero treated as absent", group)
	}
	c.Exec(`DELETE FROM music_sonic_tags WHERE entity_id=? AND tag_index=49`, b.ID)
	got, err = s.ListeningClusters(viewer, at)
	if err != nil || len(got.Groups) != 1 || got.Groups[0].TaggedTracks != 1 || len(got.Groups[0].Tags) != 0 {
		t.Fatal("partial tag head became a cluster style", got, err)
	}
}
