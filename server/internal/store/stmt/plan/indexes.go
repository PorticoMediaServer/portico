package plan

import "strings"

func isAutoindex(name string) bool { return strings.HasPrefix(name, "sqlite_autoindex_") }

// Generated names belong in the expected EQP contract, not INDEXED BY SQL.
// Token kinds distinguish real clauses from comments and SQL-looking data.
func forcedAutoindex(sql string) string {
	tokens, _ := lintTokens(sql)
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].kind == 'w' && tokens[i+1].kind == 'w' && strings.EqualFold(tokens[i].text, "INDEXED") && strings.EqualFold(tokens[i+1].text, "BY") {
			if (tokens[i+2].kind == 'w' || tokens[i+2].kind == 'q') && isAutoindex(nameKey(tokens[i+2].text)) {
				return tokens[i+2].text
			}
		}
	}
	return ""
}
