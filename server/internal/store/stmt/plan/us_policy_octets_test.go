package plan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func usOctetTestDefinition(profile bool) stmt.Definition {
	id, sqlText := "us.policy.member.octets", usMemberPolicyOctetsSQL
	params := []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "member_incarnation", Type: stmt.Text}}
	bindings := []any{"account", "member-life"}
	p := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1"}, Seeks: []string{"account_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}}}
	if profile {
		id, sqlText = "us.policy.profile.octets", usProfilePolicyOctetsSQL
		params = append(params, stmt.Param{Name: "profile", Type: stmt.Text}, stmt.Param{Name: "profile_lifetime", Type: stmt.Text})
		bindings = append(bindings, "profile", "profile-life")
		p.Indexes = append(p.Indexes, "sqlite_autoindex_direct_profiles_2")
		p.Seeks = append(p.Seeks, "account_id=? AND id=?")
		p.AutoIndexes = append(p.AutoIndexes, stmt.AutoIndexBinding{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}})
	}
	return stmt.Definition{ID: stmt.ID(id), SQL: sqlText, Kind: stmt.Read, Owner: "US native octet controls", CallSites: []string{"us_policy_octets_test.go"}, Params: params, Cohorts: []stmt.Cohort{{Name: "point", Bindings: bindings}}, Plan: p}
}

// Tiny key-layout falsifier fixture, NOT canonical schema or hold qualification.
// Canonical full-schema Capture/VM goldens are owned by writer's US tests.
func usOctetTestFixture(t *testing.T, memberKey, profileKey string) (*sql.Conn, Fixture, IndexPolicy) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 512, Synchronous: sqliteconn.Full}
	db, _, err := sqliteconn.Open(t.Context(), filepath.Join(t.TempDir(), "us-key.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	ddl := `CREATE TABLE direct_memberships(account_id TEXT NOT NULL,incarnation TEXT NOT NULL,role TEXT NOT NULL,allowed_libraries TEXT NOT NULL,PRIMARY KEY(` + memberKey + `)) STRICT;
CREATE TABLE direct_profiles(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,lifetime TEXT NOT NULL,allowed_libraries TEXT,UNIQUE(` + profileKey + `)) STRICT;
CREATE INDEX us_test_role ON direct_memberships(role) WHERE role='member';
INSERT INTO direct_memberships VALUES('account','member-life','member','["música"]');
INSERT INTO direct_profiles VALUES('profile','account','profile-life',NULL);`
	if _, err := conn.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	// The tiny falsifier schema owns one sentinel partial index; the full
	// canonical fixture in writer uses INT's entire index-policy artifact.
	policy, err := ReadIndexPolicy([]byte(`{"indexes":[{"index":"us_test_role","predicate":"role='member'","choice":"repeat_exact_predicate"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return conn, Fixture{ConnectionPolicy: cfg, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))), Statistics: Unanalyzed, Implementations: "US minimal native key/control fixture; no domain authority or qualification"}, policy
}

func usCaptureOctet(t *testing.T, c *sql.Conn, f Fixture, p IndexPolicy, d stmt.Definition) (Golden, error) {
	t.Helper()
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	return Capture(t.Context(), c, catalogue, f, p)
}

func TestUSPolicyOctetClosedShapes(t *testing.T) {
	c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
	for _, profile := range []bool{false, true} {
		d := usOctetTestDefinition(profile)
		if !usPolicyOctetDefinition(d, profile) {
			t.Fatal("test shape drift", d.ID)
		}
		if len(LintSQL(d.SQL)) == 0 || len(LintSQLOn(t.Context(), c, d.SQL)) == 0 {
			t.Fatal("generic scalar grammar widened")
		}
		g, err := usCaptureOctet(t, c, f, p, d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err != nil {
			t.Fatal(err)
		}
		for _, change := range []string{"retarget", "key", "nullable"} {
			t.Run(string(d.ID)+"/"+change, func(t *testing.T) {
				bad := usOctetTestDefinition(profile)
				switch change {
				case "retarget":
					bad.SQL = strings.Replace(bad.SQL, "m.incarnation=?", "m.account_id=?", 1)
				case "key":
					bad.Plan.AutoIndexes[0].Columns[0].Name = "incarnation"
				case "nullable":
					bad.Params[0].Nullable = true
				}
				g, err := usCaptureOctet(t, c, f, p, bad)
				if err == nil {
					t.Fatal("altered point accepted", change)
				}
				if _, err := stmt.New([]stmt.Definition{bad}, g.Receipt()); err == nil {
					t.Fatal("refused point minted a receipt")
				}
			})
		}
	}
}

func TestUSPolicyOctetNativeKeys(t *testing.T) {
	for _, tc := range []struct {
		name, member, profile string
		joined                bool
	}{
		{"member_key_width", "account_id,incarnation", "account_id,id", false},
		{"profile_key_width", "account_id", "account_id,id,lifetime", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, p := usOctetTestFixture(t, tc.member, tc.profile)
			d := usOctetTestDefinition(tc.joined)
			g, err := usCaptureOctet(t, c, f, p, d)
			if err == nil {
				t.Fatal("changed native key accepted")
			}
			if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
				t.Fatal("wrong native key minted receipt")
			}
		})
	}
}

