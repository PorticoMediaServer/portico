package plan

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func waived(d stmt.Definition, stage, form, match string) bool {
	for _, w := range d.Plan.Waivers {
		if w.StatementID == d.ID && w.Stage == stage && w.Form == form && w.Match == match && strings.TrimSpace(w.Reason) != "" {
			return true
		}
	}
	return false
}

// CheckEQP trusts SQLite's own parse/planner output independently of the lint.
// Exact per-statement detail waivers are the only exceptions, including scans;
// legacy AllowedScans metadata and a SQL-lint waiver confer no EQP permission.
func CheckEQP(d stmt.Definition, eqp Rows) error {
	for _, row := range eqp.Values {
		if len(row) != 4 {
			return fmt.Errorf("plan %s: unexpected EQP shape", d.ID)
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("plan %s: invalid EQP detail", d.ID)
		}
		upper := strings.ToUpper(detail)
		forms := []string{}
		for _, form := range []string{"SUBQUERY", "CO-ROUTINE", "MATERIALIZE", "MULTI-INDEX OR", "USE TEMP B-TREE"} {
			if strings.Contains(upper, form) {
				forms = append(forms, form)
			}
		}
		if strings.Contains(upper, "USING AUTOMATIC") {
			forms = append(forms, "AUTOMATIC INDEX")
		}
		if d.Plan.Hot && strings.HasPrefix(upper, "SCAN ") {
			forms = append(forms, "SCAN")
		}
		for _, form := range forms {
			if !waived(d, "eqp", form, detail) {
				return fmt.Errorf("plan %s: forbidden plan %s: %s", d.ID, form, detail)
			}
		}
	}
	return nil
}
