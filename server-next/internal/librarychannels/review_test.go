package librarychannels

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

type oneShow struct{ episodes []db.Episode }

func (f oneShow) ChannelCandidates(ctx context.Context, sel db.ChannelSelection, fn func(db.ChannelCandidate) error) error {
	return fn(db.ChannelCandidate{ID: 1, PublicID: "itm_show", Show: true, SortTitle: "show"})
}

func (f oneShow) ShowEpisodes(ctx context.Context, showID int64, s, n int, id int64, limit int) ([]db.Episode, error) {
	var out []db.Episode
	for _, e := range f.episodes {
		if e.Season > s || e.Season == s && (e.Number > n || e.Number == n && e.ID > id) {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (f oneShow) ShowEpisodeCount(ctx context.Context, showID int64) (int, error) { return len(f.episodes), nil }
func (f oneShow) ShowEpisodeAt(ctx context.Context, showID int64, place int) (db.Episode, error) {
	return f.episodes[place], nil
}

// Under "Only start what fits", an episode that does not fit the block must still play later,
// in order: the show's place must not move past it.
func TestReviewAnEpisodeThatDoesNotFitIsNotSkipped(t *testing.T) {
	var src oneShow
	for n := 1; n <= 200; n++ {
		ms := int64(20 * 60_000)
		if n%2 == 0 {
			ms = 50 * 60_000
		}
		src.episodes = append(src.episodes, db.Episode{ID: int64(100 + n), Season: 1, Number: n, DurationMS: ms})
	}
	sel := Selection{Libraries: []string{"lib"}, Kinds: []string{"show"}, Episodes: "showOrder"}
	b := &Builder{Selection: sel, Blocks: []Block{{ID: "short", Days: weekdays, From: "06:00", To: "06:40", Overrun: "fit"}}}
	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	rows, _, err := Build(context.Background(), src, b, time.UTC, &State{}, start, start.Add(24*time.Hour), start, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := int64(100)
	for _, r := range rows {
		if r.ItemID == 0 {
			continue
		}
		if r.ItemID != last+1 {
			t.Fatalf("after episode %d the channel played episode %d at %v: an episode was skipped", last-100, r.ItemID-100, r.StartsAt)
		}
		last = r.ItemID
	}
}
