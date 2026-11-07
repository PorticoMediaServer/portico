package plan

import (
	"fmt"
	"strings"
)

type dmlShape struct {
	kind, table, index string
	columns            []string
	conflict           []string
	references         []columnReference
}

type columnReference struct {
	parts []string
	role  string
}

// Only a completely parsed statement can be qualified. No partial DML shape
// or waiver can confer a destination exemption or legitimize unsupported syntax.
func dmlSQL(sql string) (*dmlShape, error) {
	tokens, err := lintTokens(sql)
	if err != nil {
		return nil, err
	}
	p := lintParser{tokens: tokens, issuerOctets: closedOctetSQL(sql)}
	err = p.statement()
	if err != nil {
		return nil, err
	}
	return p.dml, err
}

func (p *lintParser) nameList(role string) ([]string, error) {
	var names []string
	for {
		at := p.at
		if err := p.name(); err != nil {
			return nil, err
		}
		for _, previous := range names {
			if nameKey(previous) == nameKey(p.tokens[at].text) {
				return nil, p.fail("duplicate column in closed list")
			}
		}
		names = append(names, p.tokens[at].text)
		p.dml.references = append(p.dml.references, columnReference{[]string{p.tokens[at].text}, role})
		if !p.take(",") {
			return names, nil
		}
	}
}

func (p *lintParser) dmlStatement() error {
	kind := strings.ToUpper(p.tokens[p.at].text)
	p.at++
	if kind == "INSERT" {
		if err := p.need("INTO"); err != nil {
			return err
		}
	} else if kind == "DELETE" {
		if err := p.need("FROM"); err != nil {
			return err
		}
	}
	at := p.at
	if err := p.name(); err != nil {
		return err
	}
	p.dml = &dmlShape{kind: kind, table: p.tokens[at].text}
	if sqliteInternalSource(nameKey(p.dml.table)) {
		return p.fail("SQLite internal destinations are outside the grammar")
	}
	if kind == "INSERT" {
		if err := p.insertBody(); err != nil {
			return err
		}
	} else {
		if p.take("INDEXED") {
			if err := p.need("BY"); err != nil {
				return err
			}
			if err := p.name(); err != nil {
				return err
			}
			p.dml.index = p.tokens[p.at-1].text
			if p.tokens[p.at-1].kind != 'w' && isAutoindex(nameKey(p.dml.index)) {
				return p.fail("generated-key INDEXED BY requires an unquoted name")
			}
		}
		if kind == "UPDATE" {
			if err := p.need("SET"); err != nil {
				return err
			}
			if err := p.assignments(false); err != nil {
				return err
			}
		}
		if err := p.need("WHERE"); err != nil {
			return err
		}
		p.columnRole = "WHERE"
		if err := p.boolean(); err != nil {
			return err
		}
	}
	if p.take("RETURNING") {
		_, err := p.nameList("RETURNING")
		return err
	}
	return nil
}

func (p *lintParser) insertBody() error {
	if err := p.need("("); err != nil {
		return err
	}
	cols, err := p.nameList("INSERT columns")
	if err != nil {
		return err
	}
	p.dml.columns = cols
	for _, token := range []string{")", "VALUES", "("} {
		if err := p.need(token); err != nil {
			return err
		}
	}
	count := 0
	for {
		if err := p.need("?"); err != nil {
			return err
		}
		count++
		if !p.take(",") {
			break
		}
	}
	if count != len(cols) {
		return p.fail(fmt.Sprintf("INSERT has %d columns and %d values", len(cols), count))
	}
	if err := p.need(")"); err != nil {
		return err
	}
	if !p.take("ON") {
		return nil
	}
	for _, token := range []string{"CONFLICT", "("} {
		if err := p.need(token); err != nil {
			return err
		}
	}
	p.dml.conflict, err = p.nameList("conflict target")
	if err != nil {
		return err
	}
	for _, token := range []string{")", "DO"} {
		if err := p.need(token); err != nil {
			return err
		}
	}
	if p.take("NOTHING") {
		return nil
	}
	for _, token := range []string{"UPDATE", "SET"} {
		if err := p.need(token); err != nil {
			return err
		}
	}
	return p.assignments(true)
}

func (p *lintParser) assignments(upsert bool) error {
	for {
		at := p.at
		if err := p.name(); err != nil {
			return err
		}
		p.dml.references = append(p.dml.references, columnReference{[]string{p.tokens[at].text}, "SET target"})
		p.columnRole = "SET expression"
		if err := p.need("="); err != nil {
			return err
		}
		if upsert {
			if !p.take("?") {
				for _, token := range []string{"EXCLUDED", "."} {
					if err := p.need(token); err != nil {
						return err
					}
				}
				if err := p.name(); err != nil {
					return err
				}
				p.dml.references = append(p.dml.references, columnReference{[]string{p.tokens[p.at-1].text}, "excluded value"})
			}
		} else {
			// SET expressions use the existing scalar subset, plus at most eight
			// scalar terms joined by +/-. Predicate grammar is unchanged.
			for terms := 0; ; terms++ {
				if terms == 8 {
					return p.fail("SET expression exceeds eight scalar terms")
				}
				if err := p.scalar(); err != nil {
					return err
				}
				if !p.take("+") && !p.take("-") {
					break
				}
			}
		}
		if !p.take(",") {
			return nil
		}
	}
}
