package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Synthetic ProgressAccept custody is component-only; it registers no 610/611/
// 612 command, authorizes no principal and issues no saved receipt or Applied.
func uoTestTurn(t *testing.T, w *executor, defs []stmt.Definition) *commandTurn {
	t.Helper()
	// The existing WC fixture's default cohort builder predates boolean params.
	for i := range defs {
		var args []any
		for _, p := range defs[i].Params {
			var a any
			switch p.Type {
			case stmt.Text:
				a = "unit"
			case stmt.Real:
				a = float64(1)
			case stmt.Boolean:
				a = true
			default:
				a = int64(1)
			}
			if p.Nullable {
				a = nil
			}
			args = append(args, a)
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "component_only", Bindings: args}}
	}
	return wc008NativeTurn(t, w, ProgressAccept, defs)
}
func uoTestAddress() UOPreferenceAddress {
	return UOPreferenceAddress{"local", "account", "profile", UOProfileServer, ""}
}
func uoTestPreference(a UOPreferenceAddress, revision int64, p ...uoPreferencePatch) uoPreferenceCommand {
	return uoPreferenceCommand{address: a, expectedRevision: revision, atMS: 1000, copyBudget: 65536, patch: p}
}
func uoTestCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, e := turn.transaction.commit(); e != nil {
		t.Fatal(e)
	}
}
func uoTestChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var n int64
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestUOPreferencesSparseCASNoopAndReorder(t *testing.T) {
	w := wc008NativeExecutor(t)
	a := uoTestAddress()
	run := func(q uoPreferenceCommand, wantWrites int64) uoPreferenceEffect {
		t.Helper()
		turn := uoTestTurn(t, w, uoPreferenceDefinitions())
		before := uoTestChanges(t, turn)
		out, e := applyUOPreferences(t.Context(), turn, q)
		if e != nil {
			t.Fatal(e)
		}
		if got := uoTestChanges(t, turn) - before; got != wantWrites {
			t.Fatalf("native writes %d want %d", got, wantWrites)
		}
		uoTestCommit(t, turn)
		return out
	}
	out := run(uoTestPreference(a, 1, uoPreferencePatch{key: UOPrefMusicGapless, remove: true}), 0)
	if out.changed || out.revision != 1 {
		t.Fatal(out)
	}
	// An explicit default override is meaningful even while the scope is absent.
	out = run(uoTestPreference(a, 1, uoPreferencePatch{key: UOPrefMusicGapless, value: UOValue{Kind: UOBool, Bool: true}}), 2)
	if !out.changed || out.revision != 2 {
		t.Fatal(out)
	}
	run(uoTestPreference(a, 2, uoPreferencePatch{key: UOPrefMusicGapless, value: UOValue{Kind: UOBool, Bool: true}}), 0)
	run(uoTestPreference(a, 2, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{"A", "B", "keep"}}}), 5)
	// UNIQUE(key,value) reorder needs deletes before inserts; untouched keep stays.
	run(uoTestPreference(a, 3, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{"B", "A", "keep"}}}), 5)
	run(uoTestPreference(a, 4, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{}}}), 4)
	run(uoTestPreference(a, 5, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{}}}), 0)
	run(uoTestPreference(a, 5, uoPreferencePatch{key: UOPrefHomeRowOrder, remove: true}), 2)
	turn := uoTestTurn(t, w, uoPreferenceDefinitions())
	before := uoTestChanges(t, turn)
	got, e := applyUOPreferences(t.Context(), turn, uoTestPreference(a, 2))
	if !errors.Is(e, ErrConflict) || !reflect.DeepEqual(got, uoPreferenceEffect{}) || uoTestChanges(t, turn) != before {
		t.Fatal("stale no-op escaped", got, e)
	}
}
func TestUOPreferencesScopeIsolationClampingAndRollback(t *testing.T) {
	w := wc008NativeExecutor(t)
	a := uoTestAddress()
	turn := uoTestTurn(t, w, uoPreferenceDefinitions())
	q := uoTestPreference(a, 1, uoPreferencePatch{key: UOPrefMusicCrossfadeMs, value: UOValue{Kind: UOInteger, Number: 99999}})
	out, e := applyUOPreferences(t.Context(), turn, q)
	if e != nil || len(out.clamped) != 1 || out.revision != 2 {
		t.Fatal(out, e)
	}
	uoTestCommit(t, turn)
	// Same profile string in a different account/authority never aliases a scope.
	for _, other := range []UOPreferenceAddress{{"local", "other", "profile", UOProfileServer, ""}, {"portico", "account", "profile", UOProfileServer, ""}, {"local", "account", "other", UOProfileServer, ""}} {
		turn = uoTestTurn(t, w, uoPreferenceDefinitions())
		q = uoTestPreference(other, 1, uoPreferencePatch{key: UOPrefMusicGapless, value: UOValue{Kind: UOBool}})
		if _, e = applyUOPreferences(t.Context(), turn, q); e != nil {
			t.Fatal(e)
		}
		uoTestCommit(t, turn)
	}
	turn = uoTestTurn(t, w, uoPreferenceDefinitions())
	out, e = applyUOPreferences(t.Context(), turn, uoTestPreference(a, 2, uoPreferencePatch{key: UOPrefMusicCrossfadeMs, remove: true}))
	if e != nil || !out.changed {
		t.Fatal(out, e)
	}
	// Parent failure after the body rolls back source and revision together.
	if _, e = turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
	var rev int64
	var number float64
	if e = w.db.QueryRowContext(t.Context(), `SELECT s.revision,v.number_value FROM preference_scopes s JOIN preference_values v USING(authority,account_id,profile_id,scope,device_key) WHERE s.authority='local' AND s.account_id='account' AND s.profile_id='profile'`).Scan(&rev, &number); e != nil || rev != 2 || number != 12000 {
		t.Fatal(rev, number, e)
	}
}
func TestUOPreferencesActualBytesAndFailureZero(t *testing.T) {
	w := wc008NativeExecutor(t)
	a := uoTestAddress()
	turn := uoTestTurn(t, w, uoPreferenceDefinitions())
	q := uoTestPreference(a, 1, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{"海"}}})
	if _, e := applyUOPreferences(t.Context(), turn, q); e != nil {
		t.Fatal(e)
	}
	uoTestCommit(t, turn)
	for _, budget := range []int64{13, 14} {
		turn = uoTestTurn(t, w, uoPreferenceDefinitions())
		q = uoTestPreference(a, 2, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{"海"}}})
		q.copyBudget = budget
		out, e := applyUOPreferences(t.Context(), turn, q)
		// "string-list" =11 bytes; 海 =3 native bytes, not one rune.
		if budget == 13 {
			if e == nil || !reflect.DeepEqual(out, uoPreferenceEffect{}) {
				t.Fatal(out, e)
			}
		} else if e != nil || out.copiedVariableBytes != 14 || out.changed {
			t.Fatal(out, e)
		}
		if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
	}
	turn = uoTestTurn(t, w, uoPreferenceDefinitions())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, e := applyUOPreferences(ctx, turn, uoTestPreference(a, 2))
	if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(out, uoPreferenceEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUOPreferencesRejectUnsupportedAndDuplicate(t *testing.T) {
	w := wc008NativeExecutor(t)
	for _, q := range []uoPreferenceCommand{
		uoTestPreference(UOPreferenceAddress{"local", "a", "p", "arbitrary", ""}, 1),
		uoTestPreference(uoTestAddress(), 1, uoPreferencePatch{key: "unregistered", remove: true}),
		uoTestPreference(uoTestAddress(), 1, uoPreferencePatch{key: UOPrefAppearanceMotion, value: UOValue{Kind: UOText, Text: "reduce"}}),
		uoTestPreference(uoTestAddress(), 1, uoPreferencePatch{key: UOPrefMusicGapless, remove: true}, uoPreferencePatch{key: UOPrefMusicGapless, remove: true}),
		uoTestPreference(uoTestAddress(), 1, uoPreferencePatch{key: UOPrefHomeRowOrder, value: UOValue{Kind: UOList, List: []string{"x", "x"}}}),
	} {
		turn := uoTestTurn(t, w, uoPreferenceDefinitions())
		before := uoTestChanges(t, turn)
		out, e := applyUOPreferences(t.Context(), turn, q)
		if e == nil || !reflect.DeepEqual(out, uoPreferenceEffect{}) || uoTestChanges(t, turn) != before {
			t.Fatal(out, e)
		}
		if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
	}
}
func TestUOPreferencesFiniteVocabulary(t *testing.T) {
	seen := map[UOPreferenceKey]bool{}
	for _, s := range uoPreferenceSpecs {
		if seen[s.key] {
			t.Fatal(s.key)
		}
		seen[s.key] = true
		v, ok := UOPreferenceDefault(s.key)
		if !ok {
			t.Fatal(s.key)
		}
		canonical, clamped, e := UOValidatePreference(s.key, s.scope, v)
		if e != nil || clamped || !uoValueEqual(v, canonical) {
			t.Fatal(s.key, v, e)
		}
	}
	if len(seen) < 80 {
		t.Fatal("registry unexpectedly narrowed", len(seen))
	}
}

func TestUOPreferencesComposeExistingWCEventOnlyOnChange(t *testing.T) {
	w := wc008NativeExecutor(t)
	a := uoTestAddress()
	defs := append(uoPreferenceDefinitions(), wcAPIEventDefinitions()...)
	q := uoTestPreference(a, 1, uoPreferencePatch{key: UOPrefMusicGapless, value: UOValue{Kind: UOBool}})
	for _, commit := range []bool{false, true} {
		turn := uoTestTurn(t, w, defs)
		effect, e := applyUOPreferences(t.Context(), turn, q)
		if e != nil {
			t.Fatal(e)
		}
		if effect.changed {
			if _, e = appendWCAPIEvent(t.Context(), turn, wcAPIEvent{audience: "profile:local:account:profile", eventType: "settings.changed", resourceKind: "settings", resourceID: "preferences", revision: "2", data: `{"scope":"profile-server"}`, atMS: 1000}); e != nil {
				t.Fatal(e)
			}
		}
		if commit {
			uoTestCommit(t, turn)
		} else if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
		var count int64
		if e = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&count); e != nil || commit && count != 1 || !commit && count != 0 {
			t.Fatal(count, e)
		}
	}
	q.expectedRevision = 2
	turn := uoTestTurn(t, w, defs)
	before := uoTestChanges(t, turn)
	effect, e := applyUOPreferences(t.Context(), turn, q)
	if e != nil || effect.changed || uoTestChanges(t, turn) != before {
		t.Fatal("no-op hint/revision emitted", effect, e)
	}
}
