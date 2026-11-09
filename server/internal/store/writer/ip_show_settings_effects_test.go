package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func ipShowSettingsFixture(t *testing.T) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, r, p := ipTextLockFixture(t)
	registry, err := stmttest.New(t, append(ipEntryDefinitions(), ipShowSettingsDefinitions()...))
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{registry, r.keeper}, p
}

func ipShowSettingsTurn(t *testing.T, w *executor, r *ipEntryRegistry) (*commandTurn, *transaction) {
	t.Helper()
	turn, _ := ipEntryTurn(t, w, r, ipEntryRequest{purpose: IPFieldRestore})
	return turn, turn.transaction
}

func TestIPShowSettingsPresenceSaveRestore(t *testing.T) {
	w, r, _ := ipShowSettingsFixture(t)
	turn, tx := ipShowSettingsTurn(t, w, r)
	out, err := applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 1})
	if err != nil || out != (ipShowSettingsEffect{EntityID: 780, Revision: 2, Present: true}) {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT show_settings_present FROM catalog_entities WHERE id=780`:  1,
		`SELECT show_settings_revision FROM catalog_entities WHERE id=780`: 2,
		`SELECT revision FROM catalog_entities WHERE id=780`:               1,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// An all-false save is still an explicit presence change.
	turn, tx = ipShowSettingsTurn(t, w, r)
	out, err = applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 2})
	if err != nil || out.Revision != 3 || !out.Present {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)

	turn, tx = ipShowSettingsTurn(t, w, r)
	out, err = applyIPShowSettingsRestore(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 3})
	if err != nil || out != (ipShowSettingsEffect{EntityID: 780, Revision: 4}) {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT show_settings_present FROM catalog_entities WHERE id=780`:  0,
		`SELECT show_settings_revision FROM catalog_entities WHERE id=780`: 4,
	} {
		if value := ipTextQueryInt(t, w, query); value != want {
			t.Fatal(query, value, want)
		}
	}

	// Stale revisions and restoring an already-inheriting show are conflicts;
	// invalid inputs never reach SQL.
	turn, tx = ipShowSettingsTurn(t, w, r)
	if out, err = applyIPShowSettingsRestore(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 3}); out != (ipShowSettingsEffect{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale restore admitted", out, err)
	}
	ipTextRollback(t, turn, tx)

	turn, tx = ipShowSettingsTurn(t, w, r)
	if out, err = applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 0}); out != (ipShowSettingsEffect{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("zero revision admitted", out, err)
	}
	ipTextRollback(t, turn, tx)

	turn, tx = ipShowSettingsTurn(t, w, r)
	if out, err = applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 9}); out != (ipShowSettingsEffect{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale save admitted", out, err)
	}
	ipTextRollback(t, turn, tx)

	// Only a show entity carries saved show settings.
	turn, tx = ipShowSettingsTurn(t, w, r)
	if out, err = applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 778, ExpectedRevision: 1}); out != (ipShowSettingsEffect{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("item accepted show settings", out, err)
	}
	ipTextRollback(t, turn, tx)

	ipTextExec(t, w, `UPDATE catalog_entities SET retired=1 WHERE id=780`)
	turn, tx = ipShowSettingsTurn(t, w, r)
	if out, err = applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 4}); out != (ipShowSettingsEffect{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("retired show accepted settings", out, err)
	}
	ipTextRollback(t, turn, tx)
}

