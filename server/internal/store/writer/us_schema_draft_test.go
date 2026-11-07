package writer

import (
	_ "embed"
	"strings"
	"testing"
)

// US-001 local fixture ONLY. INT owns adoption/canonical embedding. This file
// cannot be a production bootstrap or migration and is removed when canonical
// security group adoption makes the extra fixture delta unnecessary.
//
//go:embed testdata/us-ddl-draft.sql
var usSchemaDraftSQL string

func usSchemaDraftFixture(t *testing.T) *executor {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usSchemaDraftSQL); err != nil {
		t.Fatal("US-001 exact draft delta", err)
	}
	return w
}

func TestUSSchemaDraftLoadsAndRejectsNullCustody(t *testing.T) {
	w := usSchemaDraftFixture(t)
	rows, err := w.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	found := rows.Next()
	readErr, closeErr := rows.Err(), rows.Close()
	if found || readErr != nil || closeErr != nil {
		t.Fatal("draft FK failure", found, readErr, closeErr)
	}
	// An omitted nullable witness must not pass a SQLite CHECK as NULL.
	for _, sqlText := range []string{
		`INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_profile_id,original_family_id,created_ms) VALUES('bad-auth',zeroblob(16),'authenticated','server','account','profile','family',0)`,
		`INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,capability_kind,capability_id,created_ms) VALUES('bad-cap',zeroblob(16),'capability','server','invitation','invite',0)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sqlText); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatal("expected native nullable-custody CHECK rejection", err)
		}
	}
}

func TestUSFamilyBirthRequiresExplicitSecurityKind(t *testing.T) {
	w := usSchemaDraftFixture(t)
	const columns = `id,server_id,account_id,profile_id,authority,role,epoch,current_generation,authorization_horizon,command_member_incarnation,command_profile_lifetime,command_incarnation`
	const values = `'family','server','account','profile','local','owner',1,1,'2026-11-08T14:00:00Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',zeroblob(16)`
	for _, value := range []string{"omitted", "NULL", "-1", "2", "'interactive'"} {
		query := `INSERT INTO authorization_session_families(` + columns + `) VALUES(` + values + `)`
		if value != "omitted" {
			query = `INSERT INTO authorization_session_families(` + columns + `,security_kind) VALUES(` + values + `,` + value + `)`
		}
		if _, err := w.db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("accepted unspecified/invalid birth kind %s", value)
		}
	}
	for _, kind := range []int{0, 1} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO authorization_session_families(`+columns+`,security_kind) VALUES(`+values+`,?)`, kind); err != nil {
			t.Fatal("explicit birth kind rejected", kind, err)
		}
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM authorization_session_families WHERE id='family'`); err != nil {
			t.Fatal(err)
		}
	}
}
