package writer

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usPolicyMemberLifetime = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	usPolicyProfileID      = "us-profile"
	usPolicyProfileLife    = "ffffffffffffffffffffffffffffffff"
	usPolicyInheritID      = "us-inherit"
	usPolicyInheritLife    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	usPolicyOtherMember    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// The captured registry proves these two native CAS statements against the
// canonical schema. The turn is the existing synthetic test turn: no command
// registration, current issuer authority, receipt or FULL qualification.
func usPolicyEffectsFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usPolicySeed); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usPolicyEffectDefinitions(), "us_policy_effects.go", "us_policy_effects_test.go")
	return w, r
}

func usPolicyEffectTurn(t *testing.T, w *executor, r *stmt.Registry, id CommandID) *commandTurn {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), id, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func usPolicyMemberCommand(t *testing.T, libraries []string, revision int64) *usLibraryPolicyCommand {
	t.Helper()
	identity, request := usPolicyFixture()
	request.account, request.memberIncarnation = "us-target", usPolicyMemberLifetime
	request.profile, request.profileLifetime = "", ""
	request.expectedRevision, request.libraries = revision, libraries
	command, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func usPolicyProfileCommand(t *testing.T, profile, lifetime string, libraries []string, revision int64) *usLibraryPolicyCommand {
	t.Helper()
	identity, request := usPolicyFixture()
	request.account, request.memberIncarnation = "us-target", usPolicyMemberLifetime
	request.profile, request.profileLifetime = profile, lifetime
	request.expectedRevision, request.libraries = revision, libraries
	command, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func usPolicyTarget(t *testing.T, w *executor, table, idColumn, id string) (sql.NullString, int64) {
	t.Helper()
	var policy sql.NullString
	var revision, disabled int64
	query := `SELECT allowed_libraries,revision FROM ` + table + ` WHERE ` + idColumn + `=?`
	if table == "direct_memberships" {
		query = `SELECT allowed_libraries,revision,disabled FROM direct_memberships WHERE ` + idColumn + `=?`
	}
	args := []any{id}
	if table == "direct_profiles" {
		if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&policy, &revision); err != nil {
			t.Fatal(err)
		}
		return policy, revision
	}
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&policy, &revision, &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled != 0 {
		t.Fatal("member unexpectedly disabled", id)
	}
	return policy, revision
}

func usPolicyChanges(t *testing.T, conn *sql.Conn) int64 {
	t.Helper()
	var changes int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

// One PK/lifetime/revision CAS update of ONE source row, with the complete
// before/after state. Member NULL is impossible; profile NULL inherits and an
// explicit [] remains TEXT.
func TestUSPolicyEffectsNativeCAS(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	turn := usPolicyEffectTurn(t, w, r, USMemberLibraries)
	before := usPolicyChanges(t, turn.transaction.conn)
	effect, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, []string{"movies"}, 1), sql.NullString{String: `["música"]`, Valid: true}, 1)
	if err != nil || !effect.changed || effect.lifetime != usPolicyMemberLifetime ||
		effect.before.policy.String != `["música"]` || effect.before.revision != 1 ||
		effect.after.policy.String != `["movies"]` || effect.after.revision != 2 {
		t.Fatal("member effect", effect, err)
	}
	if after := usPolicyChanges(t, turn.transaction.conn); after-before != 1 {
		t.Fatal("member effect must change exactly one row", before, after)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	policy, revision := usPolicyTarget(t, w, "direct_memberships", "account_id", "us-target")
	if policy.String != `["movies"]` || revision != 2 {
		t.Fatal("member source", policy, revision)
	}
	// SQL CAS, not command replay: the same expected revision is stale.
	turn = usPolicyEffectTurn(t, w, r, USMemberLibraries)
	stale, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, []string{"music"}, 1), policy, 1)
	if !errors.Is(err, ErrConflict) || stale != (usPolicyEffect{}) {
		t.Fatal("stale revision accepted", stale, err)
	}
	_, _ = turn.transaction.rollback(nil)

	for _, tc := range []struct {
		name, profile, lifetime string
		before                  sql.NullString
		preparedLibraries       []string
		wantPolicy              sql.NullString
	}{
		{"inherit", usPolicyProfileID, usPolicyProfileLife, sql.NullString{String: "[]", Valid: true}, nil, sql.NullString{}},
		{"explicit_empty", usPolicyInheritID, usPolicyInheritLife, sql.NullString{}, []string{}, sql.NullString{String: "[]", Valid: true}},
		{"narrow", usPolicyInheritID, usPolicyInheritLife, sql.NullString{}, []string{"movies"}, sql.NullString{String: `["movies"]`, Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r := usPolicyEffectsFixture(t)
			turn := usPolicyEffectTurn(t, w, r, USProfileLibraries)
			beforeChanges := usPolicyChanges(t, turn.transaction.conn)
			effect, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyProfileCommand(t, tc.profile, tc.lifetime, tc.preparedLibraries, 1), tc.before, 1)
			if err != nil || !effect.changed || effect.lifetime != tc.lifetime ||
				effect.before != (usPolicyTargetState{policy: tc.before, revision: 1}) ||
				effect.after != (usPolicyTargetState{policy: tc.wantPolicy, revision: 2}) {
				t.Fatal("profile effect", tc.name, effect, err)
			}
			if after := usPolicyChanges(t, turn.transaction.conn); after-beforeChanges != 1 {
				t.Fatal("profile effect must change exactly one row", tc.name, beforeChanges, after)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			policy, revision := usPolicyTarget(t, w, "direct_profiles", "id", tc.profile)
			if policy != tc.wantPolicy || revision != 2 {
				t.Fatal("profile source", tc.name, policy, revision)
			}
		})
	}
}

