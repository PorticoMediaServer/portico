package metadata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestScreenConsentAcceptanceConstantLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	var first int64
	for _, n := range []int{1, 200, 503} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			for i := 0; i < n; i++ {
				c.Library(fmt.Sprintf("lib-%04d", i), "Library", "movie", fmt.Sprintf("/films/%d", i))
			}
			s := New(c.DB, "")
			publicationExec(t, c.DB, `UPDATE screen_metadata_fanout SET pending=0,rerun=0`)
			p, err := s.ScreenPolicy(context.Background(), "lib-0000")
			if err != nil {
				t.Fatal(err)
			}
			no := false
			dbwork.ResetPlans()
			ctx, cost := dbwork.Measure(context.Background())
			if err = s.UpdateScreenPolicy(ctx, "lib-0000", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, ConfirmRemote: &no, Enabled: true, Providers: p.Providers, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); err != nil {
				t.Fatal(err)
			}
			writes := dbwork.Plans().Largest.Rows
			if first == 0 {
				first = writes
			}
			if writes != first {
				t.Fatalf("acceptance grew: %d at %d libraries, baseline %d", writes, n, first)
			}
			if got := publicationScalar(t, c.DB, `SELECT count(*) FROM metadata_lookup_fanout WHERE effect='screen_consent' AND pending=1`); got != "1" {
				t.Fatal("global intent", got)
			}
			if got := publicationScalar(t, c.DB, `SELECT count(*) FROM screen_metadata_fanout WHERE effect='screen' AND pending=1`); got != "1" {
				t.Fatal("acceptance traversed other libraries", got)
			}
			t.Logf("%d libraries: %d accepting transitive writes; reads %+v", n, writes, cost())
			before := publicationScalar(t, c.DB, `SELECT sum(generation) FROM screen_metadata_fanout WHERE effect='screen'`)
			dbwork.ResetPlans()
			if err = s.LookupFanoutStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			delta := publicationScalar(t, c.DB, `SELECT sum(generation)-? FROM screen_metadata_fanout WHERE effect='screen'`, before)
			if delta != fmt.Sprint(min(n, lookupFanoutRowLimit)) {
				t.Fatal("unbounded/rejected consent window", delta)
			}
			if rows := dbwork.Plans().Largest.Rows; rows > lookupFanoutPlan.Rows {
				t.Fatal("quantum transitive writes", rows)
			}
			settleLookupFanout(t, s)
			if got := publicationScalar(t, c.DB, `SELECT count(*) FROM screen_metadata_fanout WHERE effect='screen' AND pending=1`); got != fmt.Sprint(n) {
				t.Fatal("missing tail", got, n)
			}
		})
	}
}

func TestScreenConsentRapidToggleFinitePass(t *testing.T) {
	c := catalogtest.Open(t)
	for i := 0; i < 37; i++ {
		c.Library(fmt.Sprintf("lib-%03d", i), "TV", "tv", fmt.Sprintf("/tv/%d", i))
	}
	s := New(c.DB, "")
	publicationExec(t, c.DB, `UPDATE screen_metadata_consent SET revision=revision+1,confirmed=0`)
	if err := s.LookupFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	upper := publicationScalar(t, c.DB, `SELECT upper_kind FROM metadata_lookup_fanout WHERE effect='screen_consent'`)
	publicationExec(t, c.DB, `UPDATE screen_metadata_consent SET revision=revision+1,confirmed=1;UPDATE screen_metadata_consent SET revision=revision+1,confirmed=0`)
	if got := publicationScalar(t, c.DB, `SELECT upper_kind FROM metadata_lookup_fanout WHERE effect='screen_consent'`); got != upper {
		t.Fatal("restarted pass", got)
	}
	for i := 0; i < 8; i++ {
		if publicationScalar(t, c.DB, `SELECT pass_generation FROM metadata_lookup_fanout WHERE effect='screen_consent'`) == "0" {
			break
		}
		if err := s.LookupFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, c.DB, `SELECT served_generation||':'||generation||':'||pending FROM metadata_lookup_fanout WHERE effect='screen_consent'`); got != "1:3:1" {
		t.Fatal("lost newer intent", got)
	}
	settleLookupFanout(t, s)
	if got := publicationScalar(t, c.DB, `SELECT served_generation||':'||generation||':'||pending FROM metadata_lookup_fanout WHERE effect='screen_consent'`); got != "3:3:0" {
		t.Fatal("unsettled", got)
	}
}

