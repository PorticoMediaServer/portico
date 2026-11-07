package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type keyCondition struct{ column, operator string }
type keyOperand struct {
	owner, column, collation string
	bound                    bool
}

// Reuse the closed grammar's column production; never infer a column from a
// function/expression or a fragment the parser did not completely consume.
func accessOperand(tokens []sqlToken) keyOperand {
	if len(tokens) == 1 && (tokens[0].kind == 'p' || tokens[0].kind == 'n' || tokens[0].kind == 's') ||
		len(tokens) == 2 && (tokens[0].text == "+" || tokens[0].text == "-") && tokens[1].kind == 'n' {
		return keyOperand{bound: true}
	}
	p := lintParser{tokens: tokens, dml: &dmlShape{}}
	if err := p.column(); err != nil || p.at != len(tokens) || len(p.dml.references) != 1 {
		return keyOperand{}
	}
	parts := p.dml.references[0].parts
	var result keyOperand
	switch len(parts) {
	case 1:
		result.column = nameKey(parts[0])
	case 2:
		result.owner = nameKey(parts[0])
		result.column = nameKey(parts[1])
	default:
		return result
	}
	if len(tokens) >= 2 && strings.EqualFold(tokens[len(tokens)-2].text, "COLLATE") {
		result.collation = nameKey(tokens[len(tokens)-1].text)
	}
	return result
}
func (o keyOperand) belongs(a AutoIndexAudit, single bool) bool {
	return o.column != "" && (strings.EqualFold(o.owner, a.Alias) || single && o.owner == "")
}
func accessCondition(c lintComparison, a AutoIndexAudit, single bool) (keyCondition, bool) {
	l, r := accessOperand(c.left), accessOperand(c.right)
	operator := c.operator
	if !l.belongs(a, single) && r.belongs(a, single) {
		l, r = r, l
		switch operator {
		case ">":
			operator = "<"
		case ">=":
			operator = "<="
		case "<":
			operator = ">"
		case "<=":
			operator = ">="
		}
	}
	if !l.belongs(a, single) || !(r.bound || r.column != "" && r.owner != "" && !r.belongs(a, single)) {
		return keyCondition{}, false
	}
	for _, col := range a.Binding.Columns {
		if strings.EqualFold(col.Name, l.column) && (l.collation == "" || strings.EqualFold(col.Collation, l.collation)) {
			if operator == "==" {
				operator = "="
			}
			return keyCondition{strings.ToLower(col.Name), operator}, true
		}
	}
	return keyCondition{}, false
}

var nativeKeyCondition = regexp.MustCompile(`^(` + eqpName + `)(=|>=|<=|>|<)\?$`)

func keyConditions(detail string) []keyCondition {
	start, end := strings.Index(detail, " ("), strings.LastIndex(detail, ")")
	if start < 0 || end <= start {
		return nil
	}
	var result []keyCondition
	for _, term := range strings.Split(strings.ToUpper(detail[start+2:end]), " AND ") {
		match := nativeKeyCondition.FindStringSubmatch(term)
		if match == nil {
			return nil
		}
		result = append(result, keyCondition{strings.ToLower(match[1]), match[2]})
	}
	return result
}

func nativeKeyPrefix(a AutoIndexAudit, native []keyCondition, equalities int, rangeOp string) bool {
	count := equalities
	if rangeOp != "" {
		count++
	}
	if len(native) != count {
		return false
	}
	for i, c := range native {
		want := "="
		if i == equalities {
			want = rangeOp
		}
		if !strings.EqualFold(c.column, a.Binding.Columns[i].Name) || rangeDirection(c.operator) != rangeDirection(want) {
			return false
		}
	}
	return true
}

// EQP omits inclusive endpoints (>= becomes >, <= becomes <). SQL retains
// inclusivity; the independent native proof must agree on the seek direction.
func rangeDirection(operator string) string {
	switch operator {
	case ">=":
		return ">"
	case "<=":
		return "<"
	default:
		return operator
	}
}

