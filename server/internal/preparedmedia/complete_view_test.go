package preparedmedia

import (
	"context"
	"fmt"
	"testing"
)

func TestViewIncludesAllSourcesJobsAndVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, c, item := preparedFixture(t)
	for i := 1; i < 33; i++ {
		c.File(item.ID, fmt.Sprintf("/fixture/version-%d.mp4", i), 60)
	}
	c.Drain()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 129; i++ {
		job := fmt.Sprintf("job-%03d", i)
		version := fmt.Sprintf("version-%03d", i)
		_, err = tx.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES(?,?,?,?,?,'server','{}','revision','{}','succeeded','complete',?,?)`, job, item.ID, item.Token, "library", p.ProfileID, i, i)
		if err != nil {
			break
		}
		_, err = tx.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,edition_id,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES(?,?,?,?,?,'server','revision','{}',0,'','evidence','transform','digest',1,'{}','deleting',?,?)`, version, item.ID, item.Token, "library", p.ProfileID, i, job)
		if err != nil {
			break
		}
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.View(context.Background(), p, item.Public)
	if err != nil || len(got.Sources) != 33 || len(got.Jobs) != 129 || len(got.Versions) != 129 {
		t.Fatalf("sources=%d jobs=%d versions=%d: %v", len(got.Sources), len(got.Jobs), len(got.Versions), err)
	}
}
