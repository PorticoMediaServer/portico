package plan

import (
	"fmt"
	"strings"
)

type expressionToken struct {
	text       string
	identifier bool
	keyword    bool
	children   []expressionToken
	group      bool
	call       bool
	unsafe     bool // row value or subquery, including through grouping/expressions
}

// Structural words are syntax, not function names: AND(a,b) is a grouped row
// after a boolean operator, even without whitespace. Quoted names stay identifiers.
var expressionKeywords = func() map[string]bool {
	result := map[string]bool{}
	for _, word := range strings.Fields(`ABORT ACTION ADD AFTER ALL ALTER ANALYZE AND AS ASC ATTACH AUTOINCREMENT BEFORE BEGIN BETWEEN BY CASCADE CASE CAST CHECK COLLATE COLUMN COMMIT CONFLICT CONSTRAINT CREATE CROSS CURRENT CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT DEFERRABLE DEFERRED DELETE DESC DETACH DISTINCT DO DROP EACH ELSE END ESCAPE EXCEPT EXCLUDE EXCLUSIVE EXISTS EXPLAIN FAIL FILTER FIRST FOLLOWING FOR FOREIGN FROM FULL GENERATED GLOB GROUP GROUPS HAVING IF IGNORE IMMEDIATE IN INDEX INDEXED INITIALLY INNER INSERT INSTEAD INTERSECT INTO IS ISNULL JOIN KEY LAST LEFT LIKE LIMIT MATCH MATERIALIZED NATURAL NO NOT NOTHING NOTNULL NULL NULLS OF OFFSET ON OR ORDER OTHERS OUTER OVER PARTITION PLAN PRAGMA PRECEDING PRIMARY QUERY RAISE RANGE RECURSIVE REFERENCES REGEXP REINDEX RELEASE RENAME REPLACE RESTRICT RETURNING RIGHT ROLLBACK ROW ROWS SAVEPOINT SELECT SET TABLE TEMP TEMPORARY THEN TIES TO TRANSACTION TRIGGER UNBOUNDED UNION UNIQUE UPDATE USING VACUUM VALUES VIEW VIRTUAL WHEN WHERE WINDOW WITH WITHOUT`) {
		result[word] = true
	}
	return result
}()

