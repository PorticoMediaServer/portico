package writer

import (
	"errors"
	"testing"
)

// seedBasicCopy2First puts the change header directly into phase 2 at the exact
// entry cursor under test (the freeze/entity turns are covered by family 1).
func seedBasicCopy2First(t *testing.T, e *executor, family, revision int64, entry basicCopyCursor) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=2,family_mask=511,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31,copy_family=?,copy_phase=?,copy_id=?,copy_aux=?,copy_key='',copy_key2='',copy_key3='',job_revision=? WHERE id=51`,
		family, entry.phase, entry.id, entry.aux, revision); err != nil {
		t.Fatal(err)
	}
}

// Family 2 installs the staged streams, removes the obsolete canonical stream
// and finishes in {family:3}; a stale entry cursor is refused with no effect.
func TestBasicCopyFamily2StreamsDispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy2First(t, e, 2, 2, basicCopyCursor{family: 2})
	for _, q := range []string{
		`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES('basic-asset',0,'video','hevc','','Old video',0,'',1,0,'','','')`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES('basic-asset',2,'audio','ac3','','Obsolete audio',2,'stereo',0,0,'','','')`,
		`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(51,0,'basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(51,0,'basic-asset',0,'video','h264','','Main',0,'',1,0,'bt709','bt709','{"width":1920}')`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(51,1,'basic-asset',1,'audio','aac','en','English',2,'stereo',1,0,'','','')`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(51,2,'basic-asset',3,'subtitle','subrip','en','English',0,'',0,0,'','','')`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	head := basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 2, operation: basicControlCopyFamily}
	turn := func(revision int64, entry basicCopyCursor) (BasicResult, error) {
		in := head
		in.changeRevision, in.copy = revision, entry
		return chain.stage(in)
	}
	got, err := turn(2, basicCopyCursor{family: 2})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 2, NextCopyPhase: 1, AfterEntity: 31}) {
		t.Fatal("install turn", got, err)
	}
	got, err = turn(3, basicCopyCursor{family: 2, phase: 1})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 3, AfterEntity: 31}) {
		t.Fatal("cleanup turn", got, err)
	}
	basicDispatchReturned(t, e, authority)
	var count int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("canonical stream count", count, err)
	}
	var codec, title string
	if err := e.db.QueryRowContext(t.Context(), `SELECT codec,title FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=0`).Scan(&codec, &title); err != nil || codec != "h264" || title != "Main" {
		t.Fatal("installed stream 0", codec, title, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT codec FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=1`).Scan(&codec); err != nil || codec != "aac" {
		t.Fatal("installed stream 1", codec, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT codec FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=3`).Scan(&codec); err != nil || codec != "subrip" {
		t.Fatal("installed stream 3", codec, err)
	}
	var phase, family, id, aux, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT copy_phase,copy_family,copy_id,copy_aux,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &family, &id, &aux, &revision); err != nil ||
		phase != 0 || family != 3 || id != 0 || aux != 0 || revision != 4 {
		t.Fatal("copy post-state", phase, family, id, aux, revision, err)
	}
	// A stale entry cursor (the family already completed) is refused with no
	// canonical or header effect.
	if _, err := turn(4, basicCopyCursor{family: 2}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal("stale copy entry was not refused", err)
	}
	// A shape the frozen codec never permits is refused before any effect.
	if _, err := turn(4, basicCopyCursor{family: 2, phase: 1, id: 0, aux: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid entry shape was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("refusal mutated canonical streams", count, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 4 {
		t.Fatal("refusal moved the change", revision, err)
	}
}
