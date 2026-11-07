package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func homeFixedAllDefinitions() []stmt.Definition {
	d := append(browseDefinitions(), rbSavedViewAllDraftDefinitions()...)
	d = append(d, rbHomeManifestDefinitions()...)
	d = append(d, rbHomeManifestSourceDefinitions()...)
	d = append(d, rbHomeBindingDefinitions()...)
	d = append(d, rbHomeFixedDefinitions()...)
	return append(d, rbHomeFixedBindingByteDefinitions()...)
}
func homeFixedHash(t *testing.T) string {
	t.Helper()
	b, e := os.ReadFile("rb_home_fixed_test.go")
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), b...)))
}
func homeFixedSeed(t *testing.T, n int) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `DELETE FROM catalog_entities WHERE id>?`, n)
	f.exec(t, `UPDATE catalog_entities SET retired=0,admission_own_age=10,added_at='2026-10-01T00:00:00.000000000Z',playback_origin_incarnation=public_id,playback_origin_revision=1`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'book_file',1,0,1,1,1)`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,identity_confirmed) VALUES('fixed-source','movies','/media','/media','fixed-inc',3,1)`)
	return f
}
func homeFixedEntitySeed(t *testing.T, f *browseFixture, id, kind int64, parent, show, season any, date string) {
	t.Helper()
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[8:], uint64(id))
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, b, kind)
	var artist, album, book any
	if kind == 6 {
		artist = parent
	}
	if kind == 7 {
		album = parent
	}
	if kind == 9 {
		book = parent
	}
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,show_id,season_id,album_id,book_id,artist_id,season_number,episode_number,numbering,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,added_at,last_content_added_at,admission_own_age) VALUES(?,?,1,?,?,?,?,?,?,?,?,?,'seasonal','Fixed','fixed','f','fixed','f',0,?,?,?,1,?,?,10)`, id, b, kind, parent, show, season, album, book, artist, id, id, b, b, b, date, date)
}
func homeFixedAssetSeed(t *testing.T, f *browseFixture, id int64) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation,playback_origin_revision,prepared_source_revision) SELECT id,printf('fixed-%d',id),library_id,printf('/media/fixed-%d',id),1000,1,'mkv','h264','aac',1920,1080,120,public_id,1,1 FROM catalog_entities WHERE id=?`, id)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,playback_origin_incarnation,playback_origin_revision) SELECT id,id,library_id,public_id,1 FROM catalog_entities WHERE id=?`, id)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,'fixed-source',?,'fixed-inc',?,'object-rev','{}',1000,1)`, fmt.Sprintf("fixed-object-%d", id), fmt.Sprintf("fixed-%d", id), fmt.Sprintf("fixed-%d", id))
}
func homeFixedStateSeed(t *testing.T, f *browseFixture, id, pos int64, state string) {
	t.Helper()
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,resume_unit,activity_at,activity_state,resume_playback_id) SELECT 'p',public_id,id,library_id,?,0,'2026-11-02T12:00:00.000000000Z',?,'fixed-play' FROM catalog_entities WHERE id=?`, pos, state, id)
}
func homeFixedFixture(t *testing.T, seed func(*browseFixture)) (*browseFixture, *HomeFixedReader) {
	t.Helper()
	f := homeFixedSeed(t, 0)
	seed(f)
	f = composeReadFixture(t, f, homeFixedAllDefinitions(), readfixture.LiveConfig(time.Now), false, homeFixedHash(t))
	r, e := NewHomeFixedReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func homeFixedRead(t *testing.T, f *browseFixture, r *HomeFixedReader, row string) (HomeFixedPage, BrowseReadResult, error) {
	t.Helper()
	live, req, out := liveRequests("home-fixed")
	req.Budget.RawRows = 4096
	req.Budget.Pages = 8192
	req.Budget.VMOps = 400000
	var page HomeFixedPage
	var cost BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, out, func(ctx context.Context, o BrowseOutput) error {
		var e error
		page, cost, e = r.Row(ctx, req, time.Second, o, HomeFixedInput{View: f.input, RowID: row})
		return e
	})
	return page, cost, e
}
func homeFixedCase(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, fn) })
}
func TestRBHomeFixedRecent(t *testing.T) {
	homeFixedCase(t, "complete_last100", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			for id := int64(2000); id < 2105; id++ {
				homeFixedEntitySeed(t, f, id, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, id)
			}
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || !p.Exact || !p.Exhausted || p.Pending || len(p.Items) != 100 || p.RawPositions != 105 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "delivery_and_hidden_date", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 2, nil, nil, nil, "2026-11-04T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 2100, 3, int64(2000), int64(2000), nil, "2026-11-04T00:00:00.000000000Z")
			for n := int64(0); n < 3; n++ {
				homeFixedEntitySeed(t, f, 3000+n, 4, int64(2100), int64(2000), int64(2100), fmt.Sprintf("2026-11-01T12:0%d:00.000000000Z", n))
				homeFixedAssetSeed(t, f, 3000+n)
			}
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=18,added_at='2026-11-06T00:00:00.000000000Z' WHERE id=3002`)
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || len(p.Items) != 1 || p.Items[0].Work.ID != 2000 || p.Items[0].Target.ID != 2100 || p.Items[0].Added != "2026-11-01T12:01:00.000000000Z" || p.Items[0].EligibleLeaves != 2 {
			t.Fatal(p, c, e)
		}
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=3001`)
		p, c, e = homeFixedRead(t, f, r, "new_on_server")
		if e != nil || len(p.Items) != 1 || p.Items[0].Target.ID != 3000 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "raw_hidden_tail_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			for id := int64(2000); id < 2130; id++ {
				homeFixedEntitySeed(t, f, id, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			}
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=18`)
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || !p.Pending || p.Exact || p.Exhausted || len(p.Items) != 0 || p.RawPositions != 129 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "ambiguous_link_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 2001, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedAssetSeed(t, f, 2000)
			homeFixedAssetSeed(t, f, 2001)
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,available) VALUES(2000,2001,1,0)`)
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || !p.Pending || p.Exact || len(p.Items) != 0 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "edition_identity_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedAssetSeed(t, f, 2000)
			f.exec(t, `INSERT INTO metadata_entity_external_ids(entity_id,provider,subject_kind,provider_id) VALUES(2000,'tmdb','movie','42')`)
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || !p.Pending || p.PendingReason != "provider_edition_grouping_preparation" {
			t.Fatal(p, c, e)
		}
	})
}
func TestRBHomeFixedActivity(t *testing.T) {
	homeFixedCase(t, "movie_resume_removed_and_end_margin", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			for id := int64(2000); id < 2003; id++ {
				homeFixedEntitySeed(t, f, id, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, id)
				homeFixedStateSeed(t, f, id, 5000, "in_progress")
			}
			f.exec(t, `UPDATE personal_items SET dismissed_playback_id=resume_playback_id WHERE item_id=2001`)
			f.exec(t, `UPDATE personal_items SET resume_position=117000 WHERE item_id=2002`)
		})
		p, c, e := homeFixedRead(t, f, r, "continue")
		if e != nil || !p.Exact || len(p.Items) != 1 || p.Items[0].Target.ID != 2000 || p.Items[0].ResumePosition != 5000 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "album_next_and_recent_fallback", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			f.exec(t, `UPDATE libraries SET kind='music' WHERE id='movies'`)
			homeFixedEntitySeed(t, f, 1999, 5, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 2000, 6, int64(1999), nil, nil, "2026-10-01T00:00:00.000000000Z")
			for id := int64(3000); id < 3003; id++ {
				homeFixedEntitySeed(t, f, id, 7, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, id)
				f.exec(t, `UPDATE catalog_entities SET track_number=? WHERE id=?`, id-2999, id)
			}
			homeFixedStateSeed(t, f, 3000, 0, "ended")
		})
		p, c, e := homeFixedRead(t, f, r, "listen_next")
		if e != nil || !p.Exact || len(p.Items) != 1 || p.Items[0].Target.ID != 3001 {
			t.Fatal(p, c, e)
		}
		f.exec(t, `UPDATE personal_items SET activity_state='in_progress' WHERE item_id=3000`)
		p, c, e = homeFixedRead(t, f, r, "listen_next")
		if e != nil || len(p.Items) != 1 || p.Items[0].Target.ID != 3000 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "book_actual_resume_child", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			f.exec(t, `UPDATE libraries SET kind='audiobook' WHERE id='movies'`)
			homeFixedEntitySeed(t, f, 2000, 8, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 3000, 9, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 3001, 9, int64(2000), nil, nil, "2026-10-01T00:00:00.000000000Z")
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=3001`)
			homeFixedAssetSeed(t, f, 3000)
			homeFixedStateSeed(t, f, 2000, 7000, "in_progress")
			homeFixedStateSeed(t, f, 3000, 7000, "in_progress")
			f.exec(t, `UPDATE personal_items SET resume_item_public_id=(SELECT public_id FROM catalog_entities WHERE id=3000) WHERE item_id=2000`)
		})
		p, c, e := homeFixedRead(t, f, r, "continue_books")
		if e != nil || !p.Exact || len(p.Items) != 1 || p.Items[0].Work.ID != 2000 || p.Items[0].Target.ID != 3000 || p.Items[0].ResumePosition != 7000 || p.Items[0].EligibleLeaves != 1 {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "show_on_deck_explicit_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 2, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedStateSeed(t, f, 2000, 0, "ended")
		})
		p, c, e := homeFixedRead(t, f, r, "continue")
		if e != nil || !p.Pending || p.Exact || p.Exhausted || len(p.Items) != 0 {
			t.Fatal(p, c, e)
		}
	})
}

