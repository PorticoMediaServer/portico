package writer

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func iaConfigureSourceFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	requireC019Fixture(t)
	registry, err := stmttest.New(t, iaConfigureSourceDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaConfigureSourceRoot(t *testing.T, tail string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\` + strings.ReplaceAll(tail, "/", `\`)
	}
	return "/" + tail
}

// One live library plus its retained namespace, exactly as IA-06 leaves them.
func iaConfigureSourceLibrary(t *testing.T, w *executor, libraryID int64, libraryKey, root string) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES(?,'Library','movie',?,1,1,1)`, libraryKey, root)
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,?)`, libraryID, libraryKey, root)
}

// One live source row with the exact witnesses a caller would have observed.
func iaConfigureSourceLive(t *testing.T, w *executor, libraryKey, id string, generation int64, incarnation, root, identity string, enabled bool, health string, confirmed bool) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES(?,?,?,?,?,'local',?,0,0,'',?,?,?,?,?,'local')`,
		id, libraryKey, "Root "+id, root, root, identity, map[bool]int{false: 0, true: 1}[confirmed], incarnation, generation, health, map[bool]int{false: 0, true: 1}[enabled])
}

func iaConfigureSourceRequestFixture(t *testing.T, libraryID int64, libraryKey, sourceID string) iaConfigureSourceRequest {
	t.Helper()
	root := iaConfigureSourceRoot(t, "media")
	return iaConfigureSourceRequest{
		reference: iaConfigureLibraryReference(2, 1, 3), libraryID: libraryID, libraryKey: libraryKey, sourceID: sourceID,
		expectIncarnation: "incarnation-1", expectGeneration: 1, expectRoot: root, expectRootIdentity: "identity-1",
		incarnation: "incarnation-1", name: "Root", configuredRoot: root, resolvedRoot: root, rootIdentity: "identity-1",
		kind: "local", classification: "local", enabled: true,
	}
}

func iaConfigureSourceTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaConfigureSourceRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	purpose := IAConfigureSource
	if iaConfigureSourceDenial(q) {
		purpose = IAConfigureSourceDisable
	}
	return iaEntryTurn(t, w, r, iaEntryRequest{
		purpose: purpose, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
	})
}

func iaConfigureSourceCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// The whole configuration family: no retained row survived a refusal or a
// rollback, and the live source row is untouched.
func iaConfigureSourceNoWrites(t *testing.T, w *executor, sourceRow string, wantEnabled int64, wantGeneration int64) {
	t.Helper()
	for _, table := range []string{"catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources", "api_events"} {
		if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM inventory_strict_intents`); got != 0 {
		t.Fatal("refused request queued a rescan", got)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id=? AND enabled=? AND generation=? AND health=? AND root=?`,
		sourceRow, wantEnabled, wantGeneration, map[int64]string{0: "disabled", 1: "ready"}[wantEnabled], iaConfigureSourceRoot(t, "media")); got != 1 {
		t.Fatal("refused request changed the live source", got)
	}
}

