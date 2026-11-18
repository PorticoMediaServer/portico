package api

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func TestBecauseWatchedUsesOwnHistoryAndLocalSignals(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.setupFile), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	library := get[Libraries](t, s, owner, "/v1/libraries").Items[0].ID
	films := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=90").Items
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if strings.HasPrefix(row.ID, "because:") {
			t.Fatal("invented seed without plays")
		}
	}
	exec(`INSERT INTO people(public_id,name,sort_name,updated_at) VALUES('local_writer','Writer','Writer',0)`)
	for _, film := range films {
		exec(`INSERT INTO credits(item_id,position,person_id,role) SELECT id,999,(SELECT id FROM people WHERE public_id='local_writer'),3 FROM items WHERE public_id=?`, film.ID)
	}
	profile := []int64{}
	for _, who := range members {
		c, _ := s.tokens.CheckAccess(who, time.Now())
		profile = append(profile, c.ProfileID)
	}
	seed := films[0]
	// Choose an unrestricted seed every profile can see so only candidate restrictions differ.
	for _, film := range films {
		visible := true
		for _, who := range members {
			if status(t, s, "GET", "/v1/items/"+film.ID, who, nil) != 200 {
				visible = false
				break
			}
		}
		if visible {
			seed = film
			break
		}
	}
	exec(`DELETE FROM credits WHERE item_id=(SELECT id FROM items WHERE public_id=?) AND person_id<>(SELECT id FROM people WHERE public_id='local_writer')`, seed.ID)
	now := time.Now().Add(-time.Minute).UnixMilli()
	for n, p := range profile {
		exec(`INSERT INTO play_history(profile_id,profile_name,account_name,item_id,client_event_id,played_at,item_kind,title)
  SELECT ?, 'Own viewer','Own account',id,?, ?,kind,title FROM items WHERE public_id=?`, p, fmt.Sprint("play", n), now, seed.ID)
	}
	rowID := "because:" + seed.ID
	ids := func(items []Item) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	for _, who := range members {
		expected := get[ItemPage](t, s, who, "/v1/items?library="+library+"&kind=movie&limit=200").Items
		want := []string{}
		for _, it := range expected {
			if it.ID != seed.ID {
				want = append(want, it.ID)
			}
		}
		got := []string{}
		for cursor := ""; ; {
			row := get[HomeRow](t, s, who, "/v1/home/rows/"+rowID+"?limit=7&total=true&cursor="+url.QueryEscape(cursor))
			if row.Total == nil || *row.Total != len(want) || row.Title != "Because you watched "+seed.Title {
				t.Fatalf("seed title/count: title=%q want=%q total=%v want=%d", row.Title, "Because you watched "+seed.Title, row.Total, len(want))
			}
			got = append(got, ids(row.Items)...)
			if row.NextCursor == "" {
				break
			}
			cursor = row.NextCursor
			if len(got) > 100 {
				t.Fatal("row did not end")
			}
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatal("candidate visibility/source")
		}
		preview := HomeRow{}
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			if row.ID == rowID {
				preview = row
			}
		}
		if len(preview.Items) != min(40, len(want)) {
			t.Fatal("Because preview absent")
		}
		if status(t, s, "GET", "/v1/home/rows/because:"+films[len(films)-1].ID, who, nil) != 404 {
			t.Fatal("another seed without own play accepted")
		}
	}
	// Play from See all holds the row's titles in the row's order (a queue needs a signed-in device: the owner's).
	ownerAccess, _ := s.tokens.CheckAccess(owner, time.Now())
	exec(`INSERT INTO play_history(profile_id,profile_name,account_name,item_id,client_event_id,played_at,item_kind,title)
  SELECT ?, 'Owner','Owner',id,'play-owner',?,kind,title FROM items WHERE public_id=?`, ownerAccess.ProfileID, now, seed.ID)
	shown := []string{}
	for cursor := ""; ; {
		row := get[HomeRow](t, s, owner, "/v1/home/rows/"+rowID+"?limit=50&cursor="+url.QueryEscape(cursor))
		shown = append(shown, ids(row.Items)...)
		if cursor = row.NextCursor; cursor == "" {
			break
		}
	}
	var made QueueCreated
	if w := call(t, s, "POST", "/v1/queues", owner, QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: rowID}}}, &made); w.Code != 201 ||
		made.Queue.Total != len(shown) || len(made.Window) == 0 {
		t.Fatalf("Because queue of %d titles: %d %s", len(shown), w.Code, w.Body)
	}
	for n, entry := range made.Window {
		if entry.ItemID != shown[n] {
			t.Fatalf("Because queue entry %d is %s, the row shows %s", n, entry.ItemID, shown[n])
		}
	}
	// Current history privacy/reset and Not interested on the seed remove both the reason and row.
	exec(`UPDATE profiles SET taste_reset_at=? WHERE id=?`, now+1, profile[0])
	if status(t, s, "GET", "/v1/home/rows/"+rowID, members[0], nil) != 404 {
		t.Fatal("reset taste retained seed")
	}
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == rowID {
			t.Fatal("reset row still on Home")
		}
	}
}
