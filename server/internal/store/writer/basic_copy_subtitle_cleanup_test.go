package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicSubtitleCleanupDefinitionsAndPreflight(t *testing.T) {
	defs := basicCopySubtitleCleanupDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if strings.Contains(d.SQL, "sqlite_autoindex") || strings.Contains(d.SQL, "subtitle_sidecars") {
			t.Fatal(d.ID, d.SQL)
		}
	}
	f := &basicMediaFamily{valid: true, family: 8}
	f.source.changeID = 51
	in := basicSubtitleCleanupEffects{basicStreamCopyFence: basicStreamCopyFence{family: f, position: basicCopyPosition{changeID: 51, changeRevision: 1, phase: 2, afterEntity: 31, family: 8, copyPhase: 1}, shapeID: 1, fileSizeRevision: 1, invalidation: 1}, before: []basicSubtitle{{origin: "embedded"}}}
	if err := preflightBasicSubtitleCleanup(in); err != nil {
		t.Fatal(err)
	}
	// Empty TEXT is a real first key, distinct from an initial cursor.
	in.position.aux = 1
	if err := preflightBasicSubtitleCleanup(in); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	in.before[0].id = "a"
	if err := preflightBasicSubtitleCleanup(in); err != nil {
		t.Fatal(err)
	}
	in.before = make([]basicSubtitle, 9)
	if err := preflightBasicSubtitleCleanup(in); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	embedded := basicSubtitleCleanupTuple("asset", basicSubtitle{origin: "embedded"})
	sidecar := basicSubtitleCleanupTuple("asset", basicSubtitle{origin: "sidecar"})
	if embedded[5] != nil || sidecar[5] != "" {
		t.Fatal(embedded, sidecar)
	}
}

// Native tests are intentionally left for the parent's hosted test gate.
func basicSubtitleCleanupFixture(t *testing.T) (*executor, *commandTurn, basicSubtitleCleanupEffects) {
	t.Helper()
	defs := append(basicCopySubtitlesTestDefinitions(), basicCopySubtitleCleanupDefinitions()...)
	defs = append(defs, basicSubtitleLanguageDefinitions()...)
	w, turn, _, b := basicCopySubtitlesFixture(t, false, defs)
	// Fixture-only setup: place the already protected accepted job at family8
	// cleanup. Production cursor transitions remain exclusively parent-owned.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_changes SET copy_family=8,copy_phase=1 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	b.position.family, b.position.copyPhase = 8, 1
	in := basicSubtitleCleanupEffects{basicStreamCopyFence: b.basicStreamCopyFence}
	for n := 0; n < 9; n++ {
		id := fmt.Sprintf("a%d", n)
		if n == 0 {
			id = ""
		}
		r := basicSubtitle{id: id, origin: "embedded", locator: fmt.Sprintf("old-%d", n), streamIndex: int64(n), format: "srt", language: "en", title: "captured"}
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO asset_subtitles VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, basicSubtitleCleanupTuple("basic-asset", r)...); err != nil {
			t.Fatal(err)
		}
		if n < 8 {
			in.before = append(in.before, r)
		}
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO asset_subtitle_languages VALUES('en',10)`); err != nil {
		t.Fatal(err)
	}
	return w, turn, in
}

func TestBasicSubtitleCleanupNativeBoundedAndLanguage(t *testing.T) {
	_, turn, in := basicSubtitleCleanupFixture(t)
	got, err := cleanupBasicSubtitles(t.Context(), turn, in)
	if err != nil || got != (basicSubtitleCleanupResult{lastID: "a7", hasAfter: true, more: true, processed: 8, deleted: 8}) {
		t.Fatal(got, err)
	}
	var intact bool
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=2 FROM asset_subtitles) AND
 (SELECT tracks=2 FROM asset_subtitle_languages WHERE language='en') AND
 (SELECT count(*)=1 FROM subtitle_sidecars WHERE id='unrelated') AND
 (SELECT copy_aux=0 AND copy_key='' FROM catalog_info_changes WHERE id=51)`).Scan(&intact); err != nil || !intact {
		t.Fatal(intact, err)
	}
	basicMediaCommit(t, turn)
}

func TestBasicSubtitleCleanupNativeGapAndBeforeMismatch(t *testing.T) {
	for _, gap := range []bool{true, false} {
		t.Run(fmt.Sprint(gap), func(t *testing.T) {
			_, turn, in := basicSubtitleCleanupFixture(t)
			if gap {
				in.before = in.before[1:]
			} else {
				in.before[0].title = "stale"
			}
			if _, err := cleanupBasicSubtitles(t.Context(), turn, in); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}

func TestBasicSubtitleCleanupNativeRetainedConsumesBudget(t *testing.T) {
	_, turn, in := basicSubtitleCleanupFixture(t)
	// Membership alone matters here; the parent owns installing staged payloads.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_new_subtitles SET id='' WHERE change_id=51 AND input_ordinal=0`); err != nil {
		t.Fatal(err)
	}
	got, err := cleanupBasicSubtitles(t.Context(), turn, in)
	if err != nil || got.processed != 8 || got.deleted != 7 || got.lastID != "a7" || !got.more {
		t.Fatal(got, err)
	}
	var tracks int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT tracks FROM asset_subtitle_languages WHERE language='en'`).Scan(&tracks); err != nil || tracks != 3 {
		t.Fatal(tracks, err)
	}
	basicMediaCommit(t, turn)
}

func TestBasicSubtitleCleanupNativeUnknownPayloadIsNotRead(t *testing.T) {
	_, turn, in := basicSubtitleCleanupFixture(t)
	// An unprovided first key is detected by a constant-only gap probe.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE asset_subtitles SET title=? WHERE id=''`, strings.Repeat("x", maxCanonicalRequestBytes+1)); err != nil {
		t.Fatal(err)
	}
	in.before = in.before[1:]
	if _, err := cleanupBasicSubtitles(t.Context(), turn, in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
