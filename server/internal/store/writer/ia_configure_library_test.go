package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func iaConfigureLibraryFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	requireC019Fixture(t)
	// Frozen whole-second current clock, no real-clock assertion.
	now := w.now().UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	registry, err := stmttest.New(t, iaConfigureLibraryDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaConfigureLibraryReference(operation, incarnation, digest byte) ReceiptReference {
	return ReceiptReference{
		Identity:      CommandIdentity{Incarnation: [16]byte{incarnation}, Sequence: 1, OperationID: [16]byte{operation}},
		RequestDigest: RequestDigest{digest},
	}
}

func iaConfigureLibrarySourceFixture(ordinal int, root string) iaConfigureLibrarySource {
	return iaConfigureLibrarySource{
		id: fmt.Sprintf("source-%02d", ordinal), name: fmt.Sprintf("Root %02d", ordinal),
		incarnation: fmt.Sprintf("incarnation-%02d", ordinal), configuredRoot: root, resolvedRoot: root,
		rootIdentity: fmt.Sprintf("identity-%02d", ordinal), kind: "local", classification: "local",
	}
}

func iaConfigureLibraryTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaConfigureLibraryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{
		purpose: IAConfigureLibrary, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey,
	})
}

func iaConfigureLibraryCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func TestIAConfigureLibraryFreshReplayAndLiveCohort(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(2, 1, 3), libraryID: 777, libraryKey: "lib-films", name: "Films", kind: "movie",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/media/films"), iaConfigureLibrarySourceFixture(1, "/media/films/4k")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 2 || out.result.facts.libraryName != "Films" ||
		out.result.facts.acceptedRevision != 1 || out.result.facts.appliedMS != w.now().UnixMilli() || out.digest == (ResultDigest{}) {
		t.Fatal("fresh library result", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM libraries`, nil, 1},
		{`SELECT count(*) FROM catalog_libraries WHERE id=777 AND library_id='lib-films' AND root='/media/films' AND retired=0`, nil, 1},
		{`SELECT count(*) FROM library_sources WHERE library_id='lib-films' AND generation=1 AND identity_confirmed=1 AND enabled=1`, nil, 2},
		{`SELECT count(*) FROM library_scan_policies WHERE library_id='lib-films' AND revision=1 AND tier='basic'`, nil, 1},
		{`SELECT count(*) FROM inventory_strict_intents WHERE library_id='lib-films' AND relative_path='.' AND mode='incremental'`, nil, 2},
		{`SELECT count(*) FROM api_events WHERE type='library.changed' AND resource_kind='library' AND resource_id='lib-films' AND revision='1'`, nil, 1},
		{`SELECT count(*) FROM catalog_configuration_operations WHERE command_kind='configure_library' AND last_command_sequence=1`, nil, 1},
		{`SELECT count(*) FROM catalog_configuration_receipts WHERE command_kind='configure_library' AND result_count=2`, nil, 1},
		{`SELECT count(*) FROM catalog_configuration_sources WHERE command_kind='configure_library'`, nil, 2},
	} {
		if got := iaConfigureLibraryCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A later live rename, disable and root move never edits the saved cohort.
	wc008NativeExec(t, w, `UPDATE library_sources SET name='Changed',enabled=0,health='offline' WHERE id='source-00'`)
	wc008NativeExec(t, w, `UPDATE library_sources SET root='/moved',configured_root='/moved' WHERE id='source-01'`)
	turn, budget = iaConfigureLibraryTurn(t, w, r, q)
	replay, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest {
		t.Fatal("saved replay", replay, err)
	}
	if len(replay.result.facts.sources) != 2 || replay.result.facts.sources[0].name != "Root 00" || replay.result.facts.sources[1].resolvedRoot != "/media/films/4k" ||
		!replay.result.facts.sources[0].enabled || replay.result.facts.sources[0].health != "ready" {
		t.Fatal("live change edited the saved source cohort", replay.result.facts.sources)
	}
	turn.transaction.rollback(nil)
}

func TestIAConfigureLibraryRootBoundsAndOverlap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		want  error
	}{
		{"zero", 0, ErrInvalid},
		{"max", 64, nil},
		{"over", 65, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := iaConfigureLibraryFixture(t)
			q := iaConfigureLibraryRequest{
				reference: iaConfigureLibraryReference(4, 5, 6), libraryID: 900, libraryKey: "lib-bounds", name: "Bounds", kind: "tv",
			}
			for i := 0; i < tc.count; i++ {
				q.sources = append(q.sources, iaConfigureLibrarySourceFixture(i, fmt.Sprintf("/bounds/%02d", i)))
			}
			turn, budget := iaConfigureLibraryTurn(t, w, r, q)
			out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, tc.want) {
				t.Fatal("root bound", tc.count, out, err)
			}
			if tc.want != nil {
				turn.transaction.rollback(nil)
				// No partial namespace, source, intent, event or receipt may exist.
				for _, table := range []string{"libraries", "catalog_libraries", "library_sources", "inventory_strict_intents", "api_events", "catalog_configuration_operations"} {
					if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
						t.Fatal("refused request wrote", table, got)
					}
				}
				return
			}
			if out.decision != receiptFresh || len(out.result.facts.sources) != 64 {
				t.Fatal("64 roots", out.decision, len(out.result.facts.sources))
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM library_sources`); got != 64 {
				t.Fatal("live roots", got)
			}
		})
	}
	// Overlapping (nested) roots are legal; only an exact duplicate is refused.
	t.Run("overlap_allowed", func(t *testing.T) {
		w, r, p := iaConfigureLibraryFixture(t)
		q := iaConfigureLibraryRequest{
			reference: iaConfigureLibraryReference(7, 8, 9), libraryID: 901, libraryKey: "lib-overlap", name: "Overlap", kind: "movie",
			sources: []iaConfigureLibrarySource{
				iaConfigureLibrarySourceFixture(0, "/overlap"),
				iaConfigureLibrarySourceFixture(1, "/overlap/nested"),
				iaConfigureLibrarySourceFixture(2, "/overlap/nested/deeper"),
			},
		}
		turn, budget := iaConfigureLibraryTurn(t, w, r, q)
		out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
		if err != nil || out.decision != receiptFresh || len(out.result.facts.sources) != 3 {
			t.Fatal("overlapping roots", out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestIAConfigureLibraryImmutableNamespace(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(10, 11, 12), libraryID: 910, libraryKey: "lib-immutable", name: "Immutable", kind: "music",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/immutable")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]iaConfigureLibraryRequest{
		"changed_name": func() iaConfigureLibraryRequest {
			other := q
			other.reference = iaConfigureLibraryReference(13, 14, 15)
			other.name = "Renamed"
			return other
		}(),
		"changed_key": func() iaConfigureLibraryRequest {
			other := q
			other.reference = iaConfigureLibraryReference(16, 17, 18)
			other.libraryKey = "lib-other"
			return other
		}(),
		"other_id_same_key": func() iaConfigureLibraryRequest {
			other := q
			other.reference = iaConfigureLibraryReference(19, 20, 21)
			other.libraryID = 911
			// Fresh source identity keeps the source-point guard from masking the
			// key namespace check under test.
			other.sources = []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(9, "/immutable/other")}
			return other
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			turn, budget := iaConfigureLibraryTurn(t, w, r, mutate)
			out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, mutate)
			// ErrConflict is the namespace/live-row guard's own refusal, not a
			// downstream UNIQUE/PK failure that would pass a weaker assertion.
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("namespace mutation admitted", out, err)
			}
			turn.transaction.rollback(nil)
		})
	}
	// A retired retained namespace still replays its original (retained identity
	// is not live eligibility), but never accepts a fresh birth under a new
	// operation/incarnation.
	wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=910`)
	turn, budget = iaConfigureLibraryTurn(t, w, r, q)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptReplay {
		t.Fatal("retired original replay", out, err)
	}
	turn.transaction.rollback(nil)
	fresh := q
	fresh.reference = iaConfigureLibraryReference(25, 26, 27)
	turn, budget = iaConfigureLibraryTurn(t, w, r, fresh)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, fresh); !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("retired namespace accepted a fresh birth", out, err)
	}
}

// A live library row must agree with the requested name and kind even when no
// retained namespace row exists for it yet. Without that check the namespace
// insert would succeed and only the later live PK conflict would appear, with a
// different error and a partial effect set.
func TestIAConfigureLibraryLiveRowMustAgree(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('lib-live','Old','movie','/live',1,1,1)`)
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(62, 63, 64), libraryID: 954, libraryKey: "lib-live", name: "New", kind: "tv",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/live")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
		t.Fatal("live row disagreement", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM catalog_libraries`); got != 0 {
		t.Fatal("refused live row wrote namespace", got)
	}
}

func TestIAConfigureLibraryRejectsChangedOriginal(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(22, 23, 24), libraryID: 920, libraryKey: "lib-original", name: "Original", kind: "book",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/original")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	if _, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	changed := q
	changed.reference.RequestDigest[0]++
	turn, budget = iaConfigureLibraryTurn(t, w, r, changed)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, changed); !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed original admitted", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM catalog_configuration_receipts`); got != 1 {
		t.Fatal("conflict minted a receipt", got)
	}
}

