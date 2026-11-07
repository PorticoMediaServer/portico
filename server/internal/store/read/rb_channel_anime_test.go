package read

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestRBChannelWindowSeasonlessAbsoluteEpisode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := rbChannelFixture(t)
		seedRBChannelPlayable(t, f)
		canonical := rbChannelAcceptedFixture(t, "episode")
		f.exec(t, `UPDATE lc_channels SET config_json=? WHERE id='channel'`, string(canonical))
		f.exec(t, `UPDATE lc_generations SET config_json=? WHERE id='generation'`, string(canonical))
		f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0)`)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1200,x'00000000000000000000000000001200',1,2),(1201,x'00000000000000000000000000001201',1,4)`)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision) VALUES(1200,x'00000000000000000000000000001200',1,2,'Anime','anime','a','anime','a',0,zeroblob(16),zeroblob(16),x'01010101010101010101010101010101',1)`)
		// Today's absolute assignment keeps season_id NULL. A source can still
		// be a whole playable episode without an invented season assignment.
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,show_id,season_id,episode_number,numbering) VALUES(1201,x'00000000000000000000000000001201',1,4,'Absolute 22','absolute 22','a','absolute 22','a',0,zeroblob(16),zeroblob(16),x'01010101010101010101010101010101',1,1200,NULL,22,'absolute')`)
		f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation,playback_origin_revision,prepared_source_revision) VALUES(1201,'absolute-1201',1,'/media/absolute-1201',1000,1,'mkv','h264','aac',1920,1080,120,x'02020202020202020202020202020202',1,1)`)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,playback_origin_incarnation,playback_origin_revision) VALUES(1201,1201,1,x'03030303030303030303030303030303',1)`)
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('absolute-object','binding-source','absolute-1201','binding-inc','absolute-1201','object-rev','{}',1000,1)`)
		f.exec(t, `INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(1201,'absolute-1201','whole_source')`)
		scope, _, _, err := r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, rbChannelScopeInput())
		if err != nil {
			t.Fatal(err)
		}
		batch, _, err := r.SharedChannelWindow(t.Context(), rbChannelRequest(), time.Second, f.output, SharedChannelInput{Scope: scope, Kind: 4})
		if err != nil || len(batch.Items) != 1 || !batch.RawEOF || batch.RawConsumed != 1 || batch.After != nil {
			t.Fatal("valid seasonless anime became an empty channel", batch, err)
		}
		v := batch.Items[0]
		if v.Item.ID != 1201 || v.Show != 1200 || v.Season != 0 || v.SeasonNumber != nil || v.EpisodeNumber == nil || *v.EpisodeNumber != 22 || v.Numbering != "absolute" || v.Asset != 1201 || v.Object != "absolute-object" || v.DurationSeconds != 120 {
			t.Fatal("seasonless binding/order projection changed", v)
		}
	})
}
