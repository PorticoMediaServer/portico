package writer

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usCredentialSeed = `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES
('us-credential-account','credential-user',X'','us-credential-profile'),
('us-credential-other','credential-other',X'','us-credential-other-profile');
INSERT INTO direct_memberships(account_id,incarnation,role) VALUES
('us-credential-account','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','member');
INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES
('us-credential-profile','us-credential-account','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','Credential profile',1);`

func usCredentialNativeFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usCredentialSeed); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE accounts SET password_hash=? WHERE id='us-credential-account'`, []byte(usCredentialFixturePassword)); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usCredentialEffectDefinitions(), "us_credential_prepare.go", "us_credential_statements.go", "us_credential_statements_test.go")
	// Capture enforces the exact SEARCH/autoindex pins on the canonical layout;
	// no retained golden file outside this helper's assigned files is changed.
	if len(r.Manifest()) != 6 {
		t.Fatal("incomplete native recipe")
	}
	now := time.Date(2100, 1, 1, 0, 0, 2, 0, time.UTC)
	w.now = func() time.Time { return now }
	return w, r
}

// Synthetic storage frame only. IDs 520..522 intentionally are not registered
// by this helper. Borrow BEGIN setup solely for a native fixture, then install
// the exact private descriptor; no accepting facade, real issuer, receipt or
// FULL COMMIT is tested/claimed. All effects roll back at fixture teardown.
func usCredentialNativeTurn(t *testing.T, w *executor, r *stmt.Registry, kind CommandID) *commandTurn {
	t.Helper()
	recipe := testRecipe()
	recipe.limit.Rows = 1000 // includes every refusal exercised in this fixture
	recipe.limit.Pages = 1000
	tx, err := w.begin(t.Context(), SecurityAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	tx.descriptor, _ = describeUSCredentialCommand(kind)
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{id: d.ID, work: Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func usCredentialChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUSCredentialPasswordNativeCAS(t *testing.T) {
	w, r := usCredentialNativeFixture(t)
	turn := usCredentialNativeTurn(t, w, r, usLocalPasswordReplace)
	q := usCredentialTestPassword()
	q.nextHash[7] = 'A'
	p, err := prepareUSPasswordReplace(q)
	if err != nil {
		t.Fatal(err)
	}
	before := usCredentialChanges(t, turn)
	out, err := applyUSPasswordReplace(t.Context(), turn, p)
	if err != nil || out.account != q.target.account || out.profile != q.target.profile || out.epoch != 2 || out.memberIncarnation != q.target.memberIncarnation || out.profileLifetime != q.target.profileLifetime || out.memberRevision != 1 || out.profileRevision != 1 {
		t.Fatal("wrong native password post-result", err)
	}
	if usCredentialChanges(t, turn)-before != 1 {
		t.Fatal("password effect is not one row")
	}
	if out, err := applyUSPasswordReplace(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usPasswordPostResult{}) {
		t.Fatal("stale epoch exposed a result", err)
	}
	var epoch int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT epoch FROM accounts WHERE id='us-credential-other'`).Scan(&epoch); err != nil || epoch != 1 {
		t.Fatal("other account changed", err)
	}
}

