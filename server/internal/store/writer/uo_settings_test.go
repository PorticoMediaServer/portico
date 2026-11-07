package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoSettingsSeed(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO server_settings(singleton,"remoteSecurity") VALUES(1,'enhanced')`)
	wc008NativeExec(t, w, `INSERT INTO console_settings_revision(singleton,revision) VALUES(1,1)`)
}
func uoTestSettings(revision int64, p ...uoSettingPatch) uoSettingsCommand {
	return uoSettingsCommand{expectedRevision: revision, atMS: 1000, copyBudget: 65536, patch: p}
}
func TestUOSettingsSparsePendingGenerationAndNoop(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	run := func(q uoSettingsCommand, wantWrites int64) uoSettingsEffect {
		t.Helper()
		turn := uoTestTurn(t, w, uoSettingsDefinitions())
		before := uoTestChanges(t, turn)
		out, e := applyUOSettings(t.Context(), turn, q)
		if e != nil {
			t.Fatal(e)
		}
		if got := uoTestChanges(t, turn) - before; got != wantWrites {
			t.Fatal("native write count", got, wantWrites)
		}
		uoTestCommit(t, turn)
		return out
	}
	out := run(uoTestSettings(1, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 32}}), 0)
	if out.changed || out.revision != 1 || out.projectionGeneration != 0 {
		t.Fatal(out)
	}
	out = run(uoTestSettings(1, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 64}}, uoSettingPatch{UOSettingAccessUrls, UOValue{Kind: UOList, List: []string{"https://one.example", "https://two.example"}}}), 9)
	if !out.changed || out.revision != 2 || out.projectionGeneration != 2 || len(out.fields) != 2 {
		t.Fatal(out)
	}
	run(uoTestSettings(2, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 64}}), 0)
	run(uoTestSettings(2, uoSettingPatch{UOSettingAccessUrls, UOValue{Kind: UOList, List: []string{"https://two.example", "https://one.example"}}}), 6)
	var rev, pending, generation int64
	var security string
	if e := w.db.QueryRowContext(t.Context(), `SELECT r.revision,s.configuration_pending_revision,s."remoteSecurity",p.revision FROM console_settings_revision r JOIN server_settings s ON s.singleton=r.singleton JOIN server_configuration_pending p ON p.field='analysisStorageGiB'`).Scan(&rev, &generation, &security, &pending); e != nil || rev != 3 || generation != 3 || security != "enhanced" || pending != 2 {
		t.Fatal(rev, generation, security, pending, e)
	}
}
func TestUOSettingsProjectionStaleFailureAndAcceptance(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	patch := func(rev int64, k UOSettingKey, v UOValue) {
		t.Helper()
		turn := uoTestTurn(t, w, uoSettingsDefinitions())
		if _, e := applyUOSettings(t.Context(), turn, uoTestSettings(rev, uoSettingPatch{k, v})); e != nil {
			t.Fatal(e)
		}
		uoTestCommit(t, turn)
	}
	patch(1, UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 64})
	// Snapshot at generation2 leaves the writer; external IO is test-simulated.
	old := uoProjectionResult{UOSettingAnalysisStorageGiB, 2, 2, true}
	patch(2, UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 128})
	for _, q := range []uoProjectionResult{old, {UOSettingAnalysisStorageGiB, 2, 3, true}, {UOSettingKey("remoteSecurity"), 3, 3, true}} {
		turn := uoTestTurn(t, w, uoSettingsDefinitions())
		before := uoTestChanges(t, turn)
		out, e := acceptUOConfigurationProjection(t.Context(), turn, q)
		if e == nil || out != (uoProjectionEffect{}) || uoTestChanges(t, turn) != before {
			t.Fatal("stale accepted", out, e)
		}
		if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
	}
	// Failure is truthful, durable work remains. It changes no revision/event.
	turn := uoTestTurn(t, w, uoSettingsDefinitions())
	before := uoTestChanges(t, turn)
	out, e := acceptUOConfigurationProjection(t.Context(), turn, uoProjectionResult{UOSettingAnalysisStorageGiB, 3, 3, false})
	if e != nil || out.removed || uoTestChanges(t, turn) != before {
		t.Fatal(out, e)
	}
	uoTestCommit(t, turn)
	turn = uoTestTurn(t, w, uoSettingsDefinitions())
	out, e = acceptUOConfigurationProjection(t.Context(), turn, uoProjectionResult{UOSettingAnalysisStorageGiB, 3, 3, true})
	if e != nil || !out.removed {
		t.Fatal(out, e)
	}
	uoTestCommit(t, turn)
	var count, revision int64
	if e = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM server_configuration_pending),revision FROM console_settings_revision WHERE singleton=1`).Scan(&count, &revision); e != nil || count != 0 || revision != 3 {
		t.Fatal(count, revision, e)
	}
	turn = uoTestTurn(t, w, uoSettingsDefinitions())
	out, e = acceptUOConfigurationProjection(t.Context(), turn, uoProjectionResult{UOSettingAnalysisStorageGiB, 3, 3, true})
	if !errors.Is(e, ErrConflict) || out != (uoProjectionEffect{}) {
		t.Fatal("unclassified replay acknowledged", out, e)
	}
}
func TestUOSettingsRejectSecurityAndRollback(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	for _, q := range []uoSettingsCommand{
		uoTestSettings(2), uoTestSettings(1, uoSettingPatch{UOSettingKey("secureConnections"), UOValue{Kind: UOText, Text: "required"}}),
		uoTestSettings(1, uoSettingPatch{UOSettingAccessUrls, UOValue{Kind: UOList, List: []string{"https://user:secret@example.com"}}}),
		uoTestSettings(1, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 64}}, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 128}}),
	} {
		turn := uoTestTurn(t, w, uoSettingsDefinitions())
		before := uoTestChanges(t, turn)
		out, e := applyUOSettings(t.Context(), turn, q)
		if e == nil || !reflect.DeepEqual(out, uoSettingsEffect{}) || uoTestChanges(t, turn) != before {
			t.Fatal(out, e)
		}
		if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
	}
	turn := uoTestTurn(t, w, uoSettingsDefinitions())
	if _, e := applyUOSettings(t.Context(), turn, uoTestSettings(1, uoSettingPatch{UOSettingAnalysisStorageGiB, UOValue{Kind: UOInteger, Number: 64}})); e != nil {
		t.Fatal(e)
	}
	if _, e := turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
	var revision, count, generation int64
	if e := w.db.QueryRowContext(t.Context(), `SELECT revision,(SELECT count(*) FROM server_configuration_pending),(SELECT configuration_pending_revision FROM server_settings) FROM console_settings_revision`).Scan(&revision, &count, &generation); e != nil || revision != 1 || count != 0 || generation != 0 {
		t.Fatal(revision, count, generation, e)
	}
	turn = uoTestTurn(t, w, uoSettingsDefinitions())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, e := applyUOSettings(ctx, turn, uoTestSettings(1))
	if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(out, uoSettingsEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUOSettingsFiniteVocabularyAndByteFence(t *testing.T) {
	for _, f := range uoSettingSpecs {
		v, e := UOValidateSetting(f.key, f.def)
		if e != nil || !uoValueEqual(f.def, v) {
			t.Fatal(f.key, e)
		}
	}
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	wc008NativeExec(t, w, `UPDATE server_settings SET "hardwareDevice"='海' WHERE singleton=1`)
	for _, budget := range []int64{2, 3} {
		turn := uoTestTurn(t, w, uoSettingsDefinitions())
		q := uoTestSettings(1, uoSettingPatch{UOSettingHardwareDevice, UOValue{Kind: UOText, Text: "海"}})
		q.copyBudget = budget
		out, e := applyUOSettings(t.Context(), turn, q)
		if budget == 2 {
			if e == nil || !reflect.DeepEqual(out, uoSettingsEffect{}) {
				t.Fatal(out, e)
			}
		} else if e != nil || out.copiedVariableBytes != 3 || out.changed {
			t.Fatal(out, e)
		}
		if _, e = turn.transaction.rollback(nil); e != nil {
			t.Fatal(e)
		}
	}
}
func TestUOSettingsAndPreferencesNativeDefinitions(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	defs := append(uoPreferenceDefinitions(), uoSettingsDefinitions()...)
	wc008NativePrepare(t, w, defs)
	// No own catalogue silently mints a production receipt.
	if _, e := stmt.New(defs); !errors.Is(e, stmt.ErrUnqualified) {
		t.Fatal(e)
	}
}
