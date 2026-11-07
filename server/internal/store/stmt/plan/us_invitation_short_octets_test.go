package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usShortOctetTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "us.invitation.short.source.octets", SQL: usInvitationShortSourceOctetsSQL, Kind: stmt.Read, Owner: "US short preflight controls", CallSites: []string{"us_invitation_short_octets_test.go"}, Params: []stmt.Param{{Name: "invitation", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"invite"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitation_short_codes_1"}, Seeks: []string{"invitation_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitation_short_codes", Name: "sqlite_autoindex_access_invitation_short_codes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "invitation_id", Collation: "BINARY"}}}}}}
}
func TestUSShortOctetClosedKeyAndProjection(t *testing.T) {
	for _, key := range []string{"invitation_id", "invitation_id,purpose"} {
		c, f, p := usOctetTestFixture(t, "account_id", "account_id,id")
		if _, err := c.ExecContext(t.Context(), `CREATE TABLE access_invitation_short_codes(invitation_id TEXT NOT NULL,lookup_hash TEXT NOT NULL,binding_hash TEXT NOT NULL,server_id TEXT NOT NULL,sign_in_kind TEXT NOT NULL,purpose TEXT NOT NULL,PRIMARY KEY(`+key+`)) STRICT;INSERT INTO access_invitation_short_codes VALUES('invite','lookup','binding','界','local','accept');`); err != nil {
			t.Fatal(err)
		}
		d := usShortOctetTestDefinition()
		g, err := usCaptureOctet(t, c, f, p, d)
		if key != "invitation_id" {
			if err == nil {
				t.Fatal("partial source key accepted")
			}
			continue
		}
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
		bound, err := s.Bind("invite")
		if err != nil {
			t.Fatal(err)
		}
		var got [6]int64
		if err := bound.QueryRowContext(t.Context(), c).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5]); err != nil || got != [6]int64{6, 6, 7, 3, 5, 6} {
			t.Fatal("independent native projection", got, err)
		}
		for _, change := range []func(*stmt.Definition){func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "s.server_id", "s.lookup_hash", 1) }, func(d *stmt.Definition) { d.Params[0].Nullable = true }, func(d *stmt.Definition) { d.Plan.Hot = false }, func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Name = "purpose" }} {
			bad := usShortOctetTestDefinition()
			change(&bad)
			if _, err := usCaptureOctet(t, c, f, p, bad); err == nil {
				t.Fatal("changed closed source point accepted")
			}
		}
		if _, err := c.ExecContext(t.Context(), `CREATE TEMP TABLE access_invitation_short_codes(invitation_id TEXT PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if _, err := usCaptureOctet(t, c, f, p, d); err == nil {
			t.Fatal("TEMP source shadow accepted")
		}
	}
}
