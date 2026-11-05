package plan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type Statistics string

const (
	Unanalyzed Statistics = "unanalyzed"
	Analyzed   Statistics = "analyzed"
)

// Fixture is part of the reviewed environment. Changing the fact journal,
// statistics population, functions or collations requires a separate baseline.
type Fixture struct {
	ConnectionPolicy sqliteconn.Config `json:"connectionPolicy"`
	SHA256           string            `json:"sha256"`
	Statistics       Statistics        `json:"statistics"`
	Implementations  string            `json:"implementations"`
}
type Rows struct {
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
}
type Environment struct {
	Connection        sqliteconn.Settings `json:"connection"`
	IndexPolicySHA256 string              `json:"indexPolicySha256"`
	Fixture           Fixture             `json:"fixture"`
	Engine            Rows                `json:"engine"`
	CompileOptions    Rows                `json:"compileOptions"`
	Schema            Rows                `json:"schema"`
	SchemaSHA256      string              `json:"schemaSha256"`
	Statistics        map[string]Rows     `json:"statistics"`
	StatisticsSHA256  string              `json:"statisticsSha256"`
	Pragmas           map[string]Rows     `json:"pragmas"`
	FunctionsSHA256   string              `json:"functionsSha256"`
	Collations        Rows                `json:"collations"`
}
type Golden struct {
	Environment Environment     `json:"environment"`
	Manifest    []stmt.Entry    `json:"manifest"`
	Plans       []StatementPlan `json:"plans"`
}
type StatementPlan struct {
	ID        stmt.ID `json:"id"`
	Cohort    string  `json:"cohort"`
	Explain   Rows    `json:"explain"`
	QueryPlan Rows    `json:"queryPlan"`
}

// Capture uses one caller-owned connection to prevent pool PRAGMA/statistics
// drift. EXPLAIN never executes the accepting statement. Nothing here runs
// ANALYZE, optimize or changes connection settings to manufacture a passing plan.
func Capture(ctx context.Context, conn *sql.Conn, registry *stmt.Registry, fixture Fixture, policy IndexPolicy) (Golden, error) {
	g := Golden{Manifest: registry.Manifest(), Plans: []StatementPlan{}}
	if len(g.Manifest) == 0 {
		return g, fmt.Errorf("plan: empty registry")
	}
	env, err := environment(ctx, conn, fixture)
	if err != nil {
		return g, err
	}
	if err := policy.CheckSchema(env.Schema); err != nil {
		return g, err
	}
	env.IndexPolicySHA256 = policy.sha256
	g.Environment = env
	roots, err := readRoots(ctx, conn)
	if err != nil {
		return g, err
	}
	for _, entry := range g.Manifest {
		if err := policy.CheckPredicates(entry.Definition, env.Schema); err != nil {
			return g, err
		}
		for _, cohort := range entry.Cohorts {
			full, err := query(ctx, conn, "EXPLAIN "+entry.SQL, cohort.Bindings...)
			if err != nil {
				return g, fmt.Errorf("plan %s/%s EXPLAIN: %w", entry.ID, cohort.Name, err)
			}
			eqp, err := query(ctx, conn, "EXPLAIN QUERY PLAN "+entry.SQL, cohort.Bindings...)
			if err != nil {
				return g, fmt.Errorf("plan %s/%s EQP: %w", entry.ID, cohort.Name, err)
			}
			if err := Check(entry.Definition, eqp); err != nil {
				return g, err
			}
			full, err = canonicalVM(full, roots)
			if err != nil {
				return g, fmt.Errorf("plan %s/%s VM: %w", entry.ID, cohort.Name, err)
			}
			g.Plans = append(g.Plans, StatementPlan{ID: entry.ID, Cohort: cohort.Name, Explain: full, QueryPlan: eqp})
		}
	}
	return g, nil
}

