package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// AutoIndexAudit retains SQLite's actual constraint origin and complete native
// key/table metadata. The generated name is pinned to this exact key; it is not
// an interchangeable spelling of whichever index happens to have that number.
type AutoIndexAudit struct {
	Binding      stmt.AutoIndexBinding `json:"binding"`
	Alias        string                `json:"alias"`
	WithoutRowid bool                  `json:"withoutRowid"`
	Index        Rows                  `json:"index"`
	KeyColumns   Rows                  `json:"keyColumns"`
	IndexColumns Rows                  `json:"indexColumns"`
	TableColumns Rows                  `json:"tableColumns"`
}

func forcedAutoindexes(text string) ([]selectSource, error) {
	tokens, err := lintTokens(text)
	if err != nil {
		return nil, err
	}
	p := lintParser{tokens: tokens, issuerOctets: closedOctetSQL(text)}
	if err := p.statement(); err != nil {
		return nil, err
	}
	sources := p.sources
	if p.dml != nil {
		sources = append(sources, selectSource{name: nameKey(p.dml.table), alias: nameKey(p.dml.table), index: nameKey(p.dml.index)})
	}
	var result []selectSource
	for _, source := range sources {
		if isAutoindex(nameKey(source.index)) {
			source.index = nameKey(source.index)
			result = append(result, source)
		}
	}
	return result, nil
}

func auditAutoindexes(ctx context.Context, conn *sql.Conn, d stmt.Definition) ([]AutoIndexAudit, error) {
	if len(d.Plan.AutoIndexes) == 0 && forcedAutoindex(d.SQL) == "" {
		return nil, nil
	}
	hints, err := forcedAutoindexes(d.SQL)
	if err != nil {
		return nil, err
	}
	var audits []AutoIndexAudit
	used := make([]bool, len(d.Plan.AutoIndexes))
	for _, hint := range hints {
		position := -1
		for i, pin := range d.Plan.AutoIndexes {
			if strings.EqualFold(pin.Table, hint.name) && strings.EqualFold(pin.Name, hint.index) {
				if position >= 0 {
					return nil, fmt.Errorf("duplicate autoindex binding for %s/%s", hint.name, hint.index)
				}
				position = i
			}
		}
		if position < 0 {
			return nil, fmt.Errorf("forced autoindex %s/%s requires an exact PK/UNIQUE binding", hint.name, hint.index)
		}
		used[position] = true
		pin := d.Plan.AutoIndexes[position]
		if err := mainBaseTable(ctx, conn, hint.name); err != nil {
			return nil, err
		}
		table, err := inspectDMLTable(ctx, conn, &dmlShape{table: hint.name})
		if err != nil {
			return nil, err
		}
		indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(table.name)+")")
		if err != nil {
			return nil, err
		}
		var row []any
		for _, candidate := range indexes.Values {
			if len(candidate) != 5 {
				return nil, fmt.Errorf("unexpected index_list shape")
			}
			name, ok := candidate[1].(string)
			if !ok {
				return nil, fmt.Errorf("invalid index_list name")
			}
			if name == pin.Name {
				if row != nil {
					return nil, fmt.Errorf("ambiguous native index name %s", pin.Name)
				}
				row = candidate
			}
		}
		if row == nil {
			return nil, fmt.Errorf("pinned autoindex %s/%s absent (possible renumbering)", pin.Table, pin.Name)
		}
		origin, err := autoindexOrigin(row)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pin.Name, err)
		}
		info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+identifier(pin.Name)+")")
		if err != nil {
			return nil, err
		}
		columns, err := autoindexColumns(info, table.columns)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pin.Name, err)
		}
		key, err := query(ctx, conn, "PRAGMA main.index_info("+identifier(pin.Name)+")")
		if err != nil {
			return nil, err
		}
		if err := checkAutoindexKey(key, columns); err != nil {
			return nil, fmt.Errorf("%s: %w", pin.Name, err)
		}
		actual := stmt.AutoIndexBinding{Table: table.name, Name: row[1].(string), Origin: origin, Columns: columns}
		if pin.Table != actual.Table || pin.Name != actual.Name || pin.Origin != actual.Origin || !slices.Equal(pin.Columns, actual.Columns) {
			return nil, fmt.Errorf("autoindex name-to-key binding changed: expected %+v, actual %+v", pin, actual)
		}
		if origin == "pk" {
			primary := map[int64]string{}
			for _, col := range table.columns.Values {
				if len(col) != 7 {
					return nil, fmt.Errorf("unexpected table_xinfo shape")
				}
				ordinal, ok := col[5].(int64)
				name, nok := col[1].(string)
				if !ok || !nok {
					return nil, fmt.Errorf("invalid table PK metadata")
				}
				if ordinal > 0 {
					primary[ordinal] = name
				}
			}
			if len(primary) != len(columns) {
				return nil, fmt.Errorf("autoindex columns differ from the table PRIMARY KEY")
			}
			for i, col := range columns {
				if primary[int64(i+1)] != col.Name {
					return nil, fmt.Errorf("autoindex column order differs from table PRIMARY KEY")
				}
			}
		}
		audits = append(audits, AutoIndexAudit{actual, hint.alias, table.withoutRowid, Rows{Columns: indexes.Columns, Values: [][]any{row}}, key, info, table.columns})
	}
	for i, pin := range d.Plan.AutoIndexes {
		if !used[i] {
			return nil, fmt.Errorf("unused autoindex binding %s/%s", pin.Table, pin.Name)
		}
	}
	return audits, nil
}

