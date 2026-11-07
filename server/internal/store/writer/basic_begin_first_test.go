package writer

import (
	"bytes"
	"errors"
	"testing"
)

// Explicit retained ordinary fixture anchors, not a parent lookup/bootstrap.
func seedBasicFirstOrdinaryOwner(t *testing.T, w *executor) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`); err != nil {
		t.Fatal(err)
	}
	seedBasicFirstOwnerEntity(t, w, 31, 0x51)
}
func seedBasicFirstOwnerEntity(t *testing.T, w *executor, id int64, identityByte byte) {
	t.Helper()
	public := bytes.Repeat([]byte{identityByte}, 16)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,7,1)`, id, public); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
VALUES(?,?,7,1,'Arrival','','arrival','a','arrival','a',1,2016,'2026-11-07T00:00:00.000000000Z','en-US',?,?,?,1)`, id, public, bytes.Repeat([]byte{0x61}, 16), bytes.Repeat([]byte{0x71}, 16), bytes.Repeat([]byte{0x41}, 16)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id) VALUES(7,?,41)`, id); err != nil {
		t.Fatal(err)
	}
}
func TestBasicBeginNonFirstHolderRefusesBeforeDebt(t *testing.T) {
	for _, state := range []string{"published", "shared", "retired", "multiple_assets"} {
		t.Run(state, func(t *testing.T) {
			w, in := basicBeginFixture(t)
			switch state {
			case "published":
				for _, q := range []string{`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`, `UPDATE catalog_asset_links SET info_shape_id=61,info_duration=100 WHERE entity_id=31 AND asset_id=41`} {
					if _, err := w.db.ExecContext(t.Context(), q); err != nil {
						t.Fatal(err)
					}
				}
			case "shared":
				seedBasicFirstOwnerEntity(t, w, 32, 0x52)
			case "multiple_assets":
				for _, q := range []string{`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch)
VALUES(42,'basic-asset-42',7,'/media/Arrival-42.mkv',42,-11,'','','',0,0,0,17)`, `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id) VALUES(7,31,42)`} {
					if _, err := w.db.ExecContext(t.Context(), q); err != nil {
						t.Fatal(err)
					}
				}
			case "retired":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET retired=1 WHERE id=31`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicProbeTestTurn(t, w, basicBeginTestDefinitions())
			var beforeChanges, beforeDebt int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes(),used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&beforeChanges, &beforeDebt); err != nil {
				t.Fatal(err)
			}
			got, err := beginBasicInput(t.Context(), turn, in, 2000)
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || got != (basicBegunInput{}) {
				t.Fatal(got, err)
			}
			var changes, debt, inputs, pending int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes(),used_bytes,(SELECT count(*) FROM catalog_info_changes),(SELECT info_publication_pending FROM catalog_assets WHERE id=41) FROM catalog_info_input_budget WHERE singleton=1`).Scan(&changes, &debt, &inputs, &pending); err != nil || changes != beforeChanges || debt != beforeDebt || inputs != 0 || pending != 0 {
				t.Fatal(changes, debt, inputs, pending, err)
			}
		})
	}
}