func TestUSCredentialPasswordSourceGuards(t *testing.T) {
	w, r := usCredentialNativeFixture(t)
	turn := usCredentialNativeTurn(t, w, r, usLocalPasswordReplace)
	for _, edit := range []func(*usPasswordReplaceRequest){
		func(q *usPasswordReplaceRequest) { q.previousHash[7] = 'A' },
		func(q *usPasswordReplaceRequest) { q.target.memberIncarnation = strings.Repeat("c", 32) },
		func(q *usPasswordReplaceRequest) { q.target.memberRevision = 2 },
		func(q *usPasswordReplaceRequest) { q.target.profileLifetime = strings.Repeat("c", 32) },
		func(q *usPasswordReplaceRequest) { q.target.profileRevision = 2 },
	} {
		q := usCredentialTestPassword()
		edit(&q)
		p, err := prepareUSPasswordReplace(q)
		if err != nil {
			t.Fatal(err)
		}
		before := usCredentialChanges(t, turn)
		if out, err := applyUSPasswordReplace(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usPasswordPostResult{}) || usCredentialChanges(t, turn) != before {
			t.Fatal("stale source/proof comparison accepted", err)
		}
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('us-credential-account','hosted-account')`); err != nil {
		t.Fatal(err)
	}
	p, _ := prepareUSPasswordReplace(usCredentialTestPassword())
	before := usCredentialChanges(t, turn)
	if out, err := applyUSPasswordReplace(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usPasswordPostResult{}) || usCredentialChanges(t, turn) != before {
		t.Fatal("Hosted password replaced locally", err)
	}
}

func TestUSCredentialIssueAndRevokeNativeScalars(t *testing.T) {
	w, r := usCredentialNativeFixture(t)
	turn := usCredentialNativeTurn(t, w, r, usAPICredentialIssue)
	p, err := prepareUSCredentialIssue(usCredentialTestIssue(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	before := usCredentialChanges(t, turn)
	issued, err := applyUSCredentialIssue(t.Context(), turn, p)
	nowMS := w.now().UnixMilli()
	if err != nil || !issued.changed || issued.row.createdMS != nowMS || issued.row.revokedMS != 0 || issued.row.name != "Automation" || issued.row.target != p.target {
		t.Fatal("incorrect actual issuance row", err)
	}
	if usCredentialChanges(t, turn)-before != 1 {
		t.Fatal("issuance touched more than one source row")
	}
	var secret string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT secret FROM api_credentials WHERE id=?`, p.id).Scan(&secret); err != nil || secret != "" {
		t.Fatal("native piece stored plaintext", err)
	}
	// Raw one-row piece does not create authentication families or devices.
	var families, devices int
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM authorization_session_families),(SELECT count(*) FROM identity_devices)`).Scan(&families, &devices); err != nil || families != 0 || devices != 0 {
		t.Fatal("unexpected runtime wiring", err)
	}
	turn.transaction.descriptor, _ = describeUSCredentialCommand(usAPICredentialRevoke)
	q := issued.row
	for _, edit := range []func(*usCredentialRevokeRequest){
		func(q *usCredentialRevokeRequest) { q.secretHash = strings.Repeat("d", 64) },
		func(q *usCredentialRevokeRequest) { q.family = "replacement-family" },
		func(q *usCredentialRevokeRequest) { q.createdMS = 1000 },
	} {
		wrong := q
		edit(&wrong)
		p, err := prepareUSCredentialRevoke(wrong)
		if err != nil {
			t.Fatal(err)
		}
		before := usCredentialChanges(t, turn)
		if out, err := applyUSCredentialRevoke(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usCredentialPostResult{}) || usCredentialChanges(t, turn) != before {
			t.Fatal("reused credential identity accepted", err)
		}
	}
	revoke, err := prepareUSCredentialRevoke(q)
	if err != nil {
		t.Fatal(err)
	}
	before = usCredentialChanges(t, turn)
	out, err := applyUSCredentialRevoke(t.Context(), turn, revoke)
	if err != nil || !out.changed || out.row.revokedMS != nowMS || out.row.createdMS != issued.row.createdMS || usCredentialChanges(t, turn)-before != 1 {
		t.Fatal("incorrect actual revocation row", err)
	}
	if out, err := applyUSCredentialRevoke(t.Context(), turn, revoke); !errors.Is(err, ErrConflict) || out != (usCredentialPostResult{}) {
		t.Fatal("stale source revoke accepted", err)
	}
	revoke, err = prepareUSCredentialRevoke(out.row)
	if err != nil {
		t.Fatal(err)
	}
	before = usCredentialChanges(t, turn)
	again, err := applyUSCredentialRevoke(t.Context(), turn, revoke)
	if err != nil || again.changed || again.row != out.row || usCredentialChanges(t, turn) != before {
		t.Fatal("already-revoked native read changed history", err)
	}
}

func TestUSCredentialIssueExpiryAndTargetAreSameTurn(t *testing.T) {
	w, r := usCredentialNativeFixture(t)
	turn := usCredentialNativeTurn(t, w, r, usAPICredentialIssue)
	q := usCredentialTestIssue()
	q.expiresAt = "2100-01-01T00:01:02Z"
	p, err := prepareUSCredentialIssue(q, w.now().UnixMilli()-1000) // valid then; exactly minute floor now
	if err != nil {
		t.Fatal(err)
	}
	before := usCredentialChanges(t, turn)
	if out, err := applyUSCredentialIssue(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usCredentialPostResult{}) || usCredentialChanges(t, turn) != before {
		t.Fatal("preparation time granted a stale lifetime", err)
	}
	q = usCredentialTestIssue()
	p, err = prepareUSCredentialIssue(q, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd' WHERE id='us-credential-profile'`); err != nil {
		t.Fatal(err)
	}
	before = usCredentialChanges(t, turn)
	if out, err := applyUSCredentialIssue(t.Context(), turn, p); !errors.Is(err, ErrConflict) || out != (usCredentialPostResult{}) || usCredentialChanges(t, turn) != before {
		t.Fatal("retired profile lifetime issued a credential", err)
	}
}