func TestIAConfigureSourceDisableReenableAndReplay(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 777, "lib-films", root)
	iaConfigureSourceLive(t, w, "lib-films", "source-1", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 777, "lib-films", "source-1")
	// A disable revokes current access, so it must use the FULL 107 descriptor.
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 1 || out.digest == (ResultDigest{}) {
		t.Fatal("fresh disable", out, err)
	}
	if s := out.result.facts.sources[0]; s.enabled || s.health != "disabled" || s.generation != 2 || s.incarnation != "incarnation-1" ||
		s.id != "source-1" || s.resolvedRoot != root || !s.identityConfirmed {
		t.Fatal("disable result fabricated availability", s)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM library_sources WHERE id='source-1' AND enabled=0 AND health='disabled' AND generation=2 AND incarnation='incarnation-1' AND root_identity='identity-1'`, 1},
		{`SELECT count(*) FROM inventory_strict_intents`, 0},
		// A disable is reversible: the retained namespace and the live row stay.
		{`SELECT count(*) FROM catalog_libraries WHERE id=777 AND library_id='lib-films' AND retired=0`, 1},
		{`SELECT count(*) FROM catalog_configuration_operations WHERE command_kind='configure_source' AND original_source_id='source-1' AND last_command_sequence=1`, 1},
		{`SELECT count(*) FROM catalog_configuration_receipts WHERE command_kind='configure_source' AND result_count=1 AND result_library_name IS NULL`, 1},
		{`SELECT count(*) FROM catalog_configuration_sources WHERE command_kind='configure_source' AND ordinal=0 AND source_id='source-1' AND enabled=0`, 1},
		{`SELECT count(*) FROM api_events WHERE type='source.changed' AND resource_kind='source' AND resource_id='source-1' AND revision='2'`, 1},
	} {
		if got := iaConfigureSourceCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// A later live mutation never edits the saved cohort.
	wc008NativeExec(t, w, `UPDATE library_sources SET name='Changed',root='/moved',configured_root='/moved',health='offline' WHERE id='source-1'`)
	turn, budget = iaConfigureSourceTurn(t, w, r, q)
	replay, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest {
		t.Fatal("saved replay", replay, err)
	}
	if s := replay.result.facts.sources[0]; s.name != "Root" || s.resolvedRoot != root || s.enabled || s.health != "disabled" {
		t.Fatal("live change edited the saved cohort", s)
	}
	turn.transaction.rollback(nil)
	// Re-enable at the new generation: NORMAL, ready for the queued rescan.
	wc008NativeExec(t, w, `UPDATE library_sources SET name='Root',root=?,configured_root=?,health='disabled' WHERE id='source-1'`, root, root)
	q2 := q
	q2.reference = iaConfigureLibraryReference(4, 5, 6)
	q2.expectGeneration = 2
	q2.enabled = true
	turn, budget = iaConfigureSourceTurn(t, w, r, q2)
	out2, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q2)
	if err != nil || out2.decision != receiptFresh {
		t.Fatal("re-enable", out2, err)
	}
	if s := out2.result.facts.sources[0]; !s.enabled || s.health != "ready" || !s.identityConfirmed || s.generation != 3 {
		t.Fatal("re-enable availability", s)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM inventory_strict_intents WHERE source_id='source-1' AND relative_path='.' AND source_generation=3 AND root_incarnation='incarnation-1' AND mode='incremental'`); got != 1 {
		t.Fatal("re-enable did not queue exactly one whole-root rescan", got)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-1' AND enabled=1 AND health='ready' AND generation=3`); got != 1 {
		t.Fatal("re-enable live row", got)
	}
}

