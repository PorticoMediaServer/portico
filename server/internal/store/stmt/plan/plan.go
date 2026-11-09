package plan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/internal/qualification"
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
	// DerivedSchema names fixture-owned tables that are not part of the read
	// schema (writer-derived shapes such as catalog_browse_memberships). They
	// are excluded from the recorded schema binding, whose canonical objects
	// must still match the embedded native layout exactly. Empty for the
	// canonical reader fixtures.
	//
	// A declared derived table is installed by the test fixture, not by the
	// canonical read DDL, and its fixture DDL may be a reviewed SUBSET of the
	// canonical writer layout: only the columns, constraints and indexes the
	// reader recipe exercises. Exactness is therefore claimed only for the
	// canonical read schema; it is never claimed for these derived shapes.
	// Current membership-fixture subsets:
	//   catalog_episodes        omits local_identity_status, ordering_basis and
	//                           the catalog_episodes_show/season/position indexes
	//   catalog_seasons         omits title, overview and UNIQUE(show_id,number)
	//   catalog_albums          omits edition_key, provider_match_status,
	//                           overview, label, tags, release_* and indexes
	//   catalog_songs           omits release_status, recording_id, release_*,
	//                           track_id, provider_* and indexes
	//   catalog_browse_memberships is the canonical full shape
	DerivedSchema []string `json:"derivedSchema,omitempty"`
}
type Rows struct {
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
}
type Environment struct {
	SchemaBinding     *SchemaBinding      `json:"schemaBinding,omitempty"`
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
	Environment Environment      `json:"environment"`
	Manifest    []stmt.Entry     `json:"manifest"`
	Plans       []StatementPlan  `json:"plans"`
	ForeignKeys *ForeignKeyAudit `json:"foreignKeys,omitempty"`
	receipt     stmt.Receipt
}

// Receipt returns the opaque capability minted only by a complete successful
// Capture. A zero, failed or JSON-decoded Golden has no registration authority.
func (g Golden) Receipt() stmt.Receipt { return g.receipt }

type StatementPlan struct {
	ID             stmt.ID           `json:"id"`
	Cohort         string            `json:"cohort"`
	Explain        Rows              `json:"explain"`
	QueryPlan      Rows              `json:"queryPlan"`
	Insert         *InsertAudit      `json:"insert,omitempty"`
	AutoIndexes    []AutoIndexAudit  `json:"autoIndexes,omitempty"`
	NativeInsert   *FTSInsertAudit   `json:"nativeInsert,omitempty"`
	NativeRetained *FTSRetainedAudit `json:"nativeRetained,omitempty"`
	NativeRead     *FTSReadAudit     `json:"nativeRead,omitempty"`
}

