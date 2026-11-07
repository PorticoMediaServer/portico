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

const usMemberSeed = `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES
('us-member','us-member',X'','member-profile'),('us-sibling','us-sibling',X'','sibling-profile'),('us-owner','us-owner',X'','owner-profile');
INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES
('us-member','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member','["movies"]'),
('us-sibling','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','admin','[]'),
('us-owner','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','owner','[]');`

// Native storage only: Capture checks fixed SQL/params and SEARCH pins without
// executing effects. This does not qualify current authority, replay or COMMIT.
func TestUSMemberNativeCASAndScalarResults(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usMemberSeed); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usMemberEffectDefinitions(), "us_member_prepare.go", "us_member_statements.go", "us_member_statements_test.go")
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := func(id stmt.ID, args ...any) stmt.Row {
		t.Helper()
		s, err := r.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		return b.QueryRowContext(t.Context(), conn)
	}
	runFacts := func(account, incarnation string, revision, epoch int64) (out usMemberEffect, err error) {
		var actualEpoch int64
		err = query(usMemberTargetFacts, account, incarnation, revision, epoch).Scan(&out.account, &out.incarnation, &out.role, &out.revision, &out.disabled, &out.disabledMS, &out.disabledReason, &actualEpoch)
		return
	}
	runMember := func(id stmt.ID, args ...any) (out usMemberEffect, err error) {
		err = query(id, args...).Scan(&out.account, &out.incarnation, &out.role, &out.revision, &out.disabled, &out.disabledMS, &out.disabledReason)
		return
	}
	runEpoch := func(args ...any) (out usAccountEpochEffect, err error) {
		err = query(usAccountEpochWrite, args...).Scan(&out.account, &out.epoch)
		return
	}
	changes := func() (n int64) {
		t.Helper()
		if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	memberArgs := func(rev int64) []any { return []any{"us-member", strings.Repeat("e", 32), rev} }
	// The point join validates both source rows in the same private transaction;
	// each individual update separately CASes the row it changes.
	for _, edit := range []func([]any){
		func(a []any) { a[0] = "missing" }, func(a []any) { a[1] = strings.Repeat("f", 32) },
		func(a []any) { a[2] = int64(2) }, func(a []any) { a[3] = int64(2) },
	} {
		a := []any{"us-member", strings.Repeat("e", 32), int64(1), int64(1)}
		edit(a)
		before := changes()
		if _, err := runFacts(a[0].(string), a[1].(string), a[2].(int64), a[3].(int64)); err != sql.ErrNoRows || changes() != before {
			t.Fatal("source comparison accepted", a, err)
		}
	}
	for _, edit := range []func([]any){func(a []any) { a[0] = "missing" }, func(a []any) { a[1] = strings.Repeat("f", 32) }, func(a []any) { a[2] = int64(2) }} {
		a := memberArgs(1)
		edit(a)
		before := changes()
		for _, id := range []stmt.ID{usMemberRoleAdminWrite, usMemberRoleMemberWrite, usMemberDisableWrite} {
			bindings := a
			if id == usMemberDisableWrite {
				bindings = append([]any{int64(1000), "owner"}, a...)
			}
			if _, err := runMember(id, bindings...); err != sql.ErrNoRows || changes() != before {
				t.Fatal("membership CAS accepted", id, err)
			}
		}
	}
	for rev, role := range []string{"admin", "member", "member"} {
		a := memberArgs(int64(rev + 1))
		id := usMemberRoleMemberWrite
		if role == "admin" {
			id = usMemberRoleAdminWrite
		}
		before := changes()
		out, err := runMember(id, a...)
		want := usMemberEffect{account: "us-member", incarnation: strings.Repeat("e", 32), role: role, revision: int64(rev + 2)}
		if err != nil || out != want || changes()-before != 1 {
			t.Fatal("role did not return actual one-row result", out, err)
		}
		if _, err := runMember(id, a...); err != sql.ErrNoRows || changes()-before != 1 {
			t.Fatal("stale role edit changed storage", err)
		}
	}
	// Disable bumps only membership; epoch is an independent bounded second leg
	// whose expected revision MUST be the post-disable revision.
	for rev, now := range []int64{1000, 2000} {
		stamp := now
		if rev > 0 {
			stamp = 1000
		}
		a := append([]any{stamp, "owner"}, memberArgs(int64(rev+4))...)
		before := changes()
		out, err := runMember(usMemberDisableWrite, a...)
		want := usMemberEffect{"us-member", strings.Repeat("e", 32), "member", int64(rev + 5), true, 1000, "owner"}
		if err != nil || out != want || changes()-before != 1 {
			t.Fatal("disable did not preserve timestamp/return one-row result", out, err)
		}
		if _, err := runMember(usMemberDisableWrite, a...); err != sql.ErrNoRows || changes()-before != 1 {
			t.Fatal("stale disable changed storage", err)
		}
	}
	before := changes()
	if _, err := runFacts("us-member", strings.Repeat("e", 32), int64(5), int64(1)); err != sql.ErrNoRows {
		t.Fatal("epoch accepted pre-disable revision", err)
	}
	if _, err := runFacts("us-member", strings.Repeat("e", 32), 6, 1); err != nil {
		t.Fatal("post-disable source comparison", err)
	}
	epochArgs := []any{"us-member", int64(1)}
	if out, err := runEpoch(epochArgs...); err != nil || out != (usAccountEpochEffect{"us-member", 2}) || changes()-before != 1 {
		t.Fatal("epoch did not return one-row native result", out, err)
	}
	if _, err := runEpoch(epochArgs...); err != sql.ErrNoRows || changes()-before != 1 {
		t.Fatal("stale epoch changed storage", err)
	}
	// Owner immunity is native and needs no owner-count scan. Epoch revocation
	// for an owner remains a separately authorized self-sign-out/reset purpose.
	owner := []any{"us-owner", strings.Repeat("a", 32), int64(1)}
	before = changes()
	for _, id := range []stmt.ID{usMemberRoleAdminWrite, usMemberRoleMemberWrite, usMemberDisableWrite} {
		a := owner
		if id == usMemberDisableWrite {
			a = append([]any{int64(1000), "owner"}, owner...)
		}
		if _, err := runMember(id, a...); err != sql.ErrNoRows || changes() != before {
			t.Fatal("owner edit accepted", id, err)
		}
	}
	// Closed role statements contain literal admin/member targets: there is no
	// owner binding, and native owner targets are refused without a count scan.
	var role, libraries string
	var revision, epoch int64
	var disabled bool
	if err := conn.QueryRowContext(t.Context(), `SELECT m.role,m.revision,m.disabled,m.allowed_libraries,a.epoch FROM direct_memberships m JOIN accounts a ON a.id=m.account_id WHERE m.account_id='us-sibling'`).Scan(&role, &revision, &disabled, &libraries, &epoch); err != nil || role != "admin" || revision != 1 || disabled || libraries != "[]" || epoch != 1 {
		t.Fatal("sibling changed", err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT allowed_libraries FROM direct_memberships WHERE account_id='us-member'`).Scan(&libraries); err != nil || libraries != `["movies"]` {
		t.Fatal("unrelated target policy changed", err)
	}
	// Exhaustion is independently guarded in SQL, including the last legal
	// membership edit followed by an epoch advance at its final revision.
	if _, err := conn.ExecContext(t.Context(), "UPDATE direct_memberships SET revision=? WHERE account_id='us-member'", usMaxRevision); err != nil {
		t.Fatal(err)
	}
	before = changes()
	if _, err := runMember(usMemberRoleMemberWrite, memberArgs(usMaxRevision)...); err != sql.ErrNoRows || changes() != before {
		t.Fatal("exhausted membership edit accepted", err)
	}
	if _, err := runFacts("us-member", strings.Repeat("e", 32), usMaxRevision, 2); err != nil {
		t.Fatal("source comparison rejected final membership revision", err)
	}
	if out, err := runEpoch("us-member", int64(2)); err != nil || out.epoch != 3 {
		t.Fatal("epoch rejected final membership revision", err)
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE accounts SET epoch=? WHERE id='us-member'", usMaxRevision); err != nil {
		t.Fatal(err)
	}
	before = changes()
	if _, err := runEpoch("us-member", usMaxRevision); err != sql.ErrNoRows || changes() != before {
		t.Fatal("exhausted epoch accepted", err)
	}
}

func TestUSMemberEffectsOwnReleaseGuards(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "us_member_statements.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{"applyUSMember": false, "applyUSAccountEpoch": false, "readUSMemberTarget": false}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, wanted := found[fn.Name.Name]; !wanted {
			continue
		}
		for _, s := range fn.Body.List {
			if d, ok := s.(*ast.DeferStmt); ok && guardedFinishDefer(fn, d) {
				found[fn.Name.Name] = true
			}
		}
	}
	for name, guarded := range found {
		if !guarded {
			t.Fatal("missing own zero-on-error release guard", name)
		}
	}
	if out, err := applyUSMember(context.Background(), nil, nil, 1000); err == nil || out != (usMemberEffect{}) {
		t.Fatal("invalid member effect exposed a result")
	}
	if out, err := applyUSAccountEpoch(context.Background(), nil, nil); err == nil || out != (usAccountEpochEffect{}) {
		t.Fatal("invalid epoch effect exposed a result")
	}
	p, _ := prepareUSMember(usMemberRoleChange, usMemberRequest{target: usMemberTestTarget(), role: "member"})
	e, _ := prepareUSAccountEpoch(usMemberTestTarget())
	for _, d := range []Descriptor{
		{ID: SecurityAccept, Name: "security_accept", Durability: Full},
		{ID: usMemberRoleChange, Name: "us_member_role_change", Durability: Normal},
		{ID: usAccountEpochAdvance, Name: "wrong", Durability: Full},
	} {
		turn := &commandTurn{transaction: &transaction{descriptor: d, ctx: context.Background()}}
		if out, err := applyUSMember(context.Background(), turn, p, 1000); err == nil || out != (usMemberEffect{}) {
			t.Fatal("incorrect member purpose exposed a result")
		}
		if out, err := applyUSAccountEpoch(context.Background(), turn, e); err == nil || out != (usAccountEpochEffect{}) {
			t.Fatal("incorrect epoch purpose exposed a result")
		}
	}
}