// A prepared policy identical to the fresh target writes nothing and keeps the
// complete unchanged before/after state. Replay never enters this function.
func TestUSPolicyEffectsNoChange(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	turn := usPolicyEffectTurn(t, w, r, USProfileLibraries)
	before := usPolicyChanges(t, turn.transaction.conn)
	effect, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyProfileCommand(t, usPolicyProfileID, usPolicyProfileLife, []string{}, 1), sql.NullString{String: "[]", Valid: true}, 1)
	if err != nil || effect.changed || effect.before != effect.after ||
		effect.before != (usPolicyTargetState{policy: sql.NullString{String: "[]", Valid: true}, revision: 1}) {
		t.Fatal("no-op effect", effect, err)
	}
	if after := usPolicyChanges(t, turn.transaction.conn); after != before {
		t.Fatal("no-op mutated source", before, after)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	policy, revision := usPolicyTarget(t, w, "direct_profiles", "id", usPolicyProfileID)
	if policy.String != "[]" || revision != 1 {
		t.Fatal("no-op source", policy, revision)
	}
}

// A prepared member policy is always the explicit JSON list; a NULL member
// list is impossible and must refuse rather than silently inherit.
func TestUSPolicyEffectsMemberNilRefuses(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	identity, request := usPolicyFixture()
	request.account, request.memberIncarnation = "us-target", usPolicyMemberLifetime
	request.profile, request.profileLifetime = "", ""
	request.libraries = nil
	nilList, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil || nilList.policy.String != "[]" || !nilList.policy.Valid {
		t.Fatal("nil member list must prepare the explicit deny", nilList, err)
	}
	// A broken projection is refused even when every other witness matches.
	nilList.policy = sql.NullString{}
	turn := usPolicyEffectTurn(t, w, r, USMemberLibraries)
	effect, err := applyUSLibraryPolicy(t.Context(), turn, nilList, sql.NullString{String: `["movies"]`, Valid: true}, 1)
	if !errors.Is(err, ErrInvalid) || effect != (usPolicyEffect{}) {
		t.Fatal("nil member policy accepted", effect, err)
	}
	_, _ = turn.transaction.rollback(nil)
	// A NULL member target fact is equally impossible.
	turn = usPolicyEffectTurn(t, w, r, USMemberLibraries)
	effect, err = applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, nil, 1), sql.NullString{}, 1)
	if !errors.Is(err, ErrInvalid) || effect != (usPolicyEffect{}) {
		t.Fatal("nil member target accepted", effect, err)
	}
	_, _ = turn.transaction.rollback(nil)
	// The prepared explicit deny is the legal member projection and writes it.
	turn = usPolicyEffectTurn(t, w, r, USMemberLibraries)
	deny, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, nil, 1), sql.NullString{String: `["movies"]`, Valid: true}, 1)
	if err != nil || !deny.changed || deny.after.policy.String != "[]" || deny.after.revision != 2 {
		t.Fatal("explicit member deny", deny, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	policy, revision := usPolicyTarget(t, w, "direct_memberships", "account_id", "us-target")
	if policy.String != "[]" || revision != 2 {
		t.Fatal("member deny source", policy, revision)
	}
}

// Wrong lifetime, a disabled membership and a deleted profile all refuse even
// when the target ID and revision match. There is no public-ID-only fallback.
func TestUSPolicyEffectsWrongLifetimeHiddenRetired(t *testing.T) {
	for _, tc := range []struct {
		name, mutate string
		args         []any
		id           CommandID
	}{
		{"member_wrong_lifetime", `UPDATE direct_memberships SET incarnation=? WHERE account_id='us-target'`, []any{usPolicyInheritLife}, USMemberLibraries},
		{"member_disabled", `UPDATE direct_memberships SET disabled=1 WHERE account_id='us-target'`, nil, USMemberLibraries},
		{"profile_wrong_lifetime", `UPDATE direct_profiles SET lifetime=? WHERE id='us-profile'`, []any{usPolicyInheritLife}, USProfileLibraries},
		{"profile_retired", `UPDATE direct_profiles SET deleted=1 WHERE id='us-profile'`, nil, USProfileLibraries},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r := usPolicyEffectsFixture(t)
			if _, err := w.db.ExecContext(t.Context(), tc.mutate, tc.args...); err != nil {
				t.Fatal(err)
			}
			turn := usPolicyEffectTurn(t, w, r, tc.id)
			beforeChanges := usPolicyChanges(t, turn.transaction.conn)
			command := usPolicyMemberCommand(t, []string{"movies"}, 1)
			beforePolicy := sql.NullString{String: `["música"]`, Valid: true}
			if tc.id == USProfileLibraries {
				command = usPolicyProfileCommand(t, usPolicyProfileID, usPolicyProfileLife, []string{"movies"}, 1)
				beforePolicy = sql.NullString{String: "[]", Valid: true}
			}
			effect, err := applyUSLibraryPolicy(t.Context(), turn, command, beforePolicy, 1)
			if !errors.Is(err, ErrConflict) || effect != (usPolicyEffect{}) {
				t.Fatal("wrong/retired target accepted", tc.name, effect, err)
			}
			if after := usPolicyChanges(t, turn.transaction.conn); after != beforeChanges {
				t.Fatal("refused effect changed source", tc.name, beforeChanges, after)
			}
			_, _ = turn.transaction.rollback(nil)
		})
	}
}

