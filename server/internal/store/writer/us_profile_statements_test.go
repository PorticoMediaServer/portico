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

func TestUSProfileNativeEffects(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	const seed = `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('us-profile-account','profile-user',X'','us-profile');
INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('us-profile-account','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member');
INSERT INTO direct_profiles(account_id,id,lifetime,name,position,art,pin_hash,allowed_libraries,avatar_digest,avatar_source_mime,avatar_updated_at,avatar_version) VALUES('us-profile-account','us-profile','ffffffffffffffffffffffffffffffff','Original',9,'rose',X'01','[]','digest','image/png','original-time',3);`
	if _, err := w.db.ExecContext(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usProfileEffectDefinitions(), "us_profile_prepare.go", "us_profile_statements.go", "us_profile_statements_test.go")
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
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
		return b.QueryRowContext(t.Context(), c)
	}
	changes := func() (n int64) {
		t.Helper()
		if e := c.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&n); e != nil {
			t.Fatal(e)
		}
		return
	}
	for _, args := range [][]any{{"us-profile-account", strings.Repeat("a", 32), int64(1), int64(1)}, {"us-profile-account", strings.Repeat("e", 32), int64(2), int64(1)}, {"us-profile-account", strings.Repeat("e", 32), int64(1), int64(2)}} {
		var a, b string
		var rev, epoch int64
		if e := query(usProfileMemberFacts, args...).Scan(&a, &b, &rev, &epoch); e != sql.ErrNoRows {
			t.Fatal("stale member admitted", e)
		}
	}
	var created usProfileEffect
	before := changes()
	if err := query(usProfileCreateWrite, "us-profile-account", "us-new-profile", strings.Repeat("a", 32), "New", int64(10), "blue").Scan(created.destinations()...); err != nil {
		t.Fatal(err)
	}
	if created.position != 10 || created.primary || created.revision != 1 || created.pinRevision != 1 || created.libraries.Valid || created.deleted || created.avatarDigest.Valid || changes()-before != 1 {
		t.Fatal("create defaults/one row", created)
	}
	before = changes()
	if e := query(usProfileCreateWrite, "us-profile-account", "us-new-profile", strings.Repeat("b", 32), "Replacement", int64(11), "mint").Scan(created.destinations()...); e == nil || changes() != before {
		t.Fatal("duplicate creation replaced a lifetime", e)
	}
	var original usProfileEffect
	if err := query(usProfileMetadataFacts, "us-profile-account", "us-profile", strings.Repeat("f", 32), int64(1)).Scan(original.destinations()...); err != nil {
		t.Fatal(err)
	}
	var present int64
	if err := query(usProfilePINPresent, "us-profile-account", "us-profile", strings.Repeat("f", 32), int64(1)).Scan(&present); err != nil || present != 1 {
		t.Fatal("native PIN presence", err)
	}
	original.pinRequired = present == 1
	if !original.pinRequired || !original.libraries.Valid || original.libraries.String != "[]" || original.avatarVersion != 3 {
		t.Fatal("native image incomplete", original)
	}
	for _, args := range [][]any{{"Edited", "mint", "other", "us-profile", strings.Repeat("f", 32), int64(1)}, {"Edited", "mint", "us-profile-account", "us-profile", strings.Repeat("a", 32), int64(1)}, {"Edited", "mint", "us-profile-account", "us-profile", strings.Repeat("f", 32), int64(2)}} {
		before := changes()
		var out usProfileEffect
		if e := query(usProfileMetadataWrite, args...).Scan(out.destinations()...); e != sql.ErrNoRows || changes() != before {
			t.Fatal("stale profile changed", e)
		}
	}
	// Equal values are still an edit, but every unrelated field stays exact.
	var out usProfileEffect
	before = changes()
	if e := query(usProfileMetadataWrite, original.name, original.art, original.account, original.profile, original.lifetime, original.revision).Scan(out.destinations()...); e != nil {
		t.Fatal(e)
	}
	original.revision++
	out.pinRequired = original.pinRequired
	if out != original || changes()-before != 1 {
		t.Fatal("metadata preserved result", out, original)
	}
	if _, e := c.ExecContext(t.Context(), "UPDATE direct_profiles SET deleted=1 WHERE id='us-profile'"); e != nil {
		t.Fatal(e)
	}
	before = changes()
	if e := query(usProfileMetadataWrite, "Edited", "mint", out.account, out.profile, out.lifetime, out.revision).Scan(out.destinations()...); e != sql.ErrNoRows || changes() != before {
		t.Fatal("retired profile changed", e)
	}
	if _, e := c.ExecContext(t.Context(), "UPDATE direct_profiles SET deleted=0,revision=? WHERE id='us-profile'", usMaxRevision); e != nil {
		t.Fatal(e)
	}
	before = changes()
	if e := query(usProfileMetadataWrite, "Edited", "mint", "us-profile-account", "us-profile", strings.Repeat("f", 32), usMaxRevision).Scan(out.destinations()...); e != sql.ErrNoRows || changes() != before {
		t.Fatal("exhausted revision changed", e)
	}
	if _, e := c.ExecContext(t.Context(), "UPDATE direct_memberships SET disabled=1"); e != nil {
		t.Fatal(e)
	}
	var a, b string
	var rev, epoch int64
	if e := query(usProfileMemberFacts, "us-profile-account", strings.Repeat("e", 32), int64(1), int64(1)).Scan(&a, &b, &rev, &epoch); e != sql.ErrNoRows {
		t.Fatal("disabled source admitted", e)
	}
}

func TestUSProfileReleaseGuard(t *testing.T) {
	f, e := parser.ParseFile(token.NewFileSet(), "us_profile_statements.go", nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	guarded := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyUSProfile" {
			continue
		}
		for _, s := range fn.Body.List {
			if d, ok := s.(*ast.DeferStmt); ok && guardedFinishDefer(fn, d) {
				guarded = true
			}
		}
	}
	if !guarded {
		t.Fatal("missing zero-on-error release guard")
	}
	if out, e := applyUSProfile(context.Background(), nil, nil, 0); e == nil || out != (usProfileEffect{}) {
		t.Fatal(out, e)
	}
	p, _ := prepareUSProfileCreate(usProfileFixtureTarget().member, "new", strings.Repeat("a", 32), "New", "")
	for _, d := range []Descriptor{{ID: 530, Name: "us_profile_create", Durability: Normal}, {ID: 531, Name: "us_profile_metadata_edit", Durability: Full}, {ID: 530, Name: "wrong", Durability: Full}} {
		turn := &commandTurn{transaction: &transaction{descriptor: d, ctx: context.Background()}}
		if out, e := applyUSProfile(context.Background(), turn, p, 0); e == nil || out != (usProfileEffect{}) {
			t.Fatal("purpose crossed", out, e)
		}
	}
}
