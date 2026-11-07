package writer

import (
	"database/sql"
	"testing"
)

func TestUSHostedNativeCAS(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	_ = nativeInventoryFixtureRegistry(t, w, usHostedDefinitions(), "us_hosted_prepare.go", "us_hosted_statements.go", "us_hosted_statements_test.go")
	p := hostedFixture()
	queries := map[string]string{}
	for _, d := range usHostedDefinitions() {
		queries[string(d.ID)] = d.SQL
	}
	run := func(p usHostedPreparation, now int64) error {
		tx, e := w.db.BeginTx(t.Context(), nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, s := range p.effects(now) {
			var got string
			if e = tx.QueryRowContext(t.Context(), queries[string(s.id)], s.args...).Scan(&got); e != nil {
				return e
			}
			if got != s.want {
				t.Fatal("wrong post fact")
			}
		}
		return tx.Commit()
	}
	issue := p
	issue.kind = usHostedChallengeIssue
	if e := run(issue, p.nowMS); e != nil {
		t.Fatal(e)
	}
	wrong := p
	wrong.installation = "other"
	if e := run(wrong, p.nowMS); e != sql.ErrNoRows {
		t.Fatal(e)
	}
	if e := run(p, p.nowMS); e != nil {
		t.Fatal(e)
	}
	if e := run(p, p.nowMS); e != sql.ErrNoRows {
		t.Fatal("replay", e)
	}
	if _, e := w.db.Exec(`INSERT INTO hosted_profile_revisions VALUES('server','account','profile',1,2,3,4)`); e != nil {
		t.Fatal(e)
	}
	rev := p
	rev.kind = usHostedProfileAccept
	rev.profile = "profile"
	rev.source = usHostedRevisions{1, 2, 3, 4}
	rev.next = usHostedRevisions{2, 3, 4, 5}
	if e := run(rev, p.nowMS); e != nil {
		t.Fatal(e)
	}
	if e := run(rev, p.nowMS); e != sql.ErrNoRows {
		t.Fatal("stale source", e)
	}
}

func TestUSHostedReleaseGuard(t *testing.T) {
	p, _ := prepareUSHosted(hostedFixture())
	if out, err := applyUSHosted(t.Context(), nil, p, p.nowMS); err == nil || out != (usHostedNativeResult{}) {
		t.Fatal("unheld prefix returned facts")
	}
}