func TestIAConfigureSourceRootMoveIsHonest(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	root, moved := iaConfigureSourceRoot(t, "media"), iaConfigureSourceRoot(t, "media2")
	iaConfigureSourceLibrary(t, w, 780, "lib-move", root)
	iaConfigureSourceLive(t, w, "lib-move", "source-move", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	wc008NativeExec(t, w, `UPDATE library_sources SET last_complete_at='2026-11-01T00:00:00Z' WHERE id='source-move'`)
	q := iaConfigureSourceRequestFixture(t, 780, "lib-move", "source-move")
	q.reference = iaConfigureLibraryReference(7, 8, 9)
	q.resolvedRoot, q.configuredRoot = moved, moved
	q.incarnation, q.rootIdentity = "incarnation-2", "identity-2"
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("root move", out, err)
	}
	if s := out.result.facts.sources[0]; s.enabled != true || s.health != "unknown" || s.identityConfirmed || s.generation != 2 ||
		s.incarnation != "incarnation-2" || s.resolvedRoot != moved || s.lastCompleteAt != "" || s.lastProgressAt != "" {
		t.Fatal("root move fabricated availability", s)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-move' AND root=? AND incarnation='incarnation-2' AND generation=2 AND health='unknown' AND identity_confirmed=0 AND last_complete_at=''`, moved); got != 1 {
		t.Fatal("root move live row", got)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM inventory_strict_intents WHERE source_id='source-move' AND source_generation=2 AND root_incarnation='incarnation-2' AND root=? AND root_identity='identity-2'`, moved); got != 1 {
		t.Fatal("root move rescan intent", got)
	}
	// A same-root change that invents a new identity, or a root move that reuses
	// the old identity, is a stale IO witness.
	for name, mutate := range map[string]func(*iaConfigureSourceRequest){
		"same_root_new_identity": func(q *iaConfigureSourceRequest) { q.rootIdentity = "identity-9" },
		"moved_root_same_identity": func(q *iaConfigureSourceRequest) {
			q.resolvedRoot, q.configuredRoot = iaConfigureSourceRoot(t, "media3"), iaConfigureSourceRoot(t, "media3")
			q.incarnation = "incarnation-3"
			q.rootIdentity = "identity-2"
		},
		"moved_root_same_incarnation": func(q *iaConfigureSourceRequest) {
			q.resolvedRoot, q.configuredRoot = iaConfigureSourceRoot(t, "media3"), iaConfigureSourceRoot(t, "media3")
			q.rootIdentity = "identity-3"
		},
	} {
		t.Run(name, func(t *testing.T) {
			fresh := q
			fresh.reference = iaConfigureLibraryReference(10, 11, 12)
			fresh.expectGeneration, fresh.expectIncarnation, fresh.expectRoot, fresh.expectRootIdentity = 2, "incarnation-2", moved, "identity-2"
			mutate(&fresh)
			turn, budget := iaConfigureSourceTurn(t, w, r, fresh)
			out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, fresh)
			if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("stale witness admitted", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM catalog_configuration_operations`); got != 1 {
				t.Fatal("stale witness wrote history", got)
			}
		})
	}
}

func TestIAConfigureSourceStaleWitnesses(t *testing.T) {
	for name, mutate := range map[string]func(*iaConfigureSourceRequest){
		"generation": func(q *iaConfigureSourceRequest) { q.expectGeneration = 2 },
		"incarnation": func(q *iaConfigureSourceRequest) {
			q.expectIncarnation, q.incarnation = "incarnation-stale", "incarnation-stale"
		},
		"root": func(q *iaConfigureSourceRequest) {
			q.expectRoot = iaConfigureSourceRoot(t, "other")
			q.incarnation, q.rootIdentity = "incarnation-2", "identity-2"
		},
		"root_identity": func(q *iaConfigureSourceRequest) {
			q.expectRootIdentity, q.rootIdentity = "identity-stale", "identity-stale"
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureSourceFixture(t)
			root := iaConfigureSourceRoot(t, "media")
			iaConfigureSourceLibrary(t, w, 781, "lib-stale", root)
			iaConfigureSourceLive(t, w, "lib-stale", "source-stale", 1, "incarnation-1", root, "identity-1", true, "ready", true)
			q := iaConfigureSourceRequestFixture(t, 781, "lib-stale", "source-stale")
			mutate(&q)
			turn, budget := iaConfigureSourceTurn(t, w, r, q)
			out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
				t.Fatal("stale witness", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureSourceNoWrites(t, w, "source-stale", 1, 1)
		})
	}
}

