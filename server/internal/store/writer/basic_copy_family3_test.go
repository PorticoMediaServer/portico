package writer

import (
	"errors"
	"testing"
)

func seedBasicCopy3First(t *testing.T, e *executor, entry basicCopyCursor, revision int64) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=2,family_mask=511,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31,copy_family=?,copy_phase=?,copy_id=?,copy_aux=?,copy_key='',copy_key2='',copy_key3='',job_revision=? WHERE id=51`,
		entry.family, entry.phase, entry.id, entry.aux, revision); err != nil {
		t.Fatal(err)
	}
}

// Family 3 installs staged breadth onto the existing stream row and clears
// canonical breadth the staged set no longer names; the family-2 core row is
// never deleted by this family.
func TestBasicCopyFamily3BreadthDispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy3First(t, e, basicCopyCursor{family: 3}, 2)
	for _, q := range []string{
		`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES('basic-asset',0,'video','h264','','Main',0,'',1,0,'bt709','bt709','')`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision) VALUES('basic-asset',1,'audio','aac','','English',2,'stereo',1,0,'','','',1,1,'stale','4:2:0',24000,1001,'constant','progressive',0,0,0,0,'','','','')`,
		`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(51,0,'basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(51,0,'basic-asset',0,'video','h264','','Main',0,'',1,0,'bt709','bt709','')`,
		`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version,playback_frames_revision,packet_peak_revision) VALUES(51,0,'basic-asset',0,1,'main','4:2:0',24000,1001,'variable','progressive',1,0,0,1,'','','','')`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	head := basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 3, operation: basicControlCopyFamily}
	turn := func(revision int64, entry basicCopyCursor) (BasicResult, error) {
		in := head
		in.changeRevision, in.copy = revision, entry
		return chain.stage(in)
	}
	got, err := turn(2, basicCopyCursor{family: 3})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 3, NextCopyPhase: 1, AfterEntity: 31}) {
		t.Fatal("breadth install turn", got, err)
	}
	got, err = turn(3, basicCopyCursor{family: 3, phase: 1})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 4, AfterEntity: 31}) {
		t.Fatal("breadth cleanup turn", got, err)
	}
	basicDispatchReturned(t, e, authority)
	var has, ad, telecine, rpu, fps int64
	var variant, chroma, mode string
	if err := e.db.QueryRowContext(t.Context(), `SELECT has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_frame_rate_mode,breadth_telecine,breadth_dovi_rpu FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=0`).Scan(&has, &ad, &variant, &chroma, &fps, &mode, &telecine, &rpu); err != nil ||
		has != 1 || ad != 1 || variant != "main" || chroma != "4:2:0" || fps != 24000 || mode != "variable" || telecine != 1 || rpu != 0 {
		t.Fatal("installed breadth", has, ad, variant, chroma, fps, mode, telecine, rpu, err)
	}
	var clearedHas, clearedAD, clearedFPS int64
	var clearedVariant, clearedMode, clearedEnhancement string
	if err := e.db.QueryRowContext(t.Context(), `SELECT has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_fps_numerator,breadth_frame_rate_mode,breadth_dovi_enhancement_type FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=1`).Scan(&clearedHas, &clearedAD, &clearedVariant, &clearedFPS, &clearedMode, &clearedEnhancement); err != nil ||
		clearedHas != 0 || clearedAD != 0 || clearedVariant != "" || clearedFPS != 0 || clearedMode != "" || clearedEnhancement != "" {
		t.Fatal("cleared obsolete breadth", clearedHas, clearedAD, clearedVariant, clearedFPS, clearedMode, clearedEnhancement, err)
	}
	var cores int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&cores); err != nil || cores != 2 {
		t.Fatal("family 3 deleted a family-2 core row", cores, err)
	}
	var phase, family, id, aux, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT copy_phase,copy_family,copy_id,copy_aux,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &family, &id, &aux, &revision); err != nil ||
		phase != 0 || family != 4 || id != 0 || aux != 0 || revision != 4 {
		t.Fatal("breadth post-state", phase, family, id, aux, revision, err)
	}
	if _, err := turn(4, basicCopyCursor{family: 3}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal("stale breadth entry was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
}

// A staged breadth row without its canonical family-2 parent is refused with no
// stream-row effect.
func TestBasicCopyFamily3RefusesMissingStreamParent(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy3First(t, e, basicCopyCursor{family: 3}, 2)
	for _, q := range []string{
		`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(51,0,'basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(51,0,'basic-asset',9,'audio','aac','','Orphan',2,'stereo',0,0,'','','')`,
		`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version,playback_frames_revision,packet_peak_revision) VALUES(51,0,'basic-asset',9,0,'','',0,0,'','',0,0,0,0,'','','','')`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	in := basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 3, operation: basicControlCopyFamily,
		changeRevision: 2, copy: basicCopyCursor{family: 3}}
	if _, err := chain.stage(in); !errors.Is(err, ErrConflict) {
		t.Fatal("missing stream parent was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
	var streams, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams`).Scan(&streams); err != nil || streams != 0 {
		t.Fatal("refusal created a stream row", streams, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("refusal moved the change", revision, err)
	}
}
