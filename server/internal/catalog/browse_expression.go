package catalog

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"time"
)

// BrowseNode is one node of the browse expression tree: a group (all/any/not) or
// a predicate (field/operator/value). Parsing and validation happen together so
// every rejection names the exact path that failed.
type BrowseNode struct {
	All      []BrowseNode `json:"all,omitempty"`
	Any      []BrowseNode `json:"any,omitempty"`
	Not      *BrowseNode  `json:"not,omitempty"`
	Field    string       `json:"field,omitempty"`
	Operator string       `json:"operator,omitempty"`
	Value    any          `json:"value,omitempty"`
}

// MarshalJSON writes a predicate as exactly {field, operator, value}, with
// `"value": null` for the presence operators, and a group as exactly its one
// key (CD-29). The published schema says value is null for is-present and
// is-missing, and clients read a predicate as those three keys; the default
// encoding dropped a null value, so a saved view or deep link echoed back with
// a presence filter was refused by the client that made it.
func (n BrowseNode) MarshalJSON() ([]byte, error) {
	if n.Field != "" || n.Operator != "" {
		return json.Marshal(struct {
			Field    string `json:"field"`
			Operator string `json:"operator"`
			Value    any    `json:"value"`
		}{n.Field, n.Operator, n.Value})
	}
	type group struct {
		All []BrowseNode `json:"all,omitempty"`
		Any []BrowseNode `json:"any,omitempty"`
		Not *BrowseNode  `json:"not,omitempty"`
	}
	return json.Marshal(group{n.All, n.Any, n.Not})
}

// BrowseValidationError names the offending path so a client can attach the
// message to the control that produced it.
type BrowseValidationError struct {
	Path    string
	Message string
}

