package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicOrdinaryInputAcceptFixture(t *testing.T) (*executor, basicStreamAccept) {
	t.Helper()
	w, source := basicStreamFixture(t)
	for _, q := range []string{
		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
		`UPDATE catalog_assets SET container='mkv',video_codec='h264',height=1080 WHERE id=41`,
		`UPDATE catalog_info_changes SET job_revision=13,target_shape_id=61,copy_family=1 WHERE id=51`,
		`INSERT INTO catalog_info_new_stream_facts VALUES(51,0,'basic-asset',31,42,-11,'probe-fingerprint',1)`,
		`UPDATE catalog_info_input_families SET rows_staged=1,sealed=1 WHERE change_id=51 AND family=1`,
		`INSERT INTO catalog_info_input_families VALUES(51,2,0,2,2,1)`,
		`INSERT INTO catalog_info_input_families VALUES(51,3,0,2,2,1)`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced)
VALUES(51,0,'basic-asset',0,'video','h264','','',0,'',1,0)`,
		`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced)
VALUES(51,1,'basic-asset',3,'audio','aac','en','',2,'stereo',1,0)`,
		`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index) VALUES(51,0,'basic-asset',0)`,
		`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index) VALUES(51,1,'basic-asset',3)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	identity := bytes.Repeat([]byte{0x31}, 16)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(31,?,7,1)`, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
VALUES(31,?,7,1,'Arrival','Arrival','arrival','a','arrival','a',1,2016,'2026-11-06T00:00:00.000000000Z','en-US',?,?,?,1)`, identity, identity, identity, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id) VALUES(7,31,41)`); err != nil {
		t.Fatal(err)
	}
	return w, basicStreamAccept{source: source, changeRevision: 13, shapeID: 61, rawShape: ScanShape{LibraryID: 7, OrdinaryOwners: 1, VersionAssetIDs: []int64{41}, AuthoredParentIDs: []int64{31}, Streams: []Stream{{3, AudioStream}, {0, VideoStream}}}}
}

func basicAcceptDefinitions() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicStreamAcceptDefinitions()...)
}

func basicAcceptAddLinkedAsset(t *testing.T, w *executor, id int64, part int) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
SELECT ?,'basic-asset-'||?,library_id,'/media/basic-asset-'||?||'.mkv',size,modified_ns,container,video_codec,audio_codec,width,height,duration FROM catalog_assets WHERE id=41`, id, id, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,part_index) VALUES(7,31,?,?)`, id, part); err != nil {
		t.Fatal(err)
	}
}

func TestBasicAcceptNativePartsDoNotCountAsWholeVersions(t *testing.T) {
	w, in := basicOrdinaryInputAcceptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_asset_links SET part_index=1 WHERE entity_id=31 AND asset_id=41`); err != nil {
		t.Fatal(err)
	}
	basicAcceptAddLinkedAsset(t, w, 42, 0)
	basicAcceptAddLinkedAsset(t, w, 43, 2)
	in.rawShape.VersionAssetIDs = []int64{42}
	turn := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
	got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000)
	if err != nil || got != (basicAcceptedPending{51, 7, 17, 14, 31, 1, 61}) {
		t.Fatal(got, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var phase, epoch, canonicalStreams int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&canonicalStreams); err != nil {
		t.Fatal(err)
	}
	if phase != 1 || epoch != 17 || canonicalStreams != 0 {
		t.Fatal(phase, epoch, canonicalStreams)
	}
}

func TestBasicAcceptNativeWholeVersionOverflowRefusesPreparedPrefix(t *testing.T) {
	w, in := basicOrdinaryInputAcceptFixture(t)
	// The prepared set is valid at 16; the native set has a seventeenth whole
	// version. The bounded native read must refuse instead of trusting a prefix.
	for id := int64(42); id <= 57; id++ {
		basicAcceptAddLinkedAsset(t, w, id, 0)
		if id < 57 {
			in.rawShape.VersionAssetIDs = append(in.rawShape.VersionAssetIDs, id)
		}
	}
	turn := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
	got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000)
	if !errors.Is(err, ErrCap) || got != (basicAcceptedPending{}) {
		t.Fatal(got, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var phase, revision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision); err != nil || phase != 0 || revision != 13 {
		t.Fatal(phase, revision, err)
	}
}