// Check enforces hot-path properties in addition to the exact full-plan golden.
// Required indexes must appear in both the compiled SQL and SQLite's actual EQP.
func Check(d stmt.Definition, eqp Rows) error {
	if err := CheckEQP(d, eqp); err != nil {
		return err
	}
	for _, issue := range LintSQL(d.SQL) {
		if !waived(d, "sql-lint", issue.Form, d.SQL) {
			return fmt.Errorf("plan %s: unapproved SQL lint %s: %s", d.ID, issue.Form, issue.Detail)
		}
	}
	if !d.Plan.Hot {
		return nil
	}
	if len(d.Plan.Indexes) == 0 && len(d.Plan.PrimaryKeys) == 0 {
		return fmt.Errorf("plan %s: hot statement has no intended index", d.ID)
	}
	details := []string{}
	for _, row := range eqp.Values {
		details = append(details, row[3].(string))
	}

	for _, table := range d.Plan.PrimaryKeys {
		found := false
		for _, detail := range details {
			if strings.HasPrefix(detail, "SEARCH "+table+" USING PRIMARY KEY (") || strings.HasPrefix(detail, "SEARCH "+table+" USING INTEGER PRIMARY KEY (") {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("plan %s: primary key seek missing for %s", d.ID, table)
		}
	}
	for _, seek := range d.Plan.Seeks {
		found := false
		for _, detail := range details {
			if strings.Contains(detail, seek) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("plan %s: required seek %q absent", d.ID, seek)
		}
	}
	for _, index := range d.Plan.Indexes {
		if !forcedIndex(d.SQL, index) {
			return fmt.Errorf("plan %s: index %s not forced in SQL", d.ID, index)
		}
		found := false
		for _, detail := range details {
			if strings.Contains(detail, "USING INDEX "+index+" (") || strings.Contains(detail, "USING COVERING INDEX "+index+" (") {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("plan %s: intended index %s absent from EQP", d.ID, index)
		}
	}
	return nil
}
func (g Golden) JSON() ([]byte, error) { return json.MarshalIndent(g, "", "  ") }

// Compare rejects absent goldens. Recording is deliberately a separate action
// owned by the caller; a test can never silently accept a changed engine/plan.
func (g Golden) Compare(expected []byte) error {
	actual, err := g.JSON()
	if err != nil {
		return err
	}
	return CompareJSON(expected, actual)
}

func environment(ctx context.Context, conn *sql.Conn, f Fixture) (Environment, error) {
	env := Environment{Fixture: f, Statistics: map[string]Rows{}, Pragmas: map[string]Rows{}}
	settings, err := sqliteconn.Assert(ctx, conn, f.ConnectionPolicy)
	if err != nil {
		return env, fmt.Errorf("plan: unqualified connection: %w", err)
	}
	env.Connection = settings
	if decoded, err := hex.DecodeString(f.SHA256); err != nil || len(decoded) != 32 || f.Implementations == "" {
		return env, fmt.Errorf("plan: fixture SHA256 and implementation identity required")
	}
	if f.Statistics != Unanalyzed && f.Statistics != Analyzed {
		return env, fmt.Errorf("plan: undeclared statistics profile")
	}
	env.Engine, err = query(ctx, conn, "SELECT sqlite_version(),sqlite_source_id()")
	if err != nil {
		return env, err
	}
	env.CompileOptions, err = query(ctx, conn, "SELECT compile_options FROM pragma_compile_options ORDER BY compile_options")
	if err != nil {
		return env, err
	}
	env.Schema, err = query(ctx, conn, "SELECT type,name,tbl_name,rootpage,sql FROM sqlite_schema ORDER BY type,name")
	if err != nil {
		return env, err
	}
	env.SchemaSHA256 = digest(env.Schema)
	tables, err := query(ctx, conn, "SELECT name FROM sqlite_schema WHERE name IN ('sqlite_stat1','sqlite_stat4') ORDER BY name")
	if err != nil {
		return env, err
	}
	for _, row := range tables.Values {
		name := row[0].(string)
		// These two identifiers come from a closed compiled set above.
		order := "tbl,idx"
		if name == "sqlite_stat4" {
			order += ",neq,nlt,ndlt,sample"
		}
		stats, err := query(ctx, conn, "SELECT * FROM "+name+" ORDER BY "+order)
		if err != nil {
			return env, err
		}
		env.Statistics[name] = stats
	}
	if f.Statistics == Unanalyzed && len(env.Statistics) != 0 {
		return env, fmt.Errorf("plan: unanalyzed fixture contains statistics")
	}
	if f.Statistics == Analyzed && len(env.Statistics) == 0 {
		return env, fmt.Errorf("plan: analyzed fixture has no statistics")
	}
	env.StatisticsSHA256 = digest(env.Statistics)
	for _, name := range []string{"automatic_index", "foreign_keys", "analysis_limit", "encoding", "page_size", "cache_size", "temp_store", "journal_mode", "synchronous", "reverse_unordered_selects", "trusted_schema", "query_only"} {
		value, err := query(ctx, conn, "PRAGMA "+name)
		if err != nil {
			return env, err
		}
		env.Pragmas[name] = value
	}
	functions, err := query(ctx, conn, "SELECT name,builtin,type,enc,narg,flags FROM pragma_function_list ORDER BY name,builtin,type,enc,narg,flags")
	if err != nil {
		return env, err
	}
	env.FunctionsSHA256 = digest(functions)
	env.Collations, err = query(ctx, conn, "SELECT seq,name FROM pragma_collation_list ORDER BY seq")
	return env, err
}
func digest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func query(ctx context.Context, conn *sql.Conn, text string, args ...any) (Rows, error) {
	rows, err := conn.QueryContext(ctx, text, args...)
	if err != nil {
		return Rows{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return Rows{}, err
	}
	result := Rows{Columns: columns, Values: [][]any{}}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return Rows{}, err
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = slices.Clone(b)
			}
		}
		result.Values = append(result.Values, values)
	}
	return result, rows.Err()
}