// Capture uses one caller-owned connection to prevent pool PRAGMA/statistics
// drift. EXPLAIN never executes the accepting statement. Nothing here runs
// ANALYZE, optimize or changes connection settings to manufacture a passing plan.
func Capture(ctx context.Context, conn *sql.Conn, registry *stmt.Registry, fixture Fixture, policy IndexPolicy) (Golden, error) {
	g := Golden{Manifest: registry.Manifest(), Plans: []StatementPlan{}}
	if len(g.Manifest) == 0 {
		return g, fmt.Errorf("plan: empty registry")
	}
	for _, entry := range g.Manifest {
		if !utf8.ValidString(entry.SQL) {
			return g, fmt.Errorf("plan %s: SQL must be valid UTF-8", entry.ID)
		}
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
		var nativeRead *FTSReadAudit
		var native *FTSInsertAudit
		var retained *FTSRetainedAudit
		if readEngineDefinition(entry.Definition) && (fixture.ConnectionPolicy.Role != sqliteconn.Reader || env.Connection.QueryOnly != 1) {
			return g, fmt.Errorf("plan %s: actual engine point requires Reader custody", entry.ID)
		}
		if entry.Plan.NativeRead != nil {
			if fixture.ConnectionPolicy.Role != sqliteconn.Reader || env.Connection.QueryOnly != 1 {
				return g, fmt.Errorf("plan %s: FTS native reads require actual Reader custody", entry.ID)
			}
			nativeRead, err = inspectFTSRead(ctx, conn, entry.Definition)
			if err != nil {
				return g, fmt.Errorf("plan %s native read audit: %w", entry.ID, err)
			}
		} else if entry.Plan.NativeInsert != "" {
			if err := checkSyntax(entry.Definition); err != nil {
				return g, err
			}
			native, err = inspectFTSInsert(ctx, conn, entry.Definition)
			if err != nil {
				return g, fmt.Errorf("plan %s native INSERT: %w", entry.ID, err)
			}
		} else if entry.Plan.NativeRetained != "" {
			if err := checkSyntax(entry.Definition); err != nil {
				return g, err
			}
			retained, err = inspectFTSRetained(ctx, conn, entry.Definition)
			if err != nil {
				return g, fmt.Errorf("plan %s retained native operation: %w", entry.ID, err)
			}
		} else if err := checkSyntaxOn(ctx, conn, entry.Definition); err != nil {
			return g, err
		}
		var autoIndexes []AutoIndexAudit
		var shape *dmlShape
		if nativeRead == nil {
			autoIndexes, err = auditAutoindexes(ctx, conn, entry.Definition)
			if err != nil {
				return g, fmt.Errorf("plan %s autoindex binding: %w", entry.ID, err)
			}
			shape, err = dmlSQL(entry.SQL)
			if err != nil {
				return g, fmt.Errorf("plan %s parse: %w", entry.ID, err)
			}
		}
		var table *dmlTable
		if shape != nil && native == nil && retained == nil {
			table, err = inspectDMLTable(ctx, conn, shape)
			if err != nil {
				return g, fmt.Errorf("plan %s columns: %w", entry.ID, err)
			}
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
			var insert *InsertAudit
			if err := checkAutoindexSearches(entry.Definition, autoIndexes, eqp); err != nil {
				return g, fmt.Errorf("plan %s/%s: %w", entry.ID, cohort.Name, err)
			}
			if table != nil && !mainWriteRoot(full, table.root) {
				return g, fmt.Errorf("plan %s: native destination does not match audited main table", entry.ID)
			}
			if nativeRead != nil {
				if err := checkFTSReadEQP(entry.Definition, eqp); err != nil {
					return g, fmt.Errorf("plan %s/%s: %w", entry.ID, cohort.Name, err)
				}
				full, err = canonicalFTSRead(full, *entry.Plan.NativeRead, nativeRead)
				if err != nil {
					return g, fmt.Errorf("plan %s/%s: %w", entry.ID, cohort.Name, err)
				}
			} else if native != nil || retained != nil {
				if !slices.Equal(eqp.Columns, []string{"id", "parent", "notused", "detail"}) || len(eqp.Values) != 0 {
					return g, fmt.Errorf("plan %s: fixed FTS INSERT must have empty native EQP", entry.ID)
				}
				if native != nil {
					full, err = mapFTSInsertVM(full, native.Identity)
				} else {
					full, err = mapFTSRetainedVM(full, retained.Slot, retained.Identity)
				}
				if err != nil {
					return g, fmt.Errorf("plan %s/%s native INSERT: %w", entry.ID, cohort.Name, err)
				}
				if g.ForeignKeys == nil {
					audit, err := ForeignKeys(ctx, conn, fixture.ConnectionPolicy)
					if err != nil {
						return g, err
					}
					g.ForeignKeys = &audit
				}
			} else if shape != nil && shape.kind == "INSERT" {
				if err := checkBase(entry.Definition, eqp); err != nil {
					return g, err
				}
				insert, err = auditInsert(ctx, conn, entry.Definition, shape, table, cohort.Bindings, full)
				if err != nil {
					return g, fmt.Errorf("plan %s/%s INSERT audit: %w", entry.ID, cohort.Name, err)
				}
				if g.ForeignKeys == nil {
					audit, err := ForeignKeys(ctx, conn, fixture.ConnectionPolicy)
					if err != nil {
						return g, err
					}
					g.ForeignKeys = &audit
				}
			} else if shape == nil && writesDatabase(full) {
				return g, fmt.Errorf("plan %s: unrecognized write structure cannot skip DML audits", entry.ID)
			} else if err := checkCaptured(entry.Definition, eqp, autoIndexes...); err != nil {
				return g, err
			}
			full, err = canonicalVM(full, roots)
			if err != nil {
				return g, fmt.Errorf("plan %s/%s VM: %w", entry.ID, cohort.Name, err)
			}
			eqp, err = canonicalEQP(eqp)
			if err != nil {
				return g, fmt.Errorf("plan %s/%s EQP: %w", entry.ID, cohort.Name, err)
			}
			g.Plans = append(g.Plans, StatementPlan{ID: entry.ID, Cohort: cohort.Name, Explain: full, QueryPlan: eqp, Insert: insert, AutoIndexes: autoIndexes, NativeInsert: native, NativeRetained: retained, NativeRead: nativeRead})
		}
	}
	// Keep statement-specific source/builtin audits first. No failed or partial
	// Capture gains a schema binding or receipt; the shared gate is mandatory
	// before source classification is sealed and qualification is minted.
	if policy.sha256 == embeddedPolicySHA() {
		if err := checkEmbeddedCatalog(env.Schema); err != nil {
			return g, err
		}
		env.SchemaBinding = &SchemaBinding{Version: 1, SQLSHA256: schemaSQLSHA()}
		if err := validateDerivedEnvironment(env, runtime.GOOS+"/"+runtime.GOARCH); err != nil {
			return g, err
		}
	}
	g.Environment = env
	sources, err := capturedSources(ctx, conn, g.Manifest)
	if err != nil {
		return g, err
	}
	sourceBytes, err := qualification.Encode(sources)
	if err != nil {
		return g, err
	}
	data, err := qualification.Encode(struct {
		Environment Environment
		Plans       []StatementPlan
		ForeignKeys *ForeignKeyAudit
	}{g.Environment, g.Plans, g.ForeignKeys})
	if err != nil {
		return g, err
	}
	definitions := make(map[string][]byte, len(g.Manifest))
	for _, entry := range g.Manifest {
		definition, err := qualification.Encode(entry.Definition)
		if err != nil {
			return g, err
		}
		definitions[string(entry.ID)] = definition
	}
	g.receipt = qualification.Mint(definitions, data, sourceBytes)
	return g, nil
}

