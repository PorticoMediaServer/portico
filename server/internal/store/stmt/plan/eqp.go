package plan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These are the four SEARCH shapes in the reviewed 3.53.4 baselines. Match the
// entire row, including simple bound constraints: new planner wording, skip
// scans, expression indexes and trailing operators require explicit approval.
const eqpName = `[A-Z_][A-Z_0-9]*`
const eqpConstraint = eqpName + `(?:=|>=|<=|>|<)\?`

var expectedSearch = regexp.MustCompile(`^SEARCH ` + eqpName +
	` USING (?:(?:COVERING )?INDEX ` + eqpName + `|(?:INTEGER )?PRIMARY KEY)` +
	` \(` + eqpConstraint + `(?: AND ` + eqpConstraint + `)*\)(?: LEFT-JOIN)?$`)

func waived(d stmt.Definition, stage, form, match string) bool {
	for _, w := range d.Plan.Waivers {
		if w.StatementID == d.ID && w.Stage == stage && w.Form == form && w.Match == match && strings.TrimSpace(w.Reason) != "" {
			return true
		}
	}
	return false
}

// CheckEQP allows only reviewed SEARCH shapes from SQLite's own parse/planner
// output, independently of the lint. All other rows, including scans, flag.
// Exact per-statement detail waivers are the only exceptions, including scans;
// legacy AllowedScans metadata grants no permission. Grammar is never waivable.
// This checks rows only: it cannot certify SQL sources, Hot metadata or a
// registry. Complete qualification requires connection-aware Capture.
func CheckEQP(d stmt.Definition, eqp Rows) error {
	if d.Plan.NativeRead != nil {
		return checkFTSReadEQP(d, eqp)
	}
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
		for _, form := range []string{"SUBQUERY", "FOR IN-OPERATOR", "BLOOM FILTER", "CO-ROUTINE", "MATERIALIZE", "MULTI-INDEX OR", "USE TEMP B-TREE"} {
			if strings.Contains(upper, form) {
				forms = append(forms, form)
			}
		}
		if strings.Contains(upper, "CORRELATED") && !strings.Contains(upper, "SUBQUERY") {
			forms = append(forms, "CORRELATED")
		}
		if strings.Contains(upper, "USING AUTOMATIC") || strings.Contains(upper, "AUTOMATIC INDEX") {
			forms = append(forms, "AUTOMATIC INDEX")
		}
		if strings.HasPrefix(upper, "SCAN ") {
			forms = append(forms, "SCAN")
		}
		if len(forms) == 0 && !expectedSearch.MatchString(upper) {
			forms = append(forms, "UNRECOGNIZED EQP")
		}
		for _, form := range forms {
			if !waived(d, "eqp", form, detail) {
				return fmt.Errorf("plan %s: forbidden plan %s: %s", d.ID, form, detail)
			}
		}
	}
	return nil
}
