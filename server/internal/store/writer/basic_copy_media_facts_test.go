package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCopyChapterFactTestDefinitions() []stmt.Definition {
	return append(basicPrefenceTestDefinitions(), basicCopyMediaFactsDefinitions()...)
}

// Setup precedes FULL Capture; accept, prefence and copy share the existing NORMAL handler frame.
func basicCopyChapterFactFixture(t *testing.T, replace bool, defs []stmt.Definition) (*executor, *commandTurn, basicChapterFactCopyEffects) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	var before basicCanonicalChapterFact
	if replace {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_chapter_facts VALUES('basic-asset',17,39,-19,'old','old:17')`); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_chapters VALUES('basic-asset',9,'Retained chapter',1,2)`); err != nil {
			t.Fatal(err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,status,fingerprint FROM asset_chapter_facts WHERE asset_id='basic-asset'`).Scan(&before.revision, &before.size, &before.modifiedNS, &before.status, &before.fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	var next basicChapterFact
	var size, modified int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,status,fingerprint FROM catalog_info_new_chapter_facts WHERE change_id=51 AND input_ordinal=0`).Scan(&next.revision, &size, &modified, &next.status, &next.fingerprint); err != nil {
		t.Fatal(err)
	}
	if size != input.stream.source.size || modified != input.stream.source.modifiedNS {
		t.Fatal("source drift")
	}
	f, err := prepareBasicMediaFamily(input.stream.source, 4, 0, []basicMediaRow{next})
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_media_facts.go", "basic_copy_media_facts_statements.go", "basic_copy_media_facts_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	accepted, err := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	p, err := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if err != nil {
		t.Fatal(err)
	}
	return w, turn, basicChapterFactCopyEffects{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision, hadBefore: replace, before: before}
}
func TestBasicCopyChapterFactInsertAndReplace(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "replace"}[replace], func(t *testing.T) {
			w, turn, in := basicCopyChapterFactFixture(t, replace, basicCopyChapterFactTestDefinitions())
			got, err := installBasicChapterFact(t.Context(), turn, in)
			n := in.family.rows[0].(basicChapterFact)
			s := in.family.source
			if err != nil || got != (basicCanonicalChapterFact{n.revision, s.size, s.modifiedNS, n.status, n.fingerprint}) {
				t.Fatal(got, err)
			}
			var intact bool
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT info_publication_pending=1 AND info_epoch=17 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT rows_expected=1 AND rows_staged=1 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=4)
 AND (SELECT count(*)=1 FROM catalog_info_new_chapter_facts WHERE change_id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&intact); err != nil || !intact {
				t.Fatal(intact, err)
			}
			if replace {
				if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*)=1 FROM asset_chapters WHERE title='Retained chapter'`).Scan(&intact); err != nil || !intact {
					t.Fatal(intact, err)
				}
			}
			basicMediaCommit(t, turn)
			var rev int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT revision FROM asset_chapter_facts WHERE asset_id='basic-asset'`).Scan(&rev); err != nil || rev != n.revision {
				t.Fatal(rev, err)
			}
		})
	}
}
func basicCopyEvidenceFixture(t *testing.T, replace bool, defs []stmt.Definition) (*executor, *commandTurn, basicSubtitleEvidenceCopyEffects) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)

	var before basicSubtitleEvidence
	discriminator := 0
	var rev any
	var ev any
	if replace {
		discriminator = 1
		rev = int64(23)
		ev = "old evidence"
		before = basicSubtitleEvidence{23, "old evidence"}
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_subtitle_facts VALUES('basic-asset',17,39,-19,-7,0,'old','old:17',?,?,?)`, discriminator, rev, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_subtitles VALUES('retained-subtitle','basic-asset','embedded','9',9,NULL,'srt','fr','Retained subtitle',0,1,'retained reason',1)`); err != nil {
		t.Fatal(err)
	}
	var next basicSubtitleEvidence
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,evidence FROM catalog_info_new_subtitle_inventory_evidence WHERE change_id=51 AND input_ordinal=0`).Scan(&next.revision, &next.evidence); err != nil {
		t.Fatal(err)
	}
	f, err := prepareBasicMediaFamily(input.stream.source, 9, 0, []basicMediaRow{next})
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_media_facts.go", "basic_copy_media_facts_statements.go", "basic_copy_media_facts_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	accepted, err := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	p, err := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if err != nil {
		t.Fatal(err)
	}
	return w, turn, basicSubtitleEvidenceCopyEffects{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision, hadBefore: replace, before: before}
}

func TestBasicCopyEvidencePreservesSourceAndChildren(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[replace], func(t *testing.T) {
			w, turn, in := basicCopyEvidenceFixture(t, replace, basicCopyChapterFactTestDefinitions())
			got, err := installBasicSubtitleEvidence(t.Context(), turn, in)
			if err != nil || got != in.family.rows[0].(basicSubtitleEvidence) {
				t.Fatal(got, err)
			}
			var intact bool
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT revision=17 AND size=39 AND modified_ns=-19 AND origin_us=-7 AND timing_known=0 AND status='old' AND fingerprint='old:17' AND (SELECT count(*)=1 FROM asset_subtitles WHERE id='retained-subtitle' AND title='Retained subtitle') FROM asset_subtitle_facts`).Scan(&intact); err != nil || !intact {
				t.Fatal(intact, err)
			}
			basicMediaCommit(t, turn)
			var revision int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT inventory_revision FROM asset_subtitle_facts`).Scan(&revision); err != nil || revision != got.revision {
				t.Fatal(revision, err)
			}
		})
	}
}
func TestBasicCopyMediaFactsReturningRollback(t *testing.T) {
	for _, evidence := range []bool{false, true} {
		t.Run(map[bool]string{false: "chapter", true: "evidence"}[evidence], func(t *testing.T) {
			defs := basicCopyChapterFactTestDefinitions()
			for n := range defs {
				if !evidence && defs[n].ID == basicCopyChapterFactUpdate {
					defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,revision,size,modified_ns,status,fingerprint", "RETURNING fingerprint,revision,size,modified_ns,status,asset_id", 1)
				}
				if evidence && defs[n].ID == basicCopySubtitleEvidenceUpdate {
					defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence", "RETURNING inventory_evidence,has_subtitle_inventory_evidence,inventory_revision,asset_id", 1)
				}
			}
			var w *executor
			var turn *commandTurn
			var err error
			if evidence {
				var in basicSubtitleEvidenceCopyEffects
				w, turn, in = basicCopyEvidenceFixture(t, true, defs)
				_, err = installBasicSubtitleEvidence(t.Context(), turn, in)
			} else {
				var in basicChapterFactCopyEffects
				w, turn, in = basicCopyChapterFactFixture(t, true, defs)
				_, err = installBasicChapterFact(t.Context(), turn, in)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			var rev int64
			table := "asset_chapter_facts"
			column := "revision"
			want := int64(37)
			if evidence {
				table = "asset_subtitle_facts"
				column = "inventory_revision"
				want = 41
			}
			if e := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT "+column+" FROM "+table).Scan(&rev); e != nil || rev != want {
				t.Fatal("mutation did not write", rev, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			want = 17
			if evidence {
				want = 23
			}
			if e := w.db.QueryRowContext(t.Context(), "SELECT "+column+" FROM "+table).Scan(&rev); e != nil || rev != want {
				t.Fatal(rev, e)
			}
		})
	}
}

func TestBasicCopyMediaFactsInvalidReturnsZero(t *testing.T) {
	chapter, err := installBasicChapterFact(t.Context(), nil, basicChapterFactCopyEffects{})
	if !errors.Is(err, ErrInvalid) || chapter != (basicCanonicalChapterFact{}) {
		t.Fatal(chapter, err)
	}
	evidence, err := installBasicSubtitleEvidence(t.Context(), nil, basicSubtitleEvidenceCopyEffects{})
	if !errors.Is(err, ErrInvalid) || evidence != (basicSubtitleEvidence{}) {
		t.Fatal(evidence, err)
	}
}