// A failed effect rolls back the ENTIRE turn, including a successful nonempty
// prefix, and leaves no partial revision behind.
func TestUSPolicyEffectsRollbackAfterPrefix(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	turn := usPolicyEffectTurn(t, w, r, USMemberLibraries)
	first, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, []string{"movies"}, 1), sql.NullString{String: `["música"]`, Valid: true}, 1)
	if err != nil || !first.changed {
		t.Fatal("prefix effect", first, err)
	}
	// The second effect targets another member row with a stale revision.
	second := usPolicyMemberCommand(t, []string{"podcasts"}, 2)
	second.request.account = "us-other"
	second.request.memberIncarnation = usPolicyOtherMember
	failed, err := applyUSLibraryPolicy(t.Context(), turn, second, sql.NullString{String: "[]", Valid: true}, 2)
	if !errors.Is(err, ErrConflict) || failed != (usPolicyEffect{}) {
		t.Fatal("second effect", failed, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		account, policy string
	}{
		{"us-target", `["música"]`},
		{"us-other", "[]"},
	} {
		policy, revision := usPolicyTarget(t, w, "direct_memberships", "account_id", tc.account)
		if policy.String != tc.policy || revision != 1 {
			t.Fatal("rollback left prefix", tc.account, policy, revision)
		}
	}
}

// Cancellation is a lifetime failure, not a partial success.
func TestUSPolicyEffectsCancellation(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	turn := usPolicyEffectTurn(t, w, r, USMemberLibraries)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := usPolicyChanges(t, turn.transaction.conn)
	effect, err := applyUSLibraryPolicy(ctx, turn, usPolicyMemberCommand(t, []string{"movies"}, 1), sql.NullString{String: `["música"]`, Valid: true}, 1)
	if err == nil || effect != (usPolicyEffect{}) {
		t.Fatal("canceled effect leaked prefix", effect, err)
	}
	if after := usPolicyChanges(t, turn.transaction.conn); after != before {
		t.Fatal("canceled effect changed source", before, after)
	}
	_, _ = turn.transaction.rollback(nil)
}

// The fresh-only effect is not the replay path. After an independent source
// drift, the original expected revision conflicts and the drifted value stays;
// US-07's receipt owns the exact original result.
func TestUSPolicyEffectsReplayAfterSourceDrift(t *testing.T) {
	w, r := usPolicyEffectsFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries='["podcasts"]',revision=7 WHERE account_id='us-target'`); err != nil {
		t.Fatal(err)
	}
	turn := usPolicyEffectTurn(t, w, r, USMemberLibraries)
	effect, err := applyUSLibraryPolicy(t.Context(), turn, usPolicyMemberCommand(t, []string{"movies"}, 1), sql.NullString{String: `["música"]`, Valid: true}, 1)
	if !errors.Is(err, ErrConflict) || effect != (usPolicyEffect{}) {
		t.Fatal("original revision replayed after drift", effect, err)
	}
	_, _ = turn.transaction.rollback(nil)
	policy, revision := usPolicyTarget(t, w, "direct_memberships", "account_id", "us-target")
	if policy.String != `["podcasts"]` || revision != 7 {
		t.Fatal("drifted source overwritten", policy, revision)
	}
}

func TestUSPolicyEffectReleaseGuard(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "us_policy_effects.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyUSLibraryPolicy" {
			continue
		}
		for _, statement := range fn.Body.List {
			if deferred, ok := statement.(*ast.DeferStmt); ok && guardedFinishDefer(fn, deferred) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("US effect result must use the shared zero-on-error release guard")
	}
	out, err := applyUSLibraryPolicy(context.Background(), nil, nil, sql.NullString{}, 1)
	if err == nil || out != (usPolicyEffect{}) {
		t.Fatal(out, err)
	}
}
