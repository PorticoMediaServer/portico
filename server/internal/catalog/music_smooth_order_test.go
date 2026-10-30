package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestMusicSmoothOrderRequiresEveryCurrentPermittedWindowVector(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Artist"), "Album", 2026)
	items := []catalogtest.Item{}
	ids := []string{}
	for n := 0; n < 6; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("/music/%d.flac", n), fmt.Sprintf("Song %d", n))
		x, y := float32(1), float32(0)
		if n%2 == 1 {
			x, y = 0, 1
		}
		c.SonicEmbedding(item, x, y)
		items, ids = append(items, item), append(ids, item.Public)
	}
	hidden := c.Song(album, 7, "/music/private.flac", "Private")
	c.SonicEmbedding(hidden, 0, 1)
	c.Attributes(hidden.ID, "label", "private")
	c.Drain()
	viewer := Viewer{Profile: identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}), Libraries: []string{"music"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	s := New(c.DB)
	got, err := s.MusicSmoothOrder(viewer, ids, "")
	if err != nil || !got.BySound || got.Model == nil || !reflect.DeepEqual(got.Positions, []int64{0, 2, 4, 1, 3, 5}) {
		t.Fatal(got, err)
	}
	// Repeated songs remain separate queue occurrences, never deduplicated.
	duplicates := append(append([]string{}, ids...), ids[0])
	got, err = s.MusicSmoothOrder(viewer, duplicates, "")
	if err != nil || !got.BySound || len(got.Positions) != len(duplicates) {
		t.Fatal(got, err)
	}
	sort.Slice(got.Positions, func(i, j int) bool { return got.Positions[i] < got.Positions[j] })
	for n, position := range got.Positions {
		if position != int64(n) {
			t.Fatal("lost an occurrence", got)
		}
	}
	plain := []int64{0, 1, 2, 3, 4, 5}
	got, err = s.MusicSmoothOrder(viewer, ids, hidden.Public)
	if err != nil || got.BySound || got.Model != nil || !reflect.DeepEqual(got.Positions, plain) {
		t.Fatal("forbidden previous song influenced order", got, err)
	}
	if _, err := s.MusicSmoothOrder(viewer, []string{hidden.Public}, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("hidden window member was admitted", err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, items[2].ID)
	got, err = s.MusicSmoothOrder(viewer, ids, "")
	if err != nil || got.BySound || got.Model != nil || !reflect.DeepEqual(got.Positions, plain) {
		t.Fatal("partial/stale coverage claimed sound order", got, err)
	}
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256=? WHERE entity_id=?`, sonic.ModelSHA256, items[2].ID)
	c.Exec(`INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES('music',0,1)`)
	got, err = s.MusicSmoothOrder(viewer, ids, "")
	if err != nil || got.BySound || got.Model != nil || !reflect.DeepEqual(got.Positions, plain) {
		t.Fatal("Off changed ordinary seeded order", got, err)
	}
}