func checkAutoindexKey(info Rows, columns []stmt.IndexColumn) error {
	if len(info.Values) != len(columns) {
		return fmt.Errorf("index_info does not match the complete declared key")
	}
	for i, row := range info.Values {
		if len(row) != 3 {
			return fmt.Errorf("unexpected index_info shape")
		}
		seq, sok := row[0].(int64)
		cid, cok := row[1].(int64)
		name, nok := row[2].(string)
		if !sok || !cok || !nok || seq != int64(i) || cid < 0 || name != columns[i].Name {
			return fmt.Errorf("index_info must contain the exact ordered declared key columns")
		}
	}
	return nil
}

func autoindexOrigin(row []any) (string, error) {
	if len(row) != 5 {
		return "", fmt.Errorf("unexpected index_list shape")
	}
	unique, uok := row[2].(int64)
	origin, ook := row[3].(string)
	partial, pok := row[4].(int64)
	if !uok || !ook || !pok || unique != 1 || (origin != "pk" && origin != "u") || partial != 0 {
		return "", fmt.Errorf("must be a full PRIMARY KEY/UNIQUE constraint index: actual %v", row)
	}
	return origin, nil
}

func autoindexColumns(info, table Rows) ([]stmt.IndexColumn, error) {
	known := map[int64]string{}
	for _, row := range table.Values {
		if len(row) != 7 {
			return nil, fmt.Errorf("unexpected table_xinfo shape")
		}
		cid, ok := row[0].(int64)
		name, nok := row[1].(string)
		if !ok || !nok {
			return nil, fmt.Errorf("invalid table column metadata")
		}
		known[cid] = name
	}
	var columns []stmt.IndexColumn
	for _, row := range info.Values {
		if len(row) != 6 {
			return nil, fmt.Errorf("unexpected index_xinfo shape")
		}
		key, ok := row[5].(int64)
		if !ok || key < 0 || key > 1 {
			return nil, fmt.Errorf("invalid index key flag")
		}
		if key == 0 {
			continue
		} // auxiliary rowid/PK payload is not part of the declared key
		seq, sok := row[0].(int64)
		cid, cok := row[1].(int64)
		name, nok := row[2].(string)
		desc, dok := row[3].(int64)
		collation, lok := row[4].(string)
		if !sok || !cok || !nok || !dok || !lok || seq != int64(len(columns)) || cid < 0 || name == "" || known[cid] != name || (desc != 0 && desc != 1) || collation == "" {
			return nil, fmt.Errorf("autoindex key must contain exact ordered table columns, not expressions/rowid")
		}
		columns = append(columns, stmt.IndexColumn{Name: name, Collation: collation, Desc: desc == 1})
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("empty autoindex key")
	}
	return columns, nil
}

func autoindexSearch(a AutoIndexAudit, detail string) bool {
	upper := strings.ToUpper(detail)
	prefix := "SEARCH " + strings.ToUpper(a.Alias) + " USING "
	if !strings.HasPrefix(upper, prefix) {
		return false
	}
	tail := strings.TrimPrefix(upper, prefix)
	return strings.HasPrefix(tail, "INDEX "+strings.ToUpper(a.Binding.Name)+" (") ||
		strings.HasPrefix(tail, "COVERING INDEX "+strings.ToUpper(a.Binding.Name)+" (") ||
		a.WithoutRowid && a.Binding.Origin == "pk" && strings.HasPrefix(tail, "PRIMARY KEY (")
}

// A forced generated-name capability is stricter than exceptional EQP waivers:
// every row must be SEARCH, and every hinted alias must SEARCH its certified key.
func checkAutoindexSearches(d stmt.Definition, audits []AutoIndexAudit, eqp Rows) error {
	if len(audits) == 0 {
		return nil
	}
	if len(eqp.Values) == 0 {
		return fmt.Errorf("autoindex capability requires a SEARCH plan")
	}
	for _, row := range eqp.Values {
		if len(row) != 4 {
			return fmt.Errorf("invalid autoindex EQP shape")
		}
		detail, ok := row[3].(string)
		if !ok || !expectedSearch.MatchString(strings.ToUpper(detail)) {
			return fmt.Errorf("autoindex capability requires SEARCH-only EQP: %v", row[3])
		}
	}
	for _, a := range audits {
		found := false
		for _, row := range eqp.Values {
			if autoindexSearch(a, row[3].(string)) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("autoindex %s bound to %s has no SEARCH for alias %s", a.Binding.Name, a.Binding.Table, a.Alias)
		}
	}
	return checkAutoindexAccess(d, audits, eqp)
}
