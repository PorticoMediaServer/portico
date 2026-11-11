package librarychannels

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestAnEpisodeLongerThanItsBlockIsPassedOver(t *testing.T) {
	var src oneShow
	for n := 1; n <= 30; n++ {
		ms := int64(20 * 60_000)
		if n == 2 {
			ms = 45 * 60_000 // a double-length episode, longer than the whole block
		}
		src.episodes = append(src.episodes, db.Episode{ID: int64(100 + n), Season: 1, Number: n, DurationMS: ms})
	}
	sel := Selection{Libraries: []string{"lib"}, Kinds: []string{"show"}, Episodes: "showOrder"}
	b := &Builder{Selection: sel}
	for h := 0; h < 24; h++ {
		for _, m := range [][2]string{{":00", ":30"}, {":30", ""}} {
			from := fmt.Sprintf("%02d%s", h, m[0])
			to := fmt.Sprintf("%02d:30", h)
			if m[1] == "" {
				to = fmt.Sprintf("%02d:00", (h+1)%24)
				if h == 23 {
					to = "24:00"
				}
			}
			b.Blocks = append(b.Blocks, Block{ID: fmt.Sprintf("b%d%s", h, m[0][1:]), Days: weekdays, From: from, To: to, Overrun: "fit"})
		}
	}
	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	rows, _, err := Build(context.Background(), src, b, time.UTC, &State{}, start, start.Add(7*24*time.Hour), start, nil)
	if err != nil {
		t.Fatal(err)
	}
	var played []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			played = append(played, r.ItemID-100)
		}
	}
	if len(played) < 10 {
		t.Fatalf("a week of back-to-back 30-minute fit blocks played only episodes %v: the show stopped behind an episode longer than any block", played)
	}
}