// tupleComparison examines both operands at every expression level. Grouping,
// arithmetic, CASE and scalar calls preserve row/subquery provenance; commas in
// function arguments and IN lists do not turn ordinary scalars into row values.
func tupleComparison(sql string) bool {
	tokens, err := expressionTokens(sql)
	if err != nil {
		return true
	}
	at := 0
	expressions, err := expressionGroups(tokens, &at, "")
	if err != nil {
		return true
	}
	return forbiddenComparison(expressions)
}
func expressionTokens(sql string) ([]expressionToken, error) {
	code, err := withoutComments(sql)
	if err != nil {
		return nil, err
	}
	tokens := []expressionToken{}
	for i := 0; i < len(code); {
		c := code[i]
		if strings.ContainsRune(" \t\r\n\v\f", rune(c)) {
			i++
			continue
		}
		start := i
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			end := c
			if c == '[' {
				end = ']'
			}
			i++
			for i < len(code) {
				if code[i] != end {
					i++
					continue
				}
				i++
				if end != ']' && i < len(code) && code[i] == end {
					i++
					continue
				}
				break
			}
			tokens = append(tokens, expressionToken{text: code[start:i], identifier: c != '\''})
			continue
		}
		if wordStart(c) {
			i++
			for i < len(code) && (wordStart(code[i]) || code[i] >= '0' && code[i] <= '9') {
				i++
			}
			word := strings.ToUpper(code[start:i])
			keyword := expressionKeywords[word]
			tokens = append(tokens, expressionToken{text: word, identifier: !keyword, keyword: keyword})
			continue
		}
		i++
		if i < len(code) && strings.Contains(" <= >= == != <> << >> || -> ", " "+code[start:i+1]+" ") {
			i++
		}
		tokens = append(tokens, expressionToken{text: code[start:i]})
	}
	return tokens, nil
}
func wordStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128
}
func expressionGroups(tokens []expressionToken, at *int, closing string) ([]expressionToken, error) {
	result := []expressionToken{}
	for *at < len(tokens) {
		token := tokens[*at]
		*at = *at + 1
		if token.text == closing && !token.identifier {
			return result, nil
		}
		if token.text == ")" {
			return nil, fmt.Errorf("unmatched expression group")
		}
		if token.text == "(" || token.keyword && token.text == "CASE" {
			close := ")"
			if token.text == "CASE" {
				close = "END"
			}
			children, err := expressionGroups(tokens, at, close)
			if err != nil {
				return nil, err
			}
			call := false
			set := false
			if token.text == "(" && len(result) > 0 {
				previous := result[len(result)-1]
				call = previous.identifier || previous.keyword && strings.Contains(" CAST IF LIKE GLOB MATCH REGEXP RAISE REPLACE ", " "+previous.text+" ")
				set = previous.keyword && previous.text == "IN"
			}
			unsafe := false
			for _, child := range children {
				if child.unsafe || token.text == "(" && !call && !set && child.text == "," {
					unsafe = true
				}
			}
			if len(children) > 0 && children[0].keyword && (children[0].text == "SELECT" || children[0].text == "WITH" || children[0].text == "VALUES") {
				unsafe = true
			}
			result = append(result, expressionToken{text: token.text, children: children, group: true, call: call, unsafe: unsafe})
			continue
		}
		result = append(result, token)
	}
	if closing != "" {
		return nil, fmt.Errorf("unterminated expression group")
	}
	return result, nil
}
func comparisonWidth(tokens []expressionToken, i int) int {
	token := tokens[i]
	if token.identifier || token.group {
		return 0
	}
	if i+1 < len(tokens) && tokens[i+1].call {
		return 0
	}
	switch token.text {
	case "=", "==", "!=", "<>", "<", "<=", ">", ">=", "IN", "BETWEEN", "LIKE", "GLOB", "MATCH", "REGEXP", "ISNULL", "NOTNULL":
		return 1
	case "IS":
		end := i + 1
		if end < len(tokens) && tokens[end].keyword && tokens[end].text == "NOT" {
			end++
		}
		if end+1 < len(tokens) && tokens[end].keyword && tokens[end].text == "DISTINCT" && tokens[end+1].keyword && tokens[end+1].text == "FROM" {
			end += 2
		}
		return end - i
	}
	return 0
}
func expressionBoundary(token expressionToken) bool {
	if token.group || token.identifier {
		return false
	}
	if token.text == "," || token.text == ";" {
		return true
	}
	return token.keyword && strings.Contains(" AND OR WHERE ON HAVING WHEN THEN ELSE END SELECT FROM JOIN INNER CROSS LEFT RIGHT FULL OUTER USING GROUP ORDER BY LIMIT OFFSET UNION EXCEPT INTERSECT RETURNING SET AS ", " "+token.text+" ")
}
func forbiddenComparison(tokens []expressionToken) bool {
	for _, token := range tokens {
		if token.group && forbiddenComparison(token.children) {
			return true
		}
	}
	for i, token := range tokens {
		width := comparisonWidth(tokens, i)
		if width == 0 {
			continue
		}
		for j := i - 1; j >= 0 && !expressionBoundary(tokens[j]) && comparisonWidth(tokens, j) == 0; j-- {
			if tokens[j].unsafe {
				return true
			}
		}
		j := i + width
		for j < len(tokens) && !expressionBoundary(tokens[j]) && comparisonWidth(tokens, j) == 0 {
			if tokens[j].unsafe {
				return true
			}
			j++
		}
		// BETWEEN has two right operands separated by AND at this group level.
		if token.text == "BETWEEN" && j < len(tokens) && tokens[j].keyword && tokens[j].text == "AND" {
			for j++; j < len(tokens) && !expressionBoundary(tokens[j]) && comparisonWidth(tokens, j) == 0; j++ {
				if tokens[j].unsafe {
					return true
				}
			}
		}
	}
	return false
}
