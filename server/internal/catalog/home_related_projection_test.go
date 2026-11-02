package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Frozen Related query before the projection change. Keep its full taste
// joins: an oracle must reproduce the work and scores that are being removed.
func originalRelatedCandidates(t *testing.T, s *Service, r HomeRequest, seed string) []recCandidate {
	t.Helper()
	r.Now = s.recommendationNow(r.Now)
	candidates, _, err := s.relatedPool(r, seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		return nil
	}
	base, args := recBase(r, candidates)
	score := `COALESCE((SELECT count(*)*30 FROM facets f WHERE f.work=w.work AND f.f IN(SELECT f2.f FROM facets f2 JOIN target t ON t.work=f2.work)),0)`
	query := base + `, target AS (SELECT work FROM members WHERE public_id=pid_blob(?)),
 facet_json AS MATERIALIZED (SELECT work,json_group_array(f) AS value FROM facets GROUP BY work),
 positive_scores AS MATERIALIZED (SELECT f.work,sum(CASE WHEN f.f LIKE 'e:%' THEN x.weight*0.1 WHEN f.f LIKE 'g:%' THEN x.weight ELSE x.weight*1.4 END) AS value,max(f.f NOT LIKE 'e:%') AS non_year_match FROM facets f JOIN interest x ON x.f=f.f GROUP BY f.work),
 negative_scores AS MATERIALIZED (SELECT f.work,count(*)*25 AS value FROM facets f JOIN negative_facets n ON n.f=f.f GROUP BY f.work)
 SELECT pid(ce.public_id),w.work,w.kind,w.added,COALESCE(fj.value,'[]'),` + score + ` AS score FROM works w JOIN catalog_entities ce ON ce.id=w.id LEFT JOIN personal p ON p.work=w.work LEFT JOIN facet_json fj ON fj.work=w.work LEFT JOIN positive_scores ps ON ps.work=w.work LEFT JOIN negative_scores ns ON ns.work=w.work
 WHERE COALESCE(p.negative,0)=0 AND w.work NOT IN(SELECT work FROM target) AND (` + score + `)>0 ORDER BY score DESC,w.added DESC,w.id LIMIT ?`
	args = append(args, seed, recCandidateLimit)
	rows, err := s.read().Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []recCandidate{}
	for rows.Next() {
		var c recCandidate
		var facets string
		if err = rows.Scan(&c.ID, &c.Work, &c.Kind, &c.Added, &facets, &c.Score); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(facets), &c.Facets); err != nil {
			t.Fatal(err)
		}
		sort.Strings(c.Facets)
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return diversifyRecommendations(out, r.now())
}

func TestRelatedProjectionPreservesEveryOriginalCandidate(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest().scoped()
	keys := []string{"m1", "m2", "m3", "m4", "m5", "e101", "e102", "e201", "e202", "t1", "t2", "t3", "p1", "p2", "p3"}
	for _, key := range keys {
		engineSetTerms(t, f, f.items[key].ID, "nfo", compactcatalog.Term{SourceID: "shared", Name: "Shared"})
	}
	// Enough ties for diversification to matter, with multiple works and kinds.
	for i := 0; i < 70; i++ {
		item := f.c.Movie(f.libs["movies"], fmt.Sprintf("/movies/related-%d.mkv", i), fmt.Sprintf("Related %d", i), 2000+i%3)
		engineSetTerms(t, f, item.ID, "nfo", compactcatalog.Term{SourceID: "shared", Name: "Shared"})
	}
	engineMeta(t, f, "m2", "tmdb", "shared-edition")
	engineMeta(t, f, "m3", "tmdb", "shared-edition")
	engineWatched(t, f, r.Profile, r.Now.Add(-time.Hour).Format(time.RFC3339), "m1", "e101", "t1", "p1")
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,rating,revision)VALUES(?,?,3,1)`, r.Profile, f.items["m3"].ID)
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,favorite,watchlisted,rating,revision)VALUES(?,?,1,1,9,1)`, r.Profile, f.items["m4"].ID)
	engineExec(t, f, `INSERT INTO progress(profile_id,item_id,position,playback_id)VALUES(?,?,500000,'probe')`, r.Profile, f.items["m5"].ID)
	engineExec(t, f, `INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision)VALUES(?,'show',?,1,?,1)`, r.Profile, f.items["show"].ID, r.Now.Format(time.RFC3339))
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,rating,revision)VALUES('other',?,0,1)`, f.items["m1"].ID)
	f.c.Drain()
	check := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{"m1", "e101", "t1", "p1"} {
			s := New(f.db)
			expected := originalRelatedCandidates(t, s, r, f.items[key].Public)
			if key == "m1" && len(expected) < 5 {
				t.Fatalf("oracle fixture has only %d related movies", len(expected))
			}
			actual, err := s.recommendationCandidates(r, "related", f.items[key].Public)
			if err != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("seed%s original%v optimized%v error%v", key, expected, actual, err)
			}
		}
	}
	t.Run("personal weights, negatives, editions and inherited defaults", check)
	t.Run("current library grant", func(t *testing.T) {
		saved := r.Libraries
		r.Libraries = []string{"movies"}
		defer func() { r.Libraries = saved }()
		check(t)
	})
	t.Run("current restriction and availability", func(t *testing.T) {
		f.c.Attributes(f.items["m4"].ID, "label", "hidden")
		f.c.Drain()
		r.Restrictions.BlockedLabels = []string{"hidden"}
		engineExec(t, f, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, f.items["m2"].ID)
		check(t)
	})
	t.Run("VM avoids unused personal scoring", func(t *testing.T) {
		beforeCtx, before := dbwork.Measure(context.Background())
		originalRelatedCandidates(t, New(f.db).WithContext(beforeCtx), r, f.items["m1"].Public)
		afterCtx, after := dbwork.Measure(context.Background())
		_, err := New(f.db).WithContext(afterCtx).recommendationCandidates(r, "related", f.items["m1"].Public)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("original%d optimized%dVM", before().VMSteps, after().VMSteps)
		if before().VMSteps == 0 || after().VMSteps*5 >= before().VMSteps*4 {
			t.Fatalf("unused work remains: original%d optimized%dVM", before().VMSteps, after().VMSteps)
		}
	})
}
