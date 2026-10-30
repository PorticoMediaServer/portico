package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestContinueFallbackStartsWithTheShowsPartialEpisodes(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("tv", "TV", "tv", "/tv")
			show := c.Show(lib, "Show", 2000)
			season := c.Season(show, 1)
			first, last := c.Episode(show, season, 1, "/tv/1.mkv"), c.Episode(show, season, 2, "/tv/2.mkv")
			c.Drain()
			c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,120000,0,0,'a'),('p',?,3600000,0,1,'b')`, first.ID, last.ID)
			c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','tv',?,'2026-10-01T00:00:00Z','paused'),('p','tv',?,'2026-10-02T00:00:00Z','ended')`, first.ID, last.ID)
			// Unrelated activity has no progress/entity fixture: the old plan walked
			// these keys before rejecting them. This isolates the fallback join order.
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), c.DB, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
			for off := 0; err == nil && off < n; off += 10000 {
				_, err = conn.ExecContext(ctx, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT 'p','tv',i+1000000000,'2020-01-01T00:00:00Z','paused' FROM ids`, off+1, min(n, off+10000))
			}
			_, restore := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
			release()
			if err != nil || restore != nil {
				t.Fatalf("seed=%v restore=%v", err, restore)
			}
			c.Exec(`ANALYZE`)
			query, args := homeShowContinuation(HomeRequest{Profile: "p", Libraries: []string{"tv"}}, `{"tv":"2026-01-01T00:00:00Z"}`, `{}`, 1, "", nil)
			plan := seekCapacityPlan(t, c.DB, query, args...)
			// The fallback starts from the profile's unfinished episodes of
			// the show, never the history nor the show's every episode.
			if !strings.Contains(plan, "SEARCH ps USING COVERING INDEX profile_show_partials_show (profile_id=? AND show_id=?)") {
				t.Fatalf("fallback not show-indexed: %s", plan)
			}
			start := time.Now()
			var id, ord string
			if err = c.DB.QueryRow(query, args...).Scan(&id, &ord); err != nil || id != first.Public {
				t.Fatalf("id=%s err=%v", id, err)
			}
			elapsed := time.Since(start)
			t.Logf("history=%d elapsed=%s plan=%s", n, elapsed, plan)
			if elapsed > 250*time.Millisecond {
				t.Fatalf("fallback took %s", elapsed)
			}
		})
	}
}
