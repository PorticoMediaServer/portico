package writer

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func TestUSRestrictionsNativeCAS(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, e := w.db.ExecContext(t.Context(), `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('us-profile-account','restrictions-user',X'','us-profile'); INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('us-profile-account','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','owner'); INSERT INTO direct_profiles(account_id,id,lifetime,name,is_primary) VALUES('us-profile-account','us-profile','ffffffffffffffffffffffffffffffff','Main',1);`); e != nil {
		t.Fatal(e)
	}
	defs := usRestrictionsEffectDefinitions()

	r := nativeInventoryFixtureRegistry(t, w, defs, "us_restrictions_statements.go", "us_restrictions_prepare.go", "us_restrictions_statements_test.go")
	conn, e := w.db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	query := func(id stmt.ID, args ...any) stmt.Row {
		t.Helper()
		s, e := r.Lookup(id)
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.Bind(args...)
		if e != nil {
			t.Fatal(e)
		}
		return b.QueryRowContext(t.Context(), conn)
	}
	source := []any{"us-profile-account", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", int64(1), int64(1), "us-profile", "ffffffffffffffffffffffffffffffff", int64(1)}
	for n := range source {
		stale := append([]any{}, source...)
		switch x := stale[n].(type) {
		case string:
			stale[n] = x + "a"
		case int64:
			stale[n] = x + 1
		}
		var role string
		var primary bool
		if e := query(usRestrictionsSource, stale...).Scan(&role, &primary); e != sql.ErrNoRows {
			t.Fatal("source witness admitted", n, e)
		}
	}
	changes := func() int64 {
		var n int64
		if e := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := changes()
	args := []any{"mpaa", "PG-13", int64(13), false, `["alpha"]`, true, false, false, true, "us-profile"}
	var out usRestrictionsEffect
	if e := query(usRestrictionsInsert, append(append([]any{}, args...), int64(2))...).Scan(out.destinations()...); e != nil || out.revision != 2 || out.maximumAge != 13 {
		t.Fatal("insert/default revision", e)
	}
	if changes()-before != 1 {
		t.Fatal("insert is not one row")
	}
	before = changes()
	if e := query(usRestrictionsInsert, append(append([]any{}, args...), int64(2))...).Scan(out.destinations()...); e != sql.ErrNoRows {
		t.Fatal("duplicate changed", e)
	}
	for _, cas := range [][]any{{int64(1), false}, {int64(2), true}} {
		if e := query(usRestrictionsUpdate, append(append([]any{}, args...), cas...)...).Scan(out.destinations()...); e != sql.ErrNoRows {
			t.Fatal("stale CAS", e)
		}
	}
	if changes() != before {
		t.Fatal("stale CAS wrote")
	}
	if e := query(usRestrictionsUpdate, append(args, int64(2), false)...).Scan(out.destinations()...); e != nil || out.revision != 3 || out.labelsJSON != `["alpha"]` {
		t.Fatal("update", e)
	}
	if changes()-before != 1 {
		t.Fatal("update is not one row")
	}
}
func TestUSRestrictionsZeroRelease(t *testing.T) {
	v, err := applyUSRestrictions(t.Context(), nil, nil, nil)
	if err == nil || v != (usRestrictionsEffect{}) {
		t.Fatal("invalid release")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	v, err = finishCommand(ctx, t.Context(), usRestrictionsEffect{profile: "private"}, nil)
	if err == nil || v != (usRestrictionsEffect{}) {
		t.Fatal("cancel release")
	}
}

func TestUSRestrictionsOwnerProofRequiresCurrentFrame(t *testing.T) {
	var proof *usRestrictionsOwnerProof
	if err := proof.check(nil, usPolicyIssuer{}); err == nil {
		t.Fatal("nil owner proof admitted")
	}
	proof = &usRestrictionsOwnerProof{authority: &usPolicyAuthorization{}}
	if err := proof.check(nil, usPolicyIssuer{}); err == nil {
		t.Fatal("unbound owner proof admitted")
	}
	if err := proof.check(nil, usPolicyIssuer{server: "another"}); err == nil {
		t.Fatal("foreign issuer owner proof admitted")
	}
}
