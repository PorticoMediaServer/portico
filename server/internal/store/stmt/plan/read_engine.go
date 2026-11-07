package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This domain owns ONE exact metadata point. It is not a generic zero-argument
// function allowance, native-slot descriptor, bank proof or receipt loader.
const readEngineID stmt.ID = "read.full.native.engine"
const readEngineSQL = "SELECT sqlite_version(),sqlite_source_id() FROM search_revision WHERE singleton=?"
const readEngineSourceSQL = "SELECT singleton,singleton FROM search_revision WHERE singleton=?"

func readEngineDefinition(d stmt.Definition) bool {
	return d.ID == readEngineID && d.SQL == readEngineSQL && d.Kind == stmt.Read && d.Plan.Hot &&
		slices.Equal(d.Params, []stmt.Param{{Name: "singleton", Type: stmt.Integer}}) &&
		slices.Equal(d.Plan.PrimaryKeys, []string{"search_revision"}) && len(d.Plan.Indexes) == 0 && len(d.Plan.Seeks) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeRead == nil && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}

// Only the source-identical scalar-free form is parsed for this already exact
// definition. The original SQL executes and is retained verbatim in all plans.
// Public lint continues to reject the two zero-argument functions.
func readEngineSyntax(d stmt.Definition) []LintIssue {
	if readEngineDefinition(d) {
		return lintSQL(readEngineSourceSQL, false)
	}
	return lintSQL(d.SQL, closedOctetDefinition(d))
}

func auditReadEngine(ctx context.Context, conn *sql.Conn) error {
	rows, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	counts := map[string]int{"sqlite_version": 0, "sqlite_source_id": 0}
	for _, r := range rows.Values {
		if len(r) != 6 {
			return fmt.Errorf("read engine: invalid native function metadata")
		}
		name, ok := r[0].(string)
		if !ok {
			return fmt.Errorf("read engine: invalid function name")
		}
		name = strings.ToLower(name)
		if _, wanted := counts[name]; !wanted {
			continue
		}
		arity, ok := r[4].(int64)
		if !ok {
			return fmt.Errorf("read engine: invalid function arity")
		}
		if arity != 0 && arity != -1 {
			continue
		}
		if r[1] != int64(1) || arity != 0 || r[2] != "s" {
			return fmt.Errorf("read engine: unshadowed native %s required", name)
		}
		counts[name]++
	}
	for name, n := range counts {
		if n != 1 {
			return fmt.Errorf("read engine: one native %s required", name)
		}
	}
	return nil
}

func checkReadEnginePoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH search_revision USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("read engine: exact singleton native point required")
	}
	return nil
}
