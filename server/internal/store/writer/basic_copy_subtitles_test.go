package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func basicCopySubtitlesFixture(t *testing.T, replace bool, defs []stmt.Definition) (*executor, *commandTurn, basicSubtitleSidecarCopyEffects, basicSubtitleCopyEffects) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	before := basicSubtitleSidecar{id: "sidecar-1", path: "/media/Arrival.srt", digest: "old", size: 12, modifiedNS: 13}
	old := basicSubtitle{id: "subtitle-2", origin: "sidecar", locator: before.path, sidecarID: before.id, streamIndex: -1, format: "old", language: "old", title: "old"}
	for _, sql := range []string{
		"INSERT INTO asset_subtitle_facts VALUES('basic-asset',1,42,-11,0,1,'known','old',0,0,'')",
		"INSERT INTO subtitle_sidecars VALUES('unrelated','/unrelated.srt',2,3,'keep')",
		"INSERT INTO asset_subtitles VALUES('retained','basic-asset','sidecar','/unrelated.srt',-1,'unrelated','srt','en','Keep',0,0,'',0)",
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	if replace {
		if _, err := w.db.ExecContext(t.Context(), "INSERT INTO subtitle_sidecars VALUES(?,?,?,?,?)", before.id, before.path, before.size, before.modifiedNS, before.digest); err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), "INSERT INTO asset_subtitles VALUES('subtitle-2','basic-asset','sidecar','/media/Arrival.srt',-1,'sidecar-1','old','old','old',0,0,'',0)"); err != nil {
			t.Fatal(err)
		}
	}
	next := basicSubtitleSidecar{id: "sidecar-1", path: "/media/Arrival.srt", digest: "sidecar:30", size: 30, modifiedNS: -7}
	sub := basicSubtitle{id: "subtitle-2", origin: "sidecar", locator: next.path, sidecarID: next.id, streamIndex: -1, format: "subrip", language: "fr"}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_subtitles.go", "basic_copy_subtitles_statements.go", "basic_copy_subtitles_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	f, err := prepareBasicMediaFamily(input.stream.source, 7, 0, []basicMediaRow{next})
	if err != nil {
		t.Fatal(err)
	}
	sf, err := prepareBasicMediaFamily(input.stream.source, 8, 0, []basicMediaRow{basicSubtitle{id: "subtitle-1", origin: "embedded", locator: "2", streamIndex: 2, format: "subrip", language: "en"}, sub})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	pos, err := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if err != nil {
		t.Fatal(err)
	}
	fence := basicStreamCopyFence{family: f, position: pos, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision}
	a := basicSubtitleSidecarCopyEffects{basicStreamCopyFence: fence, hadBefore: replace}
	fence.family = sf
	fence.ordinal = 1
	b := basicSubtitleCopyEffects{basicStreamCopyFence: fence, hadBefore: replace}
	if replace {
		a.before = before
		b.before = old
	}
	return w, turn, a, b
}
func basicCopySubtitlesTestDefinitions() []stmt.Definition {
	d := append(basicPrefenceTestDefinitions(), basicCopyStreamDefinitions()...)
	return append(d, basicCopySubtitlesDefinitions()...)
}
func TestBasicCopySubtitlesInsertReplace(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "replace"}[replace], func(t *testing.T) {
			_, turn, a, b := basicCopySubtitlesFixture(t, replace, basicCopySubtitlesTestDefinitions())
			if _, err := installBasicSubtitleSidecar(t.Context(), turn, a); err != nil {
				t.Fatal(err)
			}
			if _, err := installBasicSubtitle(t.Context(), turn, b); err != nil {
				t.Fatal(err)
			}
			var intact bool
			if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT (SELECT count(*)=1 FROM asset_subtitles WHERE id='retained' AND title='Keep') AND (SELECT count(*)=1 FROM subtitle_sidecars WHERE id='unrelated' AND digest='keep') AND (SELECT phase=2 AND copy_family=0 FROM catalog_info_changes WHERE id=51) AND (SELECT count(*)=0 FROM scan_command_receipts)").Scan(&intact); err != nil || !intact {
				t.Fatal(intact, err)
			}
			basicMediaCommit(t, turn)
		})
	}
}
func TestBasicCopySubtitlesControls(t *testing.T) {
	for _, control := range []string{"sidecar_return", "subtitle_return", "staged"} {
		t.Run(control, func(t *testing.T) {
			defs := basicCopySubtitlesTestDefinitions()
			for i := range defs {
				if control == "sidecar_return" && defs[i].ID == basicCopySidecarUpdate {
					defs[i].SQL = strings.Replace(defs[i].SQL, "RETURNING id,path,size,modified_ns,digest", "RETURNING path,id,size,modified_ns,digest", 1)
				}
				if control == "subtitle_return" && defs[i].ID == basicCopySubtitleUpdate {
					defs[i].SQL = strings.Replace(defs[i].SQL, "RETURNING id,asset_id,origin,locator", "RETURNING locator,asset_id,origin,id", 1)
				}
			}
			w, turn, a, b := basicCopySubtitlesFixture(t, true, defs)
			if control == "staged" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), "UPDATE catalog_info_new_subtitles SET title='drift' WHERE change_id=51 AND id='subtitle-2'"); err != nil {
					t.Fatal(err)
				}
			}
			av, err := installBasicSubtitleSidecar(t.Context(), turn, a)
			if control == "sidecar_return" {
				if !errors.Is(err, ErrConflict) || av != (basicSubtitleSidecar{}) {
					t.Fatal(av, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				v, e := installBasicSubtitle(t.Context(), turn, b)
				if !errors.Is(e, ErrConflict) || v != (basicSubtitle{}) {
					t.Fatal(v, e)
				}
			}
			if strings.HasSuffix(control, "_return") {
				var changed bool
				query := "SELECT digest='sidecar:30' FROM subtitle_sidecars WHERE id='sidecar-1'"
				if control == "subtitle_return" {
					query = "SELECT format='subrip' FROM asset_subtitles WHERE id='subtitle-2'"
				}
				if e := turn.transaction.conn.QueryRowContext(t.Context(), query).Scan(&changed); e != nil || !changed {
					t.Fatal("RETURNING control did not write", changed, e)
				}
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(ErrConflict)
			var digest string
			if err := w.db.QueryRowContext(t.Context(), "SELECT digest FROM subtitle_sidecars WHERE id='sidecar-1'").Scan(&digest); err != nil || digest != "old" {
				t.Fatal(digest, err)
			}
		})
	}
}

