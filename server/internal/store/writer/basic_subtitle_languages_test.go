package writer

import (
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicSubtitleLanguageFixture(t *testing.T) (*executor, *commandTurn) {
	t.Helper()
	w, _, _ := testExecutor(t)
	for _, q := range []string{`INSERT INTO asset_subtitle_languages VALUES('en',1)`, `INSERT INTO asset_subtitle_languages VALUES('de',9223372036854775807)`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	defs := basicSubtitleLanguageDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_subtitle_languages.go", "basic_subtitle_language_statements.go", "basic_subtitle_languages_test.go")
	return w, basicHandlerTurn(t, w, BasicAccept, defs, registry)
}
func TestBasicSubtitleLanguageMovesAndEmptyBucket(t *testing.T) {
	w, turn := basicSubtitleLanguageFixture(t)
	for _, d := range []basicSubtitleLanguageDelta{{hasAfter: true}, {hadBefore: true, hasAfter: true, before: "en", after: "fr"}} {
		if err := applyBasicSubtitleLanguageDelta(t.Context(), turn, d); err != nil {
			t.Fatal(err)
		}
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=0 FROM asset_subtitle_languages WHERE language='en') AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language='fr') AND (SELECT tracks=1 FROM asset_subtitle_languages WHERE language='') AND (SELECT tracks=9223372036854775807 FROM asset_subtitle_languages WHERE language='de')`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}
func TestBasicSubtitleLanguageLateOverflowRollsBackOldDelta(t *testing.T) {
	w, turn := basicSubtitleLanguageFixture(t)
	err := applyBasicSubtitleLanguageDelta(t.Context(), turn, basicSubtitleLanguageDelta{true, true, "en", "de"})
	if !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	var count int
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitle_languages WHERE language='en'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("late boundary not reached", count, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var old int64
	if e := w.db.QueryRowContext(t.Context(), `SELECT tracks FROM asset_subtitle_languages WHERE language='en'`).Scan(&old); e != nil || old != 1 {
		t.Fatal(old, e)
	}
}
func TestBasicSubtitleLanguageNoopPreservesExhaustedCount(t *testing.T) {
	w, turn := basicSubtitleLanguageFixture(t)
	if err := applyBasicSubtitleLanguageDelta(t.Context(), turn, basicSubtitleLanguageDelta{true, true, "de", "de"}); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	var count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT tracks FROM asset_subtitle_languages WHERE language='de'`).Scan(&count); err != nil || count != math.MaxInt64 {
		t.Fatal(count, err)
	}
}
func TestBasicSubtitleLanguageDefinitionsAreClosed(t *testing.T) {
	for _, d := range basicSubtitleLanguageDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
}
