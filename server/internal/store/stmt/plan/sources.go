package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// LintSQLOn adds live source/destination resolution to closed syntax checks.
// LintSQL alone is lexical: it has no database catalog and cannot certify a
// source. Capture always uses this connection-aware lint before EXPLAIN.
func LintSQLOn(ctx context.Context, conn *sql.Conn, text string) []LintIssue {
	if issues := LintSQL(text); len(issues) != 0 {
		return issues
	}
	if conn == nil {
		return []LintIssue{{"sql:source", "source qualification requires a connection"}}
	}
	tokens, err := lintTokens(text)
	if err != nil {
		return []LintIssue{{"sql:source", err.Error()}}
	}
	p := lintParser{tokens: tokens}
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
