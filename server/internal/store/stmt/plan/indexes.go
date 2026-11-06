package plan

import "strings"

func isAutoindex(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "sqlite_autoindex_")
}

// Token kinds distinguish real generated-name hints from comments/data. This
// detector grants no capability: Capture must certify each live key binding.
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
