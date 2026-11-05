package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IndexPolicy consumes INT's exact contract bytes. Statement owners obtain the
// predicate here; they do not reconstruct a stronger/weaker equivalent formula.
type IndexPolicy struct {
	predicates map[string]string
	sha256     string
}

func ReadIndexPolicy(data []byte) (IndexPolicy, error) {
	var contract struct {
		Indexes []struct {
			Index     string `json:"index"`
			Predicate string `json:"predicate"`
			Choice    string `json:"choice"`
		} `json:"indexes"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		return IndexPolicy{}, err
	}
	sum := sha256.Sum256(data)
	p := IndexPolicy{predicates: map[string]string{}, sha256: hex.EncodeToString(sum[:])}
	if len(contract.Indexes) == 0 {
		return IndexPolicy{}, fmt.Errorf("plan: empty index policy")
	}
	for _, entry := range contract.Indexes {
		if entry.Index == "" || entry.Predicate == "" || entry.Choice != "repeat_exact_predicate" {
			return IndexPolicy{}, fmt.Errorf("plan: unsupported index policy")
		}
		if _, ok := p.predicates[entry.Index]; ok {
			return IndexPolicy{}, fmt.Errorf("plan: duplicate index policy %s", entry.Index)
		}
		p.predicates[entry.Index] = entry.Predicate
	}
	return p, nil
}
func (p IndexPolicy) Predicate(index string) (string, error) {
	value, ok := p.predicates[index]
	if !ok {
		return "", fmt.Errorf("plan: no partial predicate for %s", index)
	}
	return value, nil
}

// CheckPredicates proves literal predicate reuse for declared partial indexes.
// It also checks the installed DDL, so changed/removed policy indexes fail. Full
// semantic equivalence and builder/call-site coverage remain separate controls.
func (p IndexPolicy) CheckPredicates(d stmt.Definition, schema Rows) error {
	for _, index := range d.Plan.Indexes {
		predicate, partial := p.predicates[index]
		if !partial {
			for _, row := range schema.Values {
				if len(row) >= 5 && row[0] == "index" && row[1] == index {
					if ddl, ok := row[4].(string); ok && strings.Contains(ddl, " WHERE ") {
						return fmt.Errorf("plan: undeclared partial index %s", index)
					}
				}
			}
			continue
		}
		found := false
		for _, row := range schema.Values {
			if len(row) < 5 || row[0] != "index" || row[1] != index {
				continue
			}
			ddl, ok := row[4].(string)
			if !ok {
				return fmt.Errorf("plan: index %s has no DDL", index)
			}
			at := strings.Index(ddl, " WHERE ")
			if at < 0 {
				return fmt.Errorf("plan: index %s lost partial predicate", index)
			}
			installed := strings.TrimSuffix(strings.TrimSpace(ddl[at+7:]), ";")
			if installed != predicate {
				return fmt.Errorf("plan: index %s predicate drift", index)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("plan: policy index %s absent from schema", index)
		}
		// Require an entire conjunct boundary; kind=1 cannot stand in for kind IN(...).
		// Block/line comments cannot serve as predicate witnesses.
		code, err := withoutComments(d.SQL)
		if err != nil {
			return err
		}
		if !containsConjunct(code, predicate) {
			return fmt.Errorf("plan %s: exact predicate missing for %s", d.ID, index)
		}
	}
	return nil
}
func withoutComments(sql string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(sql); {
		c := sql[i]
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			end := c
			if c == '[' {
				end = ']'
			}
			b.WriteByte(c)
			i++
			closed := false
			for i < len(sql) {
				c = sql[i]
				b.WriteByte(c)
				i++
				if c != end {
					continue
				}
				if end != ']' && i < len(sql) && sql[i] == end {
					b.WriteByte(sql[i])
					i++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return "", fmt.Errorf("plan: unterminated SQL quote")
			}
			continue
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 >= len(sql) {
				return "", fmt.Errorf("plan: unterminated SQL comment")
			}
			i += 2
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

// Only WHERE/AND tokens outside quoted strings/identifiers can introduce a
// predicate witness. The predicate bytes themselves remain exact.
func containsConjunct(code, predicate string) bool {
	for i := 0; i < len(code); i++ {
		if c := code[i]; c == '\'' || c == '"' || c == '`' || c == '[' {
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
				if end != ']' && i+1 < len(code) && code[i+1] == end {
					i += 2
					continue
				}
				break
			}
			continue
		}
		if i > 0 && ((code[i-1] >= 'a' && code[i-1] <= 'z') || (code[i-1] >= 'A' && code[i-1] <= 'Z') || code[i-1] == '_') {
			continue
		}
		for _, prefix := range []string{"WHERE ", "AND "} {
			if !strings.HasPrefix(code[i:], prefix+predicate) {
				continue
			}
			tail := strings.TrimLeft(code[i+len(prefix)+len(predicate):], " \t\r\n")
			if tail == "" || strings.HasPrefix(tail, "AND ") || strings.HasPrefix(tail, "ORDER BY ") || strings.HasPrefix(tail, "LIMIT ") || tail == ";" {
				return true
			}
		}
	}
	return false
}