// This grants a deliberately smaller access subset than general SEARCH:
// full-key ANDed point equalities, or a single-source ordered/limited key-prefix
// page. It consumes metadata recorded by the existing SQL grammar and SQLite's
// independently checked EQP, not another general SQL expression parser.
func checkAutoindexAccess(d stmt.Definition, audits []AutoIndexAudit, eqp Rows) error {
	tokens, err := lintTokens(d.SQL)
	if err != nil {
		return err
	}
	p := lintParser{tokens: tokens, issuerOctets: closedOctetDefinition(d)}
	if err := p.statement(); err != nil {
		return err
	}
	if p.disjunction {
		return fmt.Errorf("forced keys require conjunctive point/keyset predicates")
	}
	if p.limit && p.tokens[p.limitAt].kind == 'n' {
		value, err := strconv.ParseInt(p.tokens[p.limitAt].text, 10, 64)
		if err != nil || value <= 0 {
			return fmt.Errorf("forced-key LIMIT literal must be strictly positive")
		}
	}
	single := len(p.sources) == 1 || p.dml != nil
	for _, a := range audits {
		var native []keyCondition
		for _, row := range eqp.Values {
			if autoindexSearch(a, row[3].(string)) {
				native = keyConditions(row[3].(string))
				break
			}
		}
		equal := map[string]int{}
		var conditions []keyCondition
		allPlain := !p.otherPredicate
		for _, c := range p.comparisons {
			condition, ok := accessCondition(c, a, single)
			if !ok {
				allPlain = false
				continue
			}
			conditions = append(conditions, condition)
			if condition.operator == "=" {
				equal[condition.column]++
			}
		}
		full := true
		for _, col := range a.Binding.Columns {
			full = full && equal[strings.ToLower(col.Name)] > 0
		}
		if full && nativeKeyPrefix(a, native, len(a.Binding.Columns), "") {
			continue
		}
		if p.dml != nil || len(p.sources) != 1 || !p.limit || !allPlain {
			return fmt.Errorf("forced key %s requires a full-key point or a bounded key-ordered prefix seek", a.Binding.Name)
		}
		prefix := 0
		for prefix < len(a.Binding.Columns) && equal[strings.ToLower(a.Binding.Columns[prefix].Name)] == 1 {
			prefix++
		}
		if prefix == len(a.Binding.Columns) {
			return fmt.Errorf("ambiguous full-key equality proof")
		}
		rangeOp := ""
		for _, c := range conditions {
			position := -1
			for i, col := range a.Binding.Columns {
				if strings.EqualFold(c.column, col.Name) {
					position = i
					break
				}
			}
			if c.operator == "=" && position < prefix && equal[c.column] == 1 {
				continue
			}
			if position != prefix || rangeOp != "" || (c.operator != ">" && c.operator != ">=" && c.operator != "<" && c.operator != "<=") {
				return fmt.Errorf("forced key range/equalities must follow a leading key prefix exactly")
			}
			rangeOp = c.operator
		}
		if !nativeKeyPrefix(a, native, prefix, rangeOp) || !keyOrdered(a, p.orders, prefix) {
			return fmt.Errorf("forced key %s requires exact adjacent native seek and key ORDER BY with LIMIT", a.Binding.Name)
		}
	}
	return nil
}

func keyOrdered(a AutoIndexAudit, orders []lintOrder, prefix int) bool {
	start := prefix
	if len(orders) == len(a.Binding.Columns) {
		start = 0
	}
	if len(orders) == 0 || len(orders) != len(a.Binding.Columns)-start {
		return false
	}
	for i, order := range orders {
		col := accessOperand(order.column)
		key := a.Binding.Columns[start+i]
		if !col.belongs(a, true) || !strings.EqualFold(col.column, key.Name) ||
			col.collation != "" && !strings.EqualFold(col.collation, key.Collation) || order.desc != orders[0].desc {
			return false
		}
	}
	return true
}
