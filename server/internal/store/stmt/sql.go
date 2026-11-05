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
func parameters(sql string) (int, error) {
	if strings.IndexByte(sql, 0) >= 0 {
		return 0, fmt.Errorf("NUL forbidden")
	}
	count, content, ended := 0, false, false
	for i := 0; i < len(sql); {
		c := sql[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
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
				return 0, fmt.Errorf("unterminated comment")
			}
			i += 2
			continue
		}
		if ended {
			return 0, fmt.Errorf("multiple statements forbidden")
		}
		if c == ';' {
			ended = true
			i++
			continue
		}
		content = true
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
				return 0, fmt.Errorf("unterminated quote")
			}
			continue
		}
		if c == '?' {
			count++
			if i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				return 0, fmt.Errorf("numbered parameters forbidden")
			}
		}
		if c == ':' || c == '@' || c == '$' {
			return 0, fmt.Errorf("named parameters forbidden")
		}
		if c == 0 {
			return 0, fmt.Errorf("NUL forbidden")
		}
		i++
	}
	if !content {
		return 0, fmt.Errorf("empty SQL")
	}
	return count, nil
}