func TestIAConfigureSourceRootUniquenessIsPerLibrary(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	rootA, rootB, rootOther := iaConfigureSourceRoot(t, "a"), iaConfigureSourceRoot(t, "b"), iaConfigureSourceRoot(t, "other")
	iaConfigureSourceLibrary(t, w, 782, "lib-one", rootA)
	iaConfigureSourceLibrary(t, w, 783, "lib-two", rootOther)
	iaConfigureSourceLive(t, w, "lib-one", "source-a", 1, "incarnation-a", rootA, "identity-a", true, "ready", true)
	iaConfigureSourceLive(t, w, "lib-one", "source-b", 1, "incarnation-b", rootB, "identity-b", true, "ready", true)
	iaConfigureSourceLive(t, w, "lib-two", "source-c", 1, "incarnation-c", rootOther, "identity-c", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 782, "lib-one", "source-b")
	q.expectIncarnation, q.expectRoot, q.expectRootIdentity = "incarnation-b", rootB, "identity-b"
	q.resolvedRoot, q.configuredRoot = rootA, rootA
	q.incarnation, q.rootIdentity = "incarnation-moved", "identity-moved"
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("library-root UNIQUE admitted", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM catalog_configuration_operations`); got != 0 {
		t.Fatal("duplicate root wrote history", got)
	}
	// The same root in another library is legal.
	q = iaConfigureSourceRequestFixture(t, 783, "lib-two", "source-c")
	q.expectIncarnation, q.expectRoot, q.expectRootIdentity = "incarnation-c", rootOther, "identity-c"
	q.incarnation, q.resolvedRoot, q.configuredRoot, q.rootIdentity = "incarnation-moved", rootA, rootA, "identity-moved"
	turn, budget = iaConfigureSourceTurn(t, w, r, q)
	if out, err = configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal("other-library root", out, err)
	}
	turn.transaction.rollback(nil)
}

func TestIAConfigureSourceDurabilityComesFromDescriptor(t *testing.T) {
	if d, err := Describe(IAConfigureSource); err != nil || d.Durability != Normal {
		t.Fatal("101 must be NORMAL", d, err)
	}
	if d, err := Describe(IAConfigureSourceDisable); err != nil || d.Durability != Full {
		t.Fatal("107 must be FULL", d, err)
	}
	if kind, err := iaEntryCommandKind(IAConfigureSourceDisable); err != nil || kind != "configure_source" {
		t.Fatal("107 must persist the configure_source result purpose", kind, err)
	}
	if purpose, ok := iaEntryResultPurpose(IAConfigureSourceDisable); !ok || purpose != IAConfigureSource {
		t.Fatal("107 result purpose", purpose, ok)
	}
	for name, tc := range map[string]struct {
		enabled bool
		purpose CommandID
	}{
		"disable_on_normal": {false, IAConfigureSource},
		"change_on_full":    {true, IAConfigureSourceDisable},
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureSourceFixture(t)
			root := iaConfigureSourceRoot(t, "media")
			iaConfigureSourceLibrary(t, w, 784, "lib-descriptor", root)
			iaConfigureSourceLive(t, w, "lib-descriptor", "source-d", 1, "incarnation-1", root, "identity-1", true, "ready", true)
			q := iaConfigureSourceRequestFixture(t, 784, "lib-descriptor", "source-d")
			q.enabled = tc.enabled
			turn, budget := iaEntryTurn(t, w, r, iaEntryRequest{
				purpose: tc.purpose, action: iaEntryApply, reference: q.reference,
				libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
			})
			out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, ErrInvalid) || out.decision != 0 {
				t.Fatal("caller-selected durability", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureSourceNoWrites(t, w, "source-d", 1, 1)
		})
	}
	// The accepted denial runs with the FULL synchronous witness the descriptor
	// requires; no caller setting can downgrade it.
	w, r, p := iaConfigureSourceFixture(t)
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 785, "lib-full", root)
	iaConfigureSourceLive(t, w, "lib-full", "source-f", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 785, "lib-full", "source-f")
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	if got := turn.transaction.settings.Synchronous; got != int(sqliteconn.Full) {
		t.Fatal("FULL denial did not open a FULL transaction", got)
	}
	if out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal("FULL denial", out, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func TestIAConfigureSourceRejectsBoundedShape(t *testing.T) {
	for name, mutate := range map[string]func(*iaConfigureSourceRequest){
		"empty_name":   func(q *iaConfigureSourceRequest) { q.name = "" },
		"padded_name":  func(q *iaConfigureSourceRequest) { q.name = " Name" },
		"long_name":    func(q *iaConfigureSourceRequest) { q.name = strings.Repeat("n", 101) },
		"control_name": func(q *iaConfigureSourceRequest) { q.name = "bad\nname" },
		"unknown_kind": func(q *iaConfigureSourceRequest) { q.kind = "tuner" },
		"remote_follow": func(q *iaConfigureSourceRequest) {
			q.kind, q.classification, q.followSymlinks = "remote", "network", true
		},
		"short_interval":   func(q *iaConfigureSourceRequest) { q.intervalSeconds = 60 },
		"relative_root":    func(q *iaConfigureSourceRequest) { q.configuredRoot, q.resolvedRoot = "media", "media" },
		"unclean_root":     func(q *iaConfigureSourceRequest) { q.resolvedRoot = iaConfigureSourceRoot(t, "a/../b") },
		"empty_identity":   func(q *iaConfigureSourceRequest) { q.rootIdentity = "" },
		"empty_source":     func(q *iaConfigureSourceRequest) { q.sourceID = "" },
		"empty_library":    func(q *iaConfigureSourceRequest) { q.libraryKey = "" },
		"zero_generation":  func(q *iaConfigureSourceRequest) { q.expectGeneration = 0 },
		"max_generation":   func(q *iaConfigureSourceRequest) { q.expectGeneration = math.MaxInt64 },
		"zero_operation":   func(q *iaConfigureSourceRequest) { q.reference.Identity.OperationID = [16]byte{} },
		"zero_incarnation": func(q *iaConfigureSourceRequest) { q.incarnation = "" },
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureSourceFixture(t)
			root := iaConfigureSourceRoot(t, "media")
			iaConfigureSourceLibrary(t, w, 786, "lib-shape", root)
			iaConfigureSourceLive(t, w, "lib-shape", "source-s", 1, "incarnation-1", root, "identity-1", true, "ready", true)
			q := iaConfigureSourceRequestFixture(t, 786, "lib-shape", "source-s")
			mutate(&q)
			turn, budget := iaConfigureSourceTurn(t, w, r, q)
			if out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); !errors.Is(err, ErrInvalid) || out.decision != 0 {
				t.Fatal("bounded shape", out, err)
			}
			turn.transaction.rollback(nil)
			iaConfigureSourceNoWrites(t, w, "source-s", 1, 1)
		})
	}
}

func TestIAConfigureSourceReplaySurvivesDetachedLiveState(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 788, "lib-detached", root)
	iaConfigureSourceLive(t, w, "lib-detached", "source-detached", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 788, "lib-detached", "source-detached")
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// The retained original is independent of the live row: removing it (as a
	// later retirement would) cannot make the saved result undecodable.
	wc008NativeExec(t, w, `DELETE FROM library_sources WHERE id='source-detached'`)
	turn, budget = iaConfigureSourceTurn(t, w, r, q)
	replay, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.sources[0].id != "source-detached" {
		t.Fatal("replay after live deletion", replay, err)
	}
	turn.transaction.rollback(nil)
	// A cohort that no longer matches its saved digest is never served. The
	// flip is byte-neutral, so only the digest guard can refuse it.
	wc008NativeExec(t, w, `UPDATE catalog_configuration_sources SET enabled=1 WHERE source_id='source-detached'`)
	turn, budget = iaConfigureSourceTurn(t, w, r, q)
	replay, err = configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.sources) != 0 {
		t.Fatal("tampered cohort served", replay, err)
	}
	turn.transaction.rollback(nil)
}

// A disable that also moves the root must forget the old root's confirmation:
// the new root has never been confirmed by the scanner, so the disable cannot
// carry the old root's evidence forward.
func TestIAConfigureSourceDisableWithRootMoveForgetsConfirmation(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	root, moved := iaConfigureSourceRoot(t, "media"), iaConfigureSourceRoot(t, "media-disabled")
	iaConfigureSourceLibrary(t, w, 795, "lib-disable-move", root)
	iaConfigureSourceLive(t, w, "lib-disable-move", "source-disable-move", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 795, "lib-disable-move", "source-disable-move")
	q.reference = iaConfigureLibraryReference(20, 21, 22)
	q.enabled = false
	q.resolvedRoot, q.configuredRoot = moved, moved
	q.incarnation, q.rootIdentity = "incarnation-2", "identity-2"
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("disable with root move", out, err)
	}
	if s := out.result.facts.sources[0]; s.enabled || s.health != "unknown" || s.identityConfirmed ||
		s.generation != 2 || s.incarnation != "incarnation-2" || s.resolvedRoot != moved {
		t.Fatal("disable with root move kept the old confirmation", s)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-disable-move' AND enabled=0 AND identity_confirmed=0 AND generation=2 AND root=?`, moved); got != 1 {
		t.Fatal("disable with root move live row", got)
	}
}