var ErrCaptureRequired = errors.New("qualification requires connection-aware Capture")

// Check cannot qualify a statement from caller-supplied SQL/EQP alone. Source,
// policy and DML certification require Capture on the actual immutable fixture.
// Deprecated: use Capture. This connection-free API always refuses qualification.
func Check(d stmt.Definition, _ Rows) error {
	return fmt.Errorf("plan %s: %w", d.ID, ErrCaptureRequired)
}

// Only Capture may invoke this after live source, policy and DML inspection.
// CheckEQP and the lexical lint are component checks, not qualification receipts.
func checkCaptured(d stmt.Definition, eqp Rows, autoIndexes ...AutoIndexAudit) error {
	if readEngineDefinition(d) {
		if err := checkReadEnginePoint(eqp); err != nil {
			return err
		}
	}
	if err := checkOctetPoint(d, eqp); err != nil {
		return err
	}
	if err := checkBase(d, eqp); err != nil {
		return err
	}
	shape, err := dmlSQL(d.SQL)
	if err != nil {
		return fmt.Errorf("plan %s parse: %w", d.ID, err)
	}
	if shape != nil && shape.kind == "INSERT" {
		return fmt.Errorf("plan %s: INSERT requires Capture destination/conflict audit", d.ID)
	}
	if !d.Plan.Hot && shape == nil {
		return nil
	}
	return checkSearch(d, eqp, autoIndexes...)
}

func checkBase(d stmt.Definition, eqp Rows) error {
	if err := CheckEQP(d, eqp); err != nil {
		return err
	}
	return checkSyntax(d)
}

func checkSyntax(d stmt.Definition) error {
	return checkSyntaxIssues(d, readEngineSyntax(d))
}

func checkSyntaxOn(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	if readEngineDefinition(d) {
		if err := checkSyntaxIssues(d, lintSQLOn(ctx, conn, readEngineSourceSQL, false)); err != nil {
			return err
		}
		return auditReadEngine(ctx, conn)
	}
	if err := checkSyntaxIssues(d, lintSQLOn(ctx, conn, d.SQL, closedOctetDefinition(d))); err != nil {
		return err
	}
	return auditOctetDefinition(ctx, conn, d)
}

