package writer

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This native SQL component test proves revision/lifetime CAS and row effects.
// It is not current issuer authorization, receipt replay or FULL hold qualification.
func TestUSPolicyNativeEffects(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usPolicySeed); err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, usPolicyEffectDefinitions(), "us_policy_native_test.go")
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	member := []any{`["movies"]`, "us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1)}
	profile := []any{nil, "us-target", "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)}
	for _, tc := range []struct {
		id     stmt.ID
		args   []any
		policy sql.NullString
	}{
		{usMemberPolicyWrite, member, sql.NullString{String: `["movies"]`, Valid: true}},
		{usProfilePolicyWrite, profile, sql.NullString{}},
	} {
		statement, err := registry.Lookup(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := statement.Bind(tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		var lifetime string
		var revision int64
		var actual sql.NullString
		if err := bound.QueryRowContext(t.Context(), conn).Scan(&lifetime, &revision, &actual); err != nil {
			t.Fatal(err)
		}
		if actual != tc.policy || revision != 2 || len(lifetime) != 32 {
			t.Fatal(tc.id, actual, revision, lifetime)
		}
		if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after-before != 1 {
			t.Fatal("policy effect must change exactly one source row", tc.id, before, after, err)
		}
		// The same expected revision is stale. This is SQL CAS, not command
		// replay: a composed command must resolve its saved receipt first.
		if err := bound.QueryRowContext(t.Context(), conn).Scan(&lifetime, &revision, &actual); err != sql.ErrNoRows {
			t.Fatal("stale revision accepted", tc.id, err)
		}
	}
	// A wrong lifetime, another account and a retired row all refuse even when
	// the target ID and revision match. No fallback uses only the public ID.
	if _, err := conn.ExecContext(t.Context(), "UPDATE direct_profiles SET deleted=1 WHERE id='us-profile'"); err != nil {
		t.Fatal(err)
	}
	cases := [][]any{
		{"[]", "us-target", "us-profile", strings.Repeat("a", 32), int64(2)},
		{"[]", "us-other", "us-profile", strings.Repeat("f", 32), int64(2)},
		{"[]", "us-target", "us-profile", strings.Repeat("f", 32), int64(2)},
	}
	statement, err := registry.Lookup(usProfilePolicyWrite)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range cases {
		bound, err := statement.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		var lifetime string
		var revision int64
		var policy sql.NullString
		if err := bound.QueryRowContext(t.Context(), conn).Scan(&lifetime, &revision, &policy); err != sql.ErrNoRows {
			t.Fatal("retired/wrong-lifetime target accepted", err)
		}
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
