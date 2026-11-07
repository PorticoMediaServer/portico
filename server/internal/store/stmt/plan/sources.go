package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// LintSQLOn adds live source/destination resolution to closed syntax checks.
// LintSQL alone is lexical: it has no database catalog and cannot certify a
// source. Capture always uses this connection-aware lint before EXPLAIN.
func LintSQLOn(ctx context.Context, conn *sql.Conn, text string) []LintIssue {
	return lintSQLOn(ctx, conn, text, false)
}

func lintSQLOn(ctx context.Context, conn *sql.Conn, text string, issuerOctets bool) []LintIssue {
	if issues := lintSQL(text, issuerOctets); len(issues) != 0 {
		return issues
	}
	if conn == nil {
		return []LintIssue{{"sql:source", "source qualification requires a connection"}}
	}
	tokens, err := lintTokens(text)
	if err != nil {
		return []LintIssue{{"sql:source", err.Error()}}
	}
	p := lintParser{tokens: tokens, issuerOctets: issuerOctets}
	if err := p.statement(); err != nil {
		return []LintIssue{{"sql:source", err.Error()}}
	}
	if p.dml != nil {
		if err := mainBaseTable(ctx, conn, nameKey(p.dml.table)); err != nil {
			return []LintIssue{{"sql:source", err.Error()}}
		}
	}
	for _, source := range p.sources {
		if err := mainBaseTable(ctx, conn, source.name); err != nil {
			return []LintIssue{{"sql:source", err.Error()}}
		}
	}
	return nil
}

func mainBaseTable(ctx context.Context, conn *sql.Conn, name string) error {
	if sqliteInternalSource(name) {
		return fmt.Errorf("SQLite internal source %s is not an approved MAIN base table", name)
	}
	// An unqualified SQLite name resolves TEMP before MAIN. A valid main table
	// does not authorize a shadowing TEMP table or view with the same name.
	var shadows int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM temp.sqlite_schema
WHERE name=? COLLATE NOCASE AND type IN('table','view')`, name).Scan(&shadows); err != nil {
		return fmt.Errorf("inspect TEMP source %s: %w", name, err)
	}
	if shadows != 0 {
		return fmt.Errorf("source %s resolves to TEMP, not a MAIN base table", name)
	}
	var kind string
	if err := conn.QueryRowContext(ctx, `SELECT type FROM main.sqlite_schema
WHERE name=? COLLATE NOCASE AND type IN('table','view')`, name).Scan(&kind); err != nil {
		return fmt.Errorf("source %s is not a resolved MAIN base table: %w", name, err)
	}
	if kind != "table" {
		return fmt.Errorf("source %s is a MAIN %s, not a base table", name, kind)
	}
	// sqlite_schema labels virtual tables and their backing shadow tables as
	// 'table'. SQLite's own classification distinguishes them without attempting
	// to parse or trust their DDL text. No FTS source registration exists here;
	// this ordinary-table grammar grants no implicit virtual/shadow exception.
	if err := conn.QueryRowContext(ctx, `SELECT type FROM pragma_table_list
WHERE schema='main' AND name=? COLLATE NOCASE`, name).Scan(&kind); err != nil {
		return fmt.Errorf("classify MAIN source %s: %w", name, err)
	}
	if kind != "table" {
		return fmt.Errorf("source %s is a MAIN %s, not an ordinary base table", name, kind)
	}
	return nil
}

func sqliteInternalSource(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "sqlite_")
}

// Recheck source classification at mint time under Capture's immutable-fixture
// and DDL-custody requirement. Seal actual declarations as well as MAIN/type.
type capturedSource struct {
	Statement stmt.ID `json:"statement"`
	Name      string  `json:"name"`
	Schema    string  `json:"schema"`
	Kind      string  `json:"kind"`
	SQL       string  `json:"sql"`
	RootPage  int64   `json:"rootpage"`
}

func capturedSources(ctx context.Context, conn *sql.Conn, entries []stmt.Entry) ([]capturedSource, error) {
	result := []capturedSource{}
	for _, entry := range entries {
		if entry.Plan.NativeRead != nil {
			audit, err := inspectFTSRead(ctx, conn, entry.Definition)
			if err != nil {
				return nil, err
			}
			spec, _ := entry.Plan.NativeRead.Specification()
			result = append(result, capturedSource{Statement: entry.ID, Name: spec.Table, Schema: "main", Kind: "virtual", SQL: audit.Declaration, RootPage: 0})
			continue
		}
		if entry.Plan.NativeRetained != "" {
			audit, err := inspectFTSRetained(ctx, conn, entry.Definition)
			if err != nil {
				return nil, err
			}
			spec, _ := entry.Plan.NativeRetained.Specification()
			view := "search_fragment_content"
			if spec.Table == "search_own_prefix" {
				view = "search_own_content"
			}
			result = append(result,
				capturedSource{Statement: entry.ID, Name: spec.Table, Schema: "main", Kind: "virtual", SQL: audit.Declaration},
				capturedSource{Statement: entry.ID, Name: view, Schema: "main", Kind: "view", SQL: audit.ContentView})
			for _, row := range audit.ContentTables.Values {
				name := row[0].(string) // inspected exact native format above
				s := capturedSource{Statement: entry.ID, Name: name, Schema: "main"}
				if err := conn.QueryRowContext(ctx, `SELECT type,sql,rootpage FROM main.sqlite_schema WHERE name=?`, name).Scan(&s.Kind, &s.SQL, &s.RootPage); err != nil {
					return nil, err
				}
				result = append(result, s)
			}
			continue
		}
		if entry.Plan.NativeInsert != "" {
			audit, err := inspectFTSInsert(ctx, conn, entry.Definition)
			if err != nil {
				return nil, err
			}
			spec, _ := entry.Plan.NativeInsert.Specification()
			result = append(result,
				capturedSource{Statement: entry.ID, Name: spec.Table, Schema: "main", Kind: "virtual", SQL: audit.Declaration},
				capturedSource{Statement: entry.ID, Name: "search_fragment_content", Schema: "main", Kind: "view", SQL: audit.ContentView})
			for _, name := range []string{"search_fragments", "search_generations"} {
				s := capturedSource{Statement: entry.ID, Name: name, Schema: "main"}
				if err := conn.QueryRowContext(ctx, `SELECT type,sql,rootpage FROM main.sqlite_schema WHERE name=?`, name).Scan(&s.Kind, &s.SQL, &s.RootPage); err != nil {
					return nil, err
				}
				result = append(result, s)
			}
			continue
		}
		sourceSQL := entry.SQL
		if readEngineDefinition(entry.Definition) {
			sourceSQL = readEngineSourceSQL
		}
		tokens, err := lintTokens(sourceSQL)
		if err != nil {
			return nil, err
		}
		p := lintParser{tokens: tokens, issuerOctets: closedOctetDefinition(entry.Definition)}
		if err := p.statement(); err != nil {
			return nil, err
		}
		var names []string
		if p.dml != nil {
			names = append(names, nameKey(p.dml.table))
		}
		for _, s := range p.sources {
			names = append(names, s.name)
		}
		for _, name := range names {
			if err := mainBaseTable(ctx, conn, name); err != nil {
				return nil, err
			}
			s := capturedSource{Statement: entry.ID, Name: name, Schema: "main"}
			if err := conn.QueryRowContext(ctx, `SELECT type,sql,rootpage FROM main.sqlite_schema WHERE name=? COLLATE NOCASE`, name).Scan(&s.Kind, &s.SQL, &s.RootPage); err != nil {
				return nil, err
			}
			result = append(result, s)
		}
	}
	return result, nil
}
