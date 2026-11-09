package plan

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func ipOctetDefinition(s ipOctetShape) stmt.Definition {
	return stmt.Definition{
		ID: stmt.ID(s.id), SQL: s.sql, Kind: stmt.Read, Owner: "store/read IP", CallSites: []string{"store/read/ip_sources.go:ForegroundReader"},
		Params:  s.params,
		Cohorts: []stmt.Cohort{{Name: "present"}, {Name: "empty"}},
		Plan:    stmt.Plan{Hot: true},
	}
}

// The closed-octet lint accepts exactly the registered ID/SQL/parameter shape and
// refuses any drift, even SQL that is otherwise legal.
func TestIPOctetLintPinsExactShape(t *testing.T) {
	if len(ipOctets) != 8 {
		t.Fatal("unexpected IP octet shape inventory", len(ipOctets))
	}
	for _, shape := range ipOctets {
		definition := ipOctetDefinition(shape)
		if issues := LintSQLDefinition(definition); len(issues) != 0 {
			t.Fatal("registered octet shape rejected", shape.id, issues)
		}
		for name, mutate := range map[string]func(*stmt.Definition){
			"sql":    func(d *stmt.Definition) { d.SQL = shape.sql + " OR 1=1" },
			"id":     func(d *stmt.Definition) { d.ID = stmt.ID(shape.id + "_other") },
			"params": func(d *stmt.Definition) { d.Params = append([]stmt.Param(nil), shape.params[:len(shape.params)-1]...) },
			"cold":   func(d *stmt.Definition) { d.Plan.Hot = false },
		} {
			changed := definition
			mutate(&changed)
			if issues := LintSQLDefinition(changed); len(issues) == 0 {
				t.Fatal("octet drift was authorized", shape.id, name)
			}
		}
	}
}

// The native audit proves the unshadowed octet_length builtin and the table the
// shape reads. It runs during Capture as well as here.
func TestIPOctetNativeAudit(t *testing.T) {
	source := readfixture.Foreground(t)
	cfg := readfixture.ReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), source.Path(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, shape := range ipOctets {
		if err := auditIPOctets(context.Background(), conn, shape.table); err != nil {
			t.Fatal("native octet audit", shape.id, err)
		}
	}
	if err := auditIPOctets(context.Background(), conn, "ip_missing_table"); err == nil {
		t.Fatal("missing table passed the native octet audit")
	}
}