func forcedIndex(sql, index string) bool {
	code, err := withoutComments(sql)
	if err != nil {
		return false
	}
	for i := 0; i < len(code); i++ {
		if c := code[i]; c == '\'' || c == '"' || c == '`' || c == '[' {
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
				if end != ']' && i+1 < len(code) && code[i+1] == end {
					i += 2
					continue
				}
				break
			}
			continue
		}
		if i > 0 && ((code[i-1] >= 'a' && code[i-1] <= 'z') || (code[i-1] >= 'A' && code[i-1] <= 'Z') || code[i-1] == '_') {
			continue
		}
		phrase := "INDEXED BY " + index
		if strings.HasPrefix(code[i:], phrase) {
			tail := code[i+len(phrase):]
			if len(tail) > 0 && strings.ContainsRune(" \n\r\t", rune(tail[0])) {
				return true
			}
		}
	}
	return false
}

// CheckSchema reconciles the entire installed partial-index set against INT's
// authority, including indexes not yet used by an implemented read adapter.
func (p IndexPolicy) CheckSchema(schema Rows) error {
	seen := map[string]bool{}
	for _, row := range schema.Values {
		if len(row) < 5 || row[0] != "index" {
			continue
		}
		ddl, ok := row[4].(string)
		if !ok {
			continue
		}
		at := strings.Index(ddl, " WHERE ")
		if at < 0 {
			continue
		}
		name, ok := row[1].(string)
		if !ok {
			return fmt.Errorf("plan: invalid index name")
		}
		predicate, ok := p.predicates[name]
		if !ok {
			return fmt.Errorf("plan: undeclared partial index %s", name)
		}
		if strings.TrimSuffix(strings.TrimSpace(ddl[at+7:]), ";") != predicate {
			return fmt.Errorf("plan: index %s predicate drift", name)
		}
		seen[name] = true
	}
	for name := range p.predicates {
		if !seen[name] {
			return fmt.Errorf("plan: policy index %s absent from schema", name)
		}
	}
	return nil
}

// Hot cursor SQL may not smuggle a tuple/subquery comparison into a seemingly
// indexed plan. Equality-tail and next-key seeks must remain distinct shapes.
func tupleComparison(sql string) bool {
	code, err := withoutComments(sql)
	if err != nil {
		return true
	}
	type frame struct{ function, comma, subquery bool }
	stack := []frame{}
	for i := 0; i < len(code); i++ {
		if c := code[i]; c == '\'' || c == '"' || c == '`' || c == '[' {
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
				if end != ']' && i+1 < len(code) && code[i+1] == end {
					i += 2
					continue
				}
				break
			}
			continue
		}
		switch code[i] {
		case '(':
			function := i > 0 && ((code[i-1] >= 'a' && code[i-1] <= 'z') || (code[i-1] >= 'A' && code[i-1] <= 'Z') || code[i-1] == '_')
			stack = append(stack, frame{function: function, subquery: strings.HasPrefix(strings.ToUpper(strings.TrimSpace(code[i+1:])), "SELECT ")})
		case ',':
			if len(stack) > 0 {
				stack[len(stack)-1].comma = true
			}
		case ')':
			if len(stack) == 0 {
				continue
			}
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tail := strings.TrimLeft(code[i+1:], " \r\n\t")
			if !current.function && (current.comma || current.subquery) && len(tail) > 0 && strings.ContainsRune("<>=!", rune(tail[0])) {
				return true
			}
		}
	}
	return false
}
