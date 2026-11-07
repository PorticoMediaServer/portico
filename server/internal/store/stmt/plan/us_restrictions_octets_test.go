package plan

import (
	"context"
	"database/sql/driver"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func usRestrictionOctetTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "us.restrictions.source.octets", SQL: usRestrictionSourceOctetsSQL, Kind: stmt.Read, Owner: "US restriction preflight controls", CallSites: []string{"us_restrictions_octets_test.go"}, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"profile"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_profile_restrictions_1"}, Seeks: []string{"profile_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "profile_restrictions", Name: "sqlite_autoindex_profile_restrictions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}}}}}}
}
func TestUSRestrictionOctetClosedSourceAndKey(t *testing.T) {
	for _, key := range []string{"profile_id", "profile_id,revision"} {
		t.Run(key, func(t *testing.T) {
			c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
			ddl := `CREATE TABLE profile_restrictions(profile_id TEXT NOT NULL,revision INTEGER NOT NULL,rating_system TEXT NOT NULL,maximum_age_rating TEXT NOT NULL,blocked_labels TEXT NOT NULL,PRIMARY KEY(` + key + `)) STRICT;`
			if _, err := c.ExecContext(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			d := usRestrictionOctetTestDefinition()
			g, err := usCaptureOctet(t, c, f, p, d)
			if key != "profile_id" {
				if err == nil {
					t.Fatal("changed actual key accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err != nil {
				t.Fatal(err)
			}
			for _, change := range []func(*stmt.Definition){
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "r.rating_system", "r.maximum_age_rating", 1) },
				func(d *stmt.Definition) { d.Params[0].Nullable = true },
				func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer; d.Cohorts[0].Bindings[0] = int64(1) },
				func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Name = "revision" },
				func(d *stmt.Definition) { d.Plan.Hot = false },
			} {
				bad := usRestrictionOctetTestDefinition()
				change(&bad)
				g, err := usCaptureOctet(t, c, f, p, bad)
				if err == nil {
					t.Fatal("changed closed definition accepted")
				}
				if _, err = stmt.New([]stmt.Definition{bad}, g.Receipt()); err == nil {
					t.Fatal("refused shape minted receipt")
				}
			}
			if _, err := c.ExecContext(t.Context(), `CREATE TEMP TABLE profile_restrictions(profile_id TEXT PRIMARY KEY,revision INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if _, err := usCaptureOctet(t, c, f, p, d); err == nil {
				t.Fatal("TEMP shadow accepted")
			}
		})
	}
}
func TestUSRestrictionOctetRejectsShadowedBuiltin(t *testing.T) {
	const env = "PORTICO_US_RESTRICTION_OCTET_SHADOW"
	if os.Getenv(env) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUSRestrictionOctetRejectsShadowedBuiltin$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated shadow control %v\n%s", err, out)
		}
		return
	}
	sqlite.MustRegisterScalarFunction("octet_length", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(0), nil })
	c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
	if _, err := c.ExecContext(t.Context(), `CREATE TABLE profile_restrictions(profile_id TEXT PRIMARY KEY,revision INTEGER,rating_system TEXT,maximum_age_rating TEXT,blocked_labels TEXT) STRICT`); err != nil {
		t.Fatal(err)
	}
	g, err := usCaptureOctet(t, c, f, p, usRestrictionOctetTestDefinition())
	if err == nil || !strings.Contains(err.Error(), "unshadowed native octet_length") {
		t.Fatal("shadowed builtin accepted", err)
	}
	if _, err := stmt.New([]stmt.Definition{usRestrictionOctetTestDefinition()}, g.Receipt()); err == nil {
		t.Fatal("shadow minted receipt")
	}
}

func usRestrictionTargetTestDefinition() stmt.Definition {
	d := usOctetTestDefinition(true)
	d.ID, d.SQL = "us.restrictions.target.octets", usRestrictionTargetOctetsSQL
	d.CallSites = []string{"us_restrictions_octets_test.go"}
	d.Params = []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}, {Name: "epoch", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "lifetime", Type: stmt.Text}, {Name: "profile_revision", Type: stmt.Integer}}
	d.Cohorts = []stmt.Cohort{{Name: "point", Bindings: []any{"account", "member-life", int64(1), int64(1), "profile", "profile-life", int64(1)}}}
	d.Plan.Indexes = []string{"sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_profiles_2"}
	d.Plan.Seeks = []string{"account_id=?", "id=?", "account_id=? AND id=?"}
	d.Plan.AutoIndexes = append(d.Plan.AutoIndexes[:1], stmt.AutoIndexBinding{Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}, d.Plan.AutoIndexes[1])
	return d
}

func TestUSRestrictionOctetTargetClosedKeysAndDefinition(t *testing.T) {
	for _, keys := range [][2]string{{"account_id", "account_id,id"}, {"account_id,incarnation", "account_id,id"}, {"account_id", "id,account_id"}} {
		t.Run(keys[0]+"/"+keys[1], func(t *testing.T) {
			c, f, p := usOctetTestFixture(t, keys[0], keys[1])
			if _, err := c.ExecContext(t.Context(), `CREATE TABLE accounts(id TEXT PRIMARY KEY,epoch INTEGER NOT NULL) STRICT; INSERT INTO accounts VALUES('account',1); ALTER TABLE direct_memberships ADD COLUMN revision INTEGER NOT NULL DEFAULT 1; ALTER TABLE direct_memberships ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0; ALTER TABLE direct_profiles ADD COLUMN revision INTEGER NOT NULL DEFAULT 1; ALTER TABLE direct_profiles ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;`); err != nil {
				t.Fatal(err)
			}
			d := usRestrictionTargetTestDefinition()
			_, err := usCaptureOctet(t, c, f, p, d)
			if keys != [2]string{"account_id", "account_id,id"} {
				if err == nil {
					t.Fatal("changed native target key accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range []func(*stmt.Definition){
				func(d *stmt.Definition) {
					d.SQL = strings.Replace(d.SQL, "octet_length(m.role)", "octet_length(m.allowed_libraries)", 1)
				},
				func(d *stmt.Definition) { d.Params[2].Nullable = true },
				func(d *stmt.Definition) { d.Plan.Seeks[0] = "incarnation=?" },
				func(d *stmt.Definition) { d.Plan.Hot = false },
				func(d *stmt.Definition) { d.Plan.AutoIndexes[1].Origin = "u" },
			} {
				bad := usRestrictionTargetTestDefinition()
				change(&bad)
				if _, err := usCaptureOctet(t, c, f, p, bad); err == nil {
					t.Fatal("changed target preflight accepted")
				}
			}
		})
	}
}
