package catalog

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSparsePlayStateMatchesCanonicalMemberExpression(t *testing.T) {
	db, _, _ := browseFixture(t)
	c := catalogtest.New(t, db)
	tv := c.Library("tv-parity", "TV", "tv", "/tv-parity")
	show := c.Show(tv, "Watched default", 2000)
	season := c.Season(show, 1)
	c.Show(tv, "Empty", 2000)
	episodes := []int64{}
	for i := 0; i < 6; i++ {
		item := c.Episode(show, season, i+1, fmt.Sprintf("/tv/%d.mkv", i))
		episodes = append(episodes, item.ID)
		c.Fields(item.ID, map[string]any{"added_text": "2026-01-01T00:00:00Z"})
		rating := "G"
		if i%2 == 0 {
			rating = "R"
		}
		c.Attributes(item.ID, "contentRating", rating)
	}
	music := c.Library("mu", "Music", "music", "/mu")
	album := c.Album(c.Artist(music, "Artist"), "Album", 2000)
	for i := 0; i < 3; i++ {
		item := c.Song(album, i+1, fmt.Sprintf("/mu/%d.flac", i), "Track")
		c.Fields(item.ID, map[string]any{"added_text": "2026-01-01T00:00:00Z"})
	}
	c.Drain()
	c.Exec(`INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision,cleared_through) VALUES('p','show',?,1,'2026-10-10T00:00:00Z',1,''),('p','season',?,0,'2026-09-10T00:00:00Z',1,'2026-10-09T00:00:00Z'),('p','album',?,1,'2026-10-10T00:00:00Z',1,'')`, show.ID, season.ID, album.ID)
	c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,0,'2026-10-11T00:00:00Z'),('p',?,1,'2026-09-01T00:00:00Z')`, episodes[0], episodes[1])
	c.Exec(`UPDATE catalog_entities SET added_ms=NULL WHERE id=?`, episodes[2])
	c.Exec(`UPDATE catalog_entities SET added_ms=1798761600000 WHERE id=?`, episodes[3])
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,10000,0,0,'pb')`, episodes[2])
	field, _ := browseFieldByID("playState")
	read := func(clause string, args []any) []int64 {
		t.Helper()
		rows, err := db.Query(`SELECT e.entity_id FROM catalog_browse_rows e WHERE `+clause+` ORDER BY e.entity_id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	age := 13
	for _, profile := range []string{"p", "empty"} {
		for _, restriction := range []identity.ContentRestrictions{{}, {MaximumAge: &age, BlockUnrated: true}} {
			states := []string{"unplayed", "in-progress", "played"}
			nodes := []BrowseNode{}
			for _, state := range states {
				nodes = append(nodes, BrowseNode{Field: "playState", Operator: "equals", Value: state})
			}
			for mask := 1; mask < 8; mask++ {
				values := []any{}
				for bit, state := range states {
					if mask&(1<<bit) != 0 {
						values = append(values, state)
					}
				}
				for _, op := range []string{"in", "not-in"} {
					nodes = append(nodes, BrowseNode{Field: "playState", Operator: op, Value: values})
				}
			}
			for _, node := range nodes {
				legacy := browseCompiler{profile: profile, restrictions: restriction}
				op := node.Operator
				if op == "not-in" {
					op = "in"
				}
				old, err := legacy.exists(field, func() (string, error) { return legacy.comparison(field, field.Value, op, node.Value) })
				if err != nil {
					t.Fatal(err)
				}
				if node.Operator == "not-in" {
					old = "NOT " + old
				}
				next := browseCompiler{profile: profile, restrictions: restriction}
				clause, err := next.compile(&node)
				if err != nil {
					t.Fatal(err)
				}
				want, got := read(old, legacy.args), read(clause, next.args)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("profile=%s restriction=%+v node=%+v got=%v want=%v", profile, restriction, node, got, want)
				}
			}
		}
	}
}