func TestIPShowSettingsNumberingInheritance(t *testing.T) {
	w, r, _ := ipShowSettingsFixture(t)
	read := func() ipShowNumberingFacts {
		t.Helper()
		turn, tx := ipShowSettingsTurn(t, w, r)
		facts, err := ipShowSettingsNumberingRead(t.Context(), turn, 780)
		if err != nil {
			t.Fatal(err)
		}
		ipTextRollback(t, turn, tx)
		return facts
	}
	facts := read()
	if facts.Present || facts.Library != "absolute" || facts.Effective != ipEpisodeAbsolute {
		t.Fatal("absent show settings must inherit the library", facts)
	}

	// First all-false save overrides the library's absolute preference with
	// season and advances the revision even though no value is true.
	turn, tx := ipShowSettingsTurn(t, w, r)
	if _, err := applyIPShowSettingsSave(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn, tx)
	facts = read()
	if !facts.Present || facts.Effective != ipEpisodeSeason || facts.Absolute {
		t.Fatal("saved all-false settings must use season", facts)
	}
	ipTextExec(t, w, `UPDATE catalog_entities SET absolute_numbering=1,hide_seasons=1,newest_first=1,show_ranges='1-2' WHERE id=780`)
	if facts = read(); !facts.Present || !facts.Absolute || facts.Effective != ipEpisodeAbsolute {
		t.Fatal("saved absolute flag", facts)
	}

	// Restore inheritance clears presence and follows the library again.
	turn, tx = ipShowSettingsTurn(t, w, r)
	if _, err := applyIPShowSettingsRestore(t.Context(), turn, ipShowSettingsInput{EntityID: 780, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	ipTextCommit(t, turn, tx)
	if facts = read(); facts.Present || facts.Effective != ipEpisodeAbsolute {
		t.Fatal("restore inheritance", facts)
	}

	// A library preference change affects only inheriting shows at read time;
	// it never rewrites the show row.
	ipTextExec(t, w, `UPDATE libraries SET preference_episode_numbering='season',preference_revision=2 WHERE id='ip-library'`)
	if facts = read(); facts.Effective != ipEpisodeSeason || facts.Library != "season" {
		t.Fatal("library preference change", facts)
	}
	if value := ipTextQueryInt(t, w, `SELECT count(*) FROM catalog_entities WHERE id=780 AND show_settings_present=0 AND show_settings_revision=3 AND hide_seasons=0 AND absolute_numbering=0 AND newest_first=0 AND show_ranges=''`); value != 1 {
		t.Fatal("library change fanned out to the show row", value)
	}
	ipTextExec(t, w, `UPDATE libraries SET preference_present=0,preference_revision=NULL,preference_group_collections=NULL,preference_local_tags=NULL,preference_lyrics_source=NULL,preference_title_source=NULL,preference_episode_numbering=NULL,preference_find_extras=NULL,preference_sort_ignore_articles=NULL WHERE id='ip-library'`)
	if facts = read(); facts.Present || facts.Library != "" || facts.Effective != ipEpisodeSeason {
		t.Fatal("absent library preference defaults to season", facts)
	}

	for _, tc := range []struct {
		present, absolute int64
		library           string
	}{
		{2, 0, "season"},
		{0, 2, "season"},
		{0, 0, "week"},
	} {
		if _, err := ipResolveEpisodeNumbering(tc.present, tc.absolute, tc.library); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid numbering admitted", tc)
		}
	}
}

func TestIPSeriesSourcePrecedence(t *testing.T) {
	local := []ipSeriesSourcePair{{Series: "Tag A", Position: "1"}, {Series: "Tag B", Position: "2.50"}}
	cases := []struct {
		name  string
		owner ipSeriesSourceState
		want  []ipSeriesSourcePair
	}{
		{"scalar_exact_position", ipSeriesSourceState{Series: "Saga", Position: "2.5", Present: true},
			[]ipSeriesSourcePair{{Series: "Saga", Position: "2.5"}}},
		{"scalar_empty_position", ipSeriesSourceState{Series: "Saga", Present: true},
			[]ipSeriesSourcePair{{Series: "Saga", Position: ""}}},
		{"locked_empty_clear", ipSeriesSourceState{Present: true, Locked: true},
			[]ipSeriesSourcePair{}},
		{"absent_local_pairs", ipSeriesSourceState{}, local},
		{"standalone_position_ignored", ipSeriesSourceState{Position: "9", Present: false}, local},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ipResolveSeriesSource(tc.owner, local)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatal(got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatal(got, tc.want)
				}
			}
		})
	}
	if _, err := ipResolveSeriesSource(ipSeriesSourceState{Series: "bad\x00"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("control bytes admitted")
	}
	if _, err := ipResolveSeriesSource(ipSeriesSourceState{}, []ipSeriesSourcePair{{Series: strings.Repeat("s", ipShowSeriesTextMax+1)}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded series admitted")
	}
}

func TestIPShowSettingsNativePlans(t *testing.T) {
	w, _, _ := ipShowSettingsFixture(t)
	for _, d := range ipShowSettingsDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if d.Kind != stmt.Read {
					continue
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") {
					t.Fatal(detail)
				}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