// Expected values come from fixed stored bytes, independently of either
// production SQL constant or the definition validator that shares that SQL.
// In particular member-life is 11 bytes, while the selected role is 6.
func TestUSPolicyOctetActualProjection(t *testing.T) {
	c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
	// Give the policy a length distinct from incarnation, role and both keys.
	if _, err := c.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries='["música","界"]' WHERE account_id='account'`); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []bool{false, true} {
		d := usOctetTestDefinition(profile)
		g, err := usCaptureOctet(t, c, f, p, d)
		if err != nil {
			t.Fatal(err)
		}
		r, err := stmt.New([]stmt.Definition{d}, g.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		s, err := r.Lookup(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := s.Bind(d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []struct {
			policy any
			bytes  int64
		}{{nil, 0}, {"[]", 2}, {`["界"]`, 7}} {
			if _, err := c.ExecContext(t.Context(), `UPDATE direct_profiles SET allowed_libraries=? WHERE id='profile'`, value.policy); err != nil {
				t.Fatal(err)
			}
			var role, memberPolicy, profilePolicy int64
			dest := []any{&role, &memberPolicy}
			if profile {
				dest = append(dest, &profilePolicy)
			}
			if err := bound.QueryRowContext(t.Context(), c).Scan(dest...); err != nil {
				t.Fatal(err)
			}
			// The member policy is literal ["música","界"]: 17 UTF-8 bytes.
			if role != 6 || memberPolicy != 17 || profile && profilePolicy != value.bytes {
				t.Fatalf("wrong native octet projection for %s: role=%d member=%d profile=%d", d.ID, role, memberPolicy, profilePolicy)
			}
		}
	}
}

func TestUSPolicyOctetRejectsShadowedFunction(t *testing.T) {
	const flag = "PORTICO_US_POLICY_OCTET_SHADOW"
	if os.Getenv(flag) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUSPolicyOctetRejectsShadowedFunction$", "-test.count=1")
		cmd.Env = append(os.Environ(), flag+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated scalar-shadow control: %v\n%s", err, output)
		}
		return
	}
	sqlite.MustRegisterScalarFunction("octet_length", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(0), nil })
	c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
	for _, profile := range []bool{false, true} {
		d := usOctetTestDefinition(profile)
		g, err := usCaptureOctet(t, c, f, p, d)
		if err == nil || !strings.Contains(err.Error(), "unshadowed native octet_length") {
			t.Fatal("shadowed function accepted", err)
		}
		if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
			t.Fatal("shadowed function minted receipt")
		}
	}
}