func checkSyntaxIssues(d stmt.Definition, issues []LintIssue) error {
	if err := checkIAEntryDefinition(d); err != nil {
		return err
	}
	if (d.ID == readEngineID || d.SQL == readEngineSQL) && !readEngineDefinition(d) {
		return fmt.Errorf("plan %s: engine metadata requires its exact closed definition", d.ID)
	}
	if err := checkOctetDefinition(d); err != nil {
		return err
	}
	for _, issue := range issues {
		return fmt.Errorf("plan %s: unapproved SQL lint %s: %s", d.ID, issue.Form, issue.Detail)
	}
	if index := forcedAutoindex(d.SQL); index != "" && len(d.Plan.AutoIndexes) == 0 {
		return fmt.Errorf("plan %s: generated index %s requires an exact live PK/UNIQUE binding", d.ID, index)
	}
	return nil
}

func checkSearch(d stmt.Definition, eqp Rows, autoIndexes ...AutoIndexAudit) error {
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
		if !isAutoindex(index) && !forcedIndex(d.SQL, index) {
			return fmt.Errorf("plan %s: index %s not forced in SQL", d.ID, index)
		}
		found := false
		for _, detail := range details {
			if strings.Contains(detail, "USING INDEX "+index+" (") || strings.Contains(detail, "USING COVERING INDEX "+index+" (") {
				found = true
			}
			for _, certified := range autoIndexes {
				if certified.Binding.Name == index && certified.WithoutRowid && certified.Binding.Origin == "pk" && autoindexSearch(certified, detail) {
					found = true
				}
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

// schemaWithoutDerived removes a fixture's declared writer-derived tables and
// the indexes that belong to them from the schema catalogue. The remaining rows
// must still prove the embedded native layout exactly; a declared name that is
// absent is an error rather than a silently ignored typo.
func schemaWithoutDerived(rows Rows, derived []string) (Rows, error) {
	declared := make(map[string]bool, len(derived))
	for _, name := range derived {
		if name == "" {
			return Rows{}, fmt.Errorf("plan: empty derived schema name")
		}
		declared[name] = true
	}
	seen := make(map[string]bool, len(declared))
	out := Rows{Columns: rows.Columns, Values: make([][]any, 0, len(rows.Values))}
	for _, row := range rows.Values {
		if len(row) != 5 {
			return Rows{}, fmt.Errorf("plan: malformed schema catalogue")
		}
		name, _ := row[1].(string)
		table, _ := row[2].(string)
		if row[0] == "table" && declared[name] {
			seen[name] = true
		}
		if declared[name] || declared[table] {
			continue
		}
		out.Values = append(out.Values, row)
	}
	for name := range declared {
		if !seen[name] {
			return Rows{}, fmt.Errorf("plan: fixture-derived table %s absent from schema", name)
		}
	}
	return out, nil
}

func environment(ctx context.Context, conn *sql.Conn, f Fixture) (Environment, error) {
	env := Environment{Fixture: f, Statistics: map[string]Rows{}, Pragmas: map[string]Rows{}}
	settings, err := sqliteconn.Assert(ctx, conn, f.ConnectionPolicy)
	if err != nil {
		return env, fmt.Errorf("plan: unqualified connection: %w", err)
	}
	env.Connection = settings
	if err := assertNoTriggers(ctx, conn); err != nil {
		return env, err
	}
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
	if len(f.DerivedSchema) != 0 {
		env.Schema, err = schemaWithoutDerived(env.Schema, f.DerivedSchema)
		if err != nil {
			return env, err
		}
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
	if len(f.DerivedSchema) != 0 {
		derived := make(map[string]bool, len(f.DerivedSchema))
		for _, name := range f.DerivedSchema {
			derived[name] = true
		}
		for name, rows := range env.Statistics {
			kept := rows.Values[:0]
			for _, row := range rows.Values {
				table, _ := row[0].(string)
				if !derived[table] {
					kept = append(kept, row)
				}
			}
			env.Statistics[name] = Rows{Columns: rows.Columns, Values: kept}
		}
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
