package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
)

func TestAttentionCountsFollowDismissalRetirementAndRemoval(t *testing.T) {
	s, db, _ := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	check := func(want int64) {
		t.Helper()
		counts, err := s.AttentionCounts(ctx)
		if err != nil || counts.Total != want {
			t.Fatalf("counts=%+v, want %d: %v", counts, want, err)
		}
		for _, library := range []string{"", "lib"} {
			page, err := s.Attention(ctx, library, "", 25)
			if err != nil || page.Total != want {
				t.Fatalf("%q page=%+v: %v", library, page, err)
			}
		}
	}
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='low_confidence' WHERE target_id=?`, item.ID)
	check(1)
	page, err := s.Attention(ctx, "lib", "", 25)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	if err = s.AttentionAction(ctx, "lib", AttentionCommand{Kind: "item", EntityID: item.Public, ExpectedRevision: page.Entries[0].Revision, Action: "dismiss"}, MBActor{}, nil); err != nil {
		t.Fatal(err)
	}
	check(0)
	publicationExec(t, db, `UPDATE screen_metadata_work SET generation=generation+1 WHERE target_id=?`, item.ID)
	check(0)
	publicationExec(t, db, `UPDATE catalog_assets SET size=size+1 WHERE id IN(SELECT asset_id FROM catalog_asset_links WHERE entity_id=?)`, item.ID)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unmatched' WHERE target_id=?`, item.ID)
	check(1)
	publicationExec(t, db, `UPDATE catalog_entities SET retired=1 WHERE id=?`, item.ID)
	check(0)
	// Recreating an attention entry for an already retired title must stay hidden.
	publicationExec(t, db, `DELETE FROM screen_metadata_attention WHERE entity_id=?`, item.ID)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unmatched' WHERE target_id=?`, item.ID)
	check(0)
	publicationExec(t, db, `UPDATE catalog_entities SET retired=0 WHERE id=?`, item.ID)
	check(1)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending' WHERE target_id=?`, item.ID)
	check(0)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unmatched' WHERE target_id=?`, item.ID)
	check(1)
	publicationExec(t, db, `DELETE FROM catalog_entities WHERE id=?`, item.ID)
	check(0)
}

func TestAttentionPageHasConstantStatementCount(t *testing.T) {
	s, db, provider := screenFixture(t)
	c := catalogtest.New(t, db)
	lib := c.Handle("lib")
	for i := 0; i < 24; i++ {
		c.Movie(lib, fmt.Sprintf("/films/scale%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
	}
	rows, err := db.Query(`SELECT id,title FROM catalog_entities WHERE kind=1 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type target struct {
		id    int64
		title string
	}
	var targets []target
	for rows.Next() {
		var target target
		if err := rows.Scan(&target.id, &target.title); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, target := range targets {
		for i := 0; i < 4; i++ {
			record := provider.record
			record.Title = target.title
			payload, _ := json.Marshal(record)
			c.Exec(`INSERT INTO screen_metadata_candidates(candidate_key,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,strong_signals,contradiction,input_digest,query_digest,source_kind,observed_at,language,region) VALUES(?,'item',?,'tmdb','movie','42',?,?,'[]',0,0,'','','provider','','en','CA')`, fmt.Sprintf("%d-%d", target.id, i), target.id, string(payload), 1-float64(i)/10)
		}
	}
	ctx, cost := dbwork.Measure(context.Background())
	page, err := s.Attention(ctx, "lib", "", 25)
	if err != nil || len(page.Entries) != 25 {
		t.Fatalf("page entries=%d: %v", len(page.Entries), err)
	}
	// Count, existence, page and page candidates; independent of page length.
	if got := cost().Statements; got > 6 {
		t.Fatalf("page issued %d statements", got)
	}
	for _, entry := range page.Entries {
		if len(entry.Candidates) != 3 {
			t.Fatalf("%s has %d candidates", entry.Title, len(entry.Candidates))
		}
		for i, candidate := range entry.Candidates {
			if candidate.Title != entry.Title || candidate.Confidence != 1-float64(i)/10 {
				t.Fatalf("wrong candidate for %s: %+v", entry.Title, candidate)
			}
		}
		if entry.EntityID == "" {
			t.Fatal("missing public identity")
		}
	}
}
