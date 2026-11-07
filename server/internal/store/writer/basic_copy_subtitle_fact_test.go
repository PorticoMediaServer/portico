package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func basicCopySubtitleFactTestDefinitions() []stmt.Definition {
	return append(basicPrefenceTestDefinitions(), basicCopySubtitleFactDefinitions()...)
}

// Setup precedes FULL Capture; accept, prefence and copy share the existing NORMAL handler frame.
func basicCopySubtitleFactFixture(t *testing.T, replace bool, defs []stmt.Definition) (*executor, *commandTurn, basicSubtitleFactCopyEffects) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	var before basicCanonicalSubtitleFact
	if replace {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_subtitle_facts VALUES('basic-asset',17,39,-19,-7,0,'old','old:17',1,23,'retained evidence')`); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_subtitles VALUES('retained-subtitle','basic-asset','embedded','9',9,NULL,'srt','fr','Retained subtitle',0,1,'retained reason',1)`); err != nil {
			t.Fatal(err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&before.revision, &before.size, &before.modifiedNS, &before.originUS, &before.timingKnown, &before.status, &before.fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	var next basicSubtitleFact
	var size, modified int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM catalog_info_new_subtitle_facts WHERE change_id=51 AND input_ordinal=0`).Scan(&next.revision, &size, &modified, &next.originUS, &next.timingKnown, &next.status, &next.fingerprint); err != nil {
		t.Fatal(err)
	}
	if size != input.stream.source.size || modified != input.stream.source.modifiedNS {
		t.Fatal("source drift")
	}
	f, err := prepareBasicMediaFamily(input.stream.source, 6, 0, []basicMediaRow{next})
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_subtitle_fact.go", "basic_copy_subtitle_fact_statements.go", "basic_copy_subtitle_fact_test.go")
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
	return w, turn, basicSubtitleFactCopyEffects{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision, hadBefore: replace, before: before}
}
func TestBasicCopySubtitleFactInsertAndReplace(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "replace"}[replace], func(t *testing.T) {
			w, turn, in := basicCopySubtitleFactFixture(t, replace, basicCopySubtitleFactTestDefinitions())
			got, err := installBasicSubtitleFact(t.Context(), turn, in)
			n := in.family.rows[0].(basicSubtitleFact)
			s := in.family.source
			if err != nil || got != (basicCanonicalSubtitleFact{n.revision, s.size, s.modifiedNS, n.originUS, n.timingKnown, n.status, n.fingerprint}) {
				t.Fatal(got, err)
			}
			var intact bool
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT info_publication_pending=1 AND info_epoch=17 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT rows_expected=1 AND rows_staged=1 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=6)
 AND (SELECT count(*)=1 FROM catalog_info_new_subtitle_facts WHERE change_id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&intact); err != nil || !intact {
				t.Fatal(intact, err)
			}
			if replace {
				if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=1 FROM asset_subtitles WHERE id='retained-subtitle' AND asset_id='basic-asset' AND origin='embedded' AND locator='9' AND stream_index=9 AND sidecar_id IS NULL AND format='srt' AND language='fr' AND title='Retained subtitle' AND is_default=0 AND is_forced=1 AND reason='retained reason' AND hearing_impaired=1) AND has_subtitle_inventory_evidence=1 AND inventory_revision=23 AND inventory_evidence='retained evidence' FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&intact); err != nil || !intact {
					t.Fatal(intact, err)
				}
			}
			basicMediaCommit(t, turn)
			var rev int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT revision FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&rev); err != nil || rev != n.revision {
				t.Fatal(rev, err)
			}
		})
	}
}
func TestBasicCopySubtitleFactDriftAndReturningRollback(t *testing.T) {
	for _, control := range []string{"before", "staged", "return_token", "return_origin"} {
		t.Run(control, func(t *testing.T) {
			defs := basicCopySubtitleFactTestDefinitions()
			for i := range defs {
				if defs[i].ID == basicCopySubtitleFactUpdate {
					if control == "return_token" {
						defs[i].SQL = strings.Replace(defs[i].SQL, "RETURNING asset_id,", "RETURNING 'wrong',", 1)
					}
					if control == "return_origin" {
						defs[i].SQL = strings.Replace(defs[i].SQL, "RETURNING asset_id,revision,size,modified_ns,origin_us,", "RETURNING asset_id,revision,size,modified_ns,origin_us+1,", 1)
					}
				}
			}
			w, turn, in := basicCopySubtitleFactFixture(t, true, defs)
			switch control {
			case "before":
				in.before.modifiedNS++
			case "staged":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_new_subtitle_facts SET fingerprint='drift' WHERE change_id=51`); err != nil {
					t.Fatal(err)
				}
			}
			got, err := installBasicSubtitleFact(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || got != (basicCanonicalSubtitleFact{}) {
				t.Fatal(got, err)
			}
			if strings.HasPrefix(control, "return_") {
				var rev int64
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT revision FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&rev); e != nil || rev != 41 {
					t.Fatal("control failed to write", rev, e)
				}
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var intact bool
			if e := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=1 FROM asset_subtitles WHERE id='retained-subtitle' AND title='Retained subtitle') AND revision=17 AND size=39 AND modified_ns=-19 AND origin_us=-7 AND timing_known=0 AND status='old' AND fingerprint='old:17' AND has_subtitle_inventory_evidence=1 AND inventory_revision=23 AND inventory_evidence='retained evidence' FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&intact); e != nil || !intact {
				t.Fatal(intact, e)
			}
		})
	}
}
func TestBasicCopySubtitleFactInvalidReturnsZero(t *testing.T) {
	got, err := installBasicSubtitleFact(t.Context(), nil, basicSubtitleFactCopyEffects{})
	if !errors.Is(err, ErrInvalid) || got != (basicCanonicalSubtitleFact{}) {
		t.Fatal(got, err)
	}
}