func (e *BrowseValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

func browseIssue(path, message string) error {
	return &BrowseValidationError{Path: path, Message: message}
}

// ParseBrowseQuery validates raw JSON against the published vocabulary and
// returns the typed tree. A nil result means "no filter".
func ParseBrowseQuery(raw json.RawMessage, path string) (*BrowseNode, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	if len(raw) > BrowseMaximumBytes {
		return nil, browseIssue(path, "query must not exceed 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, browseIssue(path, "query must be valid JSON")
	}
	if decoder.Decode(new(any)) == nil {
		return nil, browseIssue(path, "query must contain exactly one JSON value")
	}
	clauses := 0
	return parseBrowseNode(value, 0, path, &clauses)
}

func parseBrowseNode(value any, depth int, path string, clauses *int) (*BrowseNode, error) {
	if depth > BrowseMaximumDepth {
		return nil, browseIssue(path, fmt.Sprintf("query must not nest more than %d levels", BrowseMaximumDepth))
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, browseIssue(path, "must be a query object")
	}
	if len(object) == 0 {
		if depth == 0 {
			return nil, nil
		}
		return nil, browseIssue(path, "must not be empty")
	}
	if _, leaf := object["field"]; leaf {
		return parseBrowsePredicate(object, path, clauses)
	}
	if len(object) != 1 {
		return nil, browseIssue(path, "group nodes must contain exactly one of all, any or not")
	}
	for operator, child := range object {
		switch operator {
		case "all", "any":
			children, ok := child.([]any)
			if !ok || len(children) == 0 || len(children) > BrowseMaximumClauses {
				return nil, browseIssue(path+"."+operator, fmt.Sprintf("must contain between 1 and %d query nodes", BrowseMaximumClauses))
			}
			parsed := make([]BrowseNode, 0, len(children))
			for index, nested := range children {
				node, err := parseBrowseNode(nested, depth+1, fmt.Sprintf("%s.%s[%d]", path, operator, index), clauses)
				if err != nil {
					return nil, err
				}
				if node == nil {
					return nil, browseIssue(fmt.Sprintf("%s.%s[%d]", path, operator, index), "must not be empty")
				}
				parsed = append(parsed, *node)
			}
			if operator == "all" {
				return &BrowseNode{All: parsed}, nil
			}
			return &BrowseNode{Any: parsed}, nil
		case "not":
			node, err := parseBrowseNode(child, depth+1, path+".not", clauses)
			if err != nil {
				return nil, err
			}
			if node == nil {
				return nil, browseIssue(path+".not", "must not be empty")
			}
			return &BrowseNode{Not: node}, nil
		default:
			return nil, browseIssue(path, fmt.Sprintf("unknown query node %q", operator))
		}
	}
	return nil, browseIssue(path, "must be a query object")
}

func parseBrowsePredicate(object map[string]any, path string, clauses *int) (*BrowseNode, error) {
	if len(object) != 3 {
		return nil, browseIssue(path, "predicate nodes must contain exactly field, operator and value")
	}
	id, idOK := object["field"].(string)
	operator, operatorOK := object["operator"].(string)
	if !idOK || !operatorOK {
		return nil, browseIssue(path, "field and operator must be strings")
	}
	field, exists := browseFieldByID(id)
	if !exists {
		return nil, browseIssue(path+".field", fmt.Sprintf("field %q is not supported", id))
	}
	if !containsString(field.Operators, operator) {
		return nil, browseIssue(path+".operator", fmt.Sprintf("operator %q is not valid for %s", operator, id))
	}
	*clauses++
	if *clauses > BrowseMaximumClauses {
		return nil, browseIssue(path, fmt.Sprintf("query must not exceed %d predicates", BrowseMaximumClauses))
	}
	value, err := validateBrowseValue(field, operator, object["value"], path+".value")
	if err != nil {
		return nil, err
	}
	return &BrowseNode{Field: id, Operator: operator, Value: value}, nil
}

func validateBrowseValue(field browseField, operator string, value any, path string) (any, error) {
	if operator == "within-last-days" {
		return validateBrowseDays(value, path)
	}
	if operator == "is-present" || operator == "is-missing" {
		if value != nil {
			return nil, browseIssue(path, "must be null for a presence operator")
		}
		return nil, nil
	}
	if field.Type == valueBoolean {
		flag, ok := value.(bool)
		if !ok {
			return nil, browseIssue(path, "must be true or false")
		}
		return flag, nil
	}
	list := operator == "between" || operator == "in" || operator == "not-in" || operator == "contains-any" || operator == "contains-all"
	values, isList := value.([]any)
	if list != isList {
		if list {
			return nil, browseIssue(path, "must be an array for this operator")
		}
		return nil, browseIssue(path, "must be a scalar for this operator")
	}
	if !isList {
		return validateBrowseScalar(field, value, path)
	}
	if len(values) == 0 || len(values) > BrowseMaximumValues || (operator == "between" && len(values) != 2) {
		return nil, browseIssue(path, "contains an invalid number of values")
	}
	out := make([]any, 0, len(values))
	for index, candidate := range values {
		scalar, err := validateBrowseScalar(field, candidate, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, err
		}
		out = append(out, scalar)
	}
	return out, nil
}

func validateBrowseScalar(field browseField, value any, path string) (any, error) {
	switch field.Type {
	case valueString, valueEnum, valueSet, valueDate:
		text, ok := value.(string)
		if !ok {
			text, ok = numberText(value, field)
		}
		if !ok || strings.TrimSpace(text) == "" || len(text) > 500 || strings.ContainsAny(text, "\x00\r\n") {
			return nil, browseIssue(path, "must be a non-empty string no longer than 500 bytes")
		}
		if field.Type == valueDate && !validBrowseDate(text) {
			return nil, browseIssue(path, "must use YYYY-MM-DD or RFC 3339")
		}
		if len(field.AllowedValues) > 0 && !containsString(field.AllowedValues, text) {
			return nil, browseIssue(path, "contains an unsupported value")
		}
		return text, nil
	case valueNumber:
		number, ok := value.(json.Number)
		if !ok {
			// CD-02: facet values are published as strings (a decade facet
			// answers "1990"), and a client filters with the values it was
			// given, so numeric text is accepted wherever a number is.
			if text, isText := value.(string); isText {
				if _, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err != nil {
					return nil, browseIssue(path, "must be numeric")
				}
				number = json.Number(strings.TrimSpace(text))
			} else if plain, isFloat := value.(float64); isFloat {
				number = json.Number(strconv.FormatFloat(plain, 'f', -1, 64))
			} else if whole, isInt := value.(int); isInt {
				number = json.Number(strconv.Itoa(whole))
			} else {
				return nil, browseIssue(path, "must be numeric")
			}
		}
		parsed, err := number.Float64()
		if field.ID == "durationMs" && (parsed < 0 || parsed > 9007199254740991 || math.Trunc(parsed) != parsed) {
			return nil, browseIssue(path, "duration must be whole milliseconds")
		}
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, browseIssue(path, "must be a finite number")
		}
		return parsed, nil
	}
	return nil, browseIssue(path, "has an unsupported value type")
}

func numberText(value any, field browseField) (string, bool) {
	if field.Type != valueEnum && field.Type != valueSet && field.Type != valueString {
		return "", false
	}
	if number, ok := value.(json.Number); ok {
		return number.String(), true
	}
	return "", false
}

func validBrowseDate(value string) bool {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ---- compilation ----

// browseCompiler turns the validated tree into one parameterised WHERE clause
// over catalog_browse_rows aliased `e`. Every predicate is either a scalar over
// the entity row or an EXISTS over the entity's member items, so the join
// surface stays bounded no matter how deep the expression is.
type browseCompiler struct {
	reading      *readingBrowseScope
	now          time.Time
	restrictions identity.ContentRestrictions
	profile      string
	args         []any
	groupSearch  bool
	// count, when set, lets the compiler ask how many member items a value
	// matches before choosing to select them from the value side.
	count func(query string, args ...any) (int, error)
	// ids reads the entity ids such a selection names.
	ids func(query string, args ...any) ([]int64, error)
	// itemPivot says every row of the pivot is a playable item, whose only
	// membership is itself.
	itemPivot bool
	// rows are the Home rows the request names, resolved by the caller to the
	// row's titles in its order (homeRow).
	rows       map[string][]int64
	rowSources map[string]HomeRowMembership
	// kinds are the pivot's kinds, which bound a value-side candidate list.
	kinds []int
	// perTitle tests a member condition on each title it is asked of, never
	// from the value side: the titles are already a short candidate list.
	perTitle bool
	// bounded is for a caller that already drives a small candidate page. Its
	// predicates probe those candidates instead of materializing a library set.
	bounded bool
}

// drivenMaximum is the most matching member items a value-side selection
// gathers. A broader value is tested on each title instead, where a container
// stops at its first matching member.
const drivenMaximum = 20000

// presenceProbe is the most rows a presence or title-text selection reads to
// learn whether it is narrow. Presence and title text are broad in most
// lists, where the per-title test fills a page at once; reading
// drivenMaximum rows to learn that would cost every such list.
const presenceProbe = 2000

func (c *browseCompiler) compile(node *BrowseNode) (string, error) {
	previous := c.groupSearch
	c.groupSearch = browseRequiresGroup(node)
	defer func() { c.groupSearch = previous }()
	return c.compileNode(node)
}

// A positive conjunctive group bounds text search's candidate identities.
// A negated or alternative group does not bound the whole result set.
func browseRequiresGroup(node *BrowseNode) bool {
	if node == nil {
		return false
	}
	if node.Field == "genre" || browseGroupField(node.Field) {
		switch node.Operator {
		case "equals", "in", "contains", "contains-any", "contains-all":
			return true
		}
	}
	for i := range node.All {
		if browseRequiresGroup(&node.All[i]) {
			return true
		}
	}
	return false
}

func (c *browseCompiler) compileNode(node *BrowseNode) (string, error) {
	if node == nil {
		return "1=1", nil
	}
	switch {
	case node.All != nil:
		return c.group(node.All, " AND ")
	case node.Any != nil:
		return c.group(node.Any, " OR ")
	case node.Not != nil:
		inner, err := c.compileNode(node.Not)
		if err != nil {
			return "", err
		}
		return "NOT(" + inner + ")", nil
	case node.Field != "":
		return c.predicate(*node)
	}
	return "1=1", nil
}

func (c *browseCompiler) group(children []BrowseNode, glue string) (string, error) {
	parts := make([]string, 0, len(children))
	for index := range children {
		part, err := c.compileNode(&children[index])
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return "(" + strings.Join(parts, glue) + ")", nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (c *browseCompiler) predicate(node BrowseNode) (string, error) {
	if c.reading != nil {
		if !readingBrowseField(node.Field) {
			return "", browseIssue("query.field", "field is not published for Reading")
		}
		if node.Field == "author" || node.Field == "series" {
			clause, args, err := c.reading.predicate(node, "e.entity_id")
			c.args = append(c.args, args...)
			return clause, err
		}
	}
	field, ok := browseFieldByID(node.Field)
	if !ok {
		return "", browseIssue("query.field", "field is not supported")
	}
	if field.ID == "match" {
		return c.matchPredicate(node)
	}
	if field.ID == "durationMs" {
		if value, ok := node.Value.(float64); ok {
			node.Value = value / 1000
		}
		if values, ok := node.Value.([]any); ok {
			converted := make([]any, len(values))
			for i, v := range values {
				if number, ok := v.(float64); ok {
					converted[i] = number / 1000
				} else {
					converted[i] = v
				}
			}
			node.Value = converted
		}
	}
	if field.Entity != "" {
		return c.entityPredicate(field, node)
	}
	return c.itemPredicate(field, node)
}

func foldBrowseText(field browseField) bool {
	if browseIdentityField(field.ID) {
		return false
	}
	return field.Type == valueString || field.Type == valueSet || field.Type == valueEnum
}
func collated(field browseField, expression string) string {
	if field.ID == "availability" {
		return expression
	}
	if foldBrowseText(field) {
		return "portico_fold_text(" + expression + ")"
	}
	if browseIdentityField(field.ID) {
		return "(" + expression + ")"
	}
	if field.Type == valueString || field.Type == valueEnum || field.Type == valueSet {
		return "(" + expression + ") COLLATE NOCASE"
	}
	return "(" + expression + ")"
}

func likeValue(value any, prefixOnly bool) string {
	text, _ := value.(string)
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, "%", `\%`)
	text = strings.ReplaceAll(text, "_", `\_`)
	if prefixOnly {
		return text + "%"
	}
	return "%" + text + "%"
}

// browseDatePeriod is the period a date operand names: the whole UTC day for
// YYYY-MM-DD, and for an RFC 3339 instant of any offset the unit it was
// written to, a second or the last fraction digit given.
func browseDatePeriod(value any) (from, until time.Time, ok bool) {
	text, _ := value.(string)
	if day, err := time.Parse("2006-01-02", text); err == nil {
		return day, day.AddDate(0, 0, 1), true
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return from, until, false
	}
	unit := time.Second
	if match := browseDateFraction.FindStringSubmatch(text); match != nil {
		for range min(len(match[1]), 9) {
			unit /= 10
		}
	}
	at = at.UTC().Truncate(unit)
	return at, at.Add(unit), true
}

var browseDateFraction = regexp.MustCompile(`:[0-9]{2}\.([0-9]+)`)

// browseDateBound is one end of a period as stored text sorts against it.
// Stored dates are UTC text: a date, or a timestamp whose fraction has any
// number of digits or none. Text order is time order for all of them except
// the timestamps of the bound's own second whose fraction stops short of the
// bound's ("…:11Z" and "…:11.5Z" against "…:11.501"): they sort after it and
// are earlier, so they are named one by one in short.
type browseDateBound struct {
	text  string
	short []any
}

func newBrowseDateBound(at time.Time) browseDateBound {
	at = at.UTC()
	second := at.Truncate(time.Second)
	if at.Equal(second) {
		// A stored date is its day's midnight.
		if h, m, s := at.Clock(); h == 0 && m == 0 && s == 0 {
			return browseDateBound{text: at.Format("2006-01-02")}
		}
		return browseDateBound{text: at.Format("2006-01-02T15:04:05")}
	}
	base := second.Format("2006-01-02T15:04:05")
	fraction := strings.TrimRight(fmt.Sprintf("%09d", at.Nanosecond()), "0")
	bound := browseDateBound{text: base + "." + fraction, short: []any{base + "Z"}}
	for digits := 1; digits < len(fraction); digits++ {
		bound.short = append(bound.short, base+"."+fraction[:digits]+"Z")
	}
	return bound
}

// before renders "the stored date is earlier than this bound"; from renders
// "it is this bound or later". Both are unknown, so false, for a missing date.
func (c *browseCompiler) before(target string, bound browseDateBound) string {
	c.args = append(c.args, bound.text)
	if len(bound.short) == 0 {
		return target + "<?"
	}
	c.args = append(c.args, bound.short...)
	return "(" + target + "<? OR " + target + " IN (" + placeholders(len(bound.short)) + "))"
}

func (c *browseCompiler) from(target string, bound browseDateBound) string {
	c.args = append(c.args, bound.text)
	if len(bound.short) == 0 {
		return target + ">=?"
	}
	c.args = append(c.args, bound.short...)
	return "(" + target + ">=? AND " + target + " NOT IN (" + placeholders(len(bound.short)) + "))"
}

// dateComparison renders an operator over a date field: equality is falling in
// the operand's period, and an inclusive bound includes all of it.
func (c *browseCompiler) dateComparison(expression, operator string, value any) (string, bool, error) {
	target := "(" + expression + ")"
	if operator == "within-last-days" {
		days, err := validateBrowseDays(value, "query.value")
		if err != nil {
			return "", true, err
		}
		if c.now.IsZero() {
			return "", true, fmt.Errorf("relative browse requires an evaluation instant")
		}
		// Date columns store UTC calendar dates or RFC3339 timestamps. Reuse
		// precision-preserving bounds; +1ns makes the upper instant inclusive.
		return "(" + c.from(target, newBrowseDateBound(c.now.Add(-time.Duration(days)*24*time.Hour))) + " AND " + c.before(target, newBrowseDateBound(c.now.Add(time.Nanosecond))) + ")", true, nil
	}
	if operator == "between" {
		values, _ := value.([]any)
		if len(values) != 2 {
			return "", true, browseIssue("query.value", "between requires exactly two values")
		}
		first, _, firstOK := browseDatePeriod(values[0])
		_, last, lastOK := browseDatePeriod(values[1])
		if !firstOK || !lastOK {
			return "", true, browseIssue("query.value", "must use YYYY-MM-DD or RFC 3339")
		}
		return "(" + c.from(target, newBrowseDateBound(first)) + " AND " + c.before(target, newBrowseDateBound(last)) + ")", true, nil
	}
	start, end, ok := browseDatePeriod(value)
	if !ok {
		return "", false, nil
	}
	from, until := newBrowseDateBound(start), newBrowseDateBound(end)
	switch operator {
	case "equals":
		return "(" + c.from(target, from) + " AND " + c.before(target, until) + ")", true, nil
	case "not-equals":
		return "(" + target + " IS NULL OR " + c.before(target, from) + " OR " + c.from(target, until) + ")", true, nil
	case "less-than":
		return c.before(target, from), true, nil
	case "at-most":
		return c.before(target, until), true, nil
	case "greater-than":
		return c.from(target, until), true, nil
	case "at-least":
		return c.from(target, from), true, nil
	}
	return "", false, nil
}

// comparison renders one operator against a value expression and binds its args.
func (c *browseCompiler) comparison(field browseField, expression, operator string, value any) (string, error) {
	if field.Type == valueDate {
		if clause, handled, err := c.dateComparison(expression, operator, value); handled {
			return clause, err
		}
	}
	target := collated(field, expression)
	if foldBrowseText(field) {
		fold := func(v any) any {
			if text, ok := v.(string); ok {
				return sorttext.Fold(text)
			}
			return v
		}
		if values, ok := value.([]any); ok {
			copy := make([]any, len(values))
			for i, v := range values {
				copy[i] = fold(v)
			}
			value = copy
		} else {
			value = fold(value)
		}
	}
	switch operator {
	case "equals":
		c.args = append(c.args, value)
		return target + "=?", nil
	case "not-equals":
		c.args = append(c.args, value)
		return "((" + expression + ") IS NULL OR " + target + "<>?)", nil
	case "contains":
		if field.Type == valueSet {
			c.args = append(c.args, value)
			return target + "=?", nil
		}
		c.args = append(c.args, likeValue(value, false))
		return target + " LIKE ? ESCAPE '\\'", nil
	case "starts-with":
		c.args = append(c.args, likeValue(value, true))
		return target + " LIKE ? ESCAPE '\\'", nil
	case "less-than", "at-most", "greater-than", "at-least":
		c.args = append(c.args, value)
		return target + map[string]string{"less-than": "<?", "at-most": "<=?", "greater-than": ">?", "at-least": ">=?"}[operator], nil
	case "between":
		values, _ := value.([]any)
		if len(values) != 2 {
			return "", browseIssue("query.value", "between requires exactly two values")
		}
		c.args = append(c.args, values[0], values[1])
		return target + " BETWEEN ? AND ?", nil
	case "in", "contains-any":
		values, _ := value.([]any)
		c.args = append(c.args, values...)
		return target + " IN (" + placeholders(len(values)) + ")", nil
	case "not-in":
		values, _ := value.([]any)
		c.args = append(c.args, values...)
		return "((" + expression + ") IS NULL OR " + target + " NOT IN (" + placeholders(len(values)) + "))", nil
	case "is-present":
		return "(" + expression + ") IS NOT NULL", nil
	case "is-missing":
		return "(" + expression + ") IS NULL", nil
	}
	return "", browseIssue("query.operator", "operator is not supported")
}

func (c *browseCompiler) entityPredicate(field browseField, node BrowseNode) (string, error) {
	if field.ID == "search" {
		q, ok := node.Value.(string)
		if !ok {
			return "", browseIssue("query.value", "search requires text")
		}
		normalized, match, err := NormalizeSearch(q)
		if err != nil {
			return "", browseIssue("query.value", "search requires between 1 and 128 characters")
		}
		if terms, indexed := facettext.IndexedSearchTerms(normalized); c.groupSearch && indexed {
			clauses := make([]string, 0, len(terms))
			for _, term := range terms {
				c.args = append(c.args, term)
				clauses = append(clauses, `EXISTS(SELECT 1 FROM catalog_search_prefix_rows p INDEXED BY catalog_search_prefix_rows_entity WHERE p.entity_id=e.entity_id AND p.term_id=(SELECT id FROM catalog_search_prefix_terms WHERE prefix=?))`)
			}
			return "(" + strings.Join(clauses, " AND ") + ")", nil
		}
		c.args = append(c.args, match)
		clause := `e.entity_id IN(SELECT d.entity_id FROM catalog_search_titles JOIN catalog_search_documents d ON d.entity_id=catalog_search_titles.rowid WHERE catalog_search_titles MATCH ?`
		if c.groupSearch {
			// Row-id equality reaches only the already selected group's documents;
			// a broad word must not materialize every library's matching identities.
			clause = `EXISTS(SELECT 1 FROM catalog_search_titles JOIN catalog_search_documents d ON d.entity_id=catalog_search_titles.rowid WHERE catalog_search_titles.rowid=e.entity_id AND catalog_search_titles MATCH ?`
		}
		for _, word := range strings.Fields(normalized) {
			if strings.ContainsFunc(word, facettext.CJK) {
				clause += ` AND instr(d.normalized,?)>0`
				c.args = append(c.args, word)
			}
		}
		return clause + ")", nil
	}
	if field.ID == "title" {
		candidates, err := c.titleGramCandidates(node)
		if err != nil {
			return "", err
		}
		clause, err := c.comparison(field, field.Entity, node.Operator, node.Value)
		if err != nil || candidates == "" {
			return clause, err
		}
		return "(" + candidates + clause + ")", nil
	}
	if clause, ok, err := c.indexedScalar(field, node); ok || err != nil {
		return clause, err
	}
	return c.comparison(field, field.Entity, node.Operator, node.Value)
}

// titleGramCandidates bounds a title condition that matches few titles to the
// rows whose folded title holds every trigram of the typed text
// (catalog_title_grams); the condition itself is still tested, on those rows
// only. It returns a clause to put before the condition, or "" when the text
// is too short for a trigram, the operator is negative, or the rows are too
// many to name (a broad text fills its pages early anyway).
func (c *browseCompiler) titleGramCandidates(node BrowseNode) (string, error) {
	if c.count == nil || c.ids == nil {
		return "", nil
	}
	values := []any{node.Value}
	switch node.Operator {
	case "contains", "starts-with", "equals":
	case "in":
		values, _ = node.Value.([]any)
	default:
		return "", nil
	}
	arms, args := []string{}, []any{}
	for _, value := range values {
		text, _ := value.(string)
		match := titleGramMatch(sorttext.Fold(text))
		if match == "" {
			return "", nil
		}
		arms = append(arms, `SELECT rowid AS id FROM catalog_title_grams WHERE catalog_title_grams MATCH ?`)
		args = append(args, match)
	}
	if len(arms) == 0 {
		return "", nil
	}
	candidates := strings.Join(arms, " UNION ")
	found, err := c.count(`SELECT count(*) FROM (`+candidates+` LIMIT `+strconv.Itoa(presenceProbe+1)+`)`, args...)
	if err != nil || found > presenceProbe {
		return "", err
	}
	if found == 0 {
		return "0 AND ", nil
	}
	ids, err := c.ids(candidates, args...)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(ids)
	c.args = append(c.args, string(raw))
	return `e.entity_id IN(SELECT value FROM json_each(?)) AND `, nil
}

// titleGramMatch is the full-text query naming every distinct trigram of
// folded text (each quoted as one token), or "" for text shorter than three
// characters.
func titleGramMatch(folded string) string {
	runes := []rune(folded)
	if len(runes) < 3 {
		return ""
	}
	seen := map[string]bool{}
	terms := []string{}
	for i := 0; i+3 <= len(runes); i++ {
		gram := string(runes[i : i+3])
		if seen[gram] {
			continue
		}
		seen[gram] = true
		terms = append(terms, `"`+strings.ReplaceAll(gram, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " AND ")
}

// exists wraps a member-item condition. Profile placeholders inside the join are
// bound before the condition's own arguments so ordinal positions line up.
func (c *browseCompiler) exists(field browseField, condition func() (string, error)) (string, error) {
	if field.ID == "genre" {
		return c.genreExists(condition)
	}
	if source, ok := drivenMemberSource(field); ok && c.count != nil && c.ids != nil && !c.perTitle {
		if clause, driven, err := c.drivenExists(field, source, condition); driven || err != nil {
			return clause, err
		}
	}
	prefix := "EXISTS(SELECT 1 FROM catalog_browse_memberships bei " + field.Join + " WHERE bei.entity_id=e.entity_id AND "
	if field.ID == "personal-member" && !c.bounded {
		prefix = "e.entity_id IN(SELECT bei.entity_id FROM personal_items pi CROSS JOIN catalog_browse_memberships bei ON bei.item_id=pi.item_id WHERE pi.profile_id=? AND "
	}
	if field.ID == "list" {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(c.profile, "viewer:"))
		var owner []string
		if err != nil || !strings.HasPrefix(c.profile, "viewer:") || json.Unmarshal(raw, &owner) != nil || len(owner) != 3 {
			return "0", nil
		}
		for i := 0; i < 2; i++ {
			for _, part := range owner {
				c.args = append(c.args, part)
			}
		}
	} else {
		for i := 0; i < field.Profile; i++ {
			c.args = append(c.args, c.profile)
		}
	}
	restriction, args := ItemRestrictionSQL("bei.item_id", c.restrictions)
	prefix += "(" + restriction + ") AND "
	c.args = append(c.args, args...)
	body, err := condition()
	if err != nil {
		return "", err
	}
	return prefix + body + ")", nil
}

func (c *browseCompiler) itemPredicate(field browseField, node BrowseNode) (string, error) {
	// Presence is broad: probe the selected title rather than expanding every source.
	if field.ID == "genre" && (node.Operator == "is-present" || node.Operator == "is-missing") {
		field.ID = "genre-presence"
	}
	if clause, handled, err := c.groupMembershipPredicate(field, node); handled {
		return clause, err
	}
	switch field.ID {
	case "savedCollection":
		return c.savedCollectionPredicate(node)
	case "episodeGroup":
		return c.episodeGroupPredicate(node)
	case "homeRow":
		return c.homeRowPredicate(node)
	}
	if field.ID == "playState" {
		return c.playStatePredicate(node)
	}
	if field.ID == "favorite" || field.ID == "watchlisted" || field.ID == "personalRating" || field.ID == "lastPlayedAt" {
		return c.personalTitlePredicate(field, node)
	}
	if field.ID == "subtitleLanguage" {
		return c.subtitleLanguagePredicate(field, node)
	}
	if field.Type == valueBoolean {
		flag, _ := node.Value.(bool)
		positive := (node.Operator == "equals") == flag
		clause, err := c.exists(field, func() (string, error) { return "(" + field.Value + ")=1", nil })
		if err != nil {
			return "", err
		}
		if positive {
			return clause, nil
		}
		return "NOT " + clause, nil
	}
	if field.ID == "versions" && c.itemPivot && (node.Operator == "is-present" || node.Operator == "is-missing") {
		// A title's version count is never missing (no file is 0), and a
		// playable title is its own member: present for every row.
		if node.Operator == "is-present" {
			return "1", nil
		}
		return "0", nil
	}
	if _, counted := factGaps[field.ID]; counted && (node.Operator == "is-present" || node.Operator == "is-missing") {
		// The counters name the few titles that lack the field, or the few
		// that have it; those are the answer (tested again where a title's
		// members are not just itself), and the other answer is their
		// complement. Where both are many, or the list holds containers,
		// each title is tested: the field's values are too many to select.
		present := func() (string, error) {
			c.perTitle = true
			defer func() { c.perTitle = false }()
			return c.exists(field, func() (string, error) { return c.comparison(field, field.Value, "is-present", nil) })
		}
		gaps, err := c.factGapCandidates(field, false)
		if err != nil {
			return "", err
		}
		have := ""
		if gaps == "" {
			if have, err = c.factGapCandidates(field, true); err != nil {
				return "", err
			}
		}
		var clause string
		lacking := gaps != ""
		switch {
		case lacking && c.itemPivot:
			clause = "(" + gaps + "1)"
		case have != "" && c.itemPivot:
			clause = "(" + have + "1)"
		case lacking:
			clause, err = present()
			clause = "(" + gaps + "NOT " + clause + ")"
		default:
			clause, err = present()
			clause = "(" + have + clause + ")"
		}
		if err != nil {
			return "", err
		}
		// clause holds the titles lacking the field, or else those having it.
		if (node.Operator == "is-missing") == lacking {
			return clause, nil
		}
		return "NOT " + clause, nil
	}
	switch node.Operator {
	case "not-equals", "not-in", "is-missing":
		positive := map[string]string{"not-equals": "equals", "not-in": "in", "is-missing": "is-present"}[node.Operator]
		candidates, err := c.genrelessCandidates(field, node)
		if err != nil {
			return "", err
		}
		// Candidates are few: each is tested on its own.
		c.perTitle = candidates != ""
		clause, err := c.exists(field, func() (string, error) {
			return c.comparison(field, field.Value, positive, node.Value)
		})
		c.perTitle = false
		if err != nil {
			return "", err
		}
		return candidates + "NOT " + clause, nil
	case "contains-all":
		values, _ := node.Value.([]any)
		parts := make([]string, 0, len(values))
		for _, value := range values {
			single := value
			clause, err := c.exists(field, func() (string, error) {
				return c.comparison(field, field.Value, "equals", single)
			})
			if err != nil {
				return "", err
			}
			parts = append(parts, clause)
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}
	return c.exists(field, func() (string, error) {
		return c.comparison(field, field.Value, node.Operator, node.Value)
	})
}

// ---- non-personal surfaces (Library Channels) ----

// ErrBrowsePersonalField is returned when a shared surface (a Library Channel
// rule) names a field whose value depends on one viewer's profile.
var ErrBrowsePersonalField = browseIssue("query", "watched, favorite, watchlist, personal rating and last played depend on one profile and can't select a shared channel")

// CompileBrowseSelection compiles a validated tree into a WHERE clause over
// catalog_browse_rows aliased `e`, for shared surfaces outside the browse routes
// (Library Channels). Personal fields are refused: a shared surface has no
// single viewer. Additive: the browse routes do not use it.
func CompileBrowseSelection(node *BrowseNode, evaluation ...time.Time) (string, []any, error) {
	if BrowseNodePersonal(node) {
		return "", nil, ErrBrowsePersonalField
	}
	c := &browseCompiler{now: browseSelectionInstant(evaluation)}
	where, err := c.compile(node)
	if err != nil {
		return "", nil, err
	}
	return where, c.args, nil
}

// CompileCompactBrowseSelection shares the validated non-personal vocabulary
// with Library Channels over catalog_browse_rows aliased e.
func CompileCompactBrowseSelection(node *BrowseNode, evaluation ...time.Time) (string, []any, error) {
	if BrowseNodePersonal(node) {
		return "", nil, ErrBrowsePersonalField
	}
	c := &browseCompiler{now: browseSelectionInstant(evaluation)}
	where, err := c.compile(node)
	if err != nil {
		return "", nil, err
	}
	return where, c.args, nil
}

// BrowseNodePersonal reports whether any predicate reads a profile-bound field.
func BrowseNodePersonal(node *BrowseNode) bool {
	if node == nil {
		return false
	}
	for i := range node.All {
		if BrowseNodePersonal(&node.All[i]) {
			return true
		}
	}
	for i := range node.Any {
		if BrowseNodePersonal(&node.Any[i]) {
			return true
		}
	}
	if BrowseNodePersonal(node.Not) {
		return true
	}
	if node.Field != "" {
		field, ok := browseFieldByID(node.Field)
		return ok && field.Profile > 0
	}
	return false
}

// JSONSchema declares the expression's wire fields explicitly. The parser
// enforces the union, recursive children and published depth/clause bounds.
func (BrowseNode) JSONSchema() map[string]any {
	child := map[string]any{"type": "object", "required": []string{}, "properties": map[string]any{}, "additionalProperties": map[string]any{}}
	group := map[string]any{"type": "array", "minItems": 1, "maxItems": BrowseMaximumClauses, "items": child}
	operators := []string{}
	dates := []string{}
	for _, field := range browseFields {
		if field.Type == valueDate {
			dates = append(dates, field.ID)
		}
		for _, op := range field.Operators {
			if op != "within-last-days" && !containsString(operators, op) {
				operators = append(operators, op)
			}
		}
	}
	relative := map[string]any{"type": "object", "required": []string{"field", "operator", "value"}, "additionalProperties": false, "properties": map[string]any{
		"field":    map[string]any{"type": "string", "enum": dates},
		"operator": map[string]any{"type": "string", "enum": []string{"within-last-days"}},
		"value":    map[string]any{"type": "integer", "minimum": 1, "maximum": 36500},
	}, "description": "Matches dates from the evaluation instant minus N times 24 hours through that instant, inclusive. Future dates do not match. Signed continuations retain the first page's instant."}
	other := map[string]any{"type": "object", "required": []string{}, "additionalProperties": false, "properties": map[string]any{
		"field": map[string]any{"type": "string"}, "operator": map[string]any{"type": "string", "enum": operators}, "value": map[string]any{},
		"all": group, "any": group, "not": child,
	}}
	return map[string]any{"oneOf": []any{relative, other}, "description": "A predicate or all/any/not group. The parser enforces recursive vocabulary and published depth/clause bounds."}
}

// Subtitle languages come from current file evidence and the viewer's own or
// shared subtitle revisions. Private uploads never become another viewer's
// filter values, and obsolete revisions never claim a playable language.
func (c *browseCompiler) subtitleLanguagePredicate(field browseField, node BrowseNode) (string, error) {
	if node.Operator == "contains-all" {
		values, _ := node.Value.([]any)
		parts := []string{}
		for _, value := range values {
			clause, err := c.subtitleLanguagePredicate(field, BrowseNode{Field: field.ID, Operator: "equals", Value: value})
			if err != nil {
				return "", err
			}
			parts = append(parts, clause)
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}
	neg := node.Operator == "not-equals" || node.Operator == "not-in" || node.Operator == "is-missing"
	if neg {
		node.Operator = map[string]string{"not-equals": "equals", "not-in": "in", "is-missing": "is-present"}[node.Operator]
	}
	sources := []browseField{
		{ID: "subtitle-attribute", Type: valueSet, Join: attributeJoin("subtitleLanguage"), Value: "ca.source_value"},
		{ID: "subtitle-file", Type: valueSet, Join: `JOIN catalog_asset_links link ON link.entity_id=bei.item_id JOIN catalog_assets asset ON asset.id=link.asset_id JOIN asset_subtitle_facts facts ON facts.asset_id=asset.token AND facts.size=asset.size AND facts.modified_ns=asset.modified_ns AND facts.status='known' JOIN asset_subtitles track ON track.asset_id=asset.token`, Value: "track.language"},
		{ID: "subtitle-resource", Type: valueSet, Profile: 1, Join: `JOIN subtitle_resources resource ON resource.item_id=bei.item_id AND resource.deleted=0 AND (resource.scope='shared' OR resource.owner=?) JOIN subtitle_revisions revision ON revision.resource_id=resource.id AND revision.revision=resource.current_revision JOIN catalog_assets asset ON asset.token=resource.source_id AND asset.size=revision.source_size AND asset.modified_ns=revision.source_modified_ns`, Value: "revision.language"},
	}
	parts := []string{}
	for _, source := range sources {
		clause, err := c.exists(source, func() (string, error) { return c.comparison(source, source.Value, node.Operator, node.Value) })
		if err != nil {
			return "", err
		}
		parts = append(parts, clause)
	}
	clause := "(" + strings.Join(parts, " OR ") + ")"
	if neg {
		clause = "NOT " + clause
	}
	return clause, nil
}

// Container favourites and ratings belong to the title itself. Member state
// remains useful for aggregate views, but cannot replace that direct intent.
func (c *browseCompiler) personalTitlePredicate(field browseField, node BrowseNode) (string, error) {
	negative := false
	if field.Type == valueBoolean {
		value, _ := node.Value.(bool)
		negative = (node.Operator == "equals") != value
		node.Operator = "equals"
		node.Value = true
	} else if node.Operator == "not-equals" || node.Operator == "not-in" || node.Operator == "is-missing" {
		negative = true
		node.Operator = map[string]string{"not-equals": "equals", "not-in": "in", "is-missing": "is-present"}[node.Operator]
	}
	c.args = append(c.args, c.profile)
	own, err := c.comparison(field, field.Value, node.Operator, node.Value)
	if err != nil {
		return "", err
	}
	if c.bounded {
		own = `EXISTS(SELECT 1 FROM personal_items pi WHERE pi.item_id=e.entity_id AND pi.profile_id=? AND ` + own + `)`
	} else {
		own = `e.entity_id IN(SELECT pi.item_id FROM personal_items pi WHERE pi.profile_id=? AND ` + own + `)`
	}
	// A playable title holds only itself. Expanding its personal state through
	// the membership table adds no matches and prevents the direct profile
	// index from driving an empty or selective filter.
	if c.itemPivot {
		if negative {
			return "NOT " + own, nil
		}
		return own, nil
	}
	memberField := field
	memberField.ID = "personal-member"
	member, err := c.itemPredicate(memberField, node)
	if err != nil {
		return "", err
	}
	clause := "(" + own + " OR " + member + ")"
	if negative {
		clause = "NOT " + clause
	}
	return clause, nil
}

// Membership filters start at the selected group's indexed members. Testing a
// public identity once per row of the library defeats those indexes, especially
// while selecting a deep page in a small genre, collection or series.
func (c *browseCompiler) groupMembershipPredicate(field browseField, node BrowseNode) (string, bool, error) {
	if field.ID != "memberOf" && field.ID != "collection" {
		return "", false, nil
	}
	if node.Operator == "not-in" || node.Operator == "not-equals" {
		positive := node
		positive.Operator = map[string]string{"not-in": "in", "not-equals": "equals"}[node.Operator]
		clause, handled, err := c.groupMembershipPredicate(field, positive)
		return "NOT (" + clause + ")", handled, err
	}
	if node.Operator != "equals" && node.Operator != "in" && node.Operator != "contains" && node.Operator != "contains-any" {
		return "", false, nil
	}
	values := []any{node.Value}
	if list, ok := node.Value.([]any); ok {
		values = list
	}
	identity := []string{}
	for _, v := range values {
		c.args = append(c.args, v)
		identity = append(identity, "pid_blob(?)")
	}
	members := `SELECT parent.item_id FROM catalog_entities container CROSS JOIN catalog_browse_memberships parent ON parent.entity_id=container.id WHERE container.public_id IN (` + strings.Join(identity, ",") + `)`
	if field.ID == "collection" {
		members = `SELECT parent.item_id FROM catalog_entities container CROSS JOIN catalog_collection_members parent ON parent.collection_id=container.id WHERE container.public_id IN (` + strings.Join(identity, ",") + `)`
	}
	restriction, args := ItemRestrictionSQL("member.item_id", c.restrictions)
	c.args = append(c.args, args...)
	if c.bounded {
		return `EXISTS(SELECT 1 FROM catalog_browse_memberships member WHERE member.entity_id=e.entity_id AND EXISTS(SELECT 1 FROM (` + members + ` AND parent.item_id=member.item_id)) AND (` + restriction + `))`, true, nil
	}
	return `e.entity_id IN(SELECT member.entity_id FROM catalog_browse_memberships member WHERE member.item_id IN (` + members + `) AND (` + restriction + `))`, true, nil
}

// Evaluate genre vocabulary and explicit overrides before expanding membership.
// Every inherited source path remains eligible, and restrictions apply to the
// exact playable member that supplied the match.
func (c *browseCompiler) genreExists(condition func() (string, error)) (string, error) {
	before := len(c.args)
	body, err := condition()
	if err != nil {
		return "", err
	}
	operands := append([]any(nil), c.args[before:]...)
	c.args = append(c.args, operands...)
	plain := strings.ReplaceAll(body, "COALESCE(ts.label_override,mg.label)", "mg.label")
	override := strings.ReplaceAll(body, "COALESCE(ts.label_override,mg.label)", "ts.label_override")
	restriction, args := ItemRestrictionSQL("member.item_id", c.restrictions)
	c.args = append(c.args, args...)
	owners := `SELECT ts.entity_id FROM catalog_terms mg CROSS JOIN catalog_term_sources ts INDEXED BY catalog_term_sources_term ON ts.term_id=mg.id
 WHERE mg.vocab=1 AND ts.label_override IS NULL AND (` + plain + `)
 UNION SELECT ts.entity_id FROM catalog_term_sources ts INDEXED BY catalog_term_sources_overrides JOIN catalog_terms mg ON mg.id=ts.term_id
 WHERE mg.vocab=1 AND ts.label_override IS NOT NULL AND (` + override + `)`
	if c.bounded {
		related := `(SELECT e.entity_id UNION SELECT item_id FROM catalog_browse_memberships WHERE entity_id=e.entity_id
   UNION SELECT entity_id FROM catalog_browse_memberships WHERE item_id=e.entity_id
   UNION SELECT ep.show_id FROM catalog_episodes ep WHERE ep.entity_id IN(SELECT item_id FROM catalog_browse_memberships WHERE entity_id=e.entity_id))`
		owners = `SELECT ts.entity_id FROM catalog_term_sources ts JOIN catalog_terms mg ON mg.id=ts.term_id WHERE ts.entity_id IN ` + related + ` AND mg.vocab=1 AND ts.label_override IS NULL AND (` + plain + `)
   UNION SELECT ts.entity_id FROM catalog_term_sources ts JOIN catalog_terms mg ON mg.id=ts.term_id WHERE ts.entity_id IN ` + related + ` AND mg.vocab=1 AND ts.label_override IS NOT NULL AND (` + override + `)`

		// EXISTS/UNION ALL can stop at the first permitted witness. In particular an
		// episode inheriting a show's genre must not enumerate every sibling.
		c.args = append(c.args, args...)
		c.args = append(c.args, args...)
		return `EXISTS(WITH owners AS MATERIALIZED (` + owners + `)
   SELECT 1 FROM owners CROSS JOIN catalog_browse_memberships member ON member.item_id=owners.entity_id WHERE member.entity_id=e.entity_id AND (` + restriction + `)
   UNION ALL SELECT 1 FROM owners CROSS JOIN catalog_browse_memberships member ON member.entity_id=owners.entity_id WHERE member.entity_id=e.entity_id AND (` + restriction + `)
   UNION ALL SELECT 1 FROM owners CROSS JOIN catalog_episodes ep ON ep.show_id=owners.entity_id CROSS JOIN catalog_browse_memberships member ON member.item_id=ep.entity_id WHERE member.entity_id=e.entity_id AND (` + restriction + `))`, nil
	}

	return `e.entity_id IN(WITH owners AS MATERIALIZED (` + owners + `)
 SELECT member.entity_id FROM (
 SELECT bei.entity_id,bei.item_id FROM owners CROSS JOIN catalog_browse_memberships bei ON bei.item_id=owners.entity_id
 UNION SELECT bei.entity_id,bei.item_id FROM owners CROSS JOIN catalog_browse_memberships bei ON bei.entity_id=owners.entity_id
 UNION SELECT bei.entity_id,bei.item_id FROM owners CROSS JOIN catalog_episodes ep ON ep.show_id=owners.entity_id
 CROSS JOIN catalog_browse_memberships bei ON bei.item_id=ep.entity_id
 ) member WHERE (` + restriction + `))`, nil
}

// drivenSource enumerates a field's member items from the value side: a small
// vocabulary (artists, albums, books, attribute terms) or an ordered index
// (provider ratings). item is the member item it yields.
type drivenSource struct {
	from, scope, item string
	// members, when set, is the membership join condition over bei in place
	// of bei.item_id=item.
	members string
	// termKey is set for attribute fields: a term's key is its values' lower
	// case trimmed text, which folds the same as the values, so a text
	// condition is first tested once per term.
	termKey bool
	// nameKey is the vocabulary column holding exactly the value of the
	// field's own expression (keyOf): the condition is tested once per
	// vocabulary entry before its rows are read.
	nameKey, keyOf string
	// value, when set, is the field's value expression the condition is
	// written over, and column the indexed expression standing for it on the
	// value side. absent is the value of a member item the value side has no
	// row for: if the condition holds for it, the value side cannot select.
	value, column, absent string
	// profile is how many times the viewer's profile key is bound ahead of
	// the condition (a scope over the viewer's own rows).
	profile int
}

// resolutionClasses are the picture classes of the resolution field as height
// ranges, so a class is read from the files' height index.
const resolutionClasses = `(SELECT 'sd' AS cls,-9223372036854775808 AS lo,700 AS hi UNION ALL SELECT '720p',700,1000 UNION ALL SELECT '1080p',1000,2000 UNION ALL SELECT '4k',2000,9223372036854775807) rc`

const attributeJoinPrefix = `JOIN catalog_item_attribute_edges ca ON ca.item_id=bei.item_id JOIN catalog_attribute_terms ca_term ON ca_term.id=ca.term_id AND ca_term.field_id=(SELECT id FROM catalog_attribute_fields WHERE field='`

// drivenMemberSource names the value-side enumeration for fields that have
// one. A predicate tested once per title would otherwise walk every title of
// the pivot (and every member of a container) to find the few that match.
func drivenMemberSource(field browseField) (drivenSource, bool) {
	switch field.ID {
	case "subtitle-file":
		return drivenSource{from: `asset_subtitle_languages sl CROSS JOIN asset_subtitles track INDEXED BY asset_subtitles_language ON track.language=sl.language
 CROSS JOIN asset_subtitle_facts facts ON facts.asset_id=track.asset_id AND facts.status='known' CROSS JOIN catalog_assets asset ON asset.token=track.asset_id AND facts.size=asset.size AND facts.modified_ns=asset.modified_ns
 CROSS JOIN catalog_asset_links link INDEXED BY catalog_asset_links_asset ON link.asset_id=asset.id`, scope: "1", item: "link.entity_id", nameKey: "sl.language", keyOf: "track.language"}, true
	case "subtitle-resource":
		// A viewer's own and the shared subtitle uploads: few, read directly.
		return drivenSource{from: `subtitle_resources resource JOIN subtitle_revisions revision ON revision.resource_id=resource.id AND revision.revision=resource.current_revision JOIN catalog_assets asset ON asset.token=resource.source_id AND asset.size=revision.source_size AND asset.modified_ns=revision.source_modified_ns`,
			scope: `resource.deleted=0 AND (resource.scope='shared' OR resource.owner=?)`, item: "resource.item_id", profile: 1}, true
	}
	if field.Profile != 0 {
		return drivenSource{}, false
	}
	switch field.ID {
	case "artist":
		// Credited artists are artist entities of the song's library.
		return drivenSource{from: `catalog_entities art INDEXED BY catalog_entities_title CROSS JOIN catalog_song_artists sar ON sar.artist_id=art.id`,
			scope: `art.library_id IN(SELECT id FROM catalog_libraries) AND art.kind=` + strconv.Itoa(int(compactcatalog.Artist)), item: "sar.song_id"}, true
	case "album":
		return drivenSource{from: `catalog_entities alb INDEXED BY catalog_entities_title CROSS JOIN catalog_songs sng INDEXED BY catalog_songs_album_order ON sng.album_id=alb.id`,
			scope: `alb.library_id IN(SELECT id FROM catalog_libraries) AND alb.kind=` + strconv.Itoa(int(compactcatalog.Album)), item: "sng.entity_id"}, true
	case "author", "narrator":
		return drivenSource{from: `catalog_books bok CROSS JOIN catalog_book_files bfl ON bfl.book_id=bok.entity_id`, scope: "1", item: "bfl.entity_id"}, true
	case "person":
		// A credit names an item or its container (a show's cast), so the
		// credited entity is either the member item or the title itself.
		return drivenSource{from: `catalog_people person CROSS JOIN catalog_credits credit INDEXED BY catalog_credits_person ON credit.person_id=person.id`, scope: "1", item: "credit.entity_id",
			members: `(bei.item_id=credit.entity_id OR bei.entity_id=credit.entity_id)`}, true
	case "cast", "crew":
		role := castDepartment
		if field.ID == "crew" {
			role = "NOT " + castDepartment
		}
		return drivenSource{from: `catalog_people person CROSS JOIN catalog_credits credit INDEXED BY catalog_credits_person ON credit.person_id=person.id JOIN catalog_credit_labels dept ON dept.id=credit.department_id`, scope: role, item: "credit.entity_id",
			members: `(bei.item_id=credit.entity_id OR bei.entity_id=credit.entity_id)`}, true
	case "communityRating", "criticRating":
		providers := "'tmdb','imdb','musicbrainz','audible'"
		if field.ID == "criticRating" {
			providers = "'metacritic','rottentomatoes'"
		}
		return drivenSource{from: `metadata_ratings mr`, scope: `mr.provider IN(` + providers + `)`, item: "mr.item_id"}, true
	case "versions":
		return drivenSource{from: `catalog_item_version_counts vc INDEXED BY catalog_item_version_counts_value`, scope: "1", item: "vc.item_id", value: field.Value, column: "vc.versions", absent: "0"}, true
	case "durationMs":
		return drivenSource{from: `catalog_assets ast INDEXED BY catalog_assets_duration CROSS JOIN catalog_asset_links ia INDEXED BY catalog_asset_links_asset ON ia.asset_id=ast.id`, scope: "1", item: "ia.entity_id"}, true
	case "resolution":
		return drivenSource{from: resolutionClasses + ` CROSS JOIN catalog_assets ast INDEXED BY catalog_assets_height ON ast.height>=rc.lo AND ast.height<rc.hi CROSS JOIN catalog_asset_links ia INDEXED BY catalog_asset_links_asset ON ia.asset_id=ast.id`,
			scope: "1", item: "ia.entity_id", value: field.Value, column: "rc.cls"}, true
	case "actor", "director", "writer":
		// Credited names are a vocabulary: the condition is tested once per
		// name, then the name's credits are read from its index.
		labels := strings.TrimPrefix(field.Join, `JOIN catalog_credits cc ON cc.entity_id=bei.item_id `)
		return drivenSource{from: `catalog_credit_names cn CROSS JOIN catalog_credits cc INDEXED BY catalog_credits_name ON cc.credited_name=cn.name ` + labels, scope: "1", item: "cc.entity_id", nameKey: "cn.name", keyOf: "cc.credited_name"}, true
	}
	if rest, ok := strings.CutPrefix(field.Join, attributeJoinPrefix); ok && strings.HasSuffix(rest, "')") && field.Value == "ca.source_value" {
		name := strings.TrimSuffix(rest, "')")
		return drivenSource{from: `catalog_attribute_terms ca_term CROSS JOIN catalog_item_attribute_edges ca INDEXED BY catalog_item_attribute_edges_term ON ca.term_id=ca_term.id`,
			scope: `ca_term.field_id=(SELECT id FROM catalog_attribute_fields WHERE field='` + name + `')`, item: "ca.item_id", termKey: foldBrowseText(field)}, true
	}
	return drivenSource{}, false
}

// drivenExists selects the matching member items once and maps them to their
// browse entities through the membership index. The viewer's restriction
// still applies to the exact member item that supplied the match. A value
// matching more than drivenMaximum member items is left to the per-title test
// (driven=false); so is presence where the value side is that broad, while a
// field few titles have (subtitles, credits in a sparse library) is selected
// from its few values.
func (c *browseCompiler) drivenExists(field browseField, source drivenSource, condition func() (string, error)) (clause string, driven bool, err error) {
	before := len(c.args)
	body, err := condition()
	if err != nil {
		return "", false, err
	}
	if source.value != "" {
		if source.absent != "" {
			// A member item with no value-side row has the absent value.
			holds, err := c.count(`SELECT count(*) WHERE (`+strings.ReplaceAll(body, source.value, source.absent)+`)`, c.args[before:]...)
			if err != nil {
				return "", false, err
			}
			if holds > 0 {
				c.args = c.args[:before]
				return "", false, nil
			}
		}
		body = strings.ReplaceAll(body, source.value, source.column)
	}
	where := source.scope
	if source.nameKey != "" && strings.Contains(body, source.keyOf) {
		operands := append([]any(nil), c.args[before:]...)
		c.args = append(c.args[:before], append(operands, operands...)...)
		where += " AND " + strings.ReplaceAll(body, source.keyOf, source.nameKey)
	}
	if source.termKey && strings.Contains(body, "portico_fold_text(ca.source_value)") {
		// The same condition over the term's key, with its own copy of the
		// operands, runs once per term before the term's edges are read.
		operands := append([]any(nil), c.args[before:]...)
		c.args = append(c.args[:before], append(operands, operands...)...)
		where += " AND " + strings.ReplaceAll(body, "ca.source_value", "ca_term.value_key")
	}
	matching := `SELECT ` + source.item + ` AS item FROM ` + source.from + ` WHERE ` + where + ` AND (` + body + `)`
	scoped := []any{}
	for i := 0; i < source.profile; i++ {
		scoped = append(scoped, c.profile)
	}
	limit := drivenMaximum
	if strings.HasSuffix(body, " IS NOT NULL") {
		limit = presenceProbe
	}
	found, err := c.count(`SELECT count(*) FROM (`+matching+` LIMIT `+strconv.Itoa(limit+1)+`)`, append(append([]any{}, scoped...), c.args[before:]...)...)
	if err != nil {
		return "", false, err
	}
	if found > limit {
		c.args = c.args[:before]
		return "", false, nil
	}
	if found == 0 {
		// No member item has the value: no title matches.
		c.args = c.args[:before]
		return "0", true, nil
	}
	restriction, args := ItemRestrictionSQL("bei.item_id", c.restrictions)
	members := ``
	if c.itemPivot {
		// A playable item's only membership is itself.
		members = ` AND bei.source=0`
	}
	// The few matching titles are named in the clause itself, so the count
	// and the page start from them rather than test every title of the pivot.
	join := `catalog_browse_memberships bei INDEXED BY catalog_browse_membership_item ON bei.item_id=` + source.item
	if source.members != "" {
		join = `catalog_browse_memberships bei ON ` + source.members
	}
	ids, err := c.ids(`SELECT DISTINCT bei.entity_id FROM `+source.from+` CROSS JOIN `+join+members+
		` WHERE `+where+` AND (`+body+`) AND (`+restriction+`)`, append(append(append([]any{}, scoped...), c.args[before:]...), args...)...)
	if err != nil {
		return "", false, err
	}
	c.args = c.args[:before]
	if len(ids) == 0 {
		return "0", true, nil
	}
	raw, _ := json.Marshal(ids)
	c.args = append(c.args, string(raw))
	return `e.entity_id IN(SELECT value FROM json_each(?))`, true, nil
}

// genrelessCandidates bounds "has no genre" to the titles that can lack one:
// those whose own entity no genre source names (catalog_entities.genre_sources,
// kept by the term sources), and the episodes of shows none names. The exact
// condition is still tested, on those titles only. It returns a clause to put
// before the condition, or "" when the candidates are too many to name (the
// condition is then tested on every title, as before).
func (c *browseCompiler) genrelessCandidates(field browseField, node BrowseNode) (string, error) {
	if field.ID != "genre-presence" || node.Operator != "is-missing" || c.count == nil || c.ids == nil || len(c.kinds) == 0 {
		return "", nil
	}
	arms := []string{}
	for _, kind := range c.kinds {
		if kind == int(compactcatalog.Artist) || kind == int(compactcatalog.Album) {
			// Artists and albums seldom carry a genre of their own; their
			// songs do. One lacks a genre only if a song of it lacks one.
			arms = append(arms, `SELECT DISTINCT bei.entity_id AS id FROM catalog_entities song INDEXED BY catalog_entities_genreless CROSS JOIN catalog_browse_memberships bei INDEXED BY catalog_browse_membership_item ON bei.item_id=song.id
 WHERE song.genre_sources=0 AND song.kind=`+strconv.Itoa(int(compactcatalog.Track)))
			continue
		}
		if kind == int(compactcatalog.Episode) {
			arms = append(arms, `SELECT ep.entity_id AS id FROM catalog_entities sh INDEXED BY catalog_entities_genreless CROSS JOIN catalog_episodes ep ON ep.show_id=sh.id
 JOIN catalog_entities episode ON episode.id=ep.entity_id AND episode.genre_sources=0 WHERE sh.genre_sources=0 AND sh.kind=`+strconv.Itoa(int(compactcatalog.Show)))
			continue
		}
		arms = append(arms, `SELECT id FROM catalog_entities INDEXED BY catalog_entities_genreless WHERE genre_sources=0 AND kind=`+strconv.Itoa(kind))
	}
	candidates := strings.Join(arms, " UNION ALL ")
	found, err := c.count(`SELECT count(*) FROM (` + candidates + ` LIMIT ` + strconv.Itoa(drivenMaximum+1) + `)`)
	if err != nil || found > drivenMaximum {
		return "", err
	}
	if found == 0 {
		return "0 AND ", nil
	}
	ids, err := c.ids(candidates)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(ids)
	c.args = append(c.args, string(raw))
	return `e.entity_id IN(SELECT value FROM json_each(?)) AND `, nil
}

// factGaps are the member-item fields whose absence a maintained counter on
// catalog_entities answers (asset_links, acting_credits, directing_credits,
// writing_credits), as bits of factGapSQL, the expression the
// catalog_entities_fact_gaps index is built on.
var factGaps = map[string]int{"durationMs": 1, "actor": 2, "director": 4, "writer": 8}

const factGapSQL = `(asset_links=0)+(acting_credits=0)*2+(directing_credits=0)*4+(writing_credits=0)*8`

// factGapCandidates bounds "is missing" on a field nearly every title has
// (duration, cast, directors, writers) to the playable titles that can lack
// it: those whose counter is zero (a file always has a length, so a title
// lacks one only when it links no file). The exact condition is still tested
// on them. It returns a clause to put before the condition, or "" when the
// pivot holds containers or the candidates are more than presenceProbe (the
// field is then not near-universal). With having, it names instead the few
// titles that have the field, and for containers those holding a member that
// has it.
func (c *browseCompiler) factGapCandidates(field browseField, having bool) (string, error) {
	bit, ok := factGaps[field.ID]
	if !ok || c.count == nil || c.ids == nil || len(c.kinds) == 0 {
		return "", nil
	}
	masks := []string{}
	for mask := 0; mask < 16; mask++ {
		if (mask&bit != 0) != having {
			masks = append(masks, strconv.Itoa(mask))
		}
	}
	playable := []int{int(compactcatalog.Movie), int(compactcatalog.Episode), int(compactcatalog.Track), int(compactcatalog.Part)}
	arms := []string{}
	for _, kind := range c.kinds {
		if !slices.Contains(playable, kind) {
			arms = nil
			break
		}
		arms = append(arms, `SELECT id FROM catalog_entities INDEXED BY catalog_entities_fact_gaps WHERE kind=`+strconv.Itoa(kind)+` AND `+factGapSQL+` IN(`+strings.Join(masks, ",")+`)`)
	}
	probe := presenceProbe
	if arms == nil {
		if !having {
			return "", nil
		}
		// Through the memberships each probed row costs more: probe fewer.
		probe = presenceProbe / 4
		// A container has the field only if a member does: the few members
		// that have it name their containers.
		for _, kind := range playable {
			arms = append(arms, `SELECT bei.entity_id AS id FROM catalog_entities member INDEXED BY catalog_entities_fact_gaps CROSS JOIN catalog_browse_memberships bei INDEXED BY catalog_browse_membership_item ON bei.item_id=member.id
 WHERE member.kind=`+strconv.Itoa(kind)+` AND `+factGapSQL+` IN(`+strings.Join(masks, ",")+`)`)
		}
	}
	candidates := strings.Join(arms, " UNION ALL ")
	found, err := c.count(`SELECT count(*) FROM (` + candidates + ` LIMIT ` + strconv.Itoa(probe+1) + `)`)
	if err != nil || found > probe {
		return "", err
	}
	if found == 0 {
		return "0 AND ", nil
	}
	ids, err := c.ids(candidates)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(ids)
	c.args = append(c.args, string(raw))
	return `e.entity_id IN(SELECT value FROM json_each(?)) AND `, nil
}

// A current attention relation is selected once by its indexed identities. Keep
// as is only changes dismissal, so it deliberately does not remove this fact.
func (c *browseCompiler) matchPredicate(node BrowseNode) (string, error) {
	visible, args := EntityRestrictionSQL("attention.entity_id", c.restrictions)
	current := `SELECT attention.entity_id FROM (
 SELECT a.entity_id FROM screen_metadata_attention a JOIN screen_metadata_work w ON w.target_id=a.entity_id AND w.target_kind=a.target_kind AND w.generation=a.generation WHERE a.retired=0 AND a.reason NOT IN('pending','searching','pending_search','pending_episodes','pending_children','pending_apply')
 UNION SELECT a.entity_id FROM listening_metadata_attention a WHERE a.retired=0 AND a.kind IN('album','song') AND a.reason NOT IN('pending','searching','pending_search','pending_episodes','pending_children','pending_apply')
 ) attention JOIN catalog_entities target ON target.id=attention.entity_id JOIN catalog_libraries library ON library.id=target.library_id JOIN libraries l ON l.id=library.library_id LEFT JOIN library_metadata_agents agent ON agent.library_id=l.id
 WHERE l.kind IN('movie','tv','anime','music') AND COALESCE(agent.agent,'online')='online' AND target.retired=0 AND ` + visible
	if c.bounded {
		current += ` AND attention.entity_id IN(SELECT e.entity_id UNION SELECT item_id FROM catalog_browse_memberships WHERE entity_id=e.entity_id UNION SELECT entity_id FROM catalog_albums WHERE artist_id=e.entity_id)`
	}
	// Expand only direct membership edges: an artist can inherit an unmatched
	// album/song, and a season an unmatched episode. The matching member itself
	// must be visible, so a hidden member never reveals a parent's match status.
	unmatched := `e.entity_id IN(WITH attention AS MATERIALIZED (` + current + `)
 SELECT entity_id FROM attention
 UNION SELECT bm.entity_id FROM attention CROSS JOIN catalog_browse_memberships bm INDEXED BY catalog_browse_membership_item ON bm.item_id=attention.entity_id
 UNION SELECT album.artist_id FROM attention JOIN catalog_albums album ON album.entity_id=attention.entity_id)`
	c.args = append(c.args, args...)
	value := `CASE WHEN ` + unmatched + ` THEN 'unmatched' ELSE 'matched' END`
	result, err := c.comparison(browseField{ID: "match", Type: valueEnum}, value, node.Operator, node.Value)
	eligible := `e.kind IN(1,2,3,4,5,6,7) AND e.library_id IN(SELECT cl.id FROM catalog_libraries cl JOIN libraries l ON l.id=cl.library_id LEFT JOIN library_metadata_agents a ON a.library_id=l.id WHERE l.kind IN('movie','tv','anime','music') AND COALESCE(a.agent,'online')='online')`
	return "(" + eligible + " AND " + result + ")", err
}

// ValidateHomeRowDisplay binds a display projection to its positive current
// source. Alternatives and negations never confer row membership authority.
func ValidateHomeRowDisplay(query *BrowseNode, rowID string) error {
	if rowID == "" {
		return nil
	}
	if len(rowID) > 256 {
		return browseIssue("homeRowDisplay", "invalid row id")
	}
	found := 0
	var walk func(*BrowseNode)
	walk = func(node *BrowseNode) {
		if node == nil {
			return
		}
		if node.Field == "homeRow" && node.Operator == "equals" && node.Value == rowID {
			found++
		}
		for i := range node.All {
			walk(&node.All[i])
		}
	}
	walk(query)
	if found != 1 {
		return browseIssue("homeRowDisplay", "display projection needs the same positive Home row condition")
	}
	return nil
}

func ValidateHomeRowDisplayQueryLimit(rowID string, limit int) error {
	if rowID != "" && limit > HomeRowMaxLimit {
		return browseIssue("limit", "Home display projection accepts at most 60 entries")
	}
	return nil
}
