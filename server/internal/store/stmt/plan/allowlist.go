package plan

import (
	"fmt"
	"strings"
)

type LintIssue struct {
	Form   string
	Detail string
}
type sqlToken struct {
	kind byte
	text string
}

// LintSQL recognizes a closed SELECT/DML grammar. Unknown syntax is flagged; this
// lint never attempts to infer the safety of arbitrary SQLite expressions.
// SQLite's independently checked EQP remains authoritative.
func LintSQL(sql string) []LintIssue {
	return lintSQL(sql, false)
}

func lintSQL(sql string, issuerOctets bool) []LintIssue {
	tokens, err := lintTokens(sql)
	if err != nil {
		return []LintIssue{{"sql:tokenization", err.Error()}}
	}
	issues := []LintIssue{}
	add := func(form, detail string) {
		for _, issue := range issues {
			if issue.Form == form {
				return
			}
		}
		issues = append(issues, LintIssue{form, detail})
	}
	for i, token := range tokens {
		if token.kind != 'w' {
			continue
		}
		switch strings.ToUpper(token.text) {
		case "SELECT":
			if i != 0 {
				add("sql:SELECT", "SELECT outside the top-level projection")
			}
		case "WITH":
			add("sql:WITH", "CTEs are outside the allowed grammar")
		case "CASE", "WHEN", "THEN", "ELSE", "END":
			add("sql:CASE", "CASE is outside the allowed grammar")
		case "OVER", "WINDOW", "FILTER":
			add("sql:WINDOW", "window/filter clauses are outside the allowed grammar")
		}
	}
	p := lintParser{tokens: tokens, issuerOctets: issuerOctets}
	if err := p.statement(); err != nil {
		add("sql:outside-grammar", err.Error())
	}
	return issues
}
func lintTokens(sql string) ([]sqlToken, error) {
	sql = iaEntryParserSQL(sql)
	if strings.IndexByte(sql, 0) >= 0 {
		return nil, fmt.Errorf("NUL is outside the SQL grammar")
	}
	tokens := []sqlToken{}
	for i := 0; i < len(sql); {
		c := sql[i]
		if strings.ContainsRune(" \t\r\n\v\f", rune(c)) {
			i++
			continue
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 >= len(sql) {
				return nil, fmt.Errorf("unterminated comment")
			}
			i += 2
			continue
		}
		start := i
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			end := c
			if c == '[' {
				end = ']'
			}
			i++
			closed := false
			for i < len(sql) {
				if sql[i] != end {
					i++
					continue
				}
				i++
				if end != ']' && i < len(sql) && sql[i] == end {
					i++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quote")
			}
			kind := byte('q')
			if c == '\'' {
				kind = 's'
			}
			tokens = append(tokens, sqlToken{kind, sql[start:i]})
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			i++
			for i < len(sql) && (sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
			tokens = append(tokens, sqlToken{'w', sql[start:i]})
			continue
		}
		if c >= '0' && c <= '9' {
			i++
			for i < len(sql) && sql[i] >= '0' && sql[i] <= '9' {
				i++
			}
			tokens = append(tokens, sqlToken{'n', sql[start:i]})
			continue
		}
		if c == '?' {
			tokens = append(tokens, sqlToken{'p', "?"})
			i++
			continue
		}
		if strings.ContainsRune("(),.;*+-=<>!", rune(c)) {
			i++
			if i < len(sql) && strings.Contains(" <= >= != <> == ", " "+sql[start:i+1]+" ") {
				i++
			}
			tokens = append(tokens, sqlToken{'x', sql[start:i]})
			continue
		}
		return nil, fmt.Errorf("unsupported token byte %#x", c)
	}
	return tokens, nil
}

// Lexical keyword classification keeps keywords from being accepted as plain
// names. This is not an expression safety test: the grammar below must still
// consume every token. Quoting is the escape for unusual identifier spellings.
var sqliteKeywords = func() map[string]bool {
	result := map[string]bool{}
	for _, word := range strings.Fields(`ABORT ACTION ADD AFTER ALL ALTER ANALYZE AND AS ASC ATTACH AUTOINCREMENT BEFORE BEGIN BETWEEN BY CASCADE CASE CAST CHECK COLLATE COLUMN COMMIT CONFLICT CONSTRAINT CREATE CROSS CURRENT CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT DEFERRABLE DEFERRED DELETE DESC DETACH DISTINCT DO DROP EACH ELSE END ESCAPE EXCEPT EXCLUDE EXCLUSIVE EXISTS EXPLAIN FAIL FILTER FIRST FOLLOWING FOR FOREIGN FROM FULL GENERATED GLOB GROUP GROUPS HAVING IF IGNORE IMMEDIATE IN INDEX INDEXED INITIALLY INNER INSERT INSTEAD INTERSECT INTO IS ISNULL JOIN KEY LAST LEFT LIKE LIMIT MATCH MATERIALIZED NATURAL NO NOT NOTHING NOTNULL NULL NULLS OF OFFSET ON OR ORDER OTHERS OUTER OVER PARTITION PLAN PRAGMA PRECEDING PRIMARY QUERY RAISE RANGE RECURSIVE REFERENCES REGEXP REINDEX RELEASE RENAME REPLACE RESTRICT RETURNING RIGHT ROLLBACK ROW ROWS SAVEPOINT SELECT SET TABLE TEMP TEMPORARY THEN TIES TO TRANSACTION TRIGGER UNBOUNDED UNION UNIQUE UPDATE USING VACUUM VALUES VIEW VIRTUAL WHEN WHERE WINDOW WITH WITHOUT TRUE FALSE`) {
		result[word] = true
	}
	return result
}()

type lintParser struct {
	issuerOctets                       bool
	tokens                             []sqlToken
	at, depth                          int
	dml                                *dmlShape
	columnRole                         string
	sources                            []selectSource
	comparisons                        []lintComparison
	orders                             []lintOrder
	limit, disjunction, otherPredicate bool
	limitAt                            int
}

type lintComparison struct {
	left, right []sqlToken
	operator    string
}
type lintOrder struct {
	column []sqlToken
	desc   bool
}

type selectSource struct {
	name, alias, index string
	hasAlias           bool
}

func (p *lintParser) peek(text string) bool {
	return p.at < len(p.tokens) && p.tokens[p.at].kind != 'q' && p.tokens[p.at].kind != 's' && strings.EqualFold(p.tokens[p.at].text, text)
}
func (p *lintParser) take(text string) bool {
	if p.peek(text) {
		p.at++
		return true
	}
	return false
}
func (p *lintParser) need(text string) error {
	if !p.take(text) {
		return p.fail("expected " + text)
	}
	return nil
}
func (p *lintParser) fail(message string) error {
	token := "<end>"
	if p.at < len(p.tokens) {
		token = p.tokens[p.at].text
	}
	return fmt.Errorf("%s at token %d %q", message, p.at, token)
}
func (p *lintParser) name() error {
	if p.at >= len(p.tokens) {
		return p.fail("expected a column/table name")
	}
	token := p.tokens[p.at]
	// KEY is the explicitly supported SQLite identifier fallback used by the
	// fixed cursor fixture. All other keyword names must be quoted.
	if token.kind != 'q' && (token.kind != 'w' || sqliteKeywords[strings.ToUpper(token.text)] && !strings.EqualFold(token.text, "key")) {
		return p.fail("expected a plain or quoted name")
	}

	p.at++
	return nil
}
func (p *lintParser) qualifiedName() error {
	if err := p.name(); err != nil {
		return err
	}
	for segments := 1; p.take("."); segments++ {
		if segments >= 3 {
			return p.fail("name has too many qualifiers")
		}
		if err := p.name(); err != nil {
			return err
		}
	}
	return nil
}
func (p *lintParser) column() error {
	start := p.at
	if err := p.qualifiedName(); err != nil {
		return err
	}
	if p.dml != nil {
		var parts []string
		for at := start; at < p.at; at += 2 {
			parts = append(parts, p.tokens[at].text)
		}
		p.dml.references = append(p.dml.references, columnReference{parts, p.columnRole})
	}
	if p.take("COLLATE") {
		if !p.take("NOCASE") && !p.take("BINARY") {
			return p.fail("unknown collation")
		}
	}
	return nil
}

// Known scalar calls have bounded, explicit arities. No call or grouped
// expression is inferred from the spelling of the preceding SQL keyword.
var scalarCalls = map[string][2]int{
	"ABS": {1, 1}, "LOWER": {1, 1}, "UPPER": {1, 1}, "LENGTH": {1, 1},
	"COALESCE": {2, 8}, "IFNULL": {2, 2}, "NULLIF": {2, 2}, "INSTR": {2, 2},
	"REPLACE": {3, 3}, "SUBSTR": {2, 3}, "PRINTF": {1, 8}, "LIKE": {2, 3}, "GLOB": {2, 2},
}

func (p *lintParser) literal() bool {
	if p.at < len(p.tokens) && (p.tokens[p.at].kind == 'n' || p.tokens[p.at].kind == 's') {
		p.at++
		return true
	}
	if p.at+1 < len(p.tokens) && (p.peek("-") || p.peek("+")) && p.tokens[p.at+1].kind == 'n' {
		p.at += 2
		return true
	}
	return false
}
func (p *lintParser) scalar() error {
	if p.at < len(p.tokens) && p.tokens[p.at].kind == 'p' {
		p.at++
		return nil
	}
	if p.literal() {
		return nil
	}
	if p.at+1 < len(p.tokens) && p.tokens[p.at].kind == 'w' && p.tokens[p.at+1].text == "(" {
		if p.depth >= 16 {
			return p.fail("scalar-call nesting exceeds the closed grammar")
		}
		arity, known := scalarCalls[strings.ToUpper(p.tokens[p.at].text)]
		if p.issuerOctets && strings.EqualFold(p.tokens[p.at].text, "octet_length") {
			arity, known = [2]int{1, 1}, true
		}
		if !known {
			return p.fail("unknown scalar function")
		}
		p.at += 2
		p.depth++
		defer func() { p.depth-- }()
		count := 0
		for {
			if err := p.scalar(); err != nil {
				return err
			}
			count++
			if !p.take(",") {
				break
			}
		}
		if count < arity[0] || count > arity[1] {
			return p.fail("scalar function arity outside the closed grammar")
		}
		return p.need(")")
	}
	return p.column()
}
func (p *lintParser) predicate() error {
	start := p.at
	if err := p.scalar(); err != nil {
		return err
	}
	leftEnd := p.at
	if p.take("IS") {
		p.otherPredicate = true
		p.take("NOT")
		return p.need("NULL")
	}
	negated := p.take("NOT")
	if p.take("IN") {
		p.otherPredicate = true
		// Only a plain column may introduce a scalar parameter/literal list.
		left := lintParser{tokens: p.tokens[start : p.at-1]}
		if negated {
			left.tokens = left.tokens[:len(left.tokens)-1]
		}
		if err := left.column(); err != nil || left.at != len(left.tokens) {
			return p.fail("IN requires a plain column")
		}
		if err := p.need("("); err != nil {
			return err
		}
		for {
			if p.at < len(p.tokens) && p.tokens[p.at].kind == 'p' {
				p.at++
			} else if !p.literal() {
				return p.fail("IN accepts only parameters and integer/string literals")
			}
			if !p.take(",") {
				break
			}
		}
		return p.need(")")
	}
	if p.take("BETWEEN") {
		p.otherPredicate = true
		if err := p.scalar(); err != nil {
			return err
		}
		if err := p.need("AND"); err != nil {
			return err
		}
		return p.scalar()
	}
	for _, operator := range []string{"LIKE", "GLOB", "MATCH", "REGEXP"} {
		if p.take(operator) {
			p.otherPredicate = true
			end := p.at - 1
			if negated {
				end--
			}
			left := lintParser{tokens: p.tokens[start:end]}
			if len(left.tokens) == 1 && left.tokens[0].kind == 'p' {
				left.at = 1
			} else if err := left.column(); err != nil {
				return p.fail("pattern operators require column/parameter operands")
			}
			if left.at != len(left.tokens) {
				return p.fail("pattern operators require column/parameter operands")
			}
			if p.at < len(p.tokens) && p.tokens[p.at].kind == 'p' {
				p.at++
				return nil
			}
			return p.column()
		}
	}
	if negated {
		return p.fail("NOT is allowed only with IS/IN/BETWEEN/pattern operators")
	}
	for _, operator := range []string{"=", "==", "!=", "<>", "<", "<=", ">", ">="} {
		if p.take(operator) {
			right := p.at
			if err := p.scalar(); err != nil {
				return err
			}
			p.comparisons = append(p.comparisons, lintComparison{p.tokens[start:leftEnd], p.tokens[right:p.at], operator})
			return nil
		}
	}
	return p.fail("expected a supported comparison")
}
func (p *lintParser) conjunction() error {
	if err := p.predicate(); err != nil {
		return err
	}
	for p.take("AND") {
		if err := p.predicate(); err != nil {
			return err
		}
	}
	return nil
}
func (p *lintParser) boolean() error {
	if err := p.conjunction(); err != nil {
		return err
	}
	for p.take("OR") {
		p.disjunction = true
		if err := p.conjunction(); err != nil {
			return err
		}
	}
	return nil
}
func (p *lintParser) statement() error {
	var err error
	switch {
	case p.peek("SELECT"):
		err = p.selectStatement()
	case p.peek("INSERT"), p.peek("UPDATE"), p.peek("DELETE"):
		err = p.dmlStatement()
	default:
		err = p.fail("expected SELECT, INSERT, UPDATE or DELETE")
	}
	if err != nil {
		return err
	}
	p.take(";")
	if p.at != len(p.tokens) {
		return p.fail("syntax outside the closed grammar")
	}
	return nil
}

func (p *lintParser) selectStatement() error {
	if err := p.need("SELECT"); err != nil {
		return err
	}
	for {
		if !p.take("*") {
			if err := p.scalar(); err != nil {
				return err
			}
		}
		if p.take("AS") {
			if err := p.name(); err != nil {
				return err
			}
		}
		if !p.take(",") {
			break
		}
	}
	if p.take("FROM") {
		if err := p.selectTable(); err != nil {
			return err
		}
		for p.peek("INNER") || p.peek("LEFT") || p.peek("CROSS") {
			p.at++
			if err := p.need("JOIN"); err != nil {
				return err
			}
			if err := p.selectTable(); err != nil {
				return err
			}
			if err := p.need("ON"); err != nil {
				return err
			}
			if err := p.joinPoints(); err != nil {
				return err
			}
		}
		if len(p.sources) > 1 {
			for _, source := range p.sources {
				if !source.hasAlias {
					return p.fail("joined tables require explicit AS aliases")
				}
			}
			for at, token := range p.tokens {
				if token.kind == 'q' {
					return p.fail("quoted identifiers are outside the joined grammar")
				}
				if token.text == "." && at+2 < len(p.tokens) && p.tokens[at+2].text == "." {
					return p.fail("schema-qualified columns are outside the joined grammar")
				}
			}
		}
	}
	if p.take("WHERE") {
		if err := p.boolean(); err != nil {
			return err
		}
	}
	if p.take("ORDER") {
		if err := p.need("BY"); err != nil {
			return err
		}
		for {
			start := p.at
			if err := p.column(); err != nil {
				return err
			}
			end := p.at
			desc := false
			if !p.take("ASC") {
				desc = p.take("DESC")
			}
			p.orders = append(p.orders, lintOrder{p.tokens[start:end], desc})
			if !p.take(",") {
				break
			}
		}
	}
	if p.take("LIMIT") {
		p.limit = true
		p.limitAt = p.at
		if p.at >= len(p.tokens) || p.tokens[p.at].kind != 'p' && p.tokens[p.at].kind != 'n' {
			return p.fail("LIMIT requires a parameter or nonnegative integer")
		}
		p.at++
	}
	return nil
}

// Only named tables, explicit AS aliases and one native index selection are
// understood. Parenthesized joins, table functions and implicit aliases remain
// outside the grammar. NOT INDEXED does not grant an EQP scan exception.
func (p *lintParser) selectTable() error {
	start := p.at
	if err := p.unquotedName(); err != nil {
		return err
	}
	if p.peek(".") {
		return p.fail("schema-qualified sources are outside the grammar")
	}
	source := selectSource{name: p.tokens[start].text, alias: p.tokens[start].text}
	if sqliteInternalSource(source.name) {
		return p.fail("SQLite internal sources are outside the grammar")
	}
	if p.take("AS") {
		if err := p.unquotedName(); err != nil {
			return err
		}
		source.hasAlias = true
		source.alias = p.tokens[p.at-1].text
	}
	if p.take("INDEXED") {
		if err := p.need("BY"); err != nil {
			return err
		}
		if err := p.unquotedName(); err != nil {
			return err
		}
		source.index = p.tokens[p.at-1].text
	} else if p.take("NOT") {
		if err := p.need("INDEXED"); err != nil {
			return err
		}
	}
	p.sources = append(p.sources, source)
	return nil
}

func (p *lintParser) unquotedName() error {
	if p.at >= len(p.tokens) || p.tokens[p.at].kind != 'w' {
		return p.fail("expected an unquoted identifier")
	}
	return p.name()
}

// JOIN ON has a narrower grammar than WHERE: ANDed column=column/parameter/
// integer points only. It deliberately has no OR, functions, literal strings,
// range comparisons, NULL tests, grouping or inferred predicate safety.
func (p *lintParser) joinPoints() error {
	for {
		start := p.at
		if err := p.qualifiedName(); err != nil {
			return err
		}
		leftEnd := p.at
		if err := p.need("="); err != nil {
			return err
		}
		right := p.at
		switch {
		case p.at < len(p.tokens) && (p.tokens[p.at].kind == 'p' || p.tokens[p.at].kind == 'n'):
			p.at++
		case p.at+1 < len(p.tokens) && (p.peek("+") || p.peek("-")) && p.tokens[p.at+1].kind == 'n':
			p.at += 2
		default:
			if err := p.qualifiedName(); err != nil {
				return err
			}
		}
		p.comparisons = append(p.comparisons, lintComparison{p.tokens[start:leftEnd], p.tokens[right:p.at], "="})
		if !p.take("AND") {
			return nil
		}
	}
}
