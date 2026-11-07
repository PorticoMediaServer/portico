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

func usInvitationOctetTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "us.invitation.source.octets", SQL: usInvitationSourceOctetsSQL, Kind: stmt.Read, Owner: "US invitation preflight controls", CallSites: []string{"us_invitation_octets_test.go"}, Params: []stmt.Param{{Name: "invitation", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"invite", int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitations_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitations", Name: "sqlite_autoindex_access_invitations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}}
}
func TestUSInvitationOctetClosedSourceAndKey(t *testing.T) {
	for _, key := range []string{"id", "id,revision"} {
		t.Run(key, func(t *testing.T) {
			c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
			ddl := `CREATE TABLE access_invitations(id TEXT NOT NULL,revision INTEGER NOT NULL,notes TEXT,code_hash TEXT NOT NULL,role TEXT NOT NULL,allowed_libraries TEXT NOT NULL,state TEXT NOT NULL,account_id TEXT,bound_username TEXT NOT NULL,max_streams_mode TEXT NOT NULL,max_content_rating TEXT NOT NULL,PRIMARY KEY(` + key + `)) STRICT;`
			if _, err := c.ExecContext(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			d := usInvitationOctetTestDefinition()
			g, err := usCaptureOctet(t, c, f, p, d)
			if key != "id" {
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
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "i.notes", "i.role", 1) },
				func(d *stmt.Definition) { d.Params[0].Nullable = true },
				func(d *stmt.Definition) { d.Params[1].Type = stmt.Text; d.Cohorts[0].Bindings[1] = "1" },
				func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Name = "revision" },
				func(d *stmt.Definition) { d.Plan.Hot = false },
			} {
				bad := usInvitationOctetTestDefinition()
				change(&bad)
				g, err := usCaptureOctet(t, c, f, p, bad)
				if err == nil {
					t.Fatal("changed closed definition accepted")
				}
				if _, err = stmt.New([]stmt.Definition{bad}, g.Receipt()); err == nil {
					t.Fatal("refused shape minted receipt")
				}
			}
			if _, err := c.ExecContext(t.Context(), `CREATE TEMP TABLE access_invitations(id TEXT PRIMARY KEY,revision INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if _, err := usCaptureOctet(t, c, f, p, d); err == nil {
				t.Fatal("TEMP shadow accepted")
			}
		})
	}
}
func TestUSInvitationOctetRejectsShadowedBuiltin(t *testing.T) {
	const env = "PORTICO_US_INVITATION_OCTET_SHADOW"
	if os.Getenv(env) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUSInvitationOctetRejectsShadowedBuiltin$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated shadow control %v\n%s", err, out)
		}
		return
	}
	sqlite.MustRegisterScalarFunction("octet_length", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(0), nil })
	c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
	if _, err := c.ExecContext(t.Context(), `CREATE TABLE access_invitations(id TEXT PRIMARY KEY,revision INTEGER,notes TEXT,code_hash TEXT,role TEXT,allowed_libraries TEXT,state TEXT,account_id TEXT,bound_username TEXT,max_streams_mode TEXT,max_content_rating TEXT) STRICT`); err != nil {
		t.Fatal(err)
	}
	g, err := usCaptureOctet(t, c, f, p, usInvitationOctetTestDefinition())
	if err == nil || !strings.Contains(err.Error(), "unshadowed native octet_length") {
		t.Fatal("shadowed builtin accepted", err)
	}
	if _, err := stmt.New([]stmt.Definition{usInvitationOctetTestDefinition()}, g.Receipt()); err == nil {
		t.Fatal("shadow minted receipt")
	}
}