// A same-root configure carries the scanner's live health and identity
// confirmation forward; it never fabricates ready+confirmed. Only a root move
// resets them to the not-confirmed state. Without carrying the live values every
// case would publish ready/confirmed and relight playback or scan gating.
func TestIAConfigureSourcePreservesLiveHealthAndIdentity(t *testing.T) {
	root, moved := iaConfigureSourceRoot(t, "media"), iaConfigureSourceRoot(t, "media-moved")
	for name, tc := range map[string]struct {
		liveEnabled, liveConfirmed bool
		health                     string
		move                       bool
		wantHealth                 string
		wantConfirmed              bool
	}{
		"rename_root_changed":           {true, true, "root_changed", false, "root_changed", true},
		"interval_degraded_unconfirmed": {true, false, "degraded", false, "degraded", false},
		"reenable_never_confirmed":      {false, false, "disabled", false, "unknown", false},
		"reenable_root_changed":         {false, true, "root_changed", false, "root_changed", true},
		"moved_root_resets":             {true, true, "offline", true, "unknown", false},
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := iaConfigureSourceFixture(t)
			iaConfigureSourceLibrary(t, w, 790, "lib-live", root)
			iaConfigureSourceLive(t, w, "lib-live", "source-live", 1, "incarnation-1", root, "identity-1",
				tc.liveEnabled, tc.health, tc.liveConfirmed)
			q := iaConfigureSourceRequestFixture(t, 790, "lib-live", "source-live")
			q.name = "Renamed"
			if tc.move {
				q.resolvedRoot, q.configuredRoot = moved, moved
				q.incarnation, q.rootIdentity = "incarnation-2", "identity-2"
			}
			turn, budget := iaConfigureSourceTurn(t, w, r, q)
			out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q)
			if err != nil || out.decision != receiptFresh || len(out.result.facts.sources) != 1 {
				t.Fatal("fresh configure", out, err)
			}
			if s := out.result.facts.sources[0]; !s.enabled || s.health != tc.wantHealth || s.identityConfirmed != tc.wantConfirmed {
				t.Fatal("live scanner state fabricated", s)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-live' AND enabled=1 AND name='Renamed' AND generation=2 AND health=? AND identity_confirmed=?`,
				tc.wantHealth, map[bool]int{false: 0, true: 1}[tc.wantConfirmed]); got != 1 {
				t.Fatal("live row lost scanner state", tc.wantHealth, tc.wantConfirmed)
			}
		})
	}
}

func TestIAConfigureSourceRollbackLeavesNoEffect(t *testing.T) {
	w, r, p := iaConfigureSourceFixture(t)
	root := iaConfigureSourceRoot(t, "media")
	iaConfigureSourceLibrary(t, w, 787, "lib-rollback", root)
	iaConfigureSourceLive(t, w, "lib-rollback", "source-r", 1, "incarnation-1", root, "identity-1", true, "ready", true)
	q := iaConfigureSourceRequestFixture(t, 787, "lib-rollback", "source-r")
	q.enabled = false
	turn, budget := iaConfigureSourceTurn(t, w, r, q)
	if out, err := configureIASource(t.Context(), turn, p, t.Context(), budget, q); err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	for _, table := range []string{"catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources", "api_events", "inventory_strict_intents"} {
		if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("rollback retained", table, got)
		}
	}
	if got := iaConfigureSourceCount(t, w, `SELECT count(*) FROM library_sources WHERE id='source-r' AND enabled=1 AND health='ready' AND generation=1`); got != 1 {
		t.Fatal("rollback kept a live change", got)
	}
}
