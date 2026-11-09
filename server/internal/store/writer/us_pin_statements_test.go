package writer

import (
	"bytes"
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usPINSeed = `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES
('us-pin-account','us-pin-account',X'','us-pin-profile');
INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES
('us-pin-profile','us-pin-account','ffffffffffffffffffffffffffffffff','PIN target',1),
('us-pin-other','us-pin-account','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','Unchanged sibling',0);`

// Native storage fixture only: Capture validates SEARCH plans and creates the
// finite registry. It never authorizes a command or opens an accepting facade.
func TestUSPINNativeCASAndScalarResult(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usPINSeed); err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, usPINEffectDefinitions(), "us_pin_statements.go", "us_pin_statements_test.go")
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(id stmt.ID, args ...any) (out usPINEffect, err error) {
		t.Helper()
		statement, err := registry.Lookup(id)
		if err != nil {
			return out, err
		}
		bound, err := statement.Bind(args...)
		if err != nil {
			return out, err
		}
		var actualHash []byte
		err = bound.QueryRowContext(t.Context(), conn).Scan(&out.account, &out.profile, &out.lifetime, &out.revision, &out.pinRevision, &actualHash)
		out.pinRequired = actualHash != nil
		return out, err
	}
	changes := func() int64 {
		t.Helper()
		var value int64
		if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, tc := range []struct {
		id       stmt.ID
		revision int64
		pin      bool
	}{
		{usPINSetWrite, 1, true},
		{usPINClearWrite, 2, false},
		{usPINClearWrite, 3, false}, // clearing an absent PIN remains an edit
	} {
		args := []any{nil, "us-pin-account", "us-pin-profile", strings.Repeat("f", 32), tc.revision, tc.revision}
		if tc.pin {
			args[0] = []byte(usPINFixtureHash)
		}
		before := changes()
		out, err := run(tc.id, args...)
		if err != nil || out != (usPINEffect{"us-pin-account", "us-pin-profile", strings.Repeat("f", 32), tc.revision + 1, tc.revision + 1, tc.pin}) {
			t.Fatal("incorrect actual scalar post-result", out, err)
		}
		if changes()-before != 1 {
			t.Fatal("PIN effect changed more than one row")
		}
		var actualHash []byte
		if err := conn.QueryRowContext(t.Context(), "SELECT pin_hash FROM direct_profiles WHERE id='us-pin-profile'").Scan(&actualHash); err != nil {
			t.Fatal(err)
		}
		if tc.pin && !bytes.Equal(actualHash, []byte(usPINFixtureHash)) || !tc.pin && actualHash != nil {
			t.Fatal("stored hash does not match set/clear effect")
		}
		if _, err := run(tc.id, args...); err != sql.ErrNoRows || changes()-before != 1 {
			t.Fatal("stale CAS changed storage", err)
		}
	}
	// Independently falsify both revisions, target ownership and lifetime while
	// the target is live. Test retirement separately, so it cannot mask bad CAS.
	for _, tc := range []struct {
		name string
		args []any
	}{
		{"profile_revision", []any{"us-pin-account", "us-pin-profile", strings.Repeat("f", 32), int64(3), int64(4)}},
		{"pin_revision", []any{"us-pin-account", "us-pin-profile", strings.Repeat("f", 32), int64(4), int64(3)}},
		{"lifetime", []any{"us-pin-account", "us-pin-profile", strings.Repeat("a", 32), int64(4), int64(4)}},
		{"account", []any{"another-account", "us-pin-profile", strings.Repeat("f", 32), int64(4), int64(4)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range []stmt.ID{usPINSetWrite, usPINClearWrite} {
				args := append([]any{nil}, tc.args...)
				if id == usPINSetWrite {
					args[0] = []byte(usPINFixtureHash)
				}
				before := changes()
				if _, err := run(id, args...); err != sql.ErrNoRows || changes() != before {
					t.Fatal("mismatched target accepted", id, err)
				}
			}
		})
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE direct_profiles SET deleted=1 WHERE id='us-pin-profile'"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []stmt.ID{usPINSetWrite, usPINClearWrite} {
		args := []any{nil, "us-pin-account", "us-pin-profile", strings.Repeat("f", 32), int64(4), int64(4)}
		if id == usPINSetWrite {
			args[0] = []byte(usPINFixtureHash)
		}
		before := changes()
		if _, err := run(id, args...); err != sql.ErrNoRows || changes() != before {
			t.Fatal("retired target accepted", err)
		}
	}
	var revision, pinRevision int64
	var hash []byte
	if err := conn.QueryRowContext(t.Context(), "SELECT revision,pin_revision,pin_hash FROM direct_profiles WHERE id='us-pin-other'").Scan(&revision, &pinRevision, &hash); err != nil || revision != 1 || pinRevision != 1 || hash != nil {
		t.Fatal("sibling changed", err)
	}
	// Each revision has its own exact-integer ceiling, enforced natively even
	// if a future composition accidentally bypasses preparation's raw bounds.
	for _, revisions := range [][2]int64{{usMaxRevision, 4}, {4, usMaxRevision}} {
		if _, err := conn.ExecContext(t.Context(), "UPDATE direct_profiles SET deleted=0,revision=?,pin_revision=? WHERE id='us-pin-profile'", revisions[0], revisions[1]); err != nil {
			t.Fatal(err)
		}
		before := changes()
		if _, err := run(usPINClearWrite, nil, "us-pin-account", "us-pin-profile", strings.Repeat("f", 32), revisions[0], revisions[1]); err != sql.ErrNoRows || changes() != before {
			t.Fatal("exhausted revision accepted", err)
		}
	}
}

func TestUSPINEffectReleaseGuard(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "us_pin_statements.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyUSPIN" {
			continue
		}
		for _, statement := range fn.Body.List {
			if deferred, ok := statement.(*ast.DeferStmt); ok && guardedFinishDefer(fn, deferred) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing shared zero-on-error release guard")
	}
	if out, err := applyUSPIN(context.Background(), nil, nil); err == nil || out != (usPINEffect{}) {
		t.Fatal("invalid call exposed a result")
	}
	prepared, err := prepareUSPIN(usPINSet, usPINTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []Descriptor{
		{ID: SecurityAccept, Name: "security_accept", Durability: Full},
		{ID: usPINSet, Name: "us_profile_pin_set", Durability: Normal},
		{ID: usPINSet, Name: "another_purpose", Durability: Full},
	} {
		// No Describe substitution, opener or borrowed command purpose: these
		// malformed frames must fail before any native work is possible.
		turn := &commandTurn{transaction: &transaction{descriptor: descriptor, ctx: context.Background()}}
		if out, err := applyUSPIN(context.Background(), turn, prepared); err == nil || out != (usPINEffect{}) {
			t.Fatal("incorrect descriptor exposed a result")
		}
	}
}
