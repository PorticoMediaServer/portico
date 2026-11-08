package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUSPolicyPlans = flag.Bool("record-us-policy-plans", false, "record US policy point-read Capture plans")

// Raw storage fixture, including multibyte text to distinguish bytes from SQL
// characters. This is not an issued principal or an accepting policy command.
const usPolicySeed = `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES
('us-target','us-target',X'','us-profile'),('us-other','us-other',X'','us-other-profile');
INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES
('us-target','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member','["música"]'),
('us-other','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','member','[]');
INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,allowed_libraries) VALUES
('us-profile','us-target','ffffffffffffffffffffffffffffffff','Target',1,'[]'),
('us-inherit','us-target','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','Inherited',0,NULL);`

func usPolicyNativeFixture(t *testing.T) (*sql.Conn, *stmt.Registry, plan.Golden) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := conn.ExecContext(t.Context(), usPolicySeed); err != nil {
		t.Fatal(err)
	}
	defs := usPolicyDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{
		ConnectionPolicy: w.config, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(usPolicySeed))), Statistics: plan.Unanalyzed,
		Implementations: "US policy point facts; canonical schema; raw multibyte/NULL/empty target fixture; no issuer authority, admission or hold qualification",
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture changed source facts", before, after, err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	return conn, registry, golden
}

func TestUSPolicyStatementPlans(t *testing.T) {
	_, registry, golden := usPolicyNativeFixture(t)
	if len(registry.Manifest()) != 4 || len(golden.Plans) != 16 || golden.ForeignKeys != nil {
		t.Fatal("unexpected read Capture inventory", len(golden.Plans))
	}
	for _, d := range registry.Manifest() {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || d.Kind != stmt.Read {
			t.Fatal("point read must be hot without waivers", d.ID)
		}
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "us-policy-plans.json.gz")
	if *recordUSPolicyPlans {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(expected), bytes.TrimSpace(actual)) {
		t.Fatal("native US point plan changed; inspect before recording")
	}
}

func TestUSPolicyNativeLifetimeAndOctets(t *testing.T) {
	conn, registry, _ := usPolicyNativeFixture(t)
	query := func(id stmt.ID, args ...any) stmt.Row {
		t.Helper()
		statement, err := registry.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := statement.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		return bound.QueryRowContext(t.Context(), conn)
	}
	member := []any{"us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}
	var roleBytes, listBytes int64
	if err := query(usMemberPolicyOctets, member...).Scan(&roleBytes, &listBytes); err != nil {
		t.Fatal(err)
	}
	var role, libraries string
	var revision, disabled int64
	if err := query(usMemberPolicyFacts, member...).Scan(&role, &revision, &disabled, &libraries); err != nil {
		t.Fatal(err)
	}
	if roleBytes != int64(len(role)) || listBytes != int64(len(libraries)) || listBytes != 11 || revision != 1 || disabled != 0 {
		t.Fatal(roleBytes, listBytes, role, libraries, revision, disabled)
	}
	for _, tc := range []struct {
		profile, lifetime string
		inherit           bool
	}{
		{"us-profile", "ffffffffffffffffffffffffffffffff", false},
		{"us-inherit", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
	} {
		args := append(append([]any(nil), member...), tc.profile, tc.lifetime)
		var profileBytes, profileRevision, deleted, primary int64
		if err := query(usProfilePolicyOctets, args...).Scan(&roleBytes, &listBytes, &profileBytes); err != nil {
			t.Fatal(err)
		}
		var policy sql.NullString
		if err := query(usProfilePolicyFacts, args...).Scan(&role, &revision, &disabled, &libraries, &profileRevision, &deleted, &primary, &policy); err != nil {
			t.Fatal(err)
		}
		if policy.Valid == tc.inherit || profileBytes != int64(len(policy.String)) || profileRevision != 1 || deleted != 0 {
			t.Fatal(tc, policy, profileBytes)
		}
	}
	for _, d := range usPolicyDefinitions() {
		for _, cohort := range d.Cohorts {
			if cohort.Name == "current" || cohort.Name == "explicit_empty" || cohort.Name == "inherit" {
				continue
			}
			// A lifetime/account mismatch yields no target facts. It can never
			// fall back to a same-ID row from another lifetime or account.
			statement, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := statement.Bind(cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := bound.QueryContext(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			found := rows.Next()
			readErr, closeErr := rows.Err(), rows.Close()
			if found || readErr != nil || closeErr != nil {
				t.Fatal(d.ID, cohort.Name, found, readErr, closeErr)
			}
		}
	}
}

// A disabled membership and a retired profile are still readable as MARKED
// facts; the caller must refuse them. They are never silently filtered into a
// missing-target fallback or treated as an inheriting/unrestricted policy.
func TestUSPolicyNativeHiddenRetiredFacts(t *testing.T) {
	conn, registry, _ := usPolicyNativeFixture(t)
	if _, err := conn.ExecContext(t.Context(), `UPDATE direct_memberships SET disabled=1 WHERE account_id='us-target'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `UPDATE direct_profiles SET deleted=1 WHERE id='us-profile'`); err != nil {
		t.Fatal(err)
	}
	query := func(id stmt.ID, args ...any) stmt.Row {
		t.Helper()
		statement, err := registry.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := statement.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		return bound.QueryRowContext(t.Context(), conn)
	}
	member := []any{"us-target", usPolicyMemberLifetime}
	var role, libraries string
	var revision, disabled int64
	if err := query(usMemberPolicyFacts, member...).Scan(&role, &revision, &disabled, &libraries); err != nil || disabled != 1 || revision != 1 {
		t.Fatal("disabled member facts", role, revision, disabled, libraries, err)
	}
	var profileRevision, deleted, primary int64
	var policy sql.NullString
	if err := query(usProfilePolicyFacts, append(member, usPolicyProfileID, usPolicyProfileLife)...).Scan(&role, &revision, &disabled, &libraries, &profileRevision, &deleted, &primary, &policy); err != nil || deleted != 1 || profileRevision != 1 {
		t.Fatal("retired profile facts", revision, disabled, profileRevision, deleted, primary, policy, err)
	}
	// The native byte preflight remains exact; hiding is a caller decision, not
	// a change to the closed read definition.
	var roleBytes, listBytes, profileBytes int64
	if err := query(usProfilePolicyOctets, append(member, usPolicyProfileID, usPolicyProfileLife)...).Scan(&roleBytes, &listBytes, &profileBytes); err != nil {
		t.Fatal(err)
	}
	if roleBytes != int64(len(role)) || listBytes != int64(len(libraries)) || profileBytes != int64(len(policy.String)) || profileBytes != 2 {
		t.Fatal(roleBytes, listBytes, profileBytes)
	}
}
