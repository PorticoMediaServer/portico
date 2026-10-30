package subtitles

import (
	"context"
	"fmt"
	"testing"
)

func TestCatalogueIncludesLargeTitle(t *testing.T) {
	f := newSubtitleFixture(t)
	for i := 1; i < 33; i++ {
		f.catalog.File(f.item.ID, fmt.Sprintf("/fixture/part-%d.mp4", i), 60)
	}
	f.catalog.Drain()
	if _, err := f.db.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,1000,1,0,1,'known','test')`, f.item.Token); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 129; i++ {
		id := fmt.Sprintf("resource-%d", i)
		if _, err = tx.Exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision) VALUES(?,?,?,'shared','',1)`, id, f.item.ID, f.item.Token); err != nil {
			break
		}
		_, err = tx.Exec(`INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'digest',1,'srt','en','Track','upload','owner','digest',1000,1,1,'2026-09-25')`, id)
		if err != nil {
			break
		}
	}
	for i := 0; err == nil && i < 1025; i++ {
		_, err = tx.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded',?,?,'srt','en','Track',0,0,'')`, fmt.Sprintf("track-%d", i), f.item.Token, fmt.Sprintf("stream-%d", i), i)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.List(context.Background(), f.p, f.item.Public)
	if err != nil || len(got.Sources) != 33 || len(got.Resources) != 129 || len(got.Discovered) != 1025 {
		t.Fatalf("sources=%d resources=%d discovered=%d: %v", len(got.Sources), len(got.Resources), len(got.Discovered), err)
	}
}
