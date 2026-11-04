package persistence

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// This graph is test tooling, never runtime statement instrumentation. Unknown
// SQL, virtual tables and unmodelled schema operations retain an all-table
// barrier. Edges deliberately ignore WHEN and UPDATE-column predicates.
type derivedEffectGraph struct {
	edges   map[derivedSQLEffect][]derivedSQLEffect
	unknown map[derivedSQLEffect]bool
	objects map[string]string
	schema  string
}

func derivedSchemaEffects(db *sql.DB) (*derivedEffectGraph, error) {
	g := &derivedEffectGraph{edges: map[derivedSQLEffect][]derivedSQLEffect{}, unknown: map[derivedSQLEffect]bool{}, objects: map[string]string{}}
	rows, err := db.Query(`SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE type IN('table','view','trigger') ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	type definition struct{ kind, name, table, sql string }
	var definitions []definition
	for rows.Next() {
		var d definition
		if err = rows.Scan(&d.kind, &d.name, &d.table, &d.sql); err != nil {
			rows.Close()
			return nil, err
		}
		definitions = append(definitions, d)
		if d.kind != "trigger" {
			g.objects[strings.ToLower(d.name)] = d.kind
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	for _, d := range definitions {
		fmt.Fprintf(hash, "%q %q %q %q\n", d.kind, d.name, d.table, d.sql)
		if strings.HasPrefix(strings.ToUpper(d.sql), "CREATE VIRTUAL TABLE") {
			for _, verb := range []string{"INSERT", "UPDATE", "DELETE"} {
				g.unknown[derivedSQLEffect{strings.ToLower(d.name), verb}] = true
			}
		}
	}
	g.schema = hex.EncodeToString(hash.Sum(nil))
	for _, d := range definitions {
		if d.kind == "trigger" {
			tokens := derivedEffectTokens(d.sql)
			event, begin := "", -1
			for i, word := range tokens {
				if word == "begin" {
					begin = i
					break
				}
				if event == "" && (word == "insert" || word == "update" || word == "delete") {
					event = strings.ToUpper(word)
				}
			}
			if event == "" || begin < 0 || len(tokens) < begin+2 || tokens[len(tokens)-1] != "end" {
				return nil, fmt.Errorf("unsupported trigger header/body %s", d.name)
			}
			root := derivedSQLEffect{strings.ToLower(d.table), event}
			effects, err := g.tokenEffects(tokens[begin+1 : len(tokens)-1])
			if err != nil {
				g.unknown[root] = true
			} else {
				g.edges[root] = append(g.edges[root], effects...)
			}
		}
		if d.kind != "table" {
			continue
		}
		fk, err := db.Query(`PRAGMA foreign_key_list("` + strings.ReplaceAll(d.name, `"`, `""`) + `")`)
		if err != nil {
			return nil, err
		}
		for fk.Next() {
			var id, seq int
			var parent, from, to, update, delete, match sql.NullString
			if err = fk.Scan(&id, &seq, &parent, &from, &to, &update, &delete, &match); err != nil {
				fk.Close()
				return nil, err
			}
			for event, action := range map[string]string{"DELETE": delete.String, "UPDATE": update.String} {
				root := derivedSQLEffect{strings.ToLower(parent.String), event}
				childVerb := "UPDATE"
				switch action {
				case "NO ACTION", "RESTRICT":
					continue
				case "CASCADE":
					childVerb = event
				case "SET NULL", "SET DEFAULT":
				default:
					g.unknown[root] = true
					continue
				}
				g.edges[root] = append(g.edges[root], derivedSQLEffect{strings.ToLower(d.name), childVerb})
			}
		}
		err = fk.Err()
		fk.Close()
		if err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Preserve quoted identifiers as a distinct token until a target is read;
// keywords in strings, comments or quoted column names are never events.
func derivedEffectTokens(query string) []string {
	var tokens []string
	for _, token := range derivedTokens.FindAllString(query, -1) {
		if strings.HasPrefix(token, "--") || strings.HasPrefix(token, "/*") {
			continue
		}
		tokens = append(tokens, strings.ToLower(token))
	}
	for len(tokens) > 0 && tokens[len(tokens)-1] == ";" {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

func (g *derivedEffectGraph) direct(query string) ([]derivedSQLEffect, error) {
	return g.tokenEffects(derivedEffectTokens(query))
}

func (g *derivedEffectGraph) tokenEffects(tokens []string) ([]derivedSQLEffect, error) {
	var effects []derivedSQLEffect
	for len(tokens) > 0 {
		end := 0
		for end < len(tokens) && tokens[end] != ";" {
			end++
		}
		part := tokens[:end]
		if end < len(tokens) {
			end++
		}
		tokens = tokens[end:]
		if len(part) == 0 {
			continue
		}
		if part[0] == "explain" {
			continue // EXPLAIN does not execute even a DML statement.
		}
		if part[0] == "with" {
			depth, start := 0, -1
			for i := 1; i < len(part); i++ {
				switch part[i] {
				case "(":
					depth++
				case ")":
					depth--
				case "select", "insert", "update", "delete", "replace":
					if depth == 0 {
						start = i
					}
				}
				if start >= 0 {
					break
				}
			}
			if start < 0 {
				return nil, fmt.Errorf("unresolved WITH statement")
			}
			part = part[start:]
		}
		verb, at := strings.ToUpper(part[0]), 1
		switch verb {
		case "SELECT":
			continue // SQL callback/extension restrictions remain separately enforced.
		case "INSERT", "REPLACE", "UPDATE":
			if at < len(part) && part[at] == "or" {
				at += 2
			}
			if verb != "UPDATE" {
				if at >= len(part) || part[at] != "into" {
					return nil, fmt.Errorf("missing INSERT target")
				}
				at++
			}
		case "DELETE":
			if at >= len(part) || part[at] != "from" {
				return nil, fmt.Errorf("missing DELETE target")
			}
			at++
		default:
			return nil, fmt.Errorf("unsupported SQL effect %s", verb)
		}
		if at+1 < len(part) && part[at+1] == "." {
			if strings.Trim(part[at], `"`+"`[]") != "main" {
				return nil, fmt.Errorf("non-main schema effect")
			}
			at += 2
		}
		if at+1 >= len(part) {
			return nil, fmt.Errorf("incomplete DML target")
		}
		table := strings.Trim(part[at], `"`+"`[]'")
		if !derivedName.MatchString(table) || g.objects[table] == "" {
			return nil, fmt.Errorf("unknown effect target %s", table)
		}
		if verb == "REPLACE" {
			verb = "INSERT"
		}
		effects = append(effects, derivedSQLEffect{table, verb})
		// Conservative conflict roots include UPDATE/DELETE even when the
		// current fixture does not hit the conflict or recursive triggers are off.
		joined := strings.Join(part, " ")
		if strings.Contains(joined, "do update") {
			effects = append(effects, derivedSQLEffect{table, "UPDATE"})
		}
		if part[0] == "replace" || strings.Contains(joined, "or replace") {
			effects = append(effects, derivedSQLEffect{table, "DELETE"})
		}
	}
	return derivedSortedEffects(effects), nil
}

func (g *derivedEffectGraph) expand(roots []derivedSQLEffect) ([]derivedSQLEffect, error) {
	seen := map[derivedSQLEffect]bool{}
	queue := append([]derivedSQLEffect{}, roots...)
	for len(queue) > 0 {
		root := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[root] {
			continue
		}
		seen[root] = true
		if root.Table == "*" || root.Verb == "*" || g.objects[root.Table] == "" || g.unknown[root] {
			return nil, fmt.Errorf("unknown transitive effect from %s %s", root.Verb, root.Table)
		}
		queue = append(queue, g.edges[root]...)
	}
	var result []derivedSQLEffect
	for effect := range seen {
		result = append(result, effect)
	}
	return derivedSortedEffects(result), nil
}

func derivedSortedEffects(effects []derivedSQLEffect) []derivedSQLEffect {
	seen := map[derivedSQLEffect]bool{}
	result := []derivedSQLEffect{}
	for _, effect := range effects {
		if !seen[effect] {
			seen[effect] = true
			result = append(result, effect)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Table != result[j].Table {
			return result[i].Table < result[j].Table
		}
		return result[i].Verb < result[j].Verb
	})
	return result
}