func TestIAConfigureLibraryRejectsBoundedText(t *testing.T) {
	for name, mutate := range map[string]func(*iaConfigureLibraryRequest){
		"empty_name":   func(q *iaConfigureLibraryRequest) { q.name = "" },
		"padded_name":  func(q *iaConfigureLibraryRequest) { q.name = " Name" },
		"unknown_kind": func(q *iaConfigureLibraryRequest) { q.kind = "games" },
		"long_root": func(q *iaConfigureLibraryRequest) {
			q.sources[0].resolvedRoot = strings.Repeat("r", iaConfigureLibraryMaxRoot+1)
		},
		"control_root":   func(q *iaConfigureLibraryRequest) { q.sources[0].configuredRoot = "/bad\x00root" },
		"duplicate_id":   func(q *iaConfigureLibraryRequest) { q.sources = append(q.sources, q.sources[0]) },
		"short_interval": func(q *iaConfigureLibraryRequest) { q.sources[0].intervalSeconds = 60 },
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureLibraryFixture(t)
			q := iaConfigureLibraryRequest{
				reference: iaConfigureLibraryReference(30, 31, 32), libraryID: 930, libraryKey: "lib-text", name: "Text", kind: "movie",
				sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/text")},
			}
			mutate(&q)
			turn, budget := iaConfigureLibraryTurn(t, w, r, q)
			if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); !errors.Is(err, ErrInvalid) || out.decision != 0 {
				t.Fatal("bounded text", out, err)
			}
			turn.transaction.rollback(nil)
			for _, table := range []string{"libraries", "catalog_libraries", "library_sources", "catalog_configuration_operations"} {
				if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
					t.Fatal("refused text wrote", table, got)
				}
			}
		})
	}
}

func TestIAConfigureLibraryRollbackLeavesNoEffect(t *testing.T) {
	w, r, p := iaConfigureLibraryFixture(t)
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(40, 41, 42), libraryID: 940, libraryKey: "lib-rollback", name: "Rollback", kind: "movie",
		sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, "/rollback")},
	}
	turn, budget := iaConfigureLibraryTurn(t, w, r, q)
	if out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	for _, table := range []string{"libraries", "catalog_libraries", "library_sources", "library_scan_policies", "inventory_strict_intents", "api_events",
		"catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources"} {
		if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("rollback retained", table, got)
		}
	}
}
