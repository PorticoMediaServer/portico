package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicSubtitleStepDefinitions() []stmt.Definition {
	d := append(basicCopySubtitlesTestDefinitions(), basicSubtitleLanguageDefinitions()...)
	return append(d, basicCopyStepDefinitions()...)
}

// Use literal production definitions and the existing FULL Capture / NORMAL
// handler fixture. Move the setup cursor with its real CAS, then capture the
// actual native CGR before invoking the component under test.
func basicSubtitleStepFixture(t *testing.T, replace, empty bool) (*executor, *commandTurn, basicSubtitleCopyEffects) {
	t.Helper()
	w, turn, sidecar, in := basicCopySubtitlesFixture(t, replace, basicSubtitleStepDefinitions())
	if _, err := installBasicSubtitleSidecar(t.Context(), turn, sidecar); err != nil {
		t.Fatal(err)
	}
	next := in.position
	next.family, next.id = 8, in.ordinal
	if _, err := advanceBasicCopiedPosition(t.Context(), turn, in.family.source, in.position, next, in.shapeID); err != nil {
		t.Fatal(err)
	}
	p := &in.position
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT id,job_revision,phase,after_entity,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3 FROM catalog_info_changes WHERE id=51`).Scan(&p.changeID, &p.changeRevision, &p.phase, &p.afterEntity, &p.family, &p.copyPhase, &p.id, &p.aux, &p.key, &p.key2, &p.key3); err != nil {
		t.Fatal(err)
	}
	if empty {
		row := in.family.rows[in.ordinal].(basicSubtitle)
		row.language = ""
		in.family.rows[in.ordinal] = row
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_new_subtitles SET language='' WHERE change_id=51 AND input_ordinal=1`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO asset_subtitle_languages VALUES('en',1)`); err != nil {
		t.Fatal(err)
	}
	if replace {
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO asset_subtitle_languages VALUES('old',1)`); err != nil {
			t.Fatal(err)
		}
	}
	return w, turn, in
}

func TestBasicSubtitleStepInsertIncludingEmptyLanguage(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "fr", true: "empty"}[empty], func(t *testing.T) {
			w, turn, in := basicSubtitleStepFixture(t, false, empty)
			got, err := copyBasicSubtitleStep(t.Context(), turn, in)
			want := in.position
			want.changeRevision++
			want.copyPhase, want.id = 1, 0
			if err != nil || got != want {
				t.Fatal(got, err, want)
			}
			basicMediaCommit(t, turn)
			language := in.family.rows[in.ordinal].(basicSubtitle).language
			var exact bool
			if err := w.db.QueryRowContext(t.Context(), `SELECT
    (SELECT language=? FROM asset_subtitles WHERE id='subtitle-2')
    AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language=?)
    AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language='en')
    AND (SELECT title='Keep' FROM asset_subtitles WHERE id='retained')
    AND (SELECT phase=2 AND copy_family=8 AND copy_phase=1 AND copy_id=0 FROM catalog_info_changes WHERE id=51)
    AND (SELECT info_publication_pending=1 AND info_epoch=17 FROM catalog_assets WHERE id=41)
    AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
    AND (SELECT count(*)=0 FROM scan_command_receipts)`, language, language).Scan(&exact); err != nil || !exact {
				t.Fatal(exact, err)
			}
		})
	}
}

func TestBasicSubtitleStepReplacementUsesExactOldLanguage(t *testing.T) {
	w, turn, in := basicSubtitleStepFixture(t, true, false)
	got, err := copyBasicSubtitleStep(t.Context(), turn, in)
	if err != nil || got.copyPhase != 1 || got.family != 8 {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
  (SELECT language='fr' AND format='subrip' FROM asset_subtitles WHERE id='subtitle-2')
  AND (SELECT count(*)=0 FROM asset_subtitle_languages WHERE language='old')
  AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language='fr')
  AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language='en')
  AND (SELECT title='Keep' FROM asset_subtitles WHERE id='retained')
  AND (SELECT info_publication_pending=1 FROM catalog_assets WHERE id=41)
  AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}

func TestBasicSubtitleStepLateFailuresRollbackTrackAndCounters(t *testing.T) {
	for _, fault := range []string{"cursor", "language_overflow"} {
		t.Run(fault, func(t *testing.T) {
			w, turn, in := basicSubtitleStepFixture(t, true, false)
			if fault == "cursor" {
				delete(turn.steps, basicCopyPositionCAS)
			} else {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO asset_subtitle_languages VALUES('fr',9223372036854775807)`); err != nil {
					t.Fatal(err)
				}
			}
			got, err := copyBasicSubtitleStep(t.Context(), turn, in)
			if err == nil || got != (basicCopyPosition{}) {
				t.Fatal(got, err)
			}
			if fault == "language_overflow" && !errors.Is(err, ErrCap) {
				t.Fatal(err)
			}
			var written bool
			if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT language='fr' FROM asset_subtitles WHERE id='subtitle-2') AND (SELECT count(*)=0 FROM asset_subtitle_languages WHERE language='old')`).Scan(&written); e != nil || !written {
				t.Fatal("late boundary not reached", written, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var exact bool
			// Counter seeds belong to this parent frame too; its rollback restores the
			// original empty counter table as well as the original canonical track.
			if e := w.db.QueryRowContext(t.Context(), `SELECT
    (SELECT language='old' AND format='old' FROM asset_subtitles WHERE id='subtitle-2')
    AND (SELECT count(*)=0 FROM asset_subtitle_languages)
    AND (SELECT title='Keep' FROM asset_subtitles WHERE id='retained')
    AND (SELECT phase=0 AND job_revision=13 FROM catalog_info_changes WHERE id=51)
    AND (SELECT info_publication_pending=0 FROM catalog_assets WHERE id=41)
    AND (SELECT info_pending_change IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
    AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); e != nil || !exact {
				t.Fatal(exact, e)
			}
		})
	}
}