func TestRBHomeFixedPublication(t *testing.T) {
	homeFixedCase(t, "unversioned_state_change_retries", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedAssetSeed(t, f, 2000)
			homeFixedStateSeed(t, f, 2000, 5000, "in_progress")
		})
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE personal_items SET favorite=1,resume_position=6000 WHERE item_id=2000`)
			}
		}
		r.native.snapshots = hook
		p, c, e := homeFixedRead(t, f, r, "continue")
		if e != nil || c.Attempts != 2 || len(p.Items) != 1 || !p.Items[0].Personal.Favorite || p.Items[0].ResumePosition != 6000 {
			t.Fatal(p, c, e)
		}
		savedViewSameDeadline(t, hook)
	})
	homeFixedCase(t, "root_source_change_no_stale_shelf", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedAssetSeed(t, f, 2000)
		})
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE library_sources SET health='root_changed'`)
			}
		}
		r.native.snapshots = hook
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || c.Attempts != 2 || !p.Pending || p.Exact || len(p.Items) != 0 {
			t.Fatal(p, c, e)
		}
		savedViewSameDeadline(t, hook)
	})
	homeFixedCase(t, "explicit_owner_policy_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			f.exec(t, `INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:movies',1,'{}',1)`)
		})
		p, c, e := homeFixedRead(t, f, r, "continue")
		if e != nil || !p.Pending || p.PendingReason != "owner_continue_policy_uncomposed" || p.Exact {
			t.Fatal(p, c, e)
		}
	})
	homeFixedCase(t, "reading_is_not_fake_empty", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) { f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`) })
		p, c, e := homeFixedRead(t, f, r, "continue_books")
		if e != nil || !p.Pending || p.Exact || p.Exhausted {
			t.Fatal(p, c, e)
		}
	})
}

func TestRBHomeFixedTopology(t *testing.T) {
	homeFixedCase(t, "wrong_work_kind_refuses", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) {
			homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedEntitySeed(t, f, 3000, 4, nil, int64(2000), nil, "2026-10-01T00:00:00.000000000Z")
			homeFixedAssetSeed(t, f, 3000)
		})
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if !errors.Is(e, ErrUnqualified) || len(p.Items) != 0 || p.Exact {
			t.Fatal(p, c, e)
		}
	})
}