func TestBasicAcceptNativeRequiresExactRetainedOwner(t *testing.T) {
	for _, name := range []string{"wrong_library", "wrong_kind", "wrong_returned_id"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicOrdinaryInputAcceptFixture(t)
			defs := basicAcceptDefinitions()
			if name == "wrong_returned_id" {
				for n := range defs {
					if defs[n].ID == basicAcceptSubject {
						// A native point read with a wrong returned identity receives
						// its own real Capture, like the existing row-fault controls.
						defs[n].SQL = strings.Replace(defs[n].SQL, "SELECT id,", "SELECT kind,", 1)
					}
				}
			}
			turn := basicBoundedComponentTurn(t, w, defs)
			if name != "wrong_returned_id" {
				// Defer foreign keys only in this rollback-only corruption control;
				// the live entity and prepared owner remain unchanged.
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `PRAGMA defer_foreign_keys=ON`); err != nil {
					t.Fatal(err)
				}
				q := `UPDATE catalog_subjects SET library_id=8 WHERE id=31`
				if name == "wrong_kind" {
					q = `UPDATE catalog_subjects SET kind=2 WHERE id=31`
				}
				if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000)
			if !errors.Is(err, ErrConflict) || got != (basicAcceptedPending{}) {
				t.Fatal(got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var phase, revision, library, kind int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision); err != nil || phase != 0 || revision != 13 {
				t.Fatal(phase, revision, err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT library_id,kind FROM catalog_subjects WHERE id=31`).Scan(&library, &kind); err != nil || library != 7 || kind != 1 {
				t.Fatal(library, kind, err)
			}
		})
	}
}

func TestBasicAcceptNativeFreezesInputWithoutActivating(t *testing.T) {
	w, in := basicOrdinaryInputAcceptFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
	got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000)
	if err != nil || got != (basicAcceptedPending{51, 7, 17, 14, 31, 1, 61}) {
		t.Fatal(got, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var phase, revision, due, epoch, canonicalStreams, ownerEnd int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT phase,job_revision,job_next_attempt_ms,upper_entity FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision, &due, &ownerEnd); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&canonicalStreams); err != nil {
		t.Fatal(err)
	}
	if phase != 1 || revision != 14 || due != 2000 || ownerEnd != 31 || epoch != 17 || canonicalStreams != 0 {
		t.Fatal(phase, revision, due, ownerEnd, epoch, canonicalStreams)
	}
	// Family-seal reuse is rejected after whole-input freezing.
	turn2 := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
	if v, e := sealBasicStreamFact(t.Context(), turn2, in.source); !errors.Is(e, ErrConflict) || v != (basicFamilyProgress{}) {
		t.Fatal(v, e)
	}
}

func TestBasicAcceptNativeRejectsIncompleteOrDriftedInput(t *testing.T) {
	for _, name := range []string{"unsealed_family", "missing_breadth", "wrong_shape", "wrong_version_union"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicOrdinaryInputAcceptFixture(t)
			switch name {
			case "unsealed_family":
				if _, e := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_families SET sealed=0 WHERE change_id=51 AND family=3`); e != nil {
					t.Fatal(e)
				}
			case "missing_breadth":
				if _, e := w.db.ExecContext(t.Context(), `DELETE FROM catalog_info_new_stream_breadth WHERE change_id=51 AND stream_index=3`); e != nil {
					t.Fatal(e)
				}
			case "wrong_shape":
				if _, e := w.db.ExecContext(t.Context(), `UPDATE catalog_info_shapes SET height=720 WHERE id=61`); e != nil {
					t.Fatal(e)
				}
			case "wrong_version_union":
				in.rawShape.VersionAssetIDs = []int64{42}
			}
			turn := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
			got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000)
			if !errors.Is(err, ErrConflict) || got != (basicAcceptedPending{}) {
				t.Fatal(got, err)
			}
			if _, err = turn.transaction.rollback(err); err == nil {
				t.Fatal("lost refusal")
			}
			var phase, revision int64
			if e := w.db.QueryRowContext(t.Context(), `SELECT phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision); e != nil || phase != 0 || revision != 13 {
				t.Fatal(phase, revision, e)
			}
		})
	}
}

func TestBasicAcceptRawCapRefusesBeforeNativeFreeze(t *testing.T) {
	w, in := basicOrdinaryInputAcceptFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicAcceptDefinitions())
	in.rawShape.RawChapterCount = MaxRawChapters + 1
	if got, err := acceptBasicStreamInput(t.Context(), turn, in, 2000); !errors.Is(err, ErrCap) || got != (basicAcceptedPending{}) {
		t.Fatal(got, err)
	}
}