func TestUSCredentialResultReleaseGuards(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "us_credential_statements.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{"applyUSPasswordReplace": false, "applyUSCredentialIssue": false, "applyUSCredentialRevoke": false}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, ok := found[fn.Name.Name]; !ok {
			continue
		}
		for _, s := range fn.Body.List {
			if deferred, ok := s.(*ast.DeferStmt); ok && guardedFinishDefer(fn, deferred) {
				found[fn.Name.Name] = true
			}
		}
	}
	for name, ok := range found {
		if !ok {
			t.Fatal("missing shared final release guard", name)
		}
	}
	if out, err := applyUSPasswordReplace(context.Background(), nil, nil); err == nil || out != (usPasswordPostResult{}) {
		t.Fatal("nil password frame exposed output")
	}
	if out, err := applyUSCredentialIssue(context.Background(), nil, nil); err == nil || out != (usCredentialPostResult{}) {
		t.Fatal("nil issue frame exposed output")
	}
	if out, err := applyUSCredentialRevoke(context.Background(), nil, nil); err == nil || out != (usCredentialPostResult{}) {
		t.Fatal("nil revoke frame exposed output")
	}
	w, r := usCredentialNativeFixture(t)
	turn := usCredentialNativeTurn(t, w, r, usAPICredentialIssue)
	p, _ := prepareUSCredentialIssue(usCredentialTestIssue(), 1000)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := applyUSCredentialIssue(cancelled, turn, p); !errors.Is(err, context.Canceled) || out != (usCredentialPostResult{}) {
		t.Fatal("cancelled request exposed output", err)
	}
	turn.transaction.descriptor.Durability = Normal
	if out, err := applyUSCredentialIssue(t.Context(), turn, p); !errors.Is(err, ErrInvalid) || out != (usCredentialPostResult{}) {
		t.Fatal("NORMAL security descriptor accepted", err)
	}
}

// Three small executable mutants show the negative controls catch weakening
// the old-password hash, member lifetime and profile lifetime comparisons.
func TestUSCredentialSourceGuardMutants(t *testing.T) {
	w, r := usCredentialNativeFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tc := range []struct {
		name         string
		id           stmt.ID
		from, to     string
		removedParam int
		args         []any
	}{
		{"password_hash", usPasswordReplaceWrite, " AND password_hash=?", "", 3, []any{[]byte(usCredentialFixturePassword), "us-credential-account", int64(1), []byte(strings.Replace(usCredentialFixturePassword, "$10$", "$08$", 1)), "us-credential-profile"}},
		{"member_lifetime", usCredentialTargetRead, "m.incarnation=? AND ", "", 4, []any{"us-credential-account", int64(1), "credential-user", false, strings.Repeat("c", 32), int64(1), false, "us-credential-profile", strings.Repeat("b", 32), int64(1), "Credential profile", false}},
		{"profile_lifetime", usCredentialTargetRead, " AND p.lifetime=?", "", 8, []any{"us-credential-account", int64(1), "credential-user", false, strings.Repeat("a", 32), int64(1), false, "us-credential-profile", strings.Repeat("c", 32), int64(1), "Credential profile", false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := r.Lookup(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			def, err := original.Definition()
			if err != nil {
				t.Fatal(err)
			}
			bound, err := original.Bind(tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == usPasswordReplaceWrite {
				var id, profile string
				var epoch int64
				var must bool
				var hash []byte
				if err := bound.QueryRowContext(t.Context(), conn).Scan(&id, &profile, &epoch, &must, &hash); err != sql.ErrNoRows {
					t.Fatal("real old hash guard failed", err)
				}
			} else {
				var one int
				if err := bound.QueryRowContext(t.Context(), conn).Scan(&one); err != sql.ErrNoRows {
					t.Fatal("real lifetime guard failed", err)
				}
			}
			def.SQL = strings.Replace(def.SQL, tc.from, tc.to, 1)
			def.Params = append(def.Params[:tc.removedParam], def.Params[tc.removedParam+1:]...)
			for i := range def.Cohorts {
				bindings := def.Cohorts[i].Bindings
				def.Cohorts[i].Bindings = append(bindings[:tc.removedParam], bindings[tc.removedParam+1:]...)
			}
			mutant := nativeFixtureRegistry(t, conn, w.config, []stmt.Definition{def}, "us_credential_statements_test.go")
			statement, err := mutant.Lookup(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			args := append(append([]any(nil), tc.args[:tc.removedParam]...), tc.args[tc.removedParam+1:]...)
			bound, err = statement.Bind(args...)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == usPasswordReplaceWrite {
				var id, profile string
				var epoch int64
				var must bool
				var hash []byte
				if err := bound.QueryRowContext(t.Context(), conn).Scan(&id, &profile, &epoch, &must, &hash); err != nil || epoch != 2 {
					t.Fatal("mutant not exercised", err)
				}
				if _, err := conn.ExecContext(t.Context(), `UPDATE accounts SET epoch=1 WHERE id='us-credential-account'`); err != nil {
					t.Fatal(err)
				}
			} else {
				var one int
				if err := bound.QueryRowContext(t.Context(), conn).Scan(&one); err != nil || one != 1 {
					t.Fatal("mutant not exercised", err)
				}
			}
		})
	}
}