func TestScreenConsentTVDBCachedPageRefusedBeforeSettlement(t *testing.T) {
	t.Run("inflight_page", func(t *testing.T) {
		s, db := tvdbPublicationDB(t)
		tvdbPublicationSelected(t, s)
		p := tvdbPublicationClaim(t, s)
		next := 1
		if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0, NextPage: &next, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(100, 1)}}); err != nil {
			t.Fatal(err)
		}
		p = tvdbPublicationClaim(t, s)
		oldSet := p.Set
		publicationExec(t, db, `UPDATE screen_metadata_consent SET revision=revision+1,confirmed=0;UPDATE screen_metadata_consent SET revision=revision+1,confirmed=1;UPDATE screen_metadata_consent SET revision=revision+1,confirmed=0`)
		if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 1, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(101, 2)}}); !errors.Is(err, errTVDBStale) {
			t.Fatal(err)
		}
		if got := publicationScalar(t, db, `SELECT count(*) FROM tvdb_episode_evidence`); got != "1" {
			t.Fatal("old page escaped", got)
		}
		// The raw commit refuses without settling the caller's lease. Advance
		// its real deadline before a new worker claims the cached set.
		later := s.publicationTime().Add(time.Minute + time.Second)
		s.now = func() time.Time { return later }
		p = tvdbPublicationClaim(t, s)
		if p.Page != 0 || p.Set == oldSet {
			t.Fatal("reused pre-consent cached page", p.Page, p.Set)
		}
		if got := publicationScalar(t, db, `SELECT pending FROM metadata_lookup_fanout WHERE effect='screen_consent'`); got != "1" {
			t.Fatal("test accidentally settled", got)
		}
	})
	t.Run("cached_complete_set", func(t *testing.T) {
		s, db := tvdbProjectionReady(t)
		publicationExec(t, db, `UPDATE screen_metadata_consent SET revision=revision+1,confirmed=0;UPDATE screen_metadata_consent SET revision=revision+1,confirmed=1`)
		if p, err := s.claimTVDBProjection(context.Background()); err != nil || p != nil {
			t.Fatal("cached projection admitted", p, err)
		}
		if got := publicationScalar(t, db, `SELECT pending FROM metadata_lookup_fanout WHERE effect='screen_consent'`); got != "1" {
			t.Fatal("test accidentally settled", got)
		}
		if got := publicationScalar(t, db, `SELECT status FROM tvdb_jobs`); got != "pending_episodes" {
			t.Fatal("did not reacquire", got)
		}
	})
}

func TestScreenFanoutDueSeekSkipsBackoffPrefix(t *testing.T) {
	c := catalogtest.Open(t)
	for i := 0; i < 37; i++ {
		c.Library(fmt.Sprintf("lib-%03d", i), "Library", "movie", fmt.Sprintf("/f/%d", i))
	}
	publicationExec(t, c.DB, `INSERT INTO screen_metadata_fanout(library_id,effect) SELECT library_id,'screen' FROM screen_metadata_policies WHERE true ON CONFLICT(library_id,effect) DO NOTHING`)
	now := time.Now()
	s := New(c.DB, "")
	s.now = func() time.Time { return now }
	publicationExec(t, c.DB, `UPDATE screen_metadata_fanout SET pending=1,next_attempt_ms=?,last_served_ms=0`, now.Add(time.Hour).UnixMilli())
	publicationExec(t, c.DB, `UPDATE screen_metadata_fanout SET next_attempt_ms=0,last_served_ms=999 WHERE library_id='lib-036' AND effect='screen'`)
	rows, err := c.DB.Query(`EXPLAIN QUERY PLAN SELECT library_id,effect FROM screen_metadata_fanout WHERE pending=1 AND next_attempt_ms<=? ORDER BY next_attempt_ms,last_served_ms,library_id,effect LIMIT 1`, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	rows.Close()
	if !strings.Contains(plan, "screen_metadata_fanout_due") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal("unbounded due pick", plan)
	}
	if err = s.ScreenFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, c.DB, `SELECT pending FROM screen_metadata_fanout WHERE library_id='lib-036' AND effect='screen'`); got != "0" {
		t.Fatal("backoff prefix hid due tail", got)
	}
	if got := publicationScalar(t, c.DB, `SELECT count(*) FROM screen_metadata_fanout WHERE pending=0`); got != "1" {
		t.Fatal("examined blocked peers", got)
	}
}