func TestBasicCopySubtitlesPurePreflight(t *testing.T) {
	if _, err := stmt.Describe(basicCopySubtitlesDefinitions()); err != nil {
		t.Fatal(err)
	}
	source := basicStreamFactStage{changeID: 51}
	next := basicSubtitleSidecar{id: "sidecar", path: "/valid", digest: "digest", size: 1}
	f := &basicMediaFamily{valid: true, family: 7, source: source, rows: []basicMediaRow{next}}
	fence := basicStreamCopyFence{family: f, position: basicCopyPosition{changeID: 51, changeRevision: 1, phase: 2, afterEntity: 31}, shapeID: 1, fileSizeRevision: 1, invalidation: 1}
	for _, tc := range []struct {
		path string
		want error
	}{{string([]byte{'/', 255}), ErrFilenameUnrepresentable}, {"/bad\x00", ErrInvalid}} {
		before := next
		before.path = tc.path
		if err := preflightBasicSubtitleCopy(fence, next, before); !errors.Is(err, tc.want) {
			t.Fatal(err, tc.want)
		}
	}
	if v, err := installBasicSubtitleSidecar(t.Context(), nil, basicSubtitleSidecarCopyEffects{}); v != (basicSubtitleSidecar{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal(v, err)
	}
	if v, err := installBasicSubtitle(t.Context(), nil, basicSubtitleCopyEffects{}); v != (basicSubtitle{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal(v, err)
	}
}
