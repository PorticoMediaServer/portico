package stmt

import (
	"fmt"
	"strings"
)

// parameters accepts exactly one SQL statement with anonymous positional '?'
// bindings. It recognizes SQLite quotes/comments so literals cannot masquerade
// as parameters or terminators. Numbered/named bindings are deliberately rejected
// rather than silently conflating repeated slot names with ordered parameters.
// This is a lexical boundary, not a SQL parser or authorization check.
// Track LIMIT ? slots in the existing quote/comment-aware parameter scan.
// This is used only for the certified native-key subset, never as SQL admission.
func parameterLayout(sql string) (int, []int, error) {
	if strings.IndexByte(sql, 0) >= 0 {
		return 0, nil, fmt.Errorf("NUL forbidden")
	}
	count, content, ended := 0, false, false
	var limits []int
	limitNext := false
	for i := 0; i < len(sql); {
		c := sql[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
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
			if i+1 == len(sql) || i == len(sql) {
				return 0, nil, fmt.Errorf("unterminated comment")
			}
			i += 2
			continue
		}
		if ended {
			return 0, nil, fmt.Errorf("multiple statements forbidden")
		}
		if c == ';' {
			ended = true
			i++
			continue
		}
		content = true
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			limitNext = false
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
				return 0, nil, fmt.Errorf("unterminated quote")
			}
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			start := i
			i++
			for i < len(sql) && (sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
			limitNext = strings.EqualFold(sql[start:i], "LIMIT")
			continue
		}
		if c == '?' {
			if limitNext {
				limits = append(limits, count)
			}
			count++
			if i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				return 0, nil, fmt.Errorf("numbered parameters forbidden")
			}
		}
		if c == ':' || c == '@' || c == '$' {
			return 0, nil, fmt.Errorf("named parameters forbidden")
		}
		if c == 0 {
			return 0, nil, fmt.Errorf("NUL forbidden")
		}
		limitNext = false
		i++
	}
	if !content {
		return 0, nil, fmt.Errorf("empty SQL")
	}
	return count, limits, nil
}
